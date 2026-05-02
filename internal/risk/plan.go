package risk

import (
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/regime"
	"github.com/virhan/botsurv/internal/scoring"
	"github.com/virhan/botsurv/internal/strategy"
)

// OrderPlan is the concrete execution plan produced by the Phase 4 risk engine.
type OrderPlan struct {
	Symbol         string
	Side           domain.Side
	Qty            float64
	EntryPrice     float64
	EntryType      string // "market" or "limit_retest"
	StopLoss       float64
	TakeProfits    []TakeProfitPlan
	Leverage       float64
	MarginRequired float64
	EstimatedFees  float64
	RiskAmountUSD  float64
	RiskPctUsed    float64
}

// TakeProfitPlan defines one TP leg with its allocated quantity.
type TakeProfitPlan struct {
	Price float64
	Qty   float64
}

// RiskValidationResult is the structured output of the Phase 4 risk engine.
type RiskValidationResult struct {
	CandidateID       string
	Approved          bool
	RejectionReasons  []string
	OrderPlan         *OrderPlan
	ModifiersApplied  []string
	RiskConfigVersion string
}

// CandidateRiskInput holds all data needed for Phase 4 risk validation.
type CandidateRiskInput struct {
	Candidate      strategy.TradeCandidate
	ScoreResult    scoring.ScoreResult
	RegimeSnapshot regime.MarketRegimeSnapshot
	SymbolInfo     domain.SymbolInfo
	AccountState   domain.AccountState
	Portfolio      PortfolioState
	BotState       domain.BotState
}
