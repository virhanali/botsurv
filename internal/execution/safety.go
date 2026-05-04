package execution

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/risk"
)

// SafetyCheck is a named execution safety check.
type SafetyCheck struct {
	Name   string
	Check  func(plan risk.OrderPlan, market MarketState, account AccountState) (bool, string)
}

// MarketState holds live market data for safety evaluation.
type MarketState struct {
	Symbol          string
	Price           float64
	OrderBook       domain.OrderBookSummary
	SpreadPct       float64
	SlippagePct     float64
	ExchangeHealthy bool
	ExchangeLatency time.Duration
	ExchangeErrorRate float64
}

// AccountState holds local account and order state for safety evaluation.
type AccountState struct {
	OpenPositions []domain.Position
	OpenOrders    []domain.Order
}

// ExecutionSafetyResult is the structured output of the safety evaluator.
type ExecutionSafetyResult struct {
	Safe              bool
	FailedChecks      []string
	Reasons           []string
	RecommendedAction string // "abort" | "retry_after_seconds:N" | "alert_and_abort"
}

// SafetyEngine enforces runtime execution safety checks.
type SafetyEngine struct {
	cfg app.UserConfig
	mu  sync.RWMutex

	emergencyStop     EmergencyStopState
	btcPriceHistory   []btcPricePoint
	maxHistoryLen     int
}

// EmergencyStopState tracks whether new orders are halted.
type EmergencyStopState struct {
	Active      bool
	Reason      string
	SetAt       time.Time
	CooldownUntil *time.Time
}

// btcPricePoint is a single BTC price observation for flash crash detection.
type btcPricePoint struct {
	Price float64
	Ts    time.Time
}

// NewSafetyEngine creates a new execution safety engine.
func NewSafetyEngine(cfg app.UserConfig) *SafetyEngine {
	return &SafetyEngine{
		cfg:           cfg,
		maxHistoryLen: 100,
	}
}

// SetEmergencyStop manually triggers the emergency stop.
func (se *SafetyEngine) SetEmergencyStop(reason string) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.emergencyStop.Active = true
	se.emergencyStop.Reason = reason
	se.emergencyStop.SetAt = time.Now()
}

// ClearEmergencyStop clears the emergency stop.
func (se *SafetyEngine) ClearEmergencyStop() {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.emergencyStop.Active = false
	se.emergencyStop.Reason = ""
	se.emergencyStop.CooldownUntil = nil
}

// UpdateBTCPrice records a new BTC price for flash crash monitoring.
func (se *SafetyEngine) UpdateBTCPrice(price float64, ts time.Time) {
	se.mu.Lock()
	defer se.mu.Unlock()
	se.btcPriceHistory = append(se.btcPriceHistory, btcPricePoint{Price: price, Ts: ts})
	if len(se.btcPriceHistory) > se.maxHistoryLen {
		se.btcPriceHistory = se.btcPriceHistory[len(se.btcPriceHistory)-se.maxHistoryLen:]
	}
}

// CheckBTCFlashCrash evaluates whether BTC has dropped more than threshold in the lookback window.
func (se *SafetyEngine) CheckBTCFlashCrash(thresholdPct float64, window time.Duration) (triggered bool, dropPct float64) {
	se.mu.RLock()
	defer se.mu.RUnlock()
	if len(se.btcPriceHistory) < 2 {
		return false, 0
	}
	cutoff := time.Now().Add(-window)
	var baseline float64
	for i := 0; i < len(se.btcPriceHistory); i++ {
		if se.btcPriceHistory[i].Ts.After(cutoff) || se.btcPriceHistory[i].Ts.Equal(cutoff) {
			baseline = se.btcPriceHistory[i].Price
			break
		}
	}
	if baseline <= 0 {
		return false, 0
	}
	current := se.btcPriceHistory[len(se.btcPriceHistory)-1].Price
	drop := ((current - baseline) / baseline) * 100
	if drop <= thresholdPct {
		return true, drop
	}
	return false, drop
}

// AutoClearEmergencyStop removes the stop if cooldown has expired.
func (se *SafetyEngine) AutoClearEmergencyStop() {
	se.mu.Lock()
	defer se.mu.Unlock()
	if !se.emergencyStop.Active {
		return
	}
	if se.emergencyStop.CooldownUntil != nil && time.Now().After(*se.emergencyStop.CooldownUntil) {
		se.emergencyStop.Active = false
		se.emergencyStop.Reason = ""
		se.emergencyStop.CooldownUntil = nil
	}
}

// EvaluateExecutionSafety runs all safety checks and returns a combined result.
func (se *SafetyEngine) EvaluateExecutionSafety(ctx context.Context, plan risk.OrderPlan, market MarketState, account AccountState) ExecutionSafetyResult {
	se.AutoClearEmergencyStop()
	se.mu.RLock()
	emergencyStop := se.emergencyStop
	se.mu.RUnlock()

	result := ExecutionSafetyResult{Safe: true}

	check := func(name string, safe bool, reason string) {
		if safe {
			return
		}
		result.Safe = false
		result.FailedChecks = append(result.FailedChecks, name)
		result.Reasons = append(result.Reasons, reason)
	}

	// 1. SpreadGuard
	maxSpread := se.cfg.HardBlocks.MaxSpreadPctOrDefault()
	if maxSpread <= 0 {
		maxSpread = 0.15
	}
	spreadPct := market.SpreadPct
	if spreadPct <= 0 && market.OrderBook.SpreadBps > 0 {
		spreadPct = market.OrderBook.SpreadBps / 100.0
	}
	check("SpreadGuard", spreadPct <= maxSpread, fmt.Sprintf("spread %.4f%% > max %.4f%%", spreadPct, maxSpread))

	// 2. SlippageGuard
	maxSlippage := 0.10 // default 0.10%
	slippagePct := market.SlippagePct
	if slippagePct <= 0 && market.OrderBook.EstimatedSlippageBps > 0 {
		slippagePct = market.OrderBook.EstimatedSlippageBps / 100.0
	}
	check("SlippageGuard", slippagePct <= maxSlippage, fmt.Sprintf("slippage %.4f%% > max %.4f%%", slippagePct, maxSlippage))

	// 3. DuplicateOrderGuard (same symbol + side within last 60s)
	matchesSide := func(orderSide domain.OrderSide, planSide domain.Side) bool {
		if planSide == domain.SideLong && orderSide == domain.OrderSideBuy {
			return true
		}
		if planSide == domain.SideShort && orderSide == domain.OrderSideSell {
			return true
		}
		return false
	}
	duplicate := false
	for _, o := range account.OpenOrders {
		if o.Symbol == plan.Symbol && matchesSide(o.Side, plan.Side) {
			if time.Since(o.CreatedAt) < 60*time.Second {
				duplicate = true
				break
			}
		}
	}
	if !duplicate {
		for _, p := range account.OpenPositions {
			if p.Symbol == plan.Symbol && p.Side == plan.Side && p.Status == domain.PositionStatusOpen {
				duplicate = true
				break
			}
		}
	}
	check("DuplicateOrderGuard", !duplicate, fmt.Sprintf("duplicate order/position for %s %s within 60s", plan.Symbol, plan.Side))

	// 4. PositionReconciliation (if exchange state is provided, compare to local)
	// In Phase 4 we accept local state as source of truth; mismatch is handled via alert.
	// This check is a placeholder for when exchange adapter is wired.
	check("PositionReconciliation", true, "")

	// 5. ExchangeHealthCheck
	check("ExchangeHealthCheck", market.ExchangeHealthy, fmt.Sprintf("exchange degraded (latency=%s error_rate=%.2f)", market.ExchangeLatency, market.ExchangeErrorRate))

	// 6. EmergencyStopCheck
	check("EmergencyStopCheck", !emergencyStop.Active, fmt.Sprintf("emergency stop active: %s", emergencyStop.Reason))

	// 7. BTCFlashCrashCircuitBreaker
	threshold := se.cfg.HardBlocks.BTCFlashCrash5mPctOrDefault()
	if threshold == 0 {
		threshold = -4.0
	}
	triggered, drop := se.CheckBTCFlashCrash(threshold, 5*time.Minute)
	check("BTCFlashCrashCircuitBreaker", !triggered, fmt.Sprintf("BTC flash crash: %.2f%% in 5m (threshold %.2f%%)", drop, threshold))
	if triggered && !emergencyStop.Active {
		se.SetEmergencyStop(fmt.Sprintf("BTC flash crash circuit breaker: %.2f%%", drop))
		cooldown := time.Now().Add(30 * time.Minute)
		se.mu.Lock()
		se.emergencyStop.CooldownUntil = &cooldown
		se.mu.Unlock()
	}


	if !result.Safe {
		result.RecommendedAction = "abort"
		for _, name := range result.FailedChecks {
			if name == "ExchangeHealthCheck" {
				result.RecommendedAction = "retry_after_seconds:30"
			}
			if name == "PositionReconciliation" {
				result.RecommendedAction = "alert_and_abort"
			}
		}
	}

	return result
}

// EvaluateOrderTimeout determines if a pending limit order should be cancelled.
func (se *SafetyEngine) EvaluateOrderTimeout(orderCreatedAt time.Time, orderType domain.OrderType) (cancel bool, reason string) {
	if orderType != domain.OrderTypeLimit {
		return false, ""
	}
	timeout := 60 * time.Second
	if time.Since(orderCreatedAt) > timeout {
		return true, "order_timeout"
	}
	return false, ""
}

// EvaluatePartialFill determines how to handle a partial fill.
func (se *SafetyEngine) EvaluatePartialFill(intendedQty, filledQty float64) (action string, manageQty float64) {
	if intendedQty <= 0 {
		return "cancel", 0
	}
	fillPct := filledQty / intendedQty
	if fillPct < 0.5 {
		return "cancel_remainder", filledQty
	}
	if fillPct < 0.95 {
		return "manage_partial", filledQty
	}
	return "filled", intendedQty
}

// EstimateSlippageFromOrderbook walks the orderbook for required qty and estimates average fill price.
func EstimateSlippageFromOrderbook(plan risk.OrderPlan, ob domain.OrderBookSummary, levels []domain.OrderBookLevel) (slippagePct float64, estimatedFillPrice float64) {
	if plan.Qty <= 0 || plan.EntryPrice <= 0 {
		return 0, plan.EntryPrice
	}
	// Simplified: if no depth levels provided, use orderbook summary slippage estimate
	if len(levels) == 0 {
		return ob.EstimatedSlippageBps / 100.0, plan.EntryPrice
	}
	remaining := plan.Qty
	var totalCost float64
	for _, lvl := range levels {
		if remaining <= 0 {
			break
		}
		fill := lvl.Size
		if fill > remaining {
			fill = remaining
		}
		totalCost += fill * lvl.Price
		remaining -= fill
	}
	if remaining > 0 {
		// Could not fill full qty from book
		return math.MaxFloat64, 0
	}
	if totalCost <= 0 || plan.Qty <= 0 {
		return 0, plan.EntryPrice
	}
	avgPrice := totalCost / plan.Qty
	slippage := math.Abs(avgPrice-plan.EntryPrice) / plan.EntryPrice * 100
	return slippage, avgPrice
}
