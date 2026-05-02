package scheduler

import (
	"encoding/json"
	"time"

	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
	"github.com/virhan/botsurv/internal/regime"
	"github.com/virhan/botsurv/internal/risk"
	"github.com/virhan/botsurv/internal/scoring"
	"github.com/virhan/botsurv/internal/strategy"
)

type executionSafetyJSON struct {
	Safe              bool     `json:"safe"`
	FailedChecks      []string `json:"failed_checks"`
	Reasons           []string `json:"reasons"`
	RecommendedAction string   `json:"recommended_action"`
}

// DecisionLogFields accumulates decision log data for final assembly.
type DecisionLogFields struct {
	DecisionID           string
	IndicatorSnapshot    string
	RegimeSnapshot       string
	StrategyAttempted    string
	Candidate            string
	ScoreBreakdown       string
	NearMisses           string
	RiskValidation       string
	SafetyValidation     string
	OrderPlan            string
	DataValidationResult string
	BlocksTriggered      string

	// Versions
	ScoringVersion    string
	RiskConfigVersion string
	LLMPromptVersion  string

	// Phase 6: LLM Reviewer
	LLMReview       string
	LLMModeActive   bool
	LLMReviewAction string
}

func (f *DecisionLogFields) WithIndicatorSnapshot(snap indicator.IndicatorSnapshot) {
	data, _ := json.Marshal(snap)
	f.IndicatorSnapshot = string(data)
}

func (f *DecisionLogFields) WithRegimeSnapshot(snap regime.MarketRegimeSnapshot) {
	data, _ := json.Marshal(snap)
	f.RegimeSnapshot = string(data)
}

func (f *DecisionLogFields) WithStrategyAttempted(names []string) {
	data, _ := json.Marshal(names)
	f.StrategyAttempted = string(data)
}

func (f *DecisionLogFields) WithCandidate(tc strategy.TradeCandidate) {
	candData := map[string]interface{}{
		"symbol": tc.Symbol,
		"side":   string(tc.Side),
		"entry":  tc.EntryPrice,
		"sl":     tc.StopLoss,
		"rr":     tc.RiskRewardRatio,
	}
	data, _ := json.Marshal(candData)
	f.Candidate = string(data)
}

func (f *DecisionLogFields) WithScoreBreakdown(sr scoring.ScoreResult) {
	data, _ := json.Marshal(sr)
	f.ScoreBreakdown = string(data)
}

func (f *DecisionLogFields) WithNearMisses(misses []string) {
	data, _ := json.Marshal(misses)
	f.NearMisses = string(data)
}

func (f *DecisionLogFields) WithRiskValidation(rv risk.RiskValidationResult) {
	data, _ := json.Marshal(rv)
	f.RiskValidation = string(data)
}

func (f *DecisionLogFields) WithSafetyValidation(safe bool, checks, reasons []string) {
	sv := executionSafetyJSON{
		Safe:         safe,
		FailedChecks: checks,
		Reasons:      reasons,
	}
	data, _ := json.Marshal(sv)
	f.SafetyValidation = string(data)
}

func (f *DecisionLogFields) WithOrderPlan(plan *risk.OrderPlan) {
	if plan == nil {
		return
	}
	data, _ := json.Marshal(plan)
	f.OrderPlan = string(data)
}

func (f *DecisionLogFields) WithDataValidationResult(result string) {
	f.DataValidationResult = result
}

func (f *DecisionLogFields) WithBlocksTriggered(blocks []string) {
	data, _ := json.Marshal(blocks)
	f.BlocksTriggered = string(data)
}

// WithLLMReview records the Phase 6 LLM Reviewer outcome.
func (f *DecisionLogFields) WithLLMReview(outcome interface{}) {
	if outcome == nil {
		return
	}
	data, _ := json.Marshal(outcome)
	f.LLMReview = string(data)
}

// WithLLMModeActive sets the LLM review mode active flag.
func (f *DecisionLogFields) WithLLMModeActive(active bool) {
	f.LLMModeActive = active
}

// ToDomain converts fields to a domain.DecisionLog.
func (f *DecisionLogFields) ToDomain(cycleID, mode, symbol, timeframe string, candleCount int, action, reason string) domain.DecisionLog {
	return domain.DecisionLog{
		DecisionID:            f.DecisionID,
		CycleID:               cycleID,
		Timestamp:             time.Now(),
		Mode:                  mode,
		Symbol:                symbol,
		Timeframe:             timeframe,
		CandleCount:           candleCount,
		DataValidationResult:  firstNonEmpty(f.DataValidationResult, "passed"),
		IndicatorSnapshot:     f.IndicatorSnapshot,
		RegimeSnapshot:        f.RegimeSnapshot,
		StrategyAttempted:     f.StrategyAttempted,
		Candidate:             f.Candidate,
		ScoreBreakdown:        f.ScoreBreakdown,
		NearMisses:            f.NearMisses,
		RiskValidation:        f.RiskValidation,
		SafetyValidation:      f.SafetyValidation,
		OrderPlan:             f.OrderPlan,
		LLMReview:             f.LLMReview,
		LLMModeActive:         f.LLMModeActive,
		FinalAction:           action,
		FinalActionReason:     reason,
		EngineVersion:         "v2.1",
		ScoringVersion:        f.ScoringVersion,
		RiskConfigVersion:     f.RiskConfigVersion,
		LLMPromptVersion:      f.LLMPromptVersion,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
