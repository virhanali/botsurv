package indicator

import (
	"math"
	"testing"

	"github.com/virhan/botsurv/internal/domain"
)

func newTestCandle(open, high, low, close float64) domain.Candle {
	return domain.Candle{
		Open:      open,
		High:      high,
		Low:       low,
		Close:     close,
		Volume:    1000,
		Confirmed: true,
	}
}

// buildFibCandles creates lookback candles with specified swingLow/swingHigh and final price.
func buildFibCandles(n int, swingLow, swingHigh, finalPrice float64) []domain.Candle {
	if n < 10 {
		n = 10
	}
	candles := make([]domain.Candle, n)
	mid := n / 2
	for i := 0; i < mid; i++ {
		t := float64(i) / float64(mid)
		price := swingLow + (swingHigh-swingLow)*t
		candles[i] = newTestCandle(price, price, price, price)
		candles[i].OpenTime = int64(i * 60000)
	}
	// Ensure exact swingLow at start, swingHigh at peak
	candles[0].High = swingLow
	candles[0].Low = swingLow
	candles[mid-1].High = swingHigh
	candles[mid-1].Low = swingHigh
	// Set remaining candles at finalPrice
	for i := mid; i < n; i++ {
		candles[i] = newTestCandle(finalPrice, finalPrice, finalPrice, finalPrice)
		candles[i].OpenTime = int64(i * 60000)
	}
	return candles
}

func TestFibonacci_LONG_PullbackNear0618(t *testing.T) {
	// swingLow=100, swingHigh=200, range=100
	// fib_0_618 = swingHigh - range*0.618 = 200 - 61.8 = 138.2
	candles := buildFibCandles(80, 100, 200, 138.2)
	fib := ComputeFibonacciContext(candles, domain.SideLong, 80, 1.0)

	if !fib.Valid {
		t.Fatalf("expected valid fibonacci context, got reason_codes=%v", fib.ReasonCodes)
	}
	if fib.ImpulseDirection != "up" {
		t.Errorf("expected impulse_direction=up for LONG, got %s", fib.ImpulseDirection)
	}
	if fib.ZoneLabel != "in_0_618_zone" {
		t.Errorf("expected zone_label=in_0_618_zone for pullback near 0.618, got %s", fib.ZoneLabel)
	}
	if fib.SwingLow != 100 {
		t.Errorf("expected swing_low=100, got %.2f", fib.SwingLow)
	}
	if fib.SwingHigh != 200 {
		t.Errorf("expected swing_high=200, got %.2f", fib.SwingHigh)
	}
}

func TestFibonacci_SHORT_PullbackNear0618(t *testing.T) {
	// swingLow=100, swingHigh=200, range=100
	// For SHORT: fib_0_618 = swingLow + range*0.618 = 100 + 61.8 = 161.8
	candles := buildFibCandles(80, 100, 200, 161.8)
	fib := ComputeFibonacciContext(candles, domain.SideShort, 80, 1.0)

	if !fib.Valid {
		t.Fatalf("expected valid fibonacci context, got reason_codes=%v", fib.ReasonCodes)
	}
	if fib.ImpulseDirection != "down" {
		t.Errorf("expected impulse_direction=down for SHORT, got %s", fib.ImpulseDirection)
	}
	if fib.ZoneLabel != "in_0_618_zone" {
		t.Errorf("expected zone_label=in_0_618_zone for SHORT pullback near 0.618, got %s", fib.ZoneLabel)
	}
}

func TestFibonacci_NotEnoughCandles_ReturnsInvalid(t *testing.T) {
	candles := make([]domain.Candle, 5)
	for i := range candles {
		candles[i] = newTestCandle(100, 101, 99, 100)
		candles[i].OpenTime = int64(i * 60000)
	}
	fib := ComputeFibonacciContext(candles, domain.SideLong, 80, 1.0)

	if fib.Valid {
		t.Fatal("expected invalid for insufficient candles")
	}
	found := false
	for _, r := range fib.ReasonCodes {
		if r == "insufficient_closed_candles" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected insufficient_candles reason, got %v", fib.ReasonCodes)
	}
}

func TestFibonacci_FlatRange_ReturnsInvalid(t *testing.T) {
	candles := make([]domain.Candle, 100)
	for i := range candles {
		candles[i] = newTestCandle(100, 100, 100, 100)
		candles[i].OpenTime = int64(i * 60000)
	}
	fib := ComputeFibonacciContext(candles, domain.SideLong, 80, 1.0)

	if fib.Valid {
		t.Fatal("expected invalid for flat range")
	}
	found := false
	for _, r := range fib.ReasonCodes {
		if r == "flat_range" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected flat_range reason, got %v", fib.ReasonCodes)
	}
}

func TestFibonacci_InvalidCandles_ReturnsInvalid(t *testing.T) {
	candles := make([]domain.Candle, 100)
	for i := range candles {
		candles[i] = newTestCandle(math.NaN(), math.NaN(), math.NaN(), math.NaN())
		candles[i].OpenTime = int64(i * 60000)
	}
	fib := ComputeFibonacciContext(candles, domain.SideLong, 80, 1.0)

	if fib.Valid {
		t.Fatal("expected invalid for NaN candles")
	}
}

func TestFibonacci_PriceAt050Level_DistanceZero(t *testing.T) {
	// swingLow=100, swingHigh=200, range=100
	// fib_0_5 = 150, price exactly at 150 -> distance 0
	candles := buildFibCandles(80, 100, 200, 150)
	fib := ComputeFibonacciContext(candles, domain.SideLong, 80, 1.0)

	if !fib.Valid {
		t.Fatalf("expected valid, got %v", fib.ReasonCodes)
	}
	if fib.DistanceToNearestFibPct != 0 {
		t.Errorf("expected distance 0 at exact fib level, got %.4f", fib.DistanceToNearestFibPct)
	}
	if fib.ZoneLabel != "in_0_5_zone" {
		t.Errorf("expected in_0_5_zone, got %s", fib.ZoneLabel)
	}
}

func TestFibonacci_AbovePullbackZone(t *testing.T) {
	// price above fib_0_5=150 -> above_pullback_zone
	candles := buildFibCandles(80, 100, 200, 180)
	fib := ComputeFibonacciContext(candles, domain.SideLong, 80, 1.0)

	if !fib.Valid {
		t.Fatalf("expected valid, got %v", fib.ReasonCodes)
	}
	if fib.ZoneLabel != "above_pullback_zone" {
		t.Errorf("expected above_pullback_zone for price near swing high, got %s", fib.ZoneLabel)
	}
}

func TestFibonacci_BelowPullbackZone(t *testing.T) {
	// fib_0_786 = 200 - 100*0.786 = 121.4
	// price below 121.4 -> below_pullback_zone
	candles := buildFibCandles(80, 100, 200, 115)
	fib := ComputeFibonacciContext(candles, domain.SideLong, 80, 1.0)

	if !fib.Valid {
		t.Fatalf("expected valid, got %v", fib.ReasonCodes)
	}
	if fib.ZoneLabel != "below_pullback_zone" {
		t.Errorf("expected below_pullback_zone for deep pullback, got %s", fib.ZoneLabel)
	}
}
