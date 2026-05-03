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

// buildFibonacciRange creates lookback candles with a clean swing low->high->pullback structure.
// High and Low of each candle are set exactly at the interpolated price to keep swing detection clean.
func buildFibonacciRange(swingLow, swingHigh, pullbackPrice float64, lookback int) []domain.Candle {
	n := lookback
	if n < 10 {
		n = 10
	}
	candles := make([]domain.Candle, n)
	// First half: drift up from swingLow toward swingHigh
	mid := n / 2
	for i := 0; i < mid; i++ {
		t := float64(i) / float64(mid)
		price := swingLow + (swingHigh-swingLow)*t
		candles[i] = newTestCandle(price, price, price, price)
		candles[i].OpenTime = int64(i * 60000)
	}
	// Second half: drift toward pullbackPrice
	for i := mid; i < n; i++ {
		t := float64(i-mid) / float64(n-mid)
		price := swingHigh - (swingHigh-pullbackPrice)*t
		candles[i] = newTestCandle(price, price, price, price)
		candles[i].OpenTime = int64(i * 60000)
	}
	// Ensure exact swing extremes
	candles[0].High = swingLow
	candles[0].Low = swingLow
	candles[mid-1].High = swingHigh
	candles[mid-1].Low = swingHigh
	return candles
}

func TestFibonacci_LONG_PullbackNear0618(t *testing.T) {
	candles := buildFibonacciRange(100, 200, 161.8, 80)
	candles[len(candles)-1] = newTestCandle(138.2, 138.2, 138.2, 138.2)
	candles[len(candles)-1].OpenTime = int64((len(candles) - 1) * 60000)
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
	candles2 := buildFibonacciRange(100, 200, 161.8, 80)
	fib := ComputeFibonacciContext(candles2, domain.SideShort, 80, 1.0)

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
		t.Errorf("expected insufficient_closed_candles reason, got %v", fib.ReasonCodes)
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

func TestFibonacci_DistanceCalculation(t *testing.T) {
	// price exactly at 0.5 level -> distance should be 0
	candles := make([]domain.Candle, 100)
	swingLow := 100.0
	swingHigh := 200.0
	mid := 50
	for i := 0; i < mid; i++ {
		t := float64(i) / float64(mid)
		price := swingLow + (swingHigh-swingLow)*t
		candles[i] = newTestCandle(price, price, price, price)
		candles[i].OpenTime = int64(i * 60000)
	}
	candles[0].High = swingLow
	candles[0].Low = swingLow
	candles[mid-1].High = swingHigh
	candles[mid-1].Low = swingHigh
	// Set remaining candles exactly at 0.5 level = 150
	for i := mid; i < 100; i++ {
		candles[i] = newTestCandle(150, 150, 150, 150)
		candles[i].OpenTime = int64(i * 60000)
	}

	fib := ComputeFibonacciContext(candles, domain.SideLong, 100, 1.0)

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
	swingLow := 100.0
	swingHigh := 200.0
	// price above 0.5 = 150 -> should be above_pullback_zone
	candles := buildFibonacciRange(swingLow, swingHigh, 155, 80)
	// Override last candle close to be above fib_0_5
	candles[len(candles)-1] = newTestCandle(180, 181, 179, 180)
	candles[len(candles)-1].OpenTime = int64((len(candles) - 1) * 60000)

	fib := ComputeFibonacciContext(candles, domain.SideLong, 80, 1.0)

	if !fib.Valid {
		t.Fatalf("expected valid, got %v", fib.ReasonCodes)
	}
	if fib.ZoneLabel != "above_pullback_zone" {
		t.Errorf("expected above_pullback_zone for price near swing high, got %s", fib.ZoneLabel)
	}
}

func TestFibonacci_BelowPullbackZone(t *testing.T) {
	swingLow := 100.0
	swingHigh := 200.0
	// price below 0.786 = 100 + 100*(1-0.786) = 100 + 21.4 = 121.4... wait
	// For LONG: fib_0_786 = swingHigh - range*0.786 = 200 - 100*0.786 = 200 - 78.6 = 121.4
	// below_pullback_zone = price < 121.4
	candles := buildFibonacciRange(swingLow, swingHigh, 110, 80)
	candles[len(candles)-1] = newTestCandle(115, 116, 114, 115)
	candles[len(candles)-1].OpenTime = int64((len(candles) - 1) * 60000)

	fib := ComputeFibonacciContext(candles, domain.SideLong, 80, 1.0)

	if !fib.Valid {
		t.Fatalf("expected valid, got %v", fib.ReasonCodes)
	}
	if fib.ZoneLabel != "below_pullback_zone" {
		t.Errorf("expected below_pullback_zone for deep pullback, got %s", fib.ZoneLabel)
	}
}

func TestFibonacci_UnconfirmedCandle_Ignored(t *testing.T) {
	// 81 candles: last is unconfirmed with an anomalous price far above any fib zone.
	candles := buildFibonacciRange(100, 200, 161.8, 81)
	candles[80] = newTestCandle(9999, 9999, 9999, 9999)
	candles[80].OpenTime = int64(80 * 60000)
	candles[80].Confirmed = false

	fib := ComputeFibonacciContext(candles, domain.SideLong, 80, 1.0)

	if !fib.Valid {
		t.Fatalf("expected valid despite unconfirmed latest candle, got reason_codes=%v", fib.ReasonCodes)
	}
	// CurrentPrice must be from the last confirmed candle, not the unconfirmed 9999.
	if fib.CurrentPrice > 5000 {
		t.Errorf("expected current price from confirmed candle, got %f", fib.CurrentPrice)
	}
}

func TestFibonacci_InsufficientClosedCandles_ReturnsInvalid(t *testing.T) {
	candles := make([]domain.Candle, 100)
	allConfirmed := 30
	for i := 0; i < allConfirmed; i++ {
		candles[i] = newTestCandle(100, float64(100+i), float64(100+i-1), float64(100+i))
		candles[i].OpenTime = int64(i * 60000)
	}
	for i := allConfirmed; i < 100; i++ {
		candles[i] = newTestCandle(150, 155, 145, 152)
		candles[i].OpenTime = int64(i * 60000)
		candles[i].Confirmed = false
	}

	fib := ComputeFibonacciContext(candles, domain.SideLong, 80, 1.0)

	if fib.Valid {
		t.Fatal("expected invalid for insufficient closed candles")
	}
	found := false
	for _, r := range fib.ReasonCodes {
		if r == "insufficient_closed_candles" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected insufficient_closed_candles reason, got %v", fib.ReasonCodes)
	}
}
