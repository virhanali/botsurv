package paper

import (
	"context"
	"testing"
	"time"

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

type mockPaperTradeRepo struct {
	trades []domain.PaperTrade
}

func (m *mockPaperTradeRepo) Insert(ctx context.Context, t domain.PaperTrade) error {
	m.trades = append(m.trades, t)
	return nil
}
func (m *mockPaperTradeRepo) Update(ctx context.Context, t domain.PaperTrade) error {
	for i := range m.trades {
		if m.trades[i].PaperTradeID == t.PaperTradeID {
			m.trades[i] = t
			return nil
		}
	}
	return nil
}
func (m *mockPaperTradeRepo) GetOpen(ctx context.Context) ([]domain.PaperTrade, error) {
	var out []domain.PaperTrade
	for _, t := range m.trades {
		if t.ClosedAt == nil {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *mockPaperTradeRepo) GetByDecision(ctx context.Context, decisionID string) (*domain.PaperTrade, error) {
	return nil, nil
}
func (m *mockPaperTradeRepo) GetAll(ctx context.Context, since time.Time) ([]domain.PaperTrade, error) {
	return m.trades, nil
}
func (m *mockPaperTradeRepo) CountByExitReason(ctx context.Context, reason string, since time.Time) (int, error) {
	return 0, nil
}

type mockPaperAccountRepo struct {
	state domain.PaperAccountState
}

func (m *mockPaperAccountRepo) Get(ctx context.Context) (*domain.PaperAccountState, error) {
	return &m.state, nil
}
func (m *mockPaperAccountRepo) Update(ctx context.Context, s domain.PaperAccountState) error {
	m.state = s
	return nil
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

func newTestSimulatorWithRepos(price float64) (*Simulator, *mockPaperTradeRepo, *mockPaperAccountRepo) {
	md := &mockMarketData{price: price}
	cfg := app.PaperConfig{
		StartingBalanceUSD: 10000,
		FeeTakerBps:        5.5,
		SlippageBps:        5,
		DefaultLeverage:    5,
	}
	log := logger.New(nil, logger.Level("error"))
	tradeRepo := &mockPaperTradeRepo{}
	acctRepo := &mockPaperAccountRepo{
		state: domain.PaperAccountState{
			ID:              1,
			StartingEquity:  10000,
			CurrentEquity:    10000,
			TotalTrades:      0,
			Wins:             0,
			Losses:           0,
			RealizedPnL:      0,
			ConsecutiveLosses: 0,
		},
	}
	sim := NewSimulator(md, tradeRepo, acctRepo, cfg, log)
	return sim, tradeRepo, acctRepo
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

// --- Bug fix tests ---

func TestSimulator_ConsecutiveLosses_SLHit(t *testing.T) {
	sim, tradeRepo, _ := newTestSimulatorWithRepos(100.0)
	_ = sim.Initialize(context.Background())

	plan := risk.OrderPlan{
		Symbol:      "BTCUSDT",
		Side:        domain.SideLong,
		Qty:         1.0,
		EntryPrice:  100.0,
		StopLoss:    95.0,
		TakeProfits: []risk.TakeProfitPlan{{Price: 110.0, Qty: 1.0}},
		Leverage:    5,
	}

	trade, err := sim.SimulateFill(context.Background(), "decision-1", plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sim.ConsecutiveLosses() != 0 {
		t.Errorf("expected consecutive losses 0 after open, got %d", sim.ConsecutiveLosses())
	}
	if sim.Wins() != 0 {
		t.Errorf("expected wins 0 after open, got %d", sim.Wins())
	}
	if sim.Losses() != 0 {
		t.Errorf("expected losses 0 after open, got %d", sim.Losses())
	}

	// Change price to trigger SL (below 95)
	sim.md.(*mockMarketData).price = 94.0

	// Manually close the trade via closeTrade by simulating CheckOpenPositions
	err = sim.CheckOpenPositions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sim.ConsecutiveLosses() != 1 {
		t.Errorf("expected consecutive losses 1 after SL hit, got %d", sim.ConsecutiveLosses())
	}
	if sim.Losses() != 1 {
		t.Errorf("expected losses 1 after SL hit, got %d", sim.Losses())
	}
	if sim.Wins() != 0 {
		t.Errorf("expected wins 0 after SL hit, got %d", sim.Wins())
	}
	if sim.TotalTrades() != 1 {
		t.Errorf("expected totalTrades 1, got %d", sim.TotalTrades())
	}

	_ = tradeRepo
	_ = trade
}

func TestSimulator_ConsecutiveLosses_MultipleSLThenTPReset(t *testing.T) {
	sim, _, _ := newTestSimulatorWithRepos(100.0)
	_ = sim.Initialize(context.Background())

	// Open and close 3 losing trades
	for i := 0; i < 3; i++ {
		plan := risk.OrderPlan{
			Symbol:      "BTCUSDT",
			Side:        domain.SideLong,
			Qty:         1.0,
			EntryPrice:  100.0,
			StopLoss:    95.0,
			TakeProfits: []risk.TakeProfitPlan{{Price: 110.0, Qty: 1.0}},
			Leverage:    5,
		}

		// Open position at 100
		sim.md.(*mockMarketData).price = 100.0
		_, err := sim.SimulateFill(context.Background(), "decision-loss-"+string(rune('0'+i)), plan)
		if err != nil {
			t.Fatalf("unexpected error opening trade %d: %v", i, err)
		}

		// Price drops to trigger SL
		sim.md.(*mockMarketData).price = 94.0
		err = sim.CheckOpenPositions(context.Background())
		if err != nil {
			t.Fatalf("unexpected error checking positions %d: %v", i, err)
		}
	}

	if sim.ConsecutiveLosses() != 3 {
		t.Errorf("expected consecutive losses 3, got %d", sim.ConsecutiveLosses())
	}
	if sim.Losses() != 3 {
		t.Errorf("expected losses 3, got %d", sim.Losses())
	}

	// Now open a winning trade — consecutive losses should reset to 0
	plan := risk.OrderPlan{
		Symbol:      "BTCUSDT",
		Side:        domain.SideLong,
		Qty:         1.0,
		EntryPrice:  100.0,
		StopLoss:    95.0,
		TakeProfits: []risk.TakeProfitPlan{{Price: 110.0, Qty: 1.0}},
		Leverage:    5,
	}

	sim.md.(*mockMarketData).price = 100.0
	_, err := sim.SimulateFill(context.Background(), "decision-win", plan)
	if err != nil {
		t.Fatalf("unexpected error opening winning trade: %v", err)
	}

	// Price rises to trigger TP
	sim.md.(*mockMarketData).price = 111.0
	err = sim.CheckOpenPositions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error checking positions for win: %v", err)
	}

	if sim.ConsecutiveLosses() != 0 {
		t.Errorf("expected consecutive losses 0 after TP hit, got %d", sim.ConsecutiveLosses())
	}
	if sim.Wins() != 1 {
		t.Errorf("expected wins 1 after TP hit, got %d", sim.Wins())
	}
	if sim.Losses() != 3 {
		t.Errorf("expected losses still 3 after TP hit, got %d", sim.Losses())
	}
	if sim.TotalTrades() != 4 {
		t.Errorf("expected totalTrades 4, got %d", sim.TotalTrades())
	}
}

func TestSimulator_RehydratedPositionDoesNotInflateWinLossCount(t *testing.T) {
	md := &mockMarketData{price: 100.0}
	cfg := app.PaperConfig{
		StartingBalanceUSD: 10000,
		FeeTakerBps:        0,
		SlippageBps:        0,
		DefaultLeverage:    5,
	}
	log := logger.New(nil, logger.Level("error"))

	// Create a trade repo with a pre-existing open position from before init
	beforeInit := time.Now().Add(-1 * time.Hour)
	preExistingTrades := []domain.PaperTrade{
		{
			PaperTradeID: "pre-existing-1",
			Symbol:       "BTCUSDT",
			Side:         domain.SideLong,
			Qty:          1.0,
			EntryPrice:   100.0,
			StopLoss:     95.0,
			TakeProfit:   110.0,
			OpenedAt:     beforeInit,
		},
	}
	tradeRepo := &mockPaperTradeRepo{trades: preExistingTrades}
	acctRepo := &mockPaperAccountRepo{
		state: domain.PaperAccountState{
			ID:                 1,
			StartingEquity:    10000,
			CurrentEquity:      10000,
			TotalTrades:        0,
			Wins:               0,
			Losses:             0,
			RealizedPnL:        0,
			ConsecutiveLosses: 0,
		},
	}

	sim := NewSimulator(md, tradeRepo, acctRepo, cfg, log)
	err := sim.Initialize(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The pre-existing trade is from before init, so wins/losses/total should be 0
	if sim.Wins() != 0 {
		t.Errorf("expected wins 0 after init with pre-existing position, got %d", sim.Wins())
	}
	if sim.Losses() != 0 {
		t.Errorf("expected losses 0 after init, got %d", sim.Losses())
	}
	if sim.TotalTrades() != 0 {
		t.Errorf("expected totalTrades 0 after init, got %d", sim.TotalTrades())
	}

	// Now close the pre-existing position by SL (price below 95)
	md.price = 94.0
	err = sim.CheckOpenPositions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error checking positions: %v", err)
	}

	// The close should NOT increment losses because it's a rehydrated trade
	if sim.Losses() != 0 {
		t.Errorf("expected losses 0 after closing rehydrated trade (was already counted), got %d", sim.Losses())
	}
	if sim.Wins() != 0 {
		t.Errorf("expected wins 0 after closing rehydrated trade, got %d", sim.Wins())
	}
	if sim.ConsecutiveLosses() != 0 {
		t.Errorf("expected consecutive losses 0 after closing rehydrated trade (not re-counting), got %d", sim.ConsecutiveLosses())
	}
	// But totalTrades should still be 0 since no new trades were opened
	if sim.TotalTrades() != 0 {
		t.Errorf("expected totalTrades 0 (no new trades), got %d", sim.TotalTrades())
	}
	// Equity should still reflect the PnL of the closed position
	state := sim.AccountState()
	if state.RealizedPnL >= 0 {
		t.Errorf("expected negative realized PnL after SL hit, got %.2f", state.RealizedPnL)
	}
}

func TestSimulator_NewTradeAfterInit_IncrementsCounters(t *testing.T) {
	sim, _, _ := newTestSimulatorWithRepos(100.0)
	_ = sim.Initialize(context.Background())

	// Open a new trade after init
	plan := risk.OrderPlan{
		Symbol:      "BTCUSDT",
		Side:        domain.SideLong,
		Qty:         1.0,
		EntryPrice:  100.0,
		StopLoss:    95.0,
		TakeProfits: []risk.TakeProfitPlan{{Price: 110.0, Qty: 1.0}},
		Leverage:    5,
	}

	_, err := sim.SimulateFill(context.Background(), "decision-new", plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sim.TotalTrades() != 1 {
		t.Errorf("expected totalTrades 1 after new trade, got %d", sim.TotalTrades())
	}

	// Close via SL
	sim.md.(*mockMarketData).price = 94.0
	err = sim.CheckOpenPositions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sim.Losses() != 1 {
		t.Errorf("expected losses 1 after SL on new trade, got %d", sim.Losses())
	}
	if sim.ConsecutiveLosses() != 1 {
		t.Errorf("expected consecutive losses 1 after SL, got %d", sim.ConsecutiveLosses())
	}
}

func TestSimulator_MixedRehydratedAndNewPosition(t *testing.T) {
	md := &mockMarketData{price: 100.0}
	cfg := app.PaperConfig{
		StartingBalanceUSD: 10000,
		FeeTakerBps:        0,
		SlippageBps:        0,
		DefaultLeverage:    5,
	}
	log := logger.New(nil, logger.Level("error"))

	beforeInit := time.Now().Add(-1 * time.Hour)
	preExistingTrades := []domain.PaperTrade{
		{
			PaperTradeID: "pre-existing-sl",
			Symbol:       "BTCUSDT",
			Side:         domain.SideLong,
			Qty:          1.0,
			EntryPrice:   100.0,
			StopLoss:     95.0,
			TakeProfit:   110.0,
			OpenedAt:     beforeInit,
		},
	}
	tradeRepo := &mockPaperTradeRepo{trades: preExistingTrades}
	acctRepo := &mockPaperAccountRepo{
		state: domain.PaperAccountState{
			ID:                1,
			StartingEquity:    10000,
			CurrentEquity:      10000,
			TotalTrades:        5,
			Wins:               2,
			Losses:             3,
			RealizedPnL:        -50.0,
			ConsecutiveLosses:  2,
		},
	}

	sim := NewSimulator(md, tradeRepo, acctRepo, cfg, log)
	err := sim.Initialize(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify we loaded DB state
	if sim.TotalTrades() != 5 {
		t.Errorf("expected totalTrades 5 (from DB), got %d", sim.TotalTrades())
	}
	if sim.Wins() != 2 {
		t.Errorf("expected wins 2 (from DB), got %d", sim.Wins())
	}
	if sim.Losses() != 3 {
		t.Errorf("expected losses 3 (from DB), got %d", sim.Losses())
	}
	if sim.ConsecutiveLosses() != 2 {
		t.Errorf("expected consecutive losses 2 (from DB), got %d", sim.ConsecutiveLosses())
	}

	// Close the rehydrated position via SL — should NOT increment losses
	md.price = 94.0
	err = sim.CheckOpenPositions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sim.Losses() != 3 {
		t.Errorf("expected losses still 3 (rehydrated close shouldn't increment), got %d", sim.Losses())
	}
	if sim.ConsecutiveLosses() != 2 {
		t.Errorf("expected consecutive losses still 2 (rehydrated close shouldn't increment), got %d", sim.ConsecutiveLosses())
	}
	if sim.TotalTrades() != 5 {
		t.Errorf("expected totalTrades still 5, got %d", sim.TotalTrades())
	}

	// Now open a new trade and close it via SL — this SHOULD increment
	md.price = 100.0
	plan := risk.OrderPlan{
		Symbol:      "BTCUSDT",
		Side:        domain.SideLong,
		Qty:         1.0,
		EntryPrice:  100.0,
		StopLoss:    95.0,
		TakeProfits: []risk.TakeProfitPlan{{Price: 110.0, Qty: 1.0}},
		Leverage:    5,
	}
	_, err = sim.SimulateFill(context.Background(), "decision-new", plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sim.TotalTrades() != 6 {
		t.Errorf("expected totalTrades 6 after new trade, got %d", sim.TotalTrades())
	}

	// Close new trade via SL
	md.price = 94.0
	err = sim.CheckOpenPositions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sim.Losses() != 4 {
		t.Errorf("expected losses 4 (3 from DB + 1 new), got %d", sim.Losses())
	}
	if sim.ConsecutiveLosses() != 3 {
		t.Errorf("expected consecutive losses 3 (2 from DB + 1 new loss), got %d", sim.ConsecutiveLosses())
	}
}

func TestSimulator_CancelOpenPositions(t *testing.T) {
	md := &mockMarketData{price: 100.0}
	cfg := app.PaperConfig{
		StartingBalanceUSD: 10000,
		FeeTakerBps:        0,
		SlippageBps:        0,
		DefaultLeverage:    5,
	}
	log := logger.New(nil, logger.Level("error"))

	tradeRepo := &mockPaperTradeRepo{
		trades: []domain.PaperTrade{
			{PaperTradeID: "open-1", Symbol: "BTCUSDT", Side: domain.SideLong, Qty: 1.0, EntryPrice: 100.0, StopLoss: 95.0, TakeProfit: 110.0, OpenedAt: time.Now()},
			{PaperTradeID: "open-2", Symbol: "ETHUSDT", Side: domain.SideShort, Qty: 0.5, EntryPrice: 3000.0, StopLoss: 3100.0, TakeProfit: 2800.0, OpenedAt: time.Now()},
		},
	}
	acctRepo := &mockPaperAccountRepo{
		state: domain.PaperAccountState{ID: 1, StartingEquity: 10000, CurrentEquity: 10000},
	}

	sim := NewSimulator(md, tradeRepo, acctRepo, cfg, log)
	_ = sim.Initialize(context.Background())

	err := sim.CancelOpenPositions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All trades should be closed now
	openTrades, _ := tradeRepo.GetOpen(context.Background())
	if len(openTrades) != 0 {
		t.Errorf("expected 0 open trades after cancel, got %d", len(openTrades))
	}

	// Verify trades are cancelled
	for _, tr := range tradeRepo.trades {
		if tr.ExitReason != "cancelled" {
			t.Errorf("expected exit_reason 'cancelled', got %s", tr.ExitReason)
		}
		if tr.ClosedAt == nil {
			t.Error("expected closed_at to be set")
		}
	}
}

func TestSimulator_Accessors(t *testing.T) {
	sim := newTestSimulator(100.0)
	_ = sim.Initialize(context.Background())

	if sim.Wins() != 0 {
		t.Errorf("expected initial wins 0, got %d", sim.Wins())
	}
	if sim.Losses() != 0 {
		t.Errorf("expected initial losses 0, got %d", sim.Losses())
	}
	if sim.TotalTrades() != 0 {
		t.Errorf("expected initial totalTrades 0, got %d", sim.TotalTrades())
	}
	if sim.ConsecutiveLosses() != 0 {
		t.Errorf("expected initial consecutiveLosses 0, got %d", sim.ConsecutiveLosses())
	}
	if sim.GetRealizedPnL() != 0 {
		t.Errorf("expected initial realizedPnL 0, got %.2f", sim.GetRealizedPnL())
	}
}

func TestSimulator_SetConsecutiveLosses(t *testing.T) {
	sim := newTestSimulator(100.0)
	_ = sim.Initialize(context.Background())

	sim.SetConsecutiveLosses(3)
	if sim.ConsecutiveLosses() != 3 {
		t.Errorf("expected consecutive losses 3, got %d", sim.ConsecutiveLosses())
	}

	sim.SetConsecutiveLosses(0)
	if sim.ConsecutiveLosses() != 0 {
		t.Errorf("expected consecutive losses 0, got %d", sim.ConsecutiveLosses())
	}
}

func TestSimulator_CloseViaTPhit(t *testing.T) {
	sim, _, _ := newTestSimulatorWithRepos(100.0)
	_ = sim.Initialize(context.Background())

	plan := risk.OrderPlan{
		Symbol:      "BTCUSDT",
		Side:        domain.SideLong,
		Qty:         1.0,
		EntryPrice:  100.0,
		StopLoss:    95.0,
		TakeProfits: []risk.TakeProfitPlan{{Price: 110.0, Qty: 1.0}},
		Leverage:    5,
	}

	_, err := sim.SimulateFill(context.Background(), "decision-tp", plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Price rises above TP
	sim.md.(*mockMarketData).price = 111.0
	err = sim.CheckOpenPositions(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sim.Wins() != 1 {
		t.Errorf("expected wins 1 after TP hit, got %d", sim.Wins())
	}
	if sim.Losses() != 0 {
		t.Errorf("expected losses 0 after TP hit, got %d", sim.Losses())
	}
	if sim.ConsecutiveLosses() != 0 {
		t.Errorf("expected consecutive losses 0 after TP hit, got %d", sim.ConsecutiveLosses())
	}

	state := sim.AccountState()
	if state.RealizedPnL <= 0 {
		t.Errorf("expected positive realized PnL after TP hit, got %.2f", state.RealizedPnL)
	}
}
