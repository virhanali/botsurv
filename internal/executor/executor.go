package executor

import (
	"context"
	"fmt"
	"math"

	"github.com/virhan/botsurv/internal/broker"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/execution"
	"github.com/virhan/botsurv/internal/logger"
	"github.com/virhan/botsurv/internal/risk"
)

// ExecutionResult holds the outcome of an execution attempt.
type ExecutionResult struct {
	Symbol  string
	Success bool
	OrderID string
	Error   string
	Actions []string
}

// Executor executes approved RiskDecisions through the Broker.
type Executor struct {
	broker broker.Broker
	log    *logger.Logger
}

// NewExecutor creates a new Executor.
func NewExecutor(broker broker.Broker, log *logger.Logger) *Executor {
	return &Executor{broker: broker, log: log}
}

// ExecutePlan logs a validated order plan without submitting to the broker (Phase 4).
// In Phase 5 this will be replaced with actual order submission.
func (e *Executor) ExecutePlan(ctx context.Context, result risk.RiskValidationResult, safety execution.ExecutionSafetyResult) ExecutionResult {
	res := ExecutionResult{Symbol: result.OrderPlan.Symbol}
	if !result.Approved {
		res.Error = "risk not approved"
		return res
	}
	if !safety.Safe {
		res.Error = fmt.Sprintf("execution safety failed: %v", safety.Reasons)
		return res
	}
	plan := result.OrderPlan
	res.Success = true
	res.Actions = append(res.Actions, fmt.Sprintf("plan_logged %s %s qty=%.4f entry=%.2f sl=%.2f lev=%.1f margin=%.2f",
		plan.Symbol, plan.Side, plan.Qty, plan.EntryPrice, plan.StopLoss, plan.Leverage, plan.MarginRequired))
	e.log.Info("order plan validated and logged (Phase 4 — no submission)", map[string]any{
		"symbol":          plan.Symbol,
		"side":            plan.Side,
		"qty":             plan.Qty,
		"entry_price":     plan.EntryPrice,
		"stop_loss":       plan.StopLoss,
		"take_profits":    plan.TakeProfits,
		"leverage":        plan.Leverage,
		"margin_required": plan.MarginRequired,
		"risk_amount_usd": plan.RiskAmountUSD,
		"risk_pct_used":   plan.RiskPctUsed,
		"modifiers":       result.ModifiersApplied,
	})
	return res
}

// Execute places the trade with atomic SL/TP via the Broker.
// marketPrice is the current price from the WS ticker cache, required for market orders.
//
// Deprecated: Execute bypasses hard blocks, Phase 4 candidate validation, execution safety,
// and mode routing. It is preserved for backward compatibility with test harnesses but must
// not be called from production scheduler paths. Use the scheduler's RunOnce flow for
// production decisions.
func (e *Executor) Execute(ctx context.Context, cand domain.Candidate, decision domain.LLMDecision, riskOut risk.ValidateOutput, marketPrice float64) ExecutionResult {
	result := ExecutionResult{Symbol: cand.Symbol}

	orderSide := domain.OrderSideBuy
	if cand.Side == domain.SideShort {
		orderSide = domain.OrderSideSell
	}

	qty := riskOut.FinalPositionNotional / cand.ProposedEntry
	if math.IsNaN(qty) || math.IsInf(qty, 0) || qty <= 0 {
		result.Error = fmt.Sprintf("invalid quantity: %v", qty)
		e.log.Error("invalid quantity", map[string]any{
			"qty":      qty,
			"notional": riskOut.FinalPositionNotional,
			"entry":    cand.ProposedEntry,
			"symbol":   cand.Symbol,
		})
		return result
	}

	// Guard: reject LIMIT_RETEST candidate with ALLOW_MARKET decision.
	// TP/SL were calculated for the limit entry price, not market (H2).
	if cand.EntryType == domain.EntryTypeLimitRetest && decision.Decision == "ALLOW_MARKET" {
		result.Error = "entry type mismatch: LIMIT_RETEST candidate with ALLOW_MARKET decision"
		e.log.Error("execution rejected: entry type mismatch", map[string]any{
			"symbol":    cand.Symbol,
			"entry_type": cand.EntryType,
			"decision":  decision.Decision,
		})
		return result
	}

	// Push WS ticker price into broker cache so PlaceOrder can fill the market order.
	if marketPrice > 0 {
		e.broker.SetSymbolPrice(cand.Symbol, marketPrice)
	}

	orderReq := broker.OrderRequest{
		Symbol:     cand.Symbol,
		Side:       orderSide,
		OrderType:  domain.OrderTypeMarket,
		Qty:        qty,
		StopLoss:   cand.ProposedStopLoss,
		TakeProfit: cand.ProposedTakeProfit,
	}
	if cand.EntryType == domain.EntryTypeLimitRetest {
		orderReq.OrderType = domain.OrderTypeLimit
		orderReq.Price = &cand.ProposedEntry
	}

	order, err := e.broker.PlaceOrder(ctx, orderReq)
	if err != nil {
		result.Error = fmt.Sprintf("place order: %v", err)
		e.log.Error("execution failed: place order", map[string]any{
			"symbol": cand.Symbol,
			"error":  err.Error(),
		})
		return result
	}
	result.OrderID = order.BrokerOrderID
	result.Actions = append(result.Actions, fmt.Sprintf("placed %s order: %s", orderReq.OrderType, order.BrokerOrderID))

	result.Success = true
	e.log.Info("execution complete", map[string]any{
		"symbol":     cand.Symbol,
		"side":       cand.Side,
		"entry_type": cand.EntryType,
		"qty":        qty,
		"order_id":   result.OrderID,
		"sl":         cand.ProposedStopLoss,
		"tp":         cand.ProposedTakeProfit,
	})

	return result
}
