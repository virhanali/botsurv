package screener

import (
	"encoding/json"
	"testing"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
)

func TestComputeScores_HighQuality(t *testing.T) {
	ob := domain.OrderBookSummary{
		SpreadBps:            2,
		BidDepth:             50000,
		AskDepth:             50000,
		EstimatedSlippageBps: 3,
	}
	liq, exec, vol := computeScoresFromOB(ob, 500, 50000)

	if liq < 70 {
		t.Errorf("expected high liquidity, got %.2f", liq)
	}
	if exec < 70 {
		t.Errorf("expected high execution, got %.2f", exec)
	}
	if vol < 40 {
		t.Errorf("expected moderate+ volatility, got %.2f", vol)
	}
}

func TestComputeScores_LowQuality(t *testing.T) {
	ob := domain.OrderBookSummary{
		SpreadBps:            100,
		BidDepth:             10,
		AskDepth:             10,
		EstimatedSlippageBps: 200,
	}
	liq, exec, _ := computeScoresFromOB(ob, 10, 1000)

	if liq > 50 {
		t.Errorf("expected low liquidity, got %.2f", liq)
	}
	if exec > 10 {
		t.Errorf("expected very low execution, got %.2f", exec)
	}
}

func TestEvaluateLLMEligibility_AllPass(t *testing.T) {
	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			RR:                 2.5,
			ExpectedMove:       100,
			EstimatedTotalCost: 10,
		},
		CandidateScore: 80,
	}
	ob := domain.OrderBookSummary{
		SpreadBps:                10,
		EstimatedSlippageBps:     5,
		DepthToPositionSizeRatio: 0.5,
	}

	cfg := defaultTestConfig()
	result := evaluateLLMEligibility(cand, ob, cfg.Universe.Filters, cfg.LLMRouting, cfg.Strategy)

	if !result.LLMEligible {
		t.Errorf("expected eligible, reasons: %v", result.LLMRoutingReasonCodes)
	}
}

func TestEvaluateLLMEligibility_ScoreTooLow(t *testing.T) {
	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			RR:                 2.5,
			ExpectedMove:       100,
			EstimatedTotalCost: 10,
		},
		CandidateScore: 50,
	}
	ob := domain.OrderBookSummary{
		SpreadBps:                10,
		EstimatedSlippageBps:     5,
		DepthToPositionSizeRatio: 0.5,
	}

	cfg := defaultTestConfig()
	result := evaluateLLMEligibility(cand, ob, cfg.Universe.Filters, cfg.LLMRouting, cfg.Strategy)

	if result.LLMEligible {
		t.Error("should not be eligible with low score")
	}
}

func TestCandidateContext_JSON(t *testing.T) {
	ctx := CandidateContext{
		Symbol:        "BTCUSDT",
		Side:          "LONG",
		SetupType:     "breakout",
		Regime:        "trend_up",
		EntryType:     "MARKET",
		ProposedEntry: 65000,
		StopLoss:      64000,
		TakeProfit:    67000,
		RR:            2.0,
		SetupScore:    80,
		ExpectedMove:  2000,
		OrderBook: &OrderBookSummary{
			SpreadBps:   5,
			BidDepth:    50000,
			AskDepth:    50000,
			SlippageBps: 3,
		},
		RegimeSummary: &RegimeSummary{
			Regime:     "trend_up",
			EMA200:     62000,
			PriceVsEMA: 4.8,
			ATR:        1000,
			ATRPct:     1.5,
		},
		CostSummary: &CostSummary{
			EstimatedFee:      35.75,
			EstimatedSlippage: 32.5,
			TotalCost:         68.25,
			CostBps:           10.5,
		},
	}

	// Should marshal to valid JSON
	b, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(b) == 0 {
		t.Error("expected non-empty JSON")
	}

	// Verify it's compact (no excessive whitespace)
	jsonStr := string(b)
	if len(jsonStr) > 1000 {
		t.Errorf("context too large: %d bytes", len(jsonStr))
	}
}

func defaultTestConfig() app.UserConfig {
	return app.UserConfig{
		Universe: app.UniverseConfig{
			Filters: app.UniverseFiltersConfig{
				MaxSpreadBps: 50,
			},
		},
		Strategy: app.StrategyConfig{
			Indicators: app.IndicatorsConfig{
				MinRR: 2.0,
			},
		},
		LLMRouting: app.LLMRoutingConfig{
			MinCandidateScore: 75,
		},
		PortfolioRisk: app.PortfolioRiskConfig{
			MaxOpenPositions: 3,
		},
	}
}
