package execution

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/risk"
)

func defaultSafetyConfig() app.UserConfig {
	return app.UserConfig{
		HardBlocks: app.HardBlocksConfig{
			MaxSpreadPct:       0.15,
			BTCFlashCrash5mPct: -4.0,
		},
	}
}

func defaultOrderPlan() risk.OrderPlan {
	return risk.OrderPlan{
		Symbol:     "BTCUSDT",
		Side:       domain.SideLong,
		Qty:        0.1,
		EntryPrice: 50000,
	}
}

func TestSafetyEngine_SpreadGuardPass(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:          "BTCUSDT",
		SpreadPct:       0.10,
		ExchangeHealthy: true,
	}, AccountState{})
	if !result.Safe {
		t.Errorf("expected safe for spread 0.10%%, got: %v", result.Reasons)
	}
}

func TestSafetyEngine_SpreadGuardFail(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:    "BTCUSDT",
		SpreadPct: 0.20,
	}, AccountState{})
	if result.Safe {
		t.Fatal("expected unsafe for spread 0.20%")
	}
	found := false
	for _, c := range result.FailedChecks {
		if c == "SpreadGuard" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SpreadGuard in failed checks, got %v", result.FailedChecks)
	}
}

func TestSafetyEngine_SlippageGuardPass(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:          "BTCUSDT",
		SpreadPct:       0.05,
		SlippagePct:     0.08,
		ExchangeHealthy: true,
	}, AccountState{})
	if !result.Safe {
		t.Errorf("expected safe for slippage 0.08%%, got: %v", result.Reasons)
	}
}

func TestSafetyEngine_SlippageGuardFail(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:      "BTCUSDT",
		SpreadPct:   0.05,
		SlippagePct: 0.12,
	}, AccountState{})
	if result.Safe {
		t.Fatal("expected unsafe for slippage 0.12%")
	}
	assertContainsCheck(t, result.FailedChecks, "SlippageGuard")
}

func TestSafetyEngine_DuplicateOrderGuardPass(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:          "BTCUSDT",
		SpreadPct:       0.05,
		ExchangeHealthy: true,
	}, AccountState{
		OpenOrders: []domain.Order{
			{Symbol: "ETHUSDT", Side: domain.OrderSideBuy, CreatedAt: time.Now()},
		},
	})
	if !result.Safe {
		t.Errorf("expected safe for different symbol, got: %v", result.Reasons)
	}
}

func TestSafetyEngine_DuplicateOrderGuardFail(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:          "BTCUSDT",
		SpreadPct:       0.05,
		ExchangeHealthy: true,
	}, AccountState{
		OpenOrders: []domain.Order{
			{Symbol: "BTCUSDT", Side: domain.OrderSideBuy, CreatedAt: time.Now(), Status: domain.OrderStatusPending},
		},
	})
	if result.Safe {
		t.Fatal("expected unsafe for duplicate order")
	}
	assertContainsCheck(t, result.FailedChecks, "DuplicateOrderGuard")
}

func TestSafetyEngine_DuplicatePositionGuardFail(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:    "BTCUSDT",
		SpreadPct: 0.05,
	}, AccountState{
		OpenPositions: []domain.Position{
			{Symbol: "BTCUSDT", Side: domain.SideLong, Status: domain.PositionStatusOpen},
		},
	})
	if result.Safe {
		t.Fatal("expected unsafe for duplicate position")
	}
	assertContainsCheck(t, result.FailedChecks, "DuplicateOrderGuard")
}

func TestSafetyEngine_ExchangeHealthCheckPass(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:          "BTCUSDT",
		SpreadPct:       0.05,
		ExchangeHealthy: true,
	}, AccountState{})
	if !result.Safe {
		t.Errorf("expected safe for healthy exchange, got: %v", result.Reasons)
	}
}

func TestSafetyEngine_ExchangeHealthCheckFail(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:          "BTCUSDT",
		SpreadPct:       0.05,
		ExchangeHealthy: false,
		ExchangeLatency: 5 * time.Second,
	}, AccountState{})
	if result.Safe {
		t.Fatal("expected unsafe for degraded exchange")
	}
	assertContainsCheck(t, result.FailedChecks, "ExchangeHealthCheck")
	if result.RecommendedAction != "retry_after_seconds:30" {
		t.Errorf("expected retry action, got %s", result.RecommendedAction)
	}
}

func TestSafetyEngine_EmergencyStopCheckPass(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:          "BTCUSDT",
		SpreadPct:       0.05,
		ExchangeHealthy: true,
	}, AccountState{})
	if !result.Safe {
		t.Errorf("expected safe when emergency stop inactive, got: %v", result.Reasons)
	}
}

func TestSafetyEngine_EmergencyStopCheckFail(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	se.SetEmergencyStop("manual halt")
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:    "BTCUSDT",
		SpreadPct: 0.05,
	}, AccountState{})
	if result.Safe {
		t.Fatal("expected unsafe when emergency stop active")
	}
	assertContainsCheck(t, result.FailedChecks, "EmergencyStopCheck")
}

func TestSafetyEngine_BTCFlashCrashCircuitBreakerPass(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	se.UpdateBTCPrice(65000, time.Now().Add(-2*time.Minute))
	se.UpdateBTCPrice(64900, time.Now())
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:          "BTCUSDT",
		SpreadPct:       0.05,
		ExchangeHealthy: true,
	}, AccountState{})
	if !result.Safe {
		t.Errorf("expected safe for mild BTC drop, got: %v", result.Reasons)
	}
}

func TestSafetyEngine_BTCFlashCrashCircuitBreakerFail(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	se.UpdateBTCPrice(65000, time.Now().Add(-2*time.Minute))
	se.UpdateBTCPrice(62000, time.Now()) // ~4.6% drop
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:    "BTCUSDT",
		SpreadPct: 0.05,
	}, AccountState{})
	if result.Safe {
		t.Fatal("expected unsafe for BTC flash crash")
	}
	assertContainsCheck(t, result.FailedChecks, "BTCFlashCrashCircuitBreaker")
	// Should have set emergency stop
	if !se.emergencyStop.Active {
		t.Error("expected emergency stop to be auto-set after flash crash")
	}
}

func TestSafetyEngine_BTCFlashCrashCooldownAutoClear(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	se.SetEmergencyStop("test")
	past := time.Now().Add(-31 * time.Minute)
	se.emergencyStop.CooldownUntil = &past
	se.AutoClearEmergencyStop()
	if se.emergencyStop.Active {
		t.Error("expected emergency stop to auto-clear after cooldown")
	}
}

func TestSafetyEngine_PositionReconciliationPass(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:          "BTCUSDT",
		SpreadPct:       0.05,
		ExchangeHealthy: true,
	}, AccountState{})
	if !result.Safe {
		t.Errorf("expected safe for reconciliation pass, got: %v", result.Reasons)
	}
}

func TestSafetyEngine_AllChecksPass(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:          "BTCUSDT",
		SpreadPct:       0.05,
		SlippagePct:     0.05,
		ExchangeHealthy: true,
	}, AccountState{
		OpenOrders:    []domain.Order{},
		OpenPositions: []domain.Position{},
	})
	if !result.Safe {
		t.Fatalf("expected fully safe, got failed checks: %v, reasons: %v", result.FailedChecks, result.Reasons)
	}
	if len(result.FailedChecks) != 0 {
		t.Errorf("expected no failed checks, got %v", result.FailedChecks)
	}
}

func TestSafetyEngine_MultipleChecksFail(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	se.SetEmergencyStop("test")
	result := se.EvaluateExecutionSafety(context.Background(), defaultOrderPlan(), MarketState{
		Symbol:      "BTCUSDT",
		SpreadPct:   0.20,
		SlippagePct: 0.12,
	}, AccountState{})
	if result.Safe {
		t.Fatal("expected unsafe")
	}
	if len(result.FailedChecks) < 3 {
		t.Errorf("expected at least 3 failed checks, got %d: %v", len(result.FailedChecks), result.FailedChecks)
	}
}

func TestEvaluateOrderTimeout_LimitOrderPending(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	created := time.Now().Add(-61 * time.Second)
	cancel, reason := se.EvaluateOrderTimeout(created, domain.OrderTypeLimit)
	if !cancel {
		t.Error("expected cancel for limit order pending > 60s")
	}
	if reason != "order_timeout" {
		t.Errorf("expected reason order_timeout, got %s", reason)
	}
}

func TestEvaluateOrderTimeout_LimitOrderFresh(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	created := time.Now().Add(-10 * time.Second)
	cancel, reason := se.EvaluateOrderTimeout(created, domain.OrderTypeLimit)
	if cancel {
		t.Error("expected no cancel for fresh limit order")
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %s", reason)
	}
}

func TestEvaluateOrderTimeout_MarketOrderIgnored(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	created := time.Now().Add(-120 * time.Second)
	cancel, reason := se.EvaluateOrderTimeout(created, domain.OrderTypeMarket)
	if cancel {
		t.Error("expected no cancel for market order")
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %s", reason)
	}
}

func TestEvaluatePartialFill_Filled(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	action, qty := se.EvaluatePartialFill(1.0, 1.0)
	if action != "filled" {
		t.Errorf("expected filled, got %s", action)
	}
	if qty != 1.0 {
		t.Errorf("expected qty 1.0, got %f", qty)
	}
}

func TestEvaluatePartialFill_ManagePartial(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	action, qty := se.EvaluatePartialFill(1.0, 0.9)
	if action != "manage_partial" {
		t.Errorf("expected manage_partial, got %s", action)
	}
	if qty != 0.9 {
		t.Errorf("expected qty 0.9, got %f", qty)
	}
}

func TestEvaluatePartialFill_CancelRemainder(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	action, qty := se.EvaluatePartialFill(1.0, 0.4)
	if action != "cancel_remainder" {
		t.Errorf("expected cancel_remainder, got %s", action)
	}
	if qty != 0.4 {
		t.Errorf("expected qty 0.4, got %f", qty)
	}
}

func TestEvaluatePartialFill_EdgeCases(t *testing.T) {
	se := NewSafetyEngine(defaultSafetyConfig())
	action, qty := se.EvaluatePartialFill(1.0, 0.4999)
	if action != "cancel_remainder" {
		t.Errorf("expected cancel_remainder just below 50%%, got %s", action)
	}
	action, qty = se.EvaluatePartialFill(1.0, 0.5)
	if action != "manage_partial" {
		t.Errorf("expected manage_partial at exactly 50%%, got %s", action)
	}
	action, qty = se.EvaluatePartialFill(1.0, 0.5001)
	if action != "manage_partial" {
		t.Errorf("expected manage_partial just above 50%%, got %s", action)
	}
	action, qty = se.EvaluatePartialFill(1.0, 0.9499)
	if action != "manage_partial" {
		t.Errorf("expected manage_partial just below 95%%, got %s", action)
	}
	action, qty = se.EvaluatePartialFill(1.0, 0.95)
	if action != "filled" {
		t.Errorf("expected filled at exactly 95%%, got %s", action)
	}
	if qty != 1.0 {
		t.Errorf("expected qty 1.0 for filled action, got %f", qty)
	}
	action, qty = se.EvaluatePartialFill(0, 0)
	if action != "cancel" {
		t.Errorf("expected cancel for zero intended, got %s", action)
	}
}

func TestEstimateSlippageFromOrderbook(t *testing.T) {
	plan := defaultOrderPlan()
	ob := domain.OrderBookSummary{EstimatedSlippageBps: 10}
	levels := []domain.OrderBookLevel{
		{Price: 50000, Size: 0.05},
		{Price: 50010, Size: 0.05},
	}
	pct, fillPrice := EstimateSlippageFromOrderbook(plan, ob, levels)
	if pct <= 0 {
		t.Errorf("expected positive slippage, got %f", pct)
	}
	if fillPrice <= 0 {
		t.Errorf("expected positive fill price, got %f", fillPrice)
	}
	// Average fill price should be between 50000 and 50010
	if fillPrice < 50000 || fillPrice > 50010 {
		t.Errorf("expected fill price between 50000 and 50010, got %f", fillPrice)
	}
}

func TestEstimateSlippageFromOrderbook_InsufficientDepth(t *testing.T) {
	plan := defaultOrderPlan()
	ob := domain.OrderBookSummary{EstimatedSlippageBps: 10}
	levels := []domain.OrderBookLevel{
		{Price: 50000, Size: 0.01}, // not enough qty
	}
	pct, fillPrice := EstimateSlippageFromOrderbook(plan, ob, levels)
	if pct != math.MaxFloat64 {
		t.Errorf("expected MaxFloat64 for insufficient depth, got %f", pct)
	}
	if fillPrice != 0 {
		t.Errorf("expected 0 fill price for insufficient depth, got %f", fillPrice)
	}
}

func assertContainsCheck(t *testing.T, checks []string, expected string) {
	t.Helper()
	for _, c := range checks {
		if c == expected {
			return
		}
	}
	t.Errorf("expected check %q in %v", expected, checks)
}
