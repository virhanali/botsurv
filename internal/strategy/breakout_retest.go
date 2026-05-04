package strategy

import (
	"fmt"
	"math"
	"time"

	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
)

// BreakoutRetestInput contains snapshots and candles for breakout-retest evaluation.
type BreakoutRetestInput struct {
	Now                  time.Time
	Symbol               string
	Timeframe            string
	TickSize             float64
	Candles15m           []domain.Candle
	Snapshot15m          indicator.IndicatorSnapshot
	Snapshot1h           indicator.IndicatorSnapshot
	IndicatorSnapshotRef string
	RegimeSnapshotRef    string
}

// GenerateBreakoutRetest evaluates both sides and returns one candidate when valid.
func GenerateBreakoutRetest(in BreakoutRetestInput) (*TradeCandidate, *RejectedCandidate) {
	if c, rej := generateBreakoutRetestForSide(in, domain.SideLong); c != nil {
		return c, nil
	} else if rej != nil {
		if c2, _ := generateBreakoutRetestForSide(in, domain.SideShort); c2 != nil {
			return c2, nil
		}
		return nil, rej
	}
	if c, rej := generateBreakoutRetestForSide(in, domain.SideShort); c != nil {
		return c, nil
	} else if rej != nil {
		return nil, rej
	}
	return nil, nil
}

func generateBreakoutRetestForSide(in BreakoutRetestInput, side domain.Side) (*TradeCandidate, *RejectedCandidate) {
	if len(in.Candles15m) < 30 {
		rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, "insufficient 15m candles (<30)")
		return nil, &rej
	}
	atr := in.Snapshot15m.ATR14
	if atr <= 0 {
		rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, "ATR14 unavailable")
		return nil, &rej
	}

	level, breakoutIdx, priorLevel, found := findBreakout(in.Candles15m, in.Snapshot15m, side, atr)
	if !found {
		rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, "no valid breakout in last 8 candles")
		return nil, &rej
	}

	retestFound, retestExtreme := findRetest(in.Candles15m, breakoutIdx, level, atr, side)
	if !retestFound {
		rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, "retest condition failed")
		return nil, &rej
	}

	last := in.Candles15m[len(in.Candles15m)-1]
	if side == domain.SideLong {
		if last.Close < level {
			rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, "last candle closed below broken resistance")
			return nil, &rej
		}
		if !(last.Close > last.Open && last.Close > level) {
			rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, "bullish confirmation missing")
			return nil, &rej
		}
		if !in.Snapshot1h.EMAAlignment.PriceAboveEMA50 && !in.Snapshot1h.EMAAlignment.EMA20AboveEMA50 {
			rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, "1h strongly bearish against long breakout")
			return nil, &rej
		}
	} else {
		if last.Close > level {
			rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, "last candle closed above broken support")
			return nil, &rej
		}
		if !(last.Close < last.Open && last.Close < level) {
			rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, "bearish confirmation missing")
			return nil, &rej
		}
		if in.Snapshot1h.EMAAlignment.PriceAboveEMA50 && in.Snapshot1h.EMAAlignment.EMA20AboveEMA50 {
			rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, "1h strongly bullish against short breakout")
			return nil, &rej
		}
	}

	entryType := CandidateEntryMarket
	entry := last.Close
	if math.Abs(last.Close-level) > 0.4*atr {
		entryType = CandidateEntryLimitRetest
		if side == domain.SideLong {
			entry = level + tick(in.TickSize)
		} else {
			entry = level - tick(in.TickSize)
		}
	}

	var sl float64
	stopATR := QualityStopMultiplier(0.5, CountTradeActivity(in.Candles15m, 20), in.Snapshot15m.ATRPct, in.Snapshot15m.VolumeRatio)
	if side == domain.SideLong {
		sl = math.Min(retestExtreme, level-stopATR*atr)
		if sl >= entry {
			sl = entry - stopATR*atr
		}
	} else {
		sl = math.Max(retestExtreme, level+stopATR*atr)
		if sl <= entry {
			sl = entry + stopATR*atr
		}
	}

	riskDist := math.Abs(entry - sl)
	if riskDist <= 0 {
		rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, "invalid risk distance")
		return nil, &rej
	}

	var tp1 float64
	if side == domain.SideLong {
		next := nearestAbove(entry, in.Snapshot15m.ResistanceLevels)
		projected := level + (level - priorLevel)
		if next > entry {
			tp1 = next
		} else {
			tp1 = projected
		}
		if tp1 <= entry {
			tp1 = entry + 1.5*riskDist
		}
	} else {
		next := nearestBelow(entry, in.Snapshot15m.SupportLevels)
		projected := level - (priorLevel - level)
		if next > 0 && next < entry {
			tp1 = next
		} else {
			tp1 = projected
		}
		if tp1 >= entry || tp1 == 0 {
			tp1 = entry - 1.5*riskDist
		}
	}

	var tp2 float64
	if side == domain.SideLong {
		tp2 = entry + 1.5*(tp1-entry)
		if tp2 <= tp1 {
			tp2 = entry + 2.0*(tp1-entry)
		}
	} else {
		tp2 = entry - 1.5*(entry-tp1)
		if tp2 >= tp1 {
			tp2 = entry - 2.0*(entry-tp1)
		}
	}

	// Sanity check: TP/SL must be on correct side of entry
	if side == domain.SideLong {
		if sl >= entry {
			rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, fmt.Sprintf("SL %.6f >= entry %.6f for LONG", sl, entry))
			return nil, &rej
		}
		if tp1 <= entry {
			rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, fmt.Sprintf("TP %.6f <= entry %.6f for LONG", tp1, entry))
			return nil, &rej
		}
	} else {
		if sl <= entry {
			rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, fmt.Sprintf("SL %.6f <= entry %.6f for SHORT", sl, entry))
			return nil, &rej
		}
		if tp1 >= entry {
			rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, fmt.Sprintf("TP %.6f >= entry %.6f for SHORT", tp1, entry))
			return nil, &rej
		}
	}

	rr := math.Abs(tp1-entry) / riskDist
	if rr < 1.4 {
		rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, fmt.Sprintf("RR %.2f < 1.4", rr))
		return nil, &rej
	}

	id, err := NewCandidateID()
	if err != nil {
		rej := BuildRejectedCandidate(StrategyBreakoutRetest, side, in.Symbol, in.Timeframe, "failed to generate candidate id")
		return nil, &rej
	}
	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	cand := &TradeCandidate{
		CandidateID:       id,
		GeneratedAt:       now,
		Symbol:            in.Symbol,
		Timeframe:         in.Timeframe,
		Strategy:          StrategyBreakoutRetest,
		Side:              side,
		EntryType:         entryType,
		EntryPrice:        entry,
		StopLoss:          sl,
		TakeProfits:       []TakeProfitTarget{{Price: tp1, SizePct: 50}, {Price: tp2, SizePct: 50}},
		RiskRewardRatio:   rr,
		InvalidationLevel: sl,
		TAReasoning: TAReasoning{
			TrendContext: fmt.Sprintf("1h anti-trend guard passed for %s", side),
			Trigger:      fmt.Sprintf("breakout level %.6f retested within %.4f ATR", level, 0.3),
			Confirmation: fmt.Sprintf("last candle confirmed direction at close %.6f", last.Close),
			Invalidation: fmt.Sprintf("invalid if level %.6f fails and stop %.6f is breached", level, sl),
		},
		InputsSnapshot: SnapshotRefs{
			IndicatorSnapshotRef: in.IndicatorSnapshotRef,
			RegimeSnapshotRef:    in.RegimeSnapshotRef,
		},
	}
	return cand, nil
}

func findBreakout(candles []domain.Candle, snap indicator.IndicatorSnapshot, side domain.Side, atr float64) (level float64, idx int, priorLevel float64, found bool) {
	start := len(candles) - 8
	if start < 1 {
		start = 1
	}
	if side == domain.SideLong {
		for i := start; i < len(candles); i++ {
			c := candles[i]
			levels := snap.ResistanceLevels
			if len(levels) == 0 {
				levels = []float64{rollingHigh(candles, i, 20)}
			}
			for _, lvl := range levels {
				if c.Close > lvl && volumeRatioAt(candles, i, 20) >= 1.3 && bodyUpper60(c) {
					return lvl, i, nearestBelow(lvl, snap.SupportLevels), true
				}
			}
		}
		return 0, 0, 0, false
	}
	for i := start; i < len(candles); i++ {
		c := candles[i]
		levels := snap.SupportLevels
		if len(levels) == 0 {
			levels = []float64{rollingLow(candles, i, 20)}
		}
		for _, lvl := range levels {
			if c.Close < lvl && volumeRatioAt(candles, i, 20) >= 1.3 && bodyLower40(c) {
				return lvl, i, nearestAbove(lvl, snap.ResistanceLevels), true
			}
		}
	}
	return 0, 0, 0, false
}

func rollingHigh(candles []domain.Candle, idx, lookback int) float64 {
	start := idx - lookback
	if start < 0 {
		start = 0
	}
	high := candles[start].High
	for i := start + 1; i < idx; i++ {
		if candles[i].High > high {
			high = candles[i].High
		}
	}
	return high
}

func rollingLow(candles []domain.Candle, idx, lookback int) float64 {
	start := idx - lookback
	if start < 0 {
		start = 0
	}
	low := candles[start].Low
	for i := start + 1; i < idx; i++ {
		if candles[i].Low < low {
			low = candles[i].Low
		}
	}
	return low
}

func findRetest(candles []domain.Candle, breakoutIdx int, level, atr float64, side domain.Side) (bool, float64) {
	if breakoutIdx >= len(candles)-1 {
		return false, 0
	}
	buf := 0.3 * atr
	if side == domain.SideLong {
		retestLow := math.Inf(1)
		for i := breakoutIdx + 1; i < len(candles); i++ {
			c := candles[i]
			if math.Abs(c.Low-level) <= buf || (c.Low <= level+buf && c.High >= level-buf) {
				if c.Low < retestLow {
					retestLow = c.Low
				}
			}
		}
		if math.IsInf(retestLow, 1) {
			return false, 0
		}
		return true, retestLow
	}
	retestHigh := math.Inf(-1)
	for i := breakoutIdx + 1; i < len(candles); i++ {
		c := candles[i]
		if math.Abs(c.High-level) <= buf || (c.High >= level-buf && c.Low <= level+buf) {
			if c.High > retestHigh {
				retestHigh = c.High
			}
		}
	}
	if math.IsInf(retestHigh, -1) {
		return false, 0
	}
	return true, retestHigh
}

func volumeRatioAt(candles []domain.Candle, idx, period int) float64 {
	if idx <= 0 || idx >= len(candles) {
		return 0
	}
	if period <= 0 {
		period = 20
	}
	start := idx - period
	if start < 0 {
		start = 0
	}
	if start == idx {
		return 0
	}
	sum := 0.0
	for i := start; i < idx; i++ {
		sum += candles[i].Volume
	}
	ma := sum / float64(idx-start)
	if ma <= 0 {
		return 0
	}
	return candles[idx].Volume / ma
}

func bodyUpper60(c domain.Candle) bool {
	rng := c.High - c.Low
	if rng <= 0 {
		return false
	}
	pos := (c.Close - c.Low) / rng
	return c.Close > c.Open && pos >= 0.60
}

func bodyLower40(c domain.Candle) bool {
	rng := c.High - c.Low
	if rng <= 0 {
		return false
	}
	pos := (c.Close - c.Low) / rng
	return c.Close < c.Open && pos <= 0.40
}
