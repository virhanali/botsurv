package marketdata

import (
	"math"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
)

func TestValidateCandleBatch_InvalidOHLCV(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]domain.Candle)
	}{
		{"nan", func(cs []domain.Candle) { cs[0].Open = math.NaN() }},
		{"inf", func(cs []domain.Candle) { cs[0].High = math.Inf(1) }},
		{"negative", func(cs []domain.Candle) { cs[0].Low = -1 }},
		{"zero", func(cs []domain.Candle) { cs[0].Close = 0 }},
	}

	v := NewValidator(app.DataValidationConfig{MinCandles: 5, MaxDataAgeSeconds: map[string]int{"15m": 180}})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candles := makeCandles("BTCUSDT", "15m", 5, time.Now().Add(-time.Hour), true)
			tt.mutate(candles)
			_, err := v.ValidateCandleBatch(CandleValidationInput{
				Symbol:    "BTCUSDT",
				Timeframe: "15m",
				Candles:   candles,
				Now:       time.Now(),
			})
			if err == nil {
				t.Fatalf("expected validation error for %s", tt.name)
			}
		})
	}
}

func TestValidateCandleBatch_InsufficientCandles(t *testing.T) {
	v := NewValidator(app.DataValidationConfig{MinCandles: 10})
	candles := makeCandles("BTCUSDT", "15m", 5, time.Now().Add(-time.Hour), true)
	_, err := v.ValidateCandleBatch(CandleValidationInput{
		Symbol:    "BTCUSDT",
		Timeframe: "15m",
		Candles:   candles,
	})
	if err == nil {
		t.Fatal("expected insufficient candles error")
	}
}

func TestValidateCandleBatch_NonMonotonicTimestamp(t *testing.T) {
	v := NewValidator(app.DataValidationConfig{MinCandles: 5})
	candles := makeCandles("BTCUSDT", "15m", 5, time.Now().Add(-time.Hour), true)
	candles[3].OpenTime = candles[2].OpenTime
	_, err := v.ValidateCandleBatch(CandleValidationInput{
		Symbol:    "BTCUSDT",
		Timeframe: "15m",
		Candles:   candles,
	})
	if err == nil {
		t.Fatal("expected non-monotonic timestamp error")
	}
}

func TestValidateCandleBatch_GapDetected(t *testing.T) {
	v := NewValidator(app.DataValidationConfig{MinCandles: 5})
	candles := makeCandles("BTCUSDT", "15m", 5, time.Now().Add(-time.Hour), true)
	candles[3].OpenTime += int64(15 * time.Minute / time.Millisecond)
	_, err := v.ValidateCandleBatch(CandleValidationInput{
		Symbol:    "BTCUSDT",
		Timeframe: "15m",
		Candles:   candles,
	})
	if err == nil {
		t.Fatal("expected gap detection error")
	}
}

func TestValidateCandleBatch_StaleData(t *testing.T) {
	v := NewValidator(app.DataValidationConfig{
		MinCandles:        5,
		MaxDataAgeSeconds: map[string]int{"15m": 180},
	})
	start := time.Now().Add(-2 * time.Hour).Truncate(time.Minute)
	candles := makeCandles("BTCUSDT", "15m", 5, start, true)
	latestClose := time.UnixMilli(candles[len(candles)-1].OpenTime).Add(15 * time.Minute)
	now := latestClose.Add(5 * time.Minute)
	_, err := v.ValidateCandleBatch(CandleValidationInput{
		Symbol:    "BTCUSDT",
		Timeframe: "15m",
		Candles:   candles,
		Now:       now,
	})
	if err == nil {
		t.Fatal("expected stale data error")
	}
}

func TestValidateCandleBatch_ClosedVsInProgress(t *testing.T) {
	v := NewValidator(app.DataValidationConfig{
		MinCandles:        5,
		MaxDataAgeSeconds: map[string]int{"15m": 180},
	})
	start := time.Now().Add(-45 * time.Minute).Truncate(time.Minute)
	candles := makeCandles("BTCUSDT", "15m", 5, start, true)
	candles[len(candles)-1].Confirmed = false
	now := time.UnixMilli(candles[len(candles)-1].OpenTime).Add(15*time.Minute - 30*time.Second)

	res, err := v.ValidateCandleBatch(CandleValidationInput{
		Symbol:    "BTCUSDT",
		Timeframe: "15m",
		Candles:   candles,
		Now:       now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.InProgressCandle == nil {
		t.Fatal("expected in-progress candle to be exposed")
	}
	if len(res.ClosedCandles) != 4 {
		t.Fatalf("expected 4 closed candles, got %d", len(res.ClosedCandles))
	}
	if res.LatestIsClosed {
		t.Fatal("expected latest candle to be marked in-progress")
	}
}

func TestValidateCandleBatch_RequireClosedLatestStripsInProgress(t *testing.T) {
	v := NewValidator(app.DataValidationConfig{
		MinCandles:        5,
		MaxDataAgeSeconds: map[string]int{"15m": 180},
	})
	start := time.Now().Add(-45 * time.Minute).Truncate(time.Minute)
	candles := makeCandles("BTCUSDT", "15m", 5, start, true)
	candles[len(candles)-1].Confirmed = false
	now := time.UnixMilli(candles[len(candles)-2].OpenTime).Add(16 * time.Minute) // 1m after close

	result, err := v.ValidateCandleBatch(CandleValidationInput{
		Symbol:              "BTCUSDT",
		Timeframe:           "15m",
		Candles:             candles,
		MinRequired:         4, // 5 - 1 stripped
		RequireClosedLatest: true,
		Now:                 now,
	})
	if err != nil {
		t.Fatalf("expected success after stripping in-progress candle: %v", err)
	}
	if len(result.Candles) != 4 {
		t.Fatalf("expected 4 candles after stripping, got %d", len(result.Candles))
	}
	if !result.LatestIsClosed {
		t.Fatal("expected latest to be closed after stripping")
	}
}

func TestValidateCandleBatch_RequireClosedLatest_249Plus1Fails(t *testing.T) {
	v := NewValidator(app.DataValidationConfig{
		MinCandles:        250,
		MaxDataAgeSeconds: map[string]int{"15m": 180},
	})
	start := time.Now().Add(-70 * time.Hour).Truncate(time.Minute)
	// 249 closed + 1 in-progress = 250 total
	candles := makeCandles("BTCUSDT", "15m", 250, start, true)
	candles[len(candles)-1].Confirmed = false
	now := time.UnixMilli(candles[len(candles)-2].OpenTime).Add(16 * time.Minute)

	_, err := v.ValidateCandleBatch(CandleValidationInput{
		Symbol:              "BTCUSDT",
		Timeframe:           "15m",
		Candles:             candles,
		MinRequired:         250,
		RequireClosedLatest: true,
		Now:                 now,
	})
	if err == nil {
		t.Fatal("expected validation error: 249 closed < 250 minRequired")
	}
}

func TestValidateCandleBatch_RequireClosedLatest_250Plus1Passes(t *testing.T) {
	v := NewValidator(app.DataValidationConfig{
		MinCandles:        250,
		MaxDataAgeSeconds: map[string]int{"15m": 180},
	})
	start := time.Now().Add(-70 * time.Hour).Truncate(time.Minute)
	// 250 closed + 1 in-progress = 251 total
	candles := makeCandles("BTCUSDT", "15m", 251, start, true)
	candles[len(candles)-1].Confirmed = false
	now := time.UnixMilli(candles[len(candles)-2].OpenTime).Add(16 * time.Minute)

	result, err := v.ValidateCandleBatch(CandleValidationInput{
		Symbol:              "BTCUSDT",
		Timeframe:           "15m",
		Candles:             candles,
		MinRequired:         250,
		RequireClosedLatest: true,
		Now:                 now,
	})
	if err != nil {
		t.Fatalf("expected success: 250 closed >= 250 minRequired: %v", err)
	}
	if len(result.ClosedCandles) != 250 {
		t.Fatalf("expected 250 closed candles, got %d", len(result.ClosedCandles))
	}
	if len(result.Candles) != 250 {
		t.Fatalf("expected 250 candles after stripping, got %d", len(result.Candles))
	}
}

func makeCandles(symbol, timeframe string, n int, start time.Time, confirmed bool) []domain.Candle {
	step := 15 * time.Minute
	if timeframe == "1H" {
		step = time.Hour
	}
	out := make([]domain.Candle, n)
	for i := 0; i < n; i++ {
		ts := start.Add(time.Duration(i) * step)
		price := float64(100 + i)
		out[i] = domain.Candle{
			Symbol:    symbol,
			Timeframe: timeframe,
			OpenTime:  ts.UnixMilli(),
			Open:      price,
			High:      price + 1,
			Low:       price - 1,
			Close:     price + 0.5,
			Volume:    10,
			Confirmed: confirmed,
		}
	}
	return out
}
