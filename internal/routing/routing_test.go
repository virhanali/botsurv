package routing

import (
	"testing"

	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
	"github.com/virhan/botsurv/internal/regime"
	"github.com/virhan/botsurv/internal/scoring"
	"github.com/virhan/botsurv/internal/strategy"
)

func makeTestSnap15m() indicator.IndicatorSnapshot {
	return indicator.IndicatorSnapshot{
		Symbol:           "BTCUSDT",
		Timeframe:        "15m",
		LastClosePrice:   65000,
		ATR14:            500,
		VolumeRatio:      1.2,
		ResistanceLevels: []float64{65500},
		SupportLevels:    []float64{64500},
		EMAAlignment: indicator.EMAlignment{
			PriceAboveEMA50: true,
		},
	}
}

func makeTestSnap1h() indicator.IndicatorSnapshot {
	return indicator.IndicatorSnapshot{
		Timeframe: "1H",
		EMAAlignment: indicator.EMAlignment{
			PriceAboveEMA50: false,
		},
	}
}

func makeTestRegime(isBTCNearResistance bool) regime.MarketRegimeSnapshot {
	rs := regime.MarketRegimeSnapshot{
		RelativeStrength: regime.RelativeStrengthSnapshot{
			RS1H:           1.0,
			RS4H:           -1.0,
			Classification: "underperform",
		},
	}
	if isBTCNearResistance {
		rs.BTCFiltersTriggered = []string{"BTCNearMajorResistance"}
	}
	return rs
}

func makeTradeCandidate() strategy.TradeCandidate {
	return strategy.TradeCandidate{
		Symbol:    "BTCUSDT",
		Side:      domain.SideLong,
		EntryType: strategy.CandidateEntryMarket,
		Strategy:  strategy.StrategyBreakoutRetest,
		EntryPrice: 65300,
		StopLoss:   64800,
	}
}

func TestDetectAmbiguity_NearResistance(t *testing.T) {
	snap := makeTestSnap15m()
	cand := makeTradeCandidate()
	rs := makeTestRegime(false)

	flags := DetectAmbiguity(snap, makeTestSnap1h(), rs, scoring.ScoreResult{}, cand)

	if !flags.NearResistance {
		t.Error("expected near_resistance when entry close to resistance level")
	}
}

func TestDetectAmbiguity_BTCNearResistance(t *testing.T) {
	snap := makeTestSnap15m()
	cand := makeTradeCandidate()
	rs := makeTestRegime(true)

	flags := DetectAmbiguity(snap, makeTestSnap1h(), rs, scoring.ScoreResult{}, cand)

	if !flags.BTCNearResistance {
		t.Error("expected btc_near_resistance when BTC filter triggered")
	}
}

func TestDetectAmbiguity_VolumeNotStrong(t *testing.T) {
	snap := makeTestSnap15m()
	snap.VolumeRatio = 1.2
	flags := DetectAmbiguity(snap, makeTestSnap1h(), makeTestRegime(false), scoring.ScoreResult{}, makeTradeCandidate())

	if !flags.VolumeNotStrong {
		t.Error("expected volume_not_strong when ratio 1.0-1.5")
	}
}

func TestDetectAmbiguity_StrongVolumeNoFlag(t *testing.T) {
	snap := makeTestSnap15m()
	snap.VolumeRatio = 2.0
	flags := DetectAmbiguity(snap, makeTestSnap1h(), makeTestRegime(false), scoring.ScoreResult{}, makeTradeCandidate())

	if flags.VolumeNotStrong {
		t.Error("expected no flag when volume ratio >= 1.5")
	}
}

func TestDetectAmbiguity_MixedTimeframe(t *testing.T) {
	snap1h := makeTestSnap1h()
	flags := DetectAmbiguity(makeTestSnap15m(), snap1h, makeTestRegime(false), scoring.ScoreResult{}, makeTradeCandidate())

	if !flags.MixedTimeframeTrend {
		t.Error("expected mixed timeframe when 15m and 1h EMA differ")
	}
}

func TestDetectAmbiguity_EntryChasing(t *testing.T) {
	cand := makeTradeCandidate()
	cand.EntryType = strategy.CandidateEntryMarket
	cand.Strategy = strategy.StrategyBreakoutRetest

	flags := DetectAmbiguity(makeTestSnap15m(), makeTestSnap1h(), makeTestRegime(false), scoring.ScoreResult{}, cand)

	if !flags.EntryChasing {
		t.Error("expected entry_chasing for market entry on breakout retest")
	}
}

func TestRoute_RejectLowScore(t *testing.T) {
	eng := DefaultRoutingEngine()
	result := eng.RouteCandidate(40, AmbiguityFlags{}, 0, 5)

	if result.Route != RouteRejectPreLLM {
		t.Errorf("expected REJECT_PRE_LLM for score 40, got %s", result.Route)
	}
}

func TestRoute_SkipLLMHighScore(t *testing.T) {
	eng := DefaultRoutingEngine()
	result := eng.RouteCandidate(90, AmbiguityFlags{}, 0, 5)

	if result.Route != RouteSkipLLM {
		t.Errorf("expected SKIP_LLM for score 90, got %s", result.Route)
	}
}

func TestRoute_LLMVetoAmbiguous(t *testing.T) {
	eng := DefaultRoutingEngine()
	result := eng.RouteCandidate(72, AmbiguityFlags{}, 0, 5)

	if result.Route != RouteLLMVeto {
		t.Errorf("expected LLM_VETO for score 72, got %s", result.Route)
	}
}

func TestRoute_LLMBudgetExhausted_ScoreAbove70(t *testing.T) {
	eng := DefaultRoutingEngine()
	result := eng.RouteCandidate(75, AmbiguityFlags{}, 5, 5)

	if result.Route != RouteSkipLLM {
		t.Errorf("expected SKIP_LLM when budget exhausted but score >= 70, got %s", result.Route)
	}
}

func TestRoute_LLMBudgetExhausted_ScoreBelow70(t *testing.T) {
	eng := DefaultRoutingEngine()
	result := eng.RouteCandidate(60, AmbiguityFlags{}, 5, 5)

	if result.Route != RouteRejectPreLLM {
		t.Errorf("expected REJECT_PRE_LLM when budget exhausted and score < 70, got %s", result.Route)
	}
}

func TestPrioritize_HigherFlagCountFirst(t *testing.T) {
	results := []RouteResult{
		{Score: 80, FlagCount: 1, LLMCandidate: true},
		{Score: 75, FlagCount: 3, LLMCandidate: true},
	}
	PrioritizeCandidates(results)

	if results[0].FlagCount != 3 {
		t.Error("expected higher flag count to be prioritized first")
	}
}

func TestCountFlags_All(t *testing.T) {
	f := AmbiguityFlags{
		NearResistance:               true,
		NearSupport:                  true,
		BTCNearResistance:             true,
		BTCNearSupport:                true,
		TargetOutperformingShortTerm: true,
		MixedTimeframeTrend:          true,
		VolumeNotStrong:              true,
		Overextended:                 true,
		MarketRegimeMixed:            true,
		EntryChasing:                 true,
		RSMismatchShortTerm:          true,
	}
	if n := countFlags(f); n != 11 {
		t.Errorf("expected 11 flags, got %d", n)
	}
}

func TestCountFlags_None(t *testing.T) {
	if n := countFlags(AmbiguityFlags{}); n != 0 {
		t.Errorf("expected 0 flags, got %d", n)
	}
}
