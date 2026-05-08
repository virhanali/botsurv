package regime

import (
	"math"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
)

func TestComputeBTCRanging_TightConsolidation_ReturnsRanging(t *testing.T) {
	now := time.Now().UTC()
	candles := make([]domain.Candle, 30)
	basePrice := 30000.0
	for i := range candles {
		price := basePrice + float64(i%3-1)*5
		candles[i] = domain.Candle{
			OpenTime: now.Add(-time.Duration(len(candles)-i) * 15 * time.Minute).UnixMilli(),
			Open:     price,
			High:     price + 10,
			Low:      price - 10,
			Close:    price + 2,
			Volume:   100,
		}
	}

	result := ComputeBTCRanging(candles, 2.5, 16)
	if !result.IsRanging {
		t.Errorf("expected ranging for tight consolidation, got trending: detail=%s bandWidth=%.2f ratio=%.2f",
			result.Detail, result.BandWidth, result.RangeOverATR)
	}
	if result.BandWidth <= 0 {
		t.Errorf("expected positive bandwidth, got %.2f", result.BandWidth)
	}
	if result.ATR <= 0 {
		t.Errorf("expected positive ATR, got %.2f", result.ATR)
	}
}

func TestComputeBTCRanging_StrongTrend_ReturnsNotRanging(t *testing.T) {
	now := time.Now().UTC()
	candles := make([]domain.Candle, 20)
	basePrice := 30000.0
	for i := range candles {
		price := basePrice + float64(i)*500
		candles[i] = domain.Candle{
			OpenTime: now.Add(-time.Duration(len(candles)-i) * 15 * time.Minute).UnixMilli(),
			Open:     price,
			High:     price + 100,
			Low:      price - 100,
			Close:    price + 250,
			Volume:   1000,
		}
	}

	result := ComputeBTCRanging(candles, 2.5, 16)
	if result.IsRanging {
		t.Errorf("expected NOT ranging for strong trend, got ranging: detail=%s ratio=%.2f", result.Detail, result.RangeOverATR)
	}
}

func TestComputeBTCRanging_InsufficientData(t *testing.T) {
	result := ComputeBTCRanging(nil, 2.5, 16)
	if result.IsRanging {
		t.Error("expected NOT ranging with nil candles")
	}
	if result.Detail == "" {
		t.Error("expected detail message for insufficient data")
	}

	result = ComputeBTCRanging([]domain.Candle{}, 2.5, 16)
	if result.IsRanging {
		t.Error("expected NOT ranging with empty candles")
	}
}

func TestComputeBTCRanging_DefaultParameters(t *testing.T) {
	result := ComputeBTCRanging(nil, 0, 0)
	if result.IsRanging {
		t.Error("expected NOT ranging with nil candles and zero params")
	}

	now := time.Now().UTC()
	candles := []domain.Candle{
		{OpenTime: now.Add(-30 * time.Minute).UnixMilli(), Open: 30000, High: 30030, Low: 29970, Close: 30000, Volume: 100},
		{OpenTime: now.Add(-15 * time.Minute).UnixMilli(), Open: 30000, High: 30030, Low: 29970, Close: 30000, Volume: 100},
	}
	resultDef := ComputeBTCRanging(candles, 0, 0)
	if resultDef.IsRanging && resultDef.BandWidth <= 0 {
		t.Error("expected valid result with default parameters")
	}
}

func TestComputeBTCRanging_ATRComputationFails(t *testing.T) {
	now := time.Now().UTC()
	candles := []domain.Candle{
		{OpenTime: now.Add(-15 * time.Minute).UnixMilli(), Open: 30000, High: 30030, Low: 29970, Close: 30000, Volume: 100},
	}
	result := ComputeBTCRanging(candles, 2.5, 16)
	if result.IsRanging {
		t.Error("expected NOT ranging when ATR computation fails")
	}
}

func TestBuildSnapshot_SetsRangingFields(t *testing.T) {
	btc5m := twoCloseCandles(100, 99.5)
	btc15m := makeRangingCandles(20, 30000, 200)
	btc1h := linearCandles(30, 100, 100)
	target1h := linearCandles(30, 100, 103)

	btc1hSnap := indicator.IndicatorSnapshot{
		Symbol:         "BTCUSDT",
		LastClosePrice: 100,
		ATR14:          2,
		EMAAlignment: indicator.EMAlignment{
			PriceAboveEMA50: true,
		},
	}
	btc4hSnap := indicator.IndicatorSnapshot{
		Symbol:         "BTCUSDT",
		LastClosePrice: 100,
		ATR14:           2,
		ResistanceLevels: []float64{110},
		SupportLevels:    []float64{90},
		EMAAlignment: indicator.EMAlignment{
			PriceAboveEMA50: true,
		},
	}

	snap, err := BuildSnapshot(SnapshotInput{
		Now:             time.Now().UTC(),
		BTC5mCandles:    btc5m,
		BTC15mCandles:   btc15m,
		BTC1hCandles:    btc1h,
		BTC1hSnapshot:   btc1hSnap,
		BTC4hSnapshot:   btc4hSnap,
		Target1hCandles: target1h,
		Config: Config{
			BTCDumpShortThresholdPct:   -2.5,
			BTCDumpMediumThresholdPct:  -3.0,
			BTCNearLevelATRBuffer:      1.0,
			BTCDRisingFastThresholdPct: 0.8,
			RelativeStrength:           DefaultRelativeStrengthConfig(),
			RangingATRMultiplier:       2.5,
			RangingLookbackCandles:     16,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_ = snap.IsRanging
	_ = snap.RangingBandWidth

	if _, ok := snap.BTCFiltersDetail["BTCRanging"]; !ok {
		t.Error("expected BTCRanging in filters detail")
	}
}

func TestBuildSnapshot_RangingWarningAdded(t *testing.T) {
	btc15m := makeRangingCandles(20, 30000, 200)
	snap := buildTestSnapshotWithRanging(btc15m, 2.5, 16)

	rangingFound := false
	for _, w := range snap.Warnings {
		if w == "MARKET_RANGING" {
			rangingFound = true
			break
		}
	}
	if !rangingFound && snap.IsRanging {
		t.Error("expected MARKET_RANGING warning when IsRanging is true")
	}
}

func TestBuildSnapshot_StalenessSetsBTCDataStaleFlag(t *testing.T) {
	btc15m := twoCloseCandles(100, 99.5)
	btc1h := linearCandles(30, 100, 100)
	target1h := linearCandles(30, 100, 103)
	btc1hSnap := indicator.IndicatorSnapshot{Symbol: "BTCUSDT", LastClosePrice: 100, ATR14: 2}
	btc4hSnap := indicator.IndicatorSnapshot{Symbol: "BTCUSDT", LastClosePrice: 100, ATR14: 2}

	snap, err := BuildSnapshot(SnapshotInput{
		Now:               time.Now().UTC(),
		BTC5mCandles:      twoCloseCandles(100, 99),
		BTC15mCandles:     btc15m,
		BTC1hCandles:      btc1h,
		BTC1hSnapshot:     btc1hSnap,
		BTC4hSnapshot:     btc4hSnap,
		BTC5mStaleCounter: 5,
		Target1hCandles:   target1h,
		Config:            DefaultConfig(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !snap.BTCDataStale {
		t.Error("expected BTCDataStale to be true when BTC5mStaleCounter > 0")
	}

	staleFound := false
	for _, w := range snap.Warnings {
		if w == "BTC_DATA_STALE" {
			staleFound = true
			break
		}
	}
	if !staleFound {
		t.Error("expected BTC_DATA_STALE warning in snapshot")
	}
}

func TestBuildSnapshot_NoStalenessWhenCounterZero(t *testing.T) {
	snap := buildTestSnapshotWithRanging(twoCloseCandles(100, 99.5), 2.5, 16)
	if snap.BTCDataStale {
		t.Error("expected BTCDataStale to be false when BTC5mStaleCounter is 0")
	}
}

func TestConfig_DefaultRangingParameters(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.RangingATRMultiplier != 2.5 {
		t.Errorf("expected default RangingATRMultiplier 2.5, got %f", cfg.RangingATRMultiplier)
	}
	if cfg.RangingLookbackCandles != 16 {
		t.Errorf("expected default RangingLookbackCandles 16, got %d", cfg.RangingLookbackCandles)
	}
	if !cfg.ReduceNewPositionsWhenRanging {
		t.Error("expected default ReduceNewPositionsWhenRanging to be true")
	}
}

func TestConfig_WithDefaults_RangingFields(t *testing.T) {
	cfg := Config{}
	wd := withDefaults(cfg)
	if wd.RangingATRMultiplier != 2.5 {
		t.Errorf("expected default RangingATRMultiplier 2.5, got %f", wd.RangingATRMultiplier)
	}
	if wd.RangingLookbackCandles != 16 {
		t.Errorf("expected default RangingLookbackCandles 16, got %d", wd.RangingLookbackCandles)
	}
}

func makeRangingCandles(count int, basePrice, atrApprox float64) []domain.Candle {
	now := time.Now().UTC()
	candles := make([]domain.Candle, count)
	halfATR := atrApprox * 0.2
	for i := range candles {
		price := basePrice + math.Sin(float64(i)*0.3)*halfATR
		candles[i] = domain.Candle{
			OpenTime: now.Add(-time.Duration(count-i) * 15 * time.Minute).UnixMilli(),
			Open:     price,
			High:     price + halfATR,
			Low:      price - halfATR,
			Close:    price + math.Sin(float64(i)*0.5)*10,
			Volume:   100,
		}
	}
	return candles
}

func buildTestSnapshotWithRanging(btc15m []domain.Candle, atrMult float64, lookback int) MarketRegimeSnapshot {
	btc1h := linearCandles(30, 100, 100)
	target1h := linearCandles(30, 100, 103)
	btc1hSnap := indicator.IndicatorSnapshot{Symbol: "BTCUSDT", LastClosePrice: 100, ATR14: 2}
	btc4hSnap := indicator.IndicatorSnapshot{Symbol: "BTCUSDT", LastClosePrice: 100, ATR14: 2}

	snap, _ := BuildSnapshot(SnapshotInput{
		Now:             time.Now().UTC(),
		BTC5mCandles:    twoCloseCandles(100, 99.5),
		BTC15mCandles:   btc15m,
		BTC1hCandles:    btc1h,
		BTC1hSnapshot:   btc1hSnap,
		BTC4hSnapshot:   btc4hSnap,
		Target1hCandles: target1h,
		Config: Config{
			BTCDumpShortThresholdPct:   -2.5,
			BTCDumpMediumThresholdPct:  -3.0,
			BTCNearLevelATRBuffer:       1.0,
			BTCDRisingFastThresholdPct:  0.8,
			RelativeStrength:            DefaultRelativeStrengthConfig(),
			RangingATRMultiplier:         atrMult,
			RangingLookbackCandles:      lookback,
		},
	})
	return snap
}