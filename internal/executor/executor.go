package executor

import (
	"context"
	"fmt"

	"github.com/virhan/botsurv/internal/broker"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
	"github.com/virhan/botsurv/internal/risk"
)

// Executor executes approved RiskDecisions through the Broker.
type Executor struct {
	broker broker.Broker
	log    *logger.Logger
}

// NewExecutor creates a new Executor.
func NewExecutor(broker broker.Broker, log *logger.Logger) *Executor {
	return &Executor{broker: broker, log: log}
}

// ExecutionResult holds the outcome of an execution attempt.
type ExecutionResult struct {
	Symbol     string
	Success    bool
	OrderID    string
	PositionID int64
	SLOrderID  string
	TPOrderID  string
	Error      string
	Actions    []string
}

// Execute places the trade and sets protective orders.
func (e *Executor) Execute(ctx context.Context, cand domain.Candidate, decision domain.LLMDecision, riskOut risk.ValidateOutput) ExecutionResult {
	result := ExecutionResult{
		Symbol: cand.Symbol,
	}

	// Determine order side
	var orderSide domain.OrderSide
	if cand.Side == domain.SideLong {
		orderSide = domain.OrderSideBuy
	} else {
		orderSide = domain.OrderSideSell
	}

	// Calculate quantity from notional
	qty := riskOut.FinalPositionNotional / cand.ProposedEntry
	if qty <= 0 {
		result.Error = "computed quantity is zero"
		e.log.Error("execution failed", map[string]any{"symbol": cand.Symbol, "error": result.Error})
		return result
	}

	// Place main order
	var orderReq broker.OrderRequest
	switch cand.EntryType {
	case domain.EntryTypeMarket:
		orderReq = broker.OrderRequest{
			Symbol:    cand.Symbol,
			Side:      orderSide,
			OrderType: domain.OrderTypeMarket,
			Qty:       qty,
		}
	case domain.EntryTypeLimitRetest:
		orderReq = broker.OrderRequest{
			Symbol:    cand.Symbol,
			Side:      orderSide,
			OrderType: domain.OrderTypeLimit,
			Qty:       qty,
			Price:     &cand.ProposedEntry,
		}
	default:
		orderReq = broker.OrderRequest{
			Symbol:    cand.Symbol,
			Side:      orderSide,
			OrderType: domain.OrderTypeMarket,
			Qty:       qty,
		}
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

	// If market order, set protective orders immediately
	if cand.EntryType == domain.EntryTypeMarket {
		e.setProtectiveOrders(ctx, cand, &result)
	}
	// For limit orders, protective orders will be set after fill (in position monitor)

	if result.Error == "" {
		result.Success = true
		e.log.Info("execution complete", map[string]any{
			"symbol":     cand.Symbol,
			"side":       cand.Side,
			"entry_type": cand.EntryType,
			"qty":        qty,
			"order_id":   result.OrderID,
			"sl_set":     result.SLOrderID != "",
			"tp_set":     result.TPOrderID != "",
		})
	}

	return result
}

func (e *Executor) setProtectiveOrders(ctx context.Context, cand domain.Candidate, result *ExecutionResult) {
	// Use the PaperBroker's SetProtectiveOrders which validates SL/TP sides
	err := e.broker.(*broker.PaperBroker).SetProtectiveOrders(
		ctx,
		cand.Symbol,
		cand.ProposedStopLoss,
		cand.ProposedTakeProfit,
	)
	if err != nil {
		// Protective order creation failed — emergency close
		result.Actions = append(result.Actions, fmt.Sprintf("protective order failed: %v", err))
		e.log.Error("PROTECTIVE ORDER FAILED — EMERGENCY CLOSE", map[string]any{
			"symbol": cand.Symbol,
			"error":  err.Error(),
		})

		// Retry up to 3 times
		for retry := 1; retry <= 3; retry++ {
			err = e.broker.(*broker.PaperBroker).SetProtectiveOrders(
				ctx,
				cand.Symbol,
				cand.ProposedStopLoss,
				cand.ProposedTakeProfit,
			)
			if err == nil {
				result.Actions = append(result.Actions, fmt.Sprintf("protective orders set on retry %d", retry))
				return
			}
			result.Actions = append(result.Actions, fmt.Sprintf("retry %d failed: %v", retry, err))
		}

		// All retries failed — emergency close
		e.log.Error("ALL RETRIES FAILED — EMERGENCY CLOSE", map[string]any{"symbol": cand.Symbol})
		if closeErr := e.broker.EmergencyCloseAll(ctx); closeErr != nil {
			result.Actions = append(result.Actions, fmt.Sprintf("emergency close failed: %v", closeErr))
			result.Error = fmt.Sprintf("emergency close failed: %v", closeErr)
		} else {
			result.Actions = append(result.Actions, "emergency close executed")
			result.Error = "protective orders failed, position emergency closed"
		}
		return
	}

	// Verify protective orders exist
	pos, ok := e.broker.(*broker.PaperBroker).GetPosition(cand.Symbol)
	if ok {
		if pos.SLOrderID != nil {
			result.SLOrderID = fmt.Sprintf("%d", *pos.SLOrderID)
		}
		if pos.TPOrderID != nil {
			result.TPOrderID = fmt.Sprintf("%d", *pos.TPOrderID)
		}
	}
	result.Actions = append(result.Actions, "protective orders set")
}
