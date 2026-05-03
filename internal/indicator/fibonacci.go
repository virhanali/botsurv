package indicator

import (
	"math"

	"github.com/virhan/botsurv/internal/domain"
)

// FibonacciContext holds deterministic Fibonacci retracement levels
// for pullback context evaluation.
type FibonacciContext struct {
	SwingHigh               float64  `json:"swing_high"`
	SwingLow                float64  `json:"swing_low"`
	ImpulseDirection        string   `json:"impulse_direction"`
	Fib05                   float64  `json:"fib_0_5"`
	Fib0618                 float64  `json:"fib_0_618"`
	Fib0786                 float64  `json:"fib_0_786"`
	CurrentPrice            float64  `json:"current_price"`
	ZoneLabel               string   `json:"zone_label"`
	DistanceToNearestFibPct float64  `json:"distance_to_nearest_fib_pct"`
	Valid                   bool     `json:"valid"`
	ReasonCodes             []string `json:"reason_codes"`
}

// fibLevel holds a single Fibonacci retracement level for zone matching.
type fibLevel struct {
	price float64
	name  string
}

// ComputeFibonacciContext computes Fibonacci retracement context from recent closed candles.
//
// For LONG pullback context: evaluates retracement from swing_low -> swing_high.
//   - Impulse direction is "up".
//   - Fibonacci levels are measured downward from swing_high.
//
// For SHORT pullback context: evaluates retracement from swing_high -> swing_low.
//   - Impulse direction is "down".
//   - Fibonacci levels are measured upward from swing_low.
//
// zone_tolerance_pct is the percentage tolerance for "near" a fib level (e.g. 1.0 = 1%).
func ComputeFibonacciContext(candles []domain.Candle, side domain.Side, lookback int, zoneTolerancePct float64) FibonacciContext {
	if lookback <= 0 {
		lookback = 80
	}
	if zoneTolerancePct < 0 {
		zoneTolerancePct = 0
	}

	var confirmed []domain.Candle
	for _, c := range candles {
		if c.Confirmed {
			confirmed = append(confirmed, c)
		}
	}

	if len(confirmed) < lookback {
		return FibonacciContext{
			Valid:       false,
			ReasonCodes: []string{"insufficient_closed_candles"},
		}
	}

	if err := validateCandles(confirmed); err != nil {
		return FibonacciContext{
			Valid:       false,
			ReasonCodes: []string{"invalid_candles"},
		}
	}

	start := len(confirmed) - lookback
	window := confirmed[start:]

	swingHigh := 0.0
	swingLow := math.MaxFloat64
	for _, c := range window {
		if c.High > swingHigh {
			swingHigh = c.High
		}
		if c.Low < swingLow {
			swingLow = c.Low
		}
	}

	if swingHigh <= 0 || swingLow <= 0 || swingHigh <= swingLow || math.IsNaN(swingHigh) || math.IsInf(swingHigh, 0) || math.IsNaN(swingLow) || math.IsInf(swingLow, 0) {
		return FibonacciContext{
			Valid:       false,
			ReasonCodes: []string{"flat_range"},
		}
	}

	currentPrice := window[len(window)-1].Close
	if currentPrice <= 0 || math.IsNaN(currentPrice) || math.IsInf(currentPrice, 0) {
		return FibonacciContext{
			Valid:       false,
			ReasonCodes: []string{"invalid_current_price"},
		}
	}

	rangeVal := swingHigh - swingLow

	var fib05, fib0618, fib0786 float64
	var impulseDirection string

	if side == domain.SideLong {
		impulseDirection = "up"
		fib05 = swingHigh - rangeVal*0.5
		fib0618 = swingHigh - rangeVal*0.618
		fib0786 = swingHigh - rangeVal*0.786
	} else {
		impulseDirection = "down"
		fib05 = swingLow + rangeVal*0.5
		fib0618 = swingLow + rangeVal*0.618
		fib0786 = swingLow + rangeVal*0.786
	}

	zoneLabel, distPct := assignFibZone(currentPrice, fib05, fib0618, fib0786, side, zoneTolerancePct)

	return FibonacciContext{
		SwingHigh:               round2(swingHigh),
		SwingLow:                round2(swingLow),
		ImpulseDirection:        impulseDirection,
		Fib05:                   round2(fib05),
		Fib0618:                 round2(fib0618),
		Fib0786:                 round2(fib0786),
		CurrentPrice:            round2(currentPrice),
		ZoneLabel:               zoneLabel,
		DistanceToNearestFibPct: round2(distPct),
		Valid:                   true,
		ReasonCodes:             nil,
	}
}

// assignFibZone determines the zone label based on current price position
// relative to Fibonacci levels and tolerance.
func assignFibZone(price float64, fib05, fib0618, fib0786 float64, side domain.Side, tolerancePct float64) (string, float64) {
	levels := []fibLevel{
		{price: fib05, name: "in_0_5_zone"},
		{price: fib0618, name: "in_0_618_zone"},
		{price: fib0786, name: "in_0_786_zone"},
	}

	nearest := ""
	minDistAbs := math.MaxFloat64
	minDistPct := 0.0
	for _, l := range levels {
		dist := math.Abs(price - l.price)
		distPct := dist / price * 100
		if dist < minDistAbs {
			minDistAbs = dist
			minDistPct = distPct
			nearest = l.name
		}
	}

	if minDistPct <= tolerancePct {
		return nearest, minDistPct
	}

	// Price is outside tolerance of any level — determine relative position.
	if side == domain.SideLong {
		// fib05 is highest, fib0786 is lowest
		if price > fib05 {
			return "above_pullback_zone", minDistPct
		}
		if price < fib0786 {
			return "below_pullback_zone", minDistPct
		}
	} else {
		// fib0786 is highest, fib05 is lowest
		if price > fib0786 {
			return "above_pullback_zone", minDistPct
		}
		if price < fib05 {
			return "below_pullback_zone", minDistPct
		}
	}

	return "unknown", minDistPct
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
