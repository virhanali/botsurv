package indicator

import (
	"math"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

func TestBuildSnapshot_RoundTrip(t *testing.T) {
	candles := makeTrendCandles(300, time.Now().Add(-300*time.Hour), time.Hour)
	snap, err := BuildSnapshot("BTCUSDT", "1H", candles, DefaultSnapshotConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Symbol != "BTCUSDT" || snap.Timeframe != "1H" {
		t.Fatalf("unexpected identity fields: %+v", snap)
	}
	if snap.LastCloseTime.IsZero() {
		t.Fatal("expected last_close_time populated")
	}
	if snap.RecentSwings == nil {
		t.Fatal("expected recent_swings slice to be initialized")
	}
	finite := []float64{
		snap.EMA20, snap.EMA50, snap.EMA200,
		snap.RSI14, snap.MACD.Line, snap.MACD.Signal, snap.MACD.Histogram,
		snap.ATR14, snap.ATRPct, snap.VolumeCurrent, snap.VolumeMA20, snap.VolumeRatio,
	}
	for _, v := range finite {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("snapshot contains NaN/Inf value: %+v", snap)
		}
	}
}

func TestBuildSnapshot_MinimumCandles(t *testing.T) {
	candles := makeTrendCandles(50, time.Now().Add(-50*time.Hour), time.Hour)
	_, err := BuildSnapshot("BTCUSDT", "1H", candles, DefaultSnapshotConfig())
	if err == nil {
		t.Fatal("expected error due to insufficient candles for EMA200")
	}
}

func makeTrendCandles(n int, start time.Time, step time.Duration) []domain.Candle {
	candles := make([]domain.Candle, n)
	for i := 0; i < n; i++ {
		price := 100.0 + float64(i)*0.5
		ts := start.Add(time.Duration(i) * step)
		candles[i] = domain.Candle{
			Symbol:    "X",
			OpenTime:  ts.UnixMilli(),
			Open:      price,
			High:      price + 1,
			Low:       price - 1,
			Close:     price + 0.2,
			Volume:    100 + float64(i%10),
			Confirmed: true,
		}
	}
	return candles
}
