package scoring

import (
	"testing"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
	"github.com/virhan/botsurv/internal/regime"
	"github.com/virhan/botsurv/internal/strategy"
)

func TestScore_KnownInput(t *testing.T) {
	cfg := app.ScoringConfig{Mode: "balanced", ScoringVersion: "v0.1.0"}.WithDefaults()
	in := Input{
		Candidate: strategy.TradeCandidate{Side: domain.SideLong, EntryType: strategy.CandidateEntryMarket, RiskRewardRatio: 2.1},
		Snapshot15m: indicator.IndicatorSnapshot{
			EMAAlignment: indicator.EMAlignment{PriceAboveEMA50: true, EMA20AboveEMA50: true},
			RSI14:        52,
			MACD:         indicator.MACDSnapshot{HistogramDirection: "up"},
			VolumeRatio:  1.25,
			RecentSwings: []indicator.SwingPoint{{Price: 1, Type: "low"}, {Price: 2, Type: "high"}, {Price: 1.5, Type: "low"}, {Price: 2.5, Type: "high"}, {Price: 2, Type: "low"}, {Price: 3, Type: "high"}},
		},
		Snapshot1h:     indicator.IndicatorSnapshot{EMAAlignment: indicator.EMAlignment{PriceAboveEMA50: true, EMA20AboveEMA50: true}},
		Snapshot4h:     indicator.IndicatorSnapshot{EMAAlignment: indicator.EMAlignment{PriceAboveEMA50: true, EMA20AboveEMA50: true}},
		RegimeSnapshot: regime.MarketRegimeSnapshot{RelativeStrength: regime.RelativeStrengthSnapshot{Classification: "outperform"}},
	}
	out := Score(in, cfg)
	if out.ScoreTotal <= 0 {
		t.Fatalf("expected positive score, got %+v", out)
	}
	if out.ScoringVersion != "v0.1.0" {
		t.Fatalf("expected version v0.1.0, got %s", out.ScoringVersion)
	}
	if out.Components.RiskReward <= 0 {
		t.Fatalf("expected rr component >0, got %+v", out.Components)
	}
}

func TestResolveAction_Bands(t *testing.T) {
	cfg := app.ScoringConfig{Mode: "balanced"}.WithDefaults()
	check := func(score float64, want Action) {
		got, _ := resolveAction(score, cfg, strategy.CandidateEntryMarket)
		if got != want {
			t.Fatalf("score %.1f expected %s got %s", score, want, got)
		}
	}
	check(80, ActionAllowMarket)
	check(66, ActionAllowRetestOnly)
	check(56, ActionReduceSize)
	check(40, ActionReject)

	cfg.Mode = "aggressive"
	checkAgg := func(score float64, want Action) {
		got, _ := resolveAction(score, cfg, strategy.CandidateEntryMarket)
		if got != want {
			t.Fatalf("aggressive score %.1f expected %s got %s", score, want, got)
		}
	}
	checkAgg(71, ActionAllowMarket)
	checkAgg(61, ActionAllowRetestOnly)
	checkAgg(51, ActionReduceSize)
	checkAgg(49, ActionReject)
}

func TestResolveAction_NonMarketEntryCapped(t *testing.T) {
	cfg := app.ScoringConfig{Mode: "balanced"}.WithDefaults()
	// High score with LIMIT_RETEST must not return ALLOW_MARKET
	got, _ := resolveAction(90, cfg, strategy.CandidateEntryLimitRetest)
	if got != ActionAllowRetestOnly {
		t.Fatalf("expected ALLOW_RETEST_ONLY for LIMIT_RETEST at high score, got %s", got)
	}
	// STOP entry also capped
	got, _ = resolveAction(90, cfg, strategy.CandidateEntryStop)
	if got != ActionAllowRetestOnly {
		t.Fatalf("expected ALLOW_RETEST_ONLY for STOP at high score, got %s", got)
	}
	// MARKET entry allowed
	got, _ = resolveAction(90, cfg, strategy.CandidateEntryMarket)
	if got != ActionAllowMarket {
		t.Fatalf("expected ALLOW_MARKET for MARKET at high score, got %s", got)
	}
}

func TestRiskRewardComponentInterpolation(t *testing.T) {
	if riskReward(1.4) != 0 {
		t.Fatalf("expected 0 at rr=1.4, got %.4f", riskReward(1.4))
	}
	if riskReward(2.0) < 6.9 || riskReward(2.0) > 7.1 {
		t.Fatalf("expected ~7 at rr=2.0, got %.4f", riskReward(2.0))
	}
	if riskReward(2.5) != 10 {
		t.Fatalf("expected 10 at rr=2.5, got %.4f", riskReward(2.5))
	}
}
