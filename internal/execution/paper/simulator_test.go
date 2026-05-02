package paper

import (
	"context"
	"testing"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
	"github.com/virhan/botsurv/internal/risk"
)

type mockMarketData struct {
	price float64
	err   error
}

func (m *mockMarketData) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	return m.price, m.err
}

func newTestSimulator(price float64) *Simulator {
	md := &mockMarketData{price: price}
	cfg := app.PaperConfig{
		StartingBalanceUSD: 10000,
		FeeMakerBps:        2,
		FeeTakerBps:        5.5,
		SlippageBps:        5,
		DefaultLeverage:    5,
	}
	log := logger.New(nil, logger.Level("error"))
	return NewSimulator(md, nil, nil, cfg, log)
}

func TestSimulator_Initialize_NoDB(t *testing.T) {
	sim := newTestSimulator(100.0)
	err := sim.Initialize(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	state := sim.AccountState()
	if state.Equity != 10000 {
		t.Errorf("expected equity 10000, got %.2f", state.Equity)
	}
}

func TestSimulator_SimulateFill_Long(t *testing.T) {
	sim := newTestSimulator(100.0)
	_ = sim.Initialize(context.Background())

	plan := risk.OrderPlan{
		Symbol:         "BTCUSDT",
		Side:           domain.SideLong,
		Qty:            1.0,
		EntryPrice:     100.0,
		StopLoss:       95.0,
		TakeProfits:    []risk.TakeProfitPlan{{Price: 110.0, Qty: 0.5}, {Price: 120.0, Qty: 0.5}},
		Leverage:       5,
		MarginRequired: 200,
		RiskAmountUSD:  5,
	}

	trade, err := sim.SimulateFill(context.Background(), "test-decision-1", plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if trade.Symbol != "BTCUSDT" {
		t.Errorf("expected BTCUSDT, got %s", trade.Symbol)
	}
	if trade.Side != domain.SideLong {
		t.Errorf("expected LONG, got %s", trade.Side)
	}
	if trade.Qty != 1.0 {
		t.Errorf("expected qty 1.0, got %.4f", trade.Qty)
	}
	// Entry price should be > 100.0 due to slippage (long = 100.0 * 1.0005)
	if trade.EntryPrice <= 100.0 {
		t.Errorf("expected entry > 100.0 due to slippage, got %.4f", trade.EntryPrice)
	}
	if trade.StopLoss != 95.0 {
		t.Errorf("expected SL 95.0, got %.2f", trade.StopLoss)
	}
	if trade.TakeProfit != 110.0 {
		t.Errorf("expected TP 110.0, got %.2f", trade.TakeProfit)
	}
}

func TestSimulator_SimulateFill_Short(t *testing.T) {
	sim := newTestSimulator(100.0)
	_ = sim.Initialize(context.Background())

	plan := risk.OrderPlan{
		Symbol:         "BTCUSDT",
		Side:           domain.SideShort,
		Qty:            1.0,
		EntryPrice:     100.0,
		StopLoss:       105.0,
		TakeProfits:    []risk.TakeProfitPlan{{Price: 90.0, Qty: 1.0}},
		Leverage:       5,
		MarginRequired: 200,
	}

	trade, err := sim.SimulateFill(context.Background(), "test-decision-2", plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if trade.Side != domain.SideShort {
		t.Errorf("expected SHORT, got %s", trade.Side)
	}
	// Entry price should be < 100.0 due to slippage (short = 100.0 * 0.9995)
	if trade.EntryPrice >= 100.0 {
		t.Errorf("expected entry < 100.0 due to slippage, got %.4f", trade.EntryPrice)
	}
	if trade.StopLoss != 105.0 {
		t.Errorf("expected SL 105.0, got %.2f", trade.StopLoss)
	}
	if trade.TakeProfit != 90.0 {
		t.Errorf("expected TP 90.0, got %.2f", trade.TakeProfit)
	}
}

func TestSimulator_FeesDeducted(t *testing.T) {
	sim := newTestSimulator(100.0)
	_ = sim.Initialize(context.Background())

	plan := risk.OrderPlan{
		Symbol:         "BTCUSDT",
		Side:           domain.SideLong,
		Qty:            1.0,
		EntryPrice:     100.0,
		StopLoss:       95.0,
		TakeProfits:    []risk.TakeProfitPlan{{Price: 110.0, Qty: 1.0}},
		Leverage:       5,
		MarginRequired: 200,
	}

	trade, err := sim.SimulateFill(context.Background(), "test-decision-3", plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Fee should be: 1.0 * 100.05 * 0.00055 = ~0.055
	if trade.FeesPaid <= 0 {
		t.Error("expected fees > 0")
	}

	state := sim.AccountState()
	// Equity should have decreased by fee amount
	if state.Equity >= 10000 {
		t.Errorf("expected equity < 10000 after fee deduction, got %.2f", state.Equity)
	}
}

func TestSimulator_CheckOpenPositions_SLHit(t *testing.T) {
	sim := newTestSimulator(100.0)
	_ = sim.Initialize(context.Background())

	// Open a long position at 100, SL at 95
	plan := risk.OrderPlan{
		Symbol:         "BTCUSDT",
		Side:           domain.SideLong,
		Qty:            1.0,
		EntryPrice:     100.0,
		StopLoss:       95.0,
		TakeProfits:    []risk.TakeProfitPlan{{Price: 110.0, Qty: 1.0}},
		Leverage:       5,
		MarginRequired: 200,
	}

	trade, err := sim.SimulateFill(context.Background(), "test-decision-4", plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = trade

	// Simulate price dropping to 94 (below SL)
	// For CheckOpenPositions, we need to rebuild the simulator with new price
	// and mock the repo to return the open trade. Without repo, this is a no-op.
	sim2 := newTestSimulator(94.0)
	sim2.startingEquity = sim.startingEquity
	sim2.currentEquity = sim.currentEquity

	// Without repo, CheckOpenPositions won't find open trades
	err = sim2.CheckOpenPositions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSimulator_AccountState(t *testing.T) {
	sim := newTestSimulator(100.0)
	_ = sim.Initialize(context.Background())

	state := sim.AccountState()
	if state.Equity != 10000 {
		t.Errorf("expected equity 10000, got %.2f", state.Equity)
	}
	if state.Balance <= 0 {
		t.Error("expected balance > 0")
	}
}

func TestSimulator_EmptyTPPlan(t *testing.T) {
	sim := newTestSimulator(100.0)
	_ = sim.Initialize(context.Background())

	// Plan without explicit TP - should derive one from SL
	plan := risk.OrderPlan{
		Symbol:         "BTCUSDT",
		Side:           domain.SideLong,
		Qty:            1.0,
		EntryPrice:     100.0,
		StopLoss:       95.0,
		TakeProfits:    nil,
		Leverage:       5,
		MarginRequired: 200,
	}

	trade, err := sim.SimulateFill(context.Background(), "test-decision-5", plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Long with entry=100, SL=95 → risk=5, derived TP = 100+10=110
	if trade.TakeProfit != 110.0 {
		t.Errorf("expected derived TP 110.0, got %.2f", trade.TakeProfit)
	}
}
