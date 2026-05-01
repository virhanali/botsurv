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

func TestEvaluateLLMEligibility_RequireLiquidityOkFalse_AllowsZeroDepth(t *testing.T) {
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
		DepthToPositionSizeRatio: 0,
	}

	cfg := defaultTestConfig()
	cfg.LLMRouting.RequireLiquidityOk = false
	result := evaluateLLMEligibility(cand, ob, cfg.Universe.Filters, cfg.LLMRouting, cfg.Strategy)

	if !result.LLMEligible {
		t.Errorf("expected eligible when require_liquidity_ok=false, reasons: %v", result.LLMRoutingReasonCodes)
	}
	for _, r := range result.LLMRoutingReasonCodes {
		if r == "insufficient_depth" {
			t.Error("should not add insufficient_depth when require_liquidity_ok=false")
		}
	}
}

func TestEvaluateLLMEligibility_RequireLiquidityOkTrue_BlocksZeroDepth(t *testing.T) {
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
		DepthToPositionSizeRatio: 0,
	}

	cfg := defaultTestConfig()
	cfg.LLMRouting.RequireLiquidityOk = true
	result := evaluateLLMEligibility(cand, ob, cfg.Universe.Filters, cfg.LLMRouting, cfg.Strategy)

	if result.LLMEligible {
		t.Error("should not be eligible when require_liquidity_ok=true and depth ratio is 0")
	}
	found := false
	for _, r := range result.LLMRoutingReasonCodes {
		if r == "insufficient_depth" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected insufficient_depth, got %v", result.LLMRoutingReasonCodes)
	}
}

func TestEvaluateLLMEligibility_RequireExecutionOkFalse_IgnoresSpreadSlippage(t *testing.T) {
	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			RR:                 2.5,
			ExpectedMove:       100,
			EstimatedTotalCost: 10,
		},
		CandidateScore: 80,
	}
	ob := domain.OrderBookSummary{
		SpreadBps:                1000,
		EstimatedSlippageBps:     500,
		DepthToPositionSizeRatio: 0.5,
	}

	cfg := defaultTestConfig()
	cfg.LLMRouting.RequireExecutionOk = false
	cfg.LLMRouting.RequireLiquidityOk = false
	result := evaluateLLMEligibility(cand, ob, cfg.Universe.Filters, cfg.LLMRouting, cfg.Strategy)

	if !result.LLMEligible {
		t.Errorf("expected eligible when require_execution_ok=false, reasons: %v", result.LLMRoutingReasonCodes)
	}
	for _, r := range result.LLMRoutingReasonCodes {
		if r == "spread_too_wide" || r == "slippage_too_high" {
			t.Errorf("should not add %s when require_execution_ok=false", r)
		}
	}
}

func TestEvaluateLLMEligibility_RequireExecutionOkTrue_BlocksBadSpreadSlippage(t *testing.T) {
	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			RR:                 2.5,
			ExpectedMove:       100,
			EstimatedTotalCost: 10,
		},
		CandidateScore: 80,
	}
	ob := domain.OrderBookSummary{
		SpreadBps:                1000,
		EstimatedSlippageBps:     500,
		DepthToPositionSizeRatio: 0.5,
	}

	cfg := defaultTestConfig()
	cfg.LLMRouting.RequireExecutionOk = true
	cfg.LLMRouting.RequireLiquidityOk = false
	result := evaluateLLMEligibility(cand, ob, cfg.Universe.Filters, cfg.LLMRouting, cfg.Strategy)

	if result.LLMEligible {
		t.Error("should not be eligible when require_execution_ok=true and spread/slippage are bad")
	}
	foundSpread := false
	foundSlippage := false
	for _, r := range result.LLMRoutingReasonCodes {
		if r == "spread_too_wide" {
			foundSpread = true
		}
		if r == "slippage_too_high" {
			foundSlippage = true
		}
	}
	if !foundSpread {
		t.Errorf("expected spread_too_wide, got %v", result.LLMRoutingReasonCodes)
	}
	if !foundSlippage {
		t.Errorf("expected slippage_too_high, got %v", result.LLMRoutingReasonCodes)
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
