package universe

import (
	"math"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
)

// computeScores calculates liquidity, execution, and volatility scores from market data.
func computeScores(ob domain.OrderBookSummary, atr, price float64) (liquidity, execution, volatility float64) {
	if math.IsNaN(ob.SpreadBps) || math.IsInf(ob.SpreadBps, 0) {
		return 0, 0, 0
	}

	// Liquidity score: higher depth, lower spread = higher score
	depth := ob.BidDepth + ob.AskDepth
	liquidity = 30.0 // base
	if ob.SpreadBps > 0 {
		liquidity += 40.0 * math.Max(0, 1.0-ob.SpreadBps/100.0)
	}
	if depth > 0 {
		liquidity += 30.0 * math.Min(1.0, depth/10000.0)
	}
	liquidity = clamp(liquidity, 0, 100)

	// Execution score: lower slippage = higher score
	execution = 80.0
	if ob.EstimatedSlippageBps > 0 {
		execution = 80.0 * math.Max(0, 1.0-ob.EstimatedSlippageBps/200.0)
	}
	execution = clamp(execution, 0, 100)

	// Volatility score: based on ATR relative to price
	volatility = 50.0
	if price > 0 && atr > 0 {
		atrPct := atr / price * 100
		if atrPct >= 0.2 && atrPct <= 5.0 {
			volatility = 50.0 + 50.0*(1.0-math.Abs(atrPct-2.5)/2.5)
		} else if atrPct < 0.2 {
			volatility = 50.0 * (atrPct / 0.2)
		} else {
			volatility = math.Max(0, 100.0-20.0*(atrPct-5.0))
		}
	}
	volatility = clamp(volatility, 0, 100)

	return liquidity, execution, volatility
}

func candidateScore(liquidity, execution, setup, volatility float64) float64 {
	return liquidity*0.25 + execution*0.25 + setup*0.35 + volatility*0.15
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// evaluateLLMEligibility checks if a candidate meets all LLM routing criteria.
func evaluateLLMEligibility(
	cand domain.Candidate,
	ob domain.OrderBookSummary,
	filters app.UniverseFiltersConfig,
	llmConfig app.LLMRoutingConfig,
	strategy app.StrategyConfig,
) domain.Candidate {
	minScore := llmConfig.MinCandidateScore
	if minScore <= 0 {
		minScore = 75
	}
	minRR := strategy.Indicators.MinRR
	if minRR <= 0 {
		minRR = 2.0
	}

	reasons := []string{}
	if math.IsNaN(cand.CandidateScore) || math.IsInf(cand.CandidateScore, 0) {
		reasons = append(reasons, "candidate_score_non_finite")
		cand.LLMEligible = false
		cand.LLMRoutingReasonCodes = reasons
		return cand
	}

	if cand.CandidateScore < minScore {
		reasons = append(reasons, "candidate_score_below_threshold")
	}
	if llmConfig.RequireExecutionOk {
		if filters.MaxSpreadBps > 0 && ob.SpreadBps > filters.MaxSpreadBps {
			reasons = append(reasons, "spread_too_wide")
		}
		if ob.EstimatedSlippageBps >= 100 {
			reasons = append(reasons, "slippage_too_high")
		}
	}
	if llmConfig.RequireLiquidityOk && ob.DepthToPositionSizeRatio <= 0 {
		reasons = append(reasons, "insufficient_depth")
	}
	if cand.RR > 0 && cand.RR < minRR {
		reasons = append(reasons, "rr_below_min")
	}
	if cand.EstimatedTotalCost > 0 {
		if cand.ExpectedMove <= 0 || cand.ExpectedMove <= 3*cand.EstimatedTotalCost {
			reasons = append(reasons, "expected_move_too_small")
		}
	}

	cand.LLMEligible = len(reasons) == 0
	cand.LLMRoutingReasonCodes = reasons
	return cand
}
