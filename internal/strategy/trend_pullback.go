package strategy

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
)

// TrendPullbackInput contains snapshots and candles for trend-pullback evaluation.
type TrendPullbackInput struct {
	Now                  time.Time
	Symbol               string
	Timeframe            string
	TickSize             float64
	Candles15m           []domain.Candle
	Snapshot15m          indicator.IndicatorSnapshot
	Snapshot1h           indicator.IndicatorSnapshot
	Snapshot4h           indicator.IndicatorSnapshot
	IndicatorSnapshotRef string
	RegimeSnapshotRef    string
}

// GenerateTrendPullback evaluates long/short and returns the first valid candidate.
func GenerateTrendPullback(in TrendPullbackInput) (*TradeCandidate, *RejectedCandidate) {
	if c, rej := generateTrendPullbackForSide(in, domain.SideLong); c != nil {
		return c, nil
	} else if rej != nil {
		// keep for fallback if short also fails
		if c2, _ := generateTrendPullbackForSide(in, domain.SideShort); c2 != nil {
			return c2, nil
		}
		return nil, rej
	}

	if c, rej := generateTrendPullbackForSide(in, domain.SideShort); c != nil {
		return c, nil
	} else if rej != nil {
		return nil, rej
	}
	return nil, nil
}

func generateTrendPullbackForSide(in TrendPullbackInput, side domain.Side) (*TradeCandidate, *RejectedCandidate) {
	if len(in.Candles15m) < 30 {
		rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, "insufficient 15m candles (<30)")
		return nil, &rej
	}
	if in.Snapshot15m.ATR14 <= 0 {
		rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, "ATR14 unavailable")
		return nil, &rej
	}

	last := in.Candles15m[len(in.Candles15m)-1]
	atr := in.Snapshot15m.ATR14

	// 1) HTF trend supportive (1h alignment OR structure)
	htfOK := false
	if side == domain.SideLong {
		htfOK = (in.Snapshot1h.EMAAlignment.PriceAboveEMA50 && in.Snapshot1h.EMAAlignment.EMA20AboveEMA50) || hasHHHL(in.Snapshot1h.RecentSwings)
	} else {
		htfOK = (!in.Snapshot1h.EMAAlignment.PriceAboveEMA50 && !in.Snapshot1h.EMAAlignment.EMA20AboveEMA50) || hasLHLH(in.Snapshot1h.RecentSwings)
	}
	if !htfOK {
		rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, "HTF trend support condition failed")
		return nil, &rej
	}

	// 2) 4h not strongly opposite
	if side == domain.SideLong {
		if !in.Snapshot4h.EMAAlignment.PriceAboveEMA50 && !in.Snapshot4h.EMAAlignment.EMA20AboveEMA50 {
			rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, "4h strongly bearish")
			return nil, &rej
		}
	} else {
		if in.Snapshot4h.EMAAlignment.PriceAboveEMA50 && in.Snapshot4h.EMAAlignment.EMA20AboveEMA50 {
			rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, "4h strongly bullish")
			return nil, &rej
		}
	}

	// 3) pullback present
	pullbackLow, pullbackHigh := localPullbackRange(in.Candles15m, 5)
	pullbackOK := false
	if side == domain.SideLong {
		pullbackOK = distance(last.Close, in.Snapshot15m.EMA20) <= 0.5*atr ||
			distance(last.Close, in.Snapshot15m.EMA50) <= 0.5*atr ||
			nearLevel(last.Close, in.Snapshot15m.SupportLevels, 0.5*atr)
	} else {
		pullbackOK = distance(last.Close, in.Snapshot15m.EMA20) <= 0.5*atr ||
			distance(last.Close, in.Snapshot15m.EMA50) <= 0.5*atr ||
			nearLevel(last.Close, in.Snapshot15m.ResistanceLevels, 0.5*atr)
	}
	if !pullbackOK {
		rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, "pullback distance condition failed")
		return nil, &rej
	}

	// 4) reaction
	freshReaction := false
	reactionOK := false
	if side == domain.SideLong {
		reaction1 := bullishRejection(last)
		reaction2 := bullishBodyConfirmation(last, pullbackLow)
		reaction3 := higherLowWithinLastCandles(in.Candles15m, 5)
		reactionOK = reaction1 || reaction2 || reaction3
		freshReaction = reaction1 || reaction2
	} else {
		reaction1 := bearishRejection(last)
		reaction2 := bearishBodyConfirmation(last, pullbackHigh)
		reaction3 := lowerHighWithinLastCandles(in.Candles15m, 5)
		reactionOK = reaction1 || reaction2 || reaction3
		freshReaction = reaction1 || reaction2
	}
	if !reactionOK {
		rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, "reaction candle/structure condition failed")
		return nil, &rej
	}

	// 5) momentum not exhausted
	histNow, histPrev, okHist := macdHistCurrentPrev(in.Candles15m, 12, 26, 9)
	if !okHist {
		rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, "MACD histogram unavailable")
		return nil, &rej
	}
	if side == domain.SideLong {
		if in.Snapshot15m.RSI14 < 38 || in.Snapshot15m.RSI14 > 68 {
			rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, fmt.Sprintf("RSI 14 = %.2f out of range [38,68]", in.Snapshot15m.RSI14))
			return nil, &rej
		}
		if histNow <= histPrev {
			rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, "MACD histogram not improving")
			return nil, &rej
		}
	} else {
		if in.Snapshot15m.RSI14 < 32 || in.Snapshot15m.RSI14 > 62 {
			rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, fmt.Sprintf("RSI 14 = %.2f out of range [32,62]", in.Snapshot15m.RSI14))
			return nil, &rej
		}
		if histNow >= histPrev {
			rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, "MACD histogram not weakening")
			return nil, &rej
		}
	}

	// 6) volume not collapsed
	if in.Snapshot15m.VolumeCurrent < 0.7*in.Snapshot15m.VolumeMA20 {
		rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe,
			fmt.Sprintf("volume %.4f < 0.7 * volume_ma20 %.4f", in.Snapshot15m.VolumeCurrent, in.Snapshot15m.VolumeMA20))
		return nil, &rej
	}

	entry := last.Close
	entryType := CandidateEntryMarket
	if !freshReaction {
		entryType = CandidateEntryLimitRetest
		if side == domain.SideLong {
			entry = pullbackLow + tick(in.TickSize)
		} else {
			entry = pullbackHigh - tick(in.TickSize)
		}
	}

	recentSwing := latestSwingByType(in.Snapshot15m.RecentSwings, side)
	var sl float64
	if side == domain.SideLong {
		sl = math.Min(recentSwing, entry-1.5*atr)
		if sl <= 0 || sl >= entry {
			sl = entry - 1.5*atr
		}
	} else {
		sl = math.Max(recentSwing, entry+1.5*atr)
		if sl <= entry {
			sl = entry + 1.5*atr
		}
	}

	riskDist := math.Abs(entry - sl)
	if riskDist <= 0 {
		rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, "invalid risk distance")
		return nil, &rej
	}

	tp1Fallback, tp2Fallback := entryRiskTargets(side, entry, riskDist, 1.5, 2.5)
	tp1 := tp1Fallback
	tp2 := tp2Fallback
	if side == domain.SideLong {
		tp1 = closerPositive(entry, tp1Fallback, nearestAbove(entry, in.Snapshot15m.ResistanceLevels))
		nextRes := nextAbove(tp1, in.Snapshot15m.ResistanceLevels)
		if nextRes > 0 {
			tp2 = nextRes
		}
		if tp2 <= tp1 {
			tp2 = tp2Fallback
		}
	} else {
		tp1 = closerNegative(entry, tp1Fallback, nearestBelow(entry, in.Snapshot15m.SupportLevels))
		nextSup := nextBelow(tp1, in.Snapshot15m.SupportLevels)
		if nextSup > 0 {
			tp2 = nextSup
		}
		if tp2 >= tp1 {
			tp2 = tp2Fallback
		}
	}

	rr := math.Abs(tp1-entry) / riskDist
	if rr < 1.4 {
		// If nearest level is too close, fall back to the 1.5R target.
		tp1 = tp1Fallback
		if side == domain.SideLong && tp2 <= tp1 {
			tp2 = tp2Fallback
		}
		if side == domain.SideShort && tp2 >= tp1 {
			tp2 = tp2Fallback
		}
		rr = math.Abs(tp1-entry) / riskDist
	}
	if rr < 1.4 {
		rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, fmt.Sprintf("RR %.2f < 1.4", rr))
		return nil, &rej
	}

	id, err := NewCandidateID()
	if err != nil {
		rej := BuildRejectedCandidate(StrategyTrendPullback, side, in.Symbol, in.Timeframe, "failed to generate candidate id")
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
		Strategy:          StrategyTrendPullback,
		Side:              side,
		EntryType:         entryType,
		EntryPrice:        entry,
		StopLoss:          sl,
		TakeProfits:       []TakeProfitTarget{{Price: tp1, SizePct: 50}, {Price: tp2, SizePct: 50}},
		RiskRewardRatio:   rr,
		InvalidationLevel: sl,
		TAReasoning: TAReasoning{
			TrendContext: htfReason(side, in.Snapshot1h, in.Snapshot4h),
			Trigger:      fmt.Sprintf("pullback within 0.5 ATR (ATR=%.4f)", atr),
			Confirmation: fmt.Sprintf("reaction confirmed, RSI=%.2f, MACD hist %.6f -> %.6f", in.Snapshot15m.RSI14, histPrev, histNow),
			Invalidation: fmt.Sprintf("invalid if price breaches %.6f", sl),
		},
		InputsSnapshot: SnapshotRefs{
			IndicatorSnapshotRef: in.IndicatorSnapshotRef,
			RegimeSnapshotRef:    in.RegimeSnapshotRef,
		},
	}
	return cand, nil
}

func htfReason(side domain.Side, s1h, s4h indicator.IndicatorSnapshot) string {
	if side == domain.SideLong {
		return fmt.Sprintf("1h/4h bullish bias check: 1h price>ema50=%t ema20>ema50=%t, 4h bearish_override=%t", s1h.EMAAlignment.PriceAboveEMA50, s1h.EMAAlignment.EMA20AboveEMA50, (!s4h.EMAAlignment.PriceAboveEMA50 && !s4h.EMAAlignment.EMA20AboveEMA50))
	}
	return fmt.Sprintf("1h/4h bearish bias check: 1h price<ema50=%t ema20<ema50=%t, 4h bullish_override=%t", !s1h.EMAAlignment.PriceAboveEMA50, !s1h.EMAAlignment.EMA20AboveEMA50, (s4h.EMAAlignment.PriceAboveEMA50 && s4h.EMAAlignment.EMA20AboveEMA50))
}

func hasHHHL(swings []indicator.SwingPoint) bool {
	highs := make([]float64, 0)
	lows := make([]float64, 0)
	for _, s := range swings {
		if s.Type == "high" {
			highs = append(highs, s.Price)
		}
		if s.Type == "low" {
			lows = append(lows, s.Price)
		}
	}
	if len(highs) < 3 || len(lows) < 3 {
		return false
	}
	h := highs[len(highs)-3:]
	l := lows[len(lows)-3:]
	return h[0] < h[1] && h[1] < h[2] && l[0] < l[1] && l[1] < l[2]
}

func hasLHLH(swings []indicator.SwingPoint) bool {
	highs := make([]float64, 0)
	lows := make([]float64, 0)
	for _, s := range swings {
		if s.Type == "high" {
			highs = append(highs, s.Price)
		}
		if s.Type == "low" {
			lows = append(lows, s.Price)
		}
	}
	if len(highs) < 3 || len(lows) < 3 {
		return false
	}
	h := highs[len(highs)-3:]
	l := lows[len(lows)-3:]
	return h[0] > h[1] && h[1] > h[2] && l[0] > l[1] && l[1] > l[2]
}

func localPullbackRange(candles []domain.Candle, lookback int) (low, high float64) {
	if len(candles) == 0 {
		return 0, 0
	}
	if lookback <= 0 {
		lookback = 5
	}
	start := len(candles) - lookback
	if start < 0 {
		start = 0
	}
	low = candles[start].Low
	high = candles[start].High
	for i := start; i < len(candles); i++ {
		if candles[i].Low < low {
			low = candles[i].Low
		}
		if candles[i].High > high {
			high = candles[i].High
		}
	}
	return low, high
}

func distance(a, b float64) float64 {
	return math.Abs(a - b)
}

func nearLevel(price float64, levels []float64, tol float64) bool {
	for _, lvl := range levels {
		if math.Abs(price-lvl) <= tol {
			return true
		}
	}
	return false
}

func bullishRejection(c domain.Candle) bool {
	body := math.Abs(c.Close - c.Open)
	if body == 0 {
		return false
	}
	lowerWick := math.Min(c.Open, c.Close) - c.Low
	return c.Close > c.Open && lowerWick > 1.5*body
}

func bearishRejection(c domain.Candle) bool {
	body := math.Abs(c.Close - c.Open)
	if body == 0 {
		return false
	}
	upperWick := c.High - math.Max(c.Open, c.Close)
	return c.Close < c.Open && upperWick > 1.5*body
}

func bullishBodyConfirmation(c domain.Candle, pullbackLow float64) bool {
	rng := c.High - c.Low
	if rng <= 0 {
		return false
	}
	closePos := (c.Close - c.Low) / rng
	return c.Close > c.Open && closePos >= 0.60 && c.Close > pullbackLow
}

func bearishBodyConfirmation(c domain.Candle, pullbackHigh float64) bool {
	rng := c.High - c.Low
	if rng <= 0 {
		return false
	}
	closePos := (c.Close - c.Low) / rng
	return c.Close < c.Open && closePos <= 0.40 && c.Close < pullbackHigh
}

func higherLowWithinLastCandles(candles []domain.Candle, lookback int) bool {
	if len(candles) < lookback+1 {
		return false
	}
	start := len(candles) - lookback - 1
	if start < 0 {
		start = 0
	}
	prevLow := candles[start].Low
	for i := start; i < len(candles)-1; i++ {
		if candles[i].Low < prevLow {
			prevLow = candles[i].Low
		}
	}
	return candles[len(candles)-1].Low > prevLow
}

func lowerHighWithinLastCandles(candles []domain.Candle, lookback int) bool {
	if len(candles) < lookback+1 {
		return false
	}
	start := len(candles) - lookback - 1
	if start < 0 {
		start = 0
	}
	prevHigh := candles[start].High
	for i := start; i < len(candles)-1; i++ {
		if candles[i].High > prevHigh {
			prevHigh = candles[i].High
		}
	}
	return candles[len(candles)-1].High < prevHigh
}

func macdHistCurrentPrev(candles []domain.Candle, fast, slow, signal int) (float64, float64, bool) {
	need := slow + signal + 2
	if len(candles) < need {
		return 0, 0, false
	}
	closes := make([]float64, len(candles))
	for i := range candles {
		closes[i] = candles[i].Close
	}
	fastEMA := emaSeries(closes, fast)
	slowEMA := emaSeries(closes, slow)
	macd := make([]float64, len(candles))
	for i := range macd {
		macd[i] = fastEMA[i] - slowEMA[i]
	}
	sig := emaSeries(macd, signal)
	hCurr := macd[len(macd)-1] - sig[len(sig)-1]
	hPrev := macd[len(macd)-2] - sig[len(sig)-2]
	return hCurr, hPrev, true
}

func latestSwingByType(swings []indicator.SwingPoint, side domain.Side) float64 {
	if len(swings) == 0 {
		return 0
	}
	want := "low"
	if side == domain.SideShort {
		want = "high"
	}
	for i := len(swings) - 1; i >= 0; i-- {
		if swings[i].Type == want {
			return swings[i].Price
		}
	}
	return 0
}

func entryRiskTargets(side domain.Side, entry, risk, rr1, rr2 float64) (float64, float64) {
	if side == domain.SideLong {
		return entry + rr1*risk, entry + rr2*risk
	}
	return entry - rr1*risk, entry - rr2*risk
}

func nearestAbove(price float64, levels []float64) float64 {
	best := 0.0
	for _, lvl := range levels {
		if lvl > price && (best == 0 || lvl < best) {
			best = lvl
		}
	}
	return best
}

func nextAbove(anchor float64, levels []float64) float64 {
	cands := make([]float64, 0)
	for _, lvl := range levels {
		if lvl > anchor {
			cands = append(cands, lvl)
		}
	}
	if len(cands) == 0 {
		return 0
	}
	sort.Float64s(cands)
	return cands[0]
}

func nearestBelow(price float64, levels []float64) float64 {
	best := 0.0
	for _, lvl := range levels {
		if lvl < price && (best == 0 || lvl > best) {
			best = lvl
		}
	}
	return best
}

func nextBelow(anchor float64, levels []float64) float64 {
	cands := make([]float64, 0)
	for _, lvl := range levels {
		if lvl < anchor {
			cands = append(cands, lvl)
		}
	}
	if len(cands) == 0 {
		return 0
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(cands)))
	return cands[0]
}

func closerPositive(entry, fallback, level float64) float64 {
	if level <= entry {
		return fallback
	}
	if math.Abs(level-entry) < math.Abs(fallback-entry) {
		return level
	}
	return fallback
}

func closerNegative(entry, fallback, level float64) float64 {
	if level >= entry || level == 0 {
		return fallback
	}
	if math.Abs(entry-level) < math.Abs(entry-fallback) {
		return level
	}
	return fallback
}

func tick(tickSize float64) float64 {
	if tickSize > 0 {
		return tickSize
	}
	return 0.0001
}

func emaSeries(values []float64, period int) []float64 {
	out := make([]float64, len(values))
	if len(values) == 0 {
		return out
	}
	if period <= 1 {
		copy(out, values)
		return out
	}
	warm := period
	if warm > len(values) {
		warm = len(values)
	}
	sum := 0.0
	for i := 0; i < warm; i++ {
		sum += values[i]
	}
	ema := sum / float64(warm)
	for i := 0; i < warm; i++ {
		out[i] = ema
	}
	m := 2.0 / float64(period+1)
	for i := warm; i < len(values); i++ {
		ema = (values[i]-ema)*m + ema
		out[i] = ema
	}
	return out
}
