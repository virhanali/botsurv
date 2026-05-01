package marketdata

import (
	"math"
	"testing"

	"github.com/virhan/botsurv/internal/domain"
)

func TestCache_RejectInvalidCandle(t *testing.T) {
	cache := newCandleCache()
	cache.update("BTCUSDT", "15m", domain.Candle{
		Symbol:    "BTCUSDT",
		Timeframe: "15m",
		OpenTime:  1,
		Open:      100,
		High:      101,
		Low:       99,
		Close:     math.NaN(),
		Volume:    10,
		Confirmed: true,
	})

	got := cache.get("BTCUSDT", "15m", 10)
	if len(got) != 0 {
		t.Fatalf("expected invalid candle to be rejected, got %d candles", len(got))
	}
}
