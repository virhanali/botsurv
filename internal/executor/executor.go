package executor

import (
	"context"
	"fmt"
	"math"

	"github.com/virhan/botsurv/internal/broker"
	"github.com/virhan/botsurv/internal/domain"
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

// Execute places the trade with atomic SL/TP via the Broker.
func (e *Executor) Execute(ctx context.Context, cand domain.Candidate, decision domain.LLMDecision, riskOut risk.ValidateOutput) ExecutionResult {
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
