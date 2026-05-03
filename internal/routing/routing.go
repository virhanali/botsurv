package routing

import (
	"math"
	"sort"

	"github.com/virhan/botsurv/internal/indicator"
	"github.com/virhan/botsurv/internal/regime"
	"github.com/virhan/botsurv/internal/scoring"
	"github.com/virhan/botsurv/internal/strategy"
)

// AmbiguityFlags describe why a candidate is considered ambiguous.
type AmbiguityFlags struct {
	NearResistance               bool `json:"near_resistance"`
	NearSupport                  bool `json:"near_support"`
	BTCNearResistance            bool `json:"btc_near_resistance"`
	BTCNearSupport               bool `json:"btc_near_support"`
	TargetOutperformingShortTerm bool `json:"target_outperforming_short_term_only"`
	MixedTimeframeTrend          bool `json:"mixed_timeframe"`
	VolumeNotStrong              bool `json:"volume_not_strong"`
	Overextended                 bool `json:"overextended"`
	MarketRegimeMixed            bool `json:"market_regime_mixed"`
	EntryChasing                 bool `json:"entry_chasing_risk"`
	RSMismatchShortTerm          bool `json:"rs_mismatch_short_term"`
}

// Route describes what to do with a candidate.
type Route string

const (
	RouteRejectPreLLM Route = "REJECT_PRE_LLM"
	RouteLLMVeto      Route = "LLM_VETO_REQUIRED"
	RouteSkipLLM      Route = "SKIP_LLM_HIGH_SCORE"
)

// RouteResult is the output of the routing engine.
type RouteResult struct {
	Route        Route          `json:"route"`
	Flags        AmbiguityFlags `json:"flags"`
	Score        float64        `json:"score"`
	Reason       string         `json:"reason"`
	FlagCount    int            `json:"flag_count"`
	LLMCandidate bool           `json:"llm_candidate"`
}

// RoutingEngine decides LLM routing for a candidate.
type RoutingEngine struct {
	MinScoreLLM         float64
	MinScoreSkipLLM     float64
	MaxLLMCallsPerCycle int
}

// DefaultRoutingEngine returns an engine with sensible defaults.
func DefaultRoutingEngine() *RoutingEngine {
	return &RoutingEngine{
		MinScoreLLM:     58,
		MinScoreSkipLLM: 85,
	}
}

// DetectAmbiguity inspects a candidate and returns flags.
func DetectAmbiguity(
	snap15m indicator.IndicatorSnapshot,
	snap1h indicator.IndicatorSnapshot,
	rs regime.MarketRegimeSnapshot,
	score scoring.ScoreResult,
	tc strategy.TradeCandidate,
) AmbiguityFlags {
	var f AmbiguityFlags

	// Near support/resistance: entry close to levels (within 1 ATR)
	atr := snap15m.ATR14
	if atr > 0 && tc.EntryPrice > 0 {
		for _, res := range snap15m.ResistanceLevels {
			if math.Abs(tc.EntryPrice-res)/tc.EntryPrice < atr/tc.EntryPrice*1.5 {
				f.NearResistance = true
				break
			}
		}
		for _, sup := range snap15m.SupportLevels {
			if math.Abs(tc.EntryPrice-sup)/tc.EntryPrice < atr/tc.EntryPrice*1.5 {
				f.NearSupport = true
				break
			}
		}
	}

	// BTC near resistance/support from regime filters
	for _, filter := range rs.BTCFiltersTriggered {
		switch filter {
		case "BTCNearMajorResistance":
			f.BTCNearResistance = true
		case "BTCNearMajorSupport":
			f.BTCNearSupport = true
		}
	}

	// Target outperforming short-term only: RS 1h positive but 4h/24h negative
	if rs.RelativeStrength.RS1H > 0.5 && rs.RelativeStrength.RS4H < -0.5 {
		f.TargetOutperformingShortTerm = true
		f.RSMismatchShortTerm = true
	}

	// Mixed timeframe trend: EMA alignment differs across timeframes
	ema15m := snap15m.EMAAlignment
	ema1h := snap1h.EMAAlignment
	if ema15m.PriceAboveEMA50 != ema1h.PriceAboveEMA50 {
		f.MixedTimeframeTrend = true
	}

	// Volume not strongly convincing: ratio > 1.0 but < 1.5
	if snap15m.VolumeRatio > 1.0 && snap15m.VolumeRatio < 1.5 {
		f.VolumeNotStrong = true
	}

	// Overextended: breakout extension > 1.5 ATR from level
	if atr > 0 && math.Abs(tc.EntryPrice-snap15m.LastClosePrice) > atr*1.5 {
		f.Overextended = true
	}

	// Entry chasing: entry type is market but LIMIT_RETEST would be safer
	if tc.EntryType == strategy.CandidateEntryMarket && tc.Strategy == strategy.StrategyBreakoutRetest {
		f.EntryChasing = true
	}

	// Market regime mixed: warnings present in regime snapshot
	if len(rs.Warnings) > 0 {
		f.MarketRegimeMixed = true
	}

	// RS mismatch: classification conflicts with 1h RS direction
	if rs.RelativeStrength.Classification == "underperform" && rs.RelativeStrength.RS1H > 0 {
		f.RSMismatchShortTerm = true
	}

	return f
}

// Route candidate determines what to do based on score + ambiguity flags.
// maxLLMPerCycle <= 0 means unlimited per-cycle count.
func (e *RoutingEngine) RouteCandidate(
	score float64,
	flags AmbiguityFlags,
	llmCallsThisCycle int,
	maxLLMPerCycle int,
) RouteResult {
	flagCount := countFlags(flags)

	if math.IsNaN(score) || math.IsInf(score, 0) {
		return RouteResult{
			Route:  RouteRejectPreLLM,
			Score:  score,
			Reason: "score is non-finite",
		}
	}

	// Hard reject below minimum
	if score < e.MinScoreLLM {
		return RouteResult{
			Route:  RouteRejectPreLLM,
			Score:  score,
			Reason: "score below LLM minimum",
		}
	}

	// High quality: skip LLM (score >= 85)
	if score >= e.MinScoreSkipLLM {
		return RouteResult{
			Route:        RouteSkipLLM,
			Flags:        flags,
			Score:        score,
			FlagCount:    flagCount,
			Reason:       "high score, skip LLM veto",
			LLMCandidate: false,
		}
	}

	// Score 58-84: send to LLM if budget available.
	// maxLLMPerCycle <= 0 means unlimited.
	if maxLLMPerCycle <= 0 || llmCallsThisCycle < maxLLMPerCycle {
		return RouteResult{
			Route:        RouteLLMVeto,
			Flags:        flags,
			Score:        score,
			FlagCount:    flagCount,
			Reason:       "ambiguous candidate, send to LLM veto",
			LLMCandidate: true,
		}
	}

	// Positive maxLLMPerCycle exhausted: reject mid-range scores pre-LLM.
	// RouteSkipLLM is only for score >= MinScoreSkipLLM (85).
	return RouteResult{
		Route:  RouteRejectPreLLM,
		Score:  score,
		Reason: "LLM budget exhausted",
	}
}

// PrioritizeCandidates sorts candidates by priority for LLM calls.
// Higher priority = more ambiguous = should be sent to LLM first.
func PrioritizeCandidates(candidates []RouteResult) {
	sort.SliceStable(candidates, func(i, j int) bool {
		// Higher flag count = more ambiguous = higher priority
		if candidates[i].FlagCount != candidates[j].FlagCount {
			return candidates[i].FlagCount > candidates[j].FlagCount
		}
		// Ties broken by higher score
		return candidates[i].Score > candidates[j].Score
	})
}

func countFlags(f AmbiguityFlags) int {
	n := 0
	if f.NearResistance {
		n++
	}
	if f.NearSupport {
		n++
	}
	if f.BTCNearResistance {
		n++
	}
	if f.BTCNearSupport {
		n++
	}
	if f.TargetOutperformingShortTerm {
		n++
	}
	if f.MixedTimeframeTrend {
		n++
	}
	if f.VolumeNotStrong {
		n++
	}
	if f.Overextended {
		n++
	}
	if f.MarketRegimeMixed {
		n++
	}
	if f.EntryChasing {
		n++
	}
	if f.RSMismatchShortTerm {
		n++
	}
	return n
}
