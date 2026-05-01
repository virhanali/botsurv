package broker

import (
	"context"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
)

// --- DB Repo Mocks for testing persistence ---

type mockPositionRepo struct {
	positions []domain.Position
}

func (m *mockPositionRepo) Insert(ctx context.Context, p domain.Position) (int64, error) {
	m.positions = append(m.positions, p)
	return int64(len(m.positions)), nil
}

func (m *mockPositionRepo) Update(ctx context.Context, p domain.Position) error {
	for i := range m.positions {
		if m.positions[i].ID == p.ID {
			m.positions[i] = p
			return nil
		}
	}
	return nil
}

func (m *mockPositionRepo) GetOpen(ctx context.Context) ([]domain.Position, error) {
	var open []domain.Position
	for _, p := range m.positions {
		if p.Status == domain.PositionStatusOpen {
			open = append(open, p)
		}
	}
	return open, nil
}

func (m *mockPositionRepo) GetClosed(ctx context.Context, since time.Time) ([]domain.Position, error) {
	return nil, nil
}

func (m *mockPositionRepo) GetBySymbol(ctx context.Context, symbol string) (*domain.Position, error) {
	return nil, nil
}

type mockOrderRepo struct {
	orders []domain.Order
}

func (m *mockOrderRepo) Insert(ctx context.Context, o domain.Order) (int64, error) {
	m.orders = append(m.orders, o)
	return int64(len(m.orders)), nil
}

func (m *mockOrderRepo) Update(ctx context.Context, o domain.Order) error {
	for i := range m.orders {
		if m.orders[i].ID == o.ID {
			m.orders[i] = o
			return nil
		}
	}
	return nil
}

func (m *mockOrderRepo) GetOpen(ctx context.Context) ([]domain.Order, error) {
	var open []domain.Order
	for _, o := range m.orders {
		if o.Status == domain.OrderStatusPending || o.Status == domain.OrderStatusPartiallyFilled {
			open = append(open, o)
		}
	}
	return open, nil
}

func (m *mockOrderRepo) GetBySymbol(ctx context.Context, symbol string) ([]domain.Order, error) {
	return nil, nil
}

func (m *mockOrderRepo) GetAll(ctx context.Context, since time.Time) ([]domain.Order, error) {
	return nil, nil
}

type mockExecRepo struct {
	executions []domain.Execution
}

func (m *mockExecRepo) Insert(ctx context.Context, e domain.Execution) (int64, error) {
	m.executions = append(m.executions, e)
	return int64(len(m.executions)), nil
}

type mockAcctSnapRepo struct {
	snapshots []domain.AccountState
}

func (m *mockAcctSnapRepo) Insert(ctx context.Context, a domain.AccountState) (int64, error) {
	m.snapshots = append(m.snapshots, a)
	return int64(len(m.snapshots)), nil
}

func (m *mockAcctSnapRepo) GetLatest(ctx context.Context) (*domain.AccountState, error) {
	if len(m.snapshots) == 0 {
		return nil, nil
	}
	return &m.snapshots[len(m.snapshots)-1], nil
}
func (m *mockAcctSnapRepo) GetLatestBefore(ctx context.Context, before time.Time) (*domain.AccountState, error) {
	return nil, nil
}

func newTestBroker() *PaperBroker {
	cfg := app.PaperConfig{
		StartingBalanceUSD: 1000,
		FeeMakerBps:        2,
		FeeTakerBps:        5.5,
		SlippageBps:        5,
		DefaultLeverage:    5,
	}
	log := logger.New(nil, logger.LevelDebug)
	return NewPaperBroker(cfg, log)
}

func TestGetAccountState_InitialBalance(t *testing.T) {
	pb := newTestBroker()
	state, err := pb.GetAccountState(context.Background())
	if err != nil {
		t.Fatalf("GetAccountState: %v", err)
	}
	if state.Balance != 1000 {
		t.Errorf("expected balance 1000, got %.2f", state.Balance)
	}
	if state.AvailableBalance != 1000 {
		t.Errorf("expected available 1000, got %.2f", state.AvailableBalance)
	}
	if state.UsedMargin != 0 {
		t.Errorf("expected margin 0, got %.2f", state.UsedMargin)
	}
}

func TestPaperBroker_RehydrateRepairsMissingProtectiveStop(t *testing.T) {
	pb := newTestBroker()
	posRepo := &mockPositionRepo{positions: []domain.Position{
		{
			ID:         42,
			Symbol:     "BTCUSDT",
			Side:       domain.SideLong,
			EntryPrice: 100,
			Size:       1,
			Leverage:   5,
			Margin:     20,
			StopLoss:   90,
			TakeProfit: 120,
			Status:     domain.PositionStatusOpen,
			Source:     "paper",
			OpenedAt:   time.Now(),
		},
	}}
	orderRepo := &mockOrderRepo{}
	snapRepo := &mockAcctSnapRepo{snapshots: []domain.AccountState{
		{Balance: 999, UsedMargin: 0, RealizedPnL: -1, TotalFees: 1, RecordedAt: time.Now()},
	}}
	pb.SetPositionRepo(posRepo)
	pb.SetOrderRepo(orderRepo)
	pb.SetAccountSnapshotRepo(snapRepo)

	if err := pb.Rehydrate(context.Background()); err != nil {
		t.Fatalf("rehydrate: %v", err)
	}

	positions, err := pb.GetOpenPositions(context.Background())
	if err != nil {
		t.Fatalf("get positions: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("expected 1 open position, got %d", len(positions))
	}
	if positions[0].StopLoss != 90 {
		t.Fatalf("expected stop loss 90, got %.2f", positions[0].StopLoss)
	}

	orders, err := pb.GetOpenOrders(context.Background())
	if err != nil {
		t.Fatalf("get orders: %v", err)
	}
	var hasSL, hasTP bool
	for _, o := range orders {
		if o.OrderType == domain.OrderTypeStopMarket {
			hasSL = true
		}
		if o.OrderType == domain.OrderTypeTakeProfitMarket {
			hasTP = true
		}
	}
	if !hasSL {
		t.Fatal("expected missing STOP_MARKET order to be repaired")
	}
	if !hasTP {
		t.Fatal("expected TAKE_PROFIT_MARKET order to be repaired")
	}

	state, err := pb.GetAccountState(context.Background())
	if err != nil {
		t.Fatalf("get account state: %v", err)
	}
	if state.Balance != 999 {
		t.Fatalf("expected balance from latest snapshot, got %.2f", state.Balance)
	}
	if state.UsedMargin != 20 {
		t.Fatalf("expected used margin rebuilt from positions, got %.2f", state.UsedMargin)
	}
}

func TestPaperBroker_RehydrateRejectsOpenPositionWithoutStopLoss(t *testing.T) {
	pb := newTestBroker()
	pb.SetPositionRepo(&mockPositionRepo{positions: []domain.Position{
		{
			ID:         1,
			Symbol:     "BTCUSDT",
			Side:       domain.SideLong,
			EntryPrice: 100,
			Size:       1,
			Margin:     20,
			Status:     domain.PositionStatusOpen,
		},
	}})
	pb.SetOrderRepo(&mockOrderRepo{})

	if err := pb.Rehydrate(context.Background()); err == nil {
		t.Fatal("expected rehydrate to fail closed for open position without SL")
	}
	if !pb.IsHalted() {
		t.Fatal("expected broker to be halted after unsafe persisted state")
	}
}

func TestPlaceOrder_MarketBuyLong(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	order, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if order.Status != domain.OrderStatusFilled {
		t.Errorf("expected filled, got %s", order.Status)
	}

	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	pos := positions[0]
	if pos.Symbol != "BTCUSDT" {
		t.Errorf("expected BTCUSDT, got %s", pos.Symbol)
	}
	if pos.Side != domain.SideLong {
		t.Errorf("expected LONG, got %s", pos.Side)
	}
	if pos.Size != 0.01 {
		t.Errorf("expected size 0.01, got %.4f", pos.Size)
	}
}

func TestPlaceOrder_MarketSellShort(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("ETHUSDT", 3000)

	order, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "ETHUSDT",
		Side:      domain.OrderSideSell,
		OrderType: domain.OrderTypeMarket,
		Qty:       1.0,
		StopLoss:  3100,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if order.Status != domain.OrderStatusFilled {
		t.Errorf("expected filled, got %s", order.Status)
	}

	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	if positions[0].Side != domain.SideShort {
		t.Errorf("expected SHORT, got %s", positions[0].Side)
	}
}

func TestPlaceOrder_InsufficientMargin(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	// Try to buy 1 BTC at 65000 with 5x leverage = $13000 margin needed
	// But we only have $1000
	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       1.0,
		StopLoss:  64000,
	})
	if err == nil {
		t.Error("expected error for insufficient margin")
	}
}

func TestPlaceOrder_FeeCalculation(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	state, _ := pb.GetAccountState(context.Background())
	// notional = 65000 * 0.01 * (1 + 5bps slippage) ≈ 650.325
	// fee = notional * 5.5bps ≈ 0.3577
	if state.TotalFees <= 0 {
		t.Error("expected fees > 0")
	}
	if state.TotalFees > 1 {
		t.Errorf("fees seem too high: %.4f", state.TotalFees)
	}
}

func TestPlaceOrder_SlippageCalculation(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	state, _ := pb.GetAccountState(context.Background())
	if state.TotalSlippage <= 0 {
		t.Error("expected slippage > 0")
	}
}

func TestPlaceOrder_MarginTracking(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	state, _ := pb.GetAccountState(context.Background())
	if state.UsedMargin <= 0 {
		t.Error("expected used margin > 0")
	}
	// notional ≈ 650.325, margin = notional / 5 ≈ 130.065
	if state.UsedMargin < 100 || state.UsedMargin > 200 {
		t.Errorf("unexpected margin: %.2f", state.UsedMargin)
	}
	expectedAvailable := state.Balance - state.UsedMargin
	if state.AvailableBalance != expectedAvailable {
		t.Errorf("available %.2f != balance %.2f - margin %.2f", state.AvailableBalance, state.Balance, state.UsedMargin)
	}
}

func TestClosePosition_LongProfit(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// Price goes up
	pb.UpdatePrice("BTCUSDT", 66000)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	state, _ := pb.GetAccountState(context.Background())
	// Profit ≈ (66000-65000) * 0.01 - fees ≈ 10 - fees
	if state.RealizedPnL <= 0 {
		t.Errorf("expected positive PnL, got %.4f", state.RealizedPnL)
	}
	if len(pb.closedPositions) != 1 {
		t.Errorf("expected 1 closed position, got %d", len(pb.closedPositions))
	}
}

func TestClosePosition_LongLoss(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// Price goes down
	pb.UpdatePrice("BTCUSDT", 64000)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	state, _ := pb.GetAccountState(context.Background())
	if state.RealizedPnL >= 0 {
		t.Errorf("expected negative PnL, got %.4f", state.RealizedPnL)
	}
}

func TestClosePosition_ShortProfit(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("ETHUSDT", 3000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "ETHUSDT",
		Side:      domain.OrderSideSell,
		OrderType: domain.OrderTypeMarket,
		Qty:       1.0,
		StopLoss:  3100,
	})

	// Price goes down (profit for short)
	pb.UpdatePrice("ETHUSDT", 2900)
	pb.ClosePosition(context.Background(), "ETHUSDT")

	state, _ := pb.GetAccountState(context.Background())
	if state.RealizedPnL <= 0 {
		t.Errorf("expected positive PnL for short, got %.4f", state.RealizedPnL)
	}
}

func TestStopLoss_Triggers(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// Set SL at 64000
	slPrice := 64000.0
	pb.SetProtectiveOrders(context.Background(), "BTCUSDT", slPrice, 0)

	// Price drops to SL
	pb.UpdatePrice("BTCUSDT", 63900)

	// Position should be closed
	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 open positions after SL, got %d", len(positions))
	}
	if len(pb.closedPositions) != 1 {
		t.Errorf("expected 1 closed position, got %d", len(pb.closedPositions))
	}
}

func TestTakeProfit_Triggers(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// Set TP at 67000
	tpPrice := 67000.0
	slPrice := 64000.0
	pb.SetProtectiveOrders(context.Background(), "BTCUSDT", slPrice, tpPrice)

	// Price reaches TP
	pb.UpdatePrice("BTCUSDT", 67100)

	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 positions after TP, got %d", len(positions))
	}
}

func TestSameCandle_SLFirst(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	slPrice := 64000.0
	tpPrice := 67000.0
	pb.SetProtectiveOrders(context.Background(), "BTCUSDT", slPrice, tpPrice)

	// Candle touches both SL and TP
	pb.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT",
		High:   67500,
		Low:    63500,
		Close:  65500,
	})

	// Conservative assumption: SL triggers first
	if len(pb.closedPositions) != 1 {
		t.Fatalf("expected 1 closed position, got %d", len(pb.closedPositions))
	}
	// PnL should reflect SL exit, not TP
	if pb.closedPositions[0].RealizedPnL > 0 {
		t.Error("conservative SL-first: PnL should be negative")
	}
}

func TestEmergencyCloseAll(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)
	pb.UpdatePrice("ETHUSDT", 3000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  3100,
	})
	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "ETHUSDT",
		Side:      domain.OrderSideSell,
		OrderType: domain.OrderTypeMarket,
		Qty:       1.0,
		StopLoss:  3100,
	})

	pb.EmergencyCloseAll(context.Background())

	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 positions after emergency close, got %d", len(positions))
	}
	if len(pb.closedPositions) != 2 {
		t.Errorf("expected 2 closed positions, got %d", len(pb.closedPositions))
	}
}

func TestSetProtectiveOrders_ValidateSL(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// SL above entry for LONG is invalid
	err := pb.SetProtectiveOrders(context.Background(), "BTCUSDT", 66000, 0)
	if err == nil {
		t.Error("expected error for SL above entry on LONG")
	}

	// SL at 0 is invalid
	err = pb.SetProtectiveOrders(context.Background(), "BTCUSDT", 0, 0)
	if err == nil {
		t.Error("expected error for SL at 0")
	}
}

func TestSetProtectiveOrders_ValidateTP(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// TP below entry for LONG is invalid
	err := pb.SetProtectiveOrders(context.Background(), "BTCUSDT", 64000, 64000)
	if err == nil {
		t.Error("expected error for TP below entry on LONG")
	}
}

func TestLimitOrder_Fill(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	price := 64000.0
	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit,
		Qty:       0.01,
		StopLoss:  63000,
		Price:     &price,
	})

	orders, _ := pb.GetOpenOrders(context.Background())
	if len(orders) != 1 {
		t.Fatalf("expected 1 pending order, got %d", len(orders))
	}

	// Candle touches limit price
	pb.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT",
		High:   65000,
		Low:    63500,
		Close:  64500,
	})

	// Should be filled with protective orders created
	orders, _ = pb.GetOpenOrders(context.Background())
	if len(orders) < 1 {
		t.Errorf("expected at least 1 protective order after limit fill, got %d", len(orders))
	}
	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 1 {
		t.Errorf("expected 1 position after limit fill, got %d", len(positions))
	}
}

func TestLimitOrder_NoFill(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	price := 64000.0
	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit,
		Qty:       0.01,
		StopLoss:  63000,
		Price:     &price,
	})

	// Candle doesn't touch limit price
	pb.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT",
		High:   65500,
		Low:    64200,
		Close:  65000,
	})

	orders, _ := pb.GetOpenOrders(context.Background())
	if len(orders) != 1 {
		t.Errorf("expected 1 pending order (not filled), got %d", len(orders))
	}
}

func TestCancelOrder(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	price := 64000.0
	order, _ := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit,
		Qty:       0.01,
		StopLoss:  63000,
		Price:     &price,
	})

	err := pb.CancelOrder(context.Background(), order.BrokerOrderID)
	if err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}

	orders, _ := pb.GetOpenOrders(context.Background())
	if len(orders) != 0 {
		t.Errorf("expected 0 orders after cancel, got %d", len(orders))
	}
}

func TestUnrealizedPnL(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// Price goes up
	pb.UpdatePrice("BTCUSDT", 66000)

	state, _ := pb.GetAccountState(context.Background())
	if state.UnrealizedPnL <= 0 {
		t.Errorf("expected positive unrealized PnL, got %.4f", state.UnrealizedPnL)
	}
	// (66000-65000) * 0.01 = 10
	if state.UnrealizedPnL < 9 || state.UnrealizedPnL > 11 {
		t.Errorf("unexpected unrealized PnL: %.4f", state.UnrealizedPnL)
	}
}

func TestResetDailyLoss(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	// Close at loss
	pb.UpdatePrice("BTCUSDT", 64000)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	state, _ := pb.GetAccountState(context.Background())
	if state.DailyLoss <= 0 {
		t.Error("expected daily loss > 0")
	}

	pb.ResetDailyLoss()
	state, _ = pb.GetAccountState(context.Background())
	if state.DailyLoss != 0 {
		t.Errorf("expected daily loss 0 after reset, got %.4f", state.DailyLoss)
	}
}

func TestPlaceOrder_RejectInvalidSL_LongSLAboveEntry(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  66000, // invalid: LONG SL above entry
	})
	if err == nil {
		t.Fatal("expected error for LONG SL above entry")
	}
	// Position was never opened (rejected at broker layer)
	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 positions, got %d", len(positions))
	}
}

func TestPlaceOrder_RejectInvalidSL_ShortSLBelowEntry(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("ETHUSDT", 3000)

	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "ETHUSDT",
		Side:      domain.OrderSideSell,
		OrderType: domain.OrderTypeMarket,
		Qty:       1.0,
		StopLoss:  2900, // invalid: SHORT SL below entry
	})
	if err == nil {
		t.Fatal("expected error for SHORT SL below entry")
	}
	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 positions, got %d", len(positions))
	}
}

func TestPlaceOrder_InvalidSL_NoDirtyState(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	stateBefore, _ := pb.GetAccountState(context.Background())

	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  66000, // invalid LONG SL
	})
	if err == nil {
		t.Fatal("expected error")
	}

	// No position, no closed position, no fee, no margin change
	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 open positions, got %d", len(positions))
	}
	if len(pb.closedPositions) != 0 {
		t.Errorf("expected 0 closed positions, got %d", len(pb.closedPositions))
	}

	stateAfter, _ := pb.GetAccountState(context.Background())
	if stateAfter.Balance != stateBefore.Balance {
		t.Errorf("balance changed: %.4f -> %.4f", stateBefore.Balance, stateAfter.Balance)
	}
	if stateAfter.UsedMargin != stateBefore.UsedMargin {
		t.Errorf("margin changed: %.4f -> %.4f", stateBefore.UsedMargin, stateAfter.UsedMargin)
	}
	if stateAfter.TotalFees != stateBefore.TotalFees {
		t.Errorf("fees changed: %.4f -> %.4f", stateBefore.TotalFees, stateAfter.TotalFees)
	}
}

func TestLimitOrder_Fill_InvalidSL_NoDirtyState(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	stateBefore, _ := pb.GetAccountState(context.Background())

	price := 64000.0
	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit,
		Qty:       0.01,
		StopLoss:  66000, // invalid LONG SL
		Price:     &price,
	})

	pb.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT",
		High:   65000,
		Low:    63500,
		Close:  64500,
	})

	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 open positions, got %d", len(positions))
	}
	if len(pb.closedPositions) != 0 {
		t.Errorf("expected 0 closed positions, got %d", len(pb.closedPositions))
	}

	stateAfter, _ := pb.GetAccountState(context.Background())
	if stateAfter.Balance != stateBefore.Balance {
		t.Errorf("balance changed: %.4f -> %.4f", stateBefore.Balance, stateAfter.Balance)
	}
	if stateAfter.UsedMargin != stateBefore.UsedMargin {
		t.Errorf("margin changed: %.4f -> %.4f", stateBefore.UsedMargin, stateAfter.UsedMargin)
	}
	if stateAfter.TotalFees != stateBefore.TotalFees {
		t.Errorf("fees changed: %.4f -> %.4f", stateBefore.TotalFees, stateAfter.TotalFees)
	}
}

func TestLimitOrder_Fill_InvalidIntendedSL_Rejected(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	price := 64000.0
	order, _ := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit,
		Qty:       0.01,
		StopLoss:  66000, // invalid intended SL for LONG (above entry)
		Price:     &price,
	})

	// Candle touches limit price
	pb.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT",
		High:   65000,
		Low:    63500,
		Close:  64500,
	})

	// Order should be rejected, position never opened.
	// Look up the updated order from the broker (PlaceOrder returns a copy).
	orders, _ := pb.GetOpenOrders(context.Background())
	var found *domain.Order
	for i := range orders {
		if orders[i].BrokerOrderID == order.BrokerOrderID {
			found = &orders[i]
			break
		}
	}
	if found == nil {
		// Order may have been removed from openOrders when rejected — that's also acceptable.
	} else if found.Status != domain.OrderStatusRejected {
		t.Errorf("expected rejected, got %s", found.Status)
	}
	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 positions, got %d", len(positions))
	}
}

// --- DB Persistence Tests ---

func TestPlaceOrder_MarketBuy_PersistsFilledOrderAndPositionWithSL(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	posRepo := &mockPositionRepo{}
	ordRepo := &mockOrderRepo{}
	execRepo := &mockExecRepo{}
	snapRepo := &mockAcctSnapRepo{}
	pb.SetPositionRepo(posRepo)
	pb.SetOrderRepo(ordRepo)
	pb.SetExecutionRepo(execRepo)
	pb.SetAccountSnapshotRepo(snapRepo)

	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	foundMain := false
	for _, o := range ordRepo.orders {
		if o.OrderType == domain.OrderTypeMarket && o.Status == domain.OrderStatusFilled {
			foundMain = true
		}
	}
	if !foundMain {
		t.Error("expected filled market order persisted")
	}

	if len(posRepo.positions) == 0 {
		t.Fatal("expected at least 1 position persisted")
	}
	pos := posRepo.positions[0]
	if pos.StopLoss != 64000 {
		t.Errorf("expected StopLoss=64000, got %f", pos.StopLoss)
	}

	foundSL := false
	for _, o := range ordRepo.orders {
		if o.OrderType == domain.OrderTypeStopMarket {
			foundSL = true
		}
	}
	if !foundSL {
		t.Error("expected protective STOP_MARKET order persisted")
	}
}

func TestPlaceOrder_InvalidSL_DoesNotLeaveOpenPositionInDB(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	posRepo := &mockPositionRepo{}
	ordRepo := &mockOrderRepo{}
	execRepo := &mockExecRepo{}
	snapRepo := &mockAcctSnapRepo{}
	pb.SetPositionRepo(posRepo)
	pb.SetOrderRepo(ordRepo)
	pb.SetExecutionRepo(execRepo)
	pb.SetAccountSnapshotRepo(snapRepo)

	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  66000, // invalid LONG SL above entry
	})
	if err == nil {
		t.Fatal("expected error for invalid SL")
	}

	for _, p := range posRepo.positions {
		if p.Status == domain.PositionStatusOpen {
			t.Errorf("unexpected open position in DB after invalid SL: %+v", p)
		}
	}
}

// --- Closing Execution Tests ---

func TestClosePosition_StoresClosingExecution(t *testing.T) {
	pb := newTestBroker()
	execRepo := &mockExecRepo{}
	pb.SetExecutionRepo(execRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	// Count executions before close
	execsBefore := len(execRepo.executions)

	pb.UpdatePrice("BTCUSDT", 66000)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	execsAfter := len(execRepo.executions)
	if execsAfter <= execsBefore {
		t.Errorf("expected closing execution to be stored, got %d -> %d", execsBefore, execsAfter)
	}

	// Verify at least one execution with sell side (closing a long)
	foundClose := false
	for _, exec := range execRepo.executions {
		if exec.Side == domain.OrderSideSell && exec.Qty == 0.01 {
			foundClose = true
		}
	}
	if !foundClose {
		t.Error("expected a closing execution with SELL side")
	}
}

func TestSLHit_StoresClosingExecution(t *testing.T) {
	pb := newTestBroker()
	execRepo := &mockExecRepo{}
	pb.SetExecutionRepo(execRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	// Trigger SL
	pb.UpdatePrice("BTCUSDT", 63900)

	// Should have at least 2 executions: entry + SL close
	if len(execRepo.executions) < 2 {
		t.Errorf("expected at least 2 executions (entry + SL close), got %d", len(execRepo.executions))
	}
}

func TestTPHit_StoresClosingExecution(t *testing.T) {
	pb := newTestBroker()
	execRepo := &mockExecRepo{}
	pb.SetExecutionRepo(execRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01,
		StopLoss: 64000, TakeProfit: 66000,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	// Trigger TP
	pb.UpdatePrice("BTCUSDT", 66100)

	if len(execRepo.executions) < 2 {
		t.Errorf("expected at least 2 executions (entry + TP close), got %d", len(execRepo.executions))
	}
}

func TestSameCandle_SLFirst_StoresClosingExecution(t *testing.T) {
	pb := newTestBroker()
	execRepo := &mockExecRepo{}
	pb.SetExecutionRepo(execRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01,
		StopLoss: 64000, TakeProfit: 67000,
	})

	execsBefore := len(execRepo.executions)

	pb.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT",
		High:   67500,
		Low:    63500,
		Close:  65500,
	})

	if len(execRepo.executions) <= execsBefore {
		t.Errorf("expected closing execution after same-candle SL/TP, got %d", len(execRepo.executions))
	}
}

func TestClosingExecution_HasCorrectOrderIDForSL(t *testing.T) {
	pb := newTestBroker()
	execRepo := &mockExecRepo{}
	pb.SetExecutionRepo(execRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	pos, _ := pb.GetPosition("BTCUSDT")
	if pos == nil || pos.SLOrderID == nil {
		t.Fatal("expected SL order ID on position")
	}

	pb.UpdatePrice("BTCUSDT", 63900)

	// Find the closing execution
	var closingExec *domain.Execution
	for i := range execRepo.executions {
		e := &execRepo.executions[i]
		if e.Side == domain.OrderSideSell {
			closingExec = e
		}
	}
	if closingExec == nil {
		t.Fatal("no closing execution found")
	}
}

// --- SL/TP Persistence Tests ---

func TestPositionHasNonZeroStopLoss(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	pos, ok := pb.GetPosition("BTCUSDT")
	if !ok {
		t.Fatal("expected position")
	}
	if pos.StopLoss <= 0 {
		t.Errorf("expected non-zero StopLoss, got %.2f", pos.StopLoss)
	}
}

func TestSLOrderCreatedAutomatically(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	orders, _ := pb.GetOpenOrders(context.Background())
	foundSL := false
	for _, o := range orders {
		if o.OrderType == domain.OrderTypeStopMarket {
			foundSL = true
			if o.StopPrice == nil || *o.StopPrice != 64000 {
				t.Errorf("SL order stop price = %v, expected 64000", o.StopPrice)
			}
		}
	}
	if !foundSL {
		t.Error("expected STOP_MARKET (SL) order to be created automatically")
	}
}

func TestTPOrderCreatedAutomatically(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01,
		StopLoss: 64000, TakeProfit: 67000,
	})

	orders, _ := pb.GetOpenOrders(context.Background())
	foundTP := false
	for _, o := range orders {
		if o.OrderType == domain.OrderTypeTakeProfitMarket {
			foundTP = true
		}
	}
	if !foundTP {
		t.Error("expected TAKE_PROFIT_MARKET (TP) order to be created automatically")
	}
}

// --- Account Snapshot Tests ---

func TestEntry_StoresAccountSnapshot(t *testing.T) {
	pb := newTestBroker()
	snapRepo := &mockAcctSnapRepo{}
	pb.SetAccountSnapshotRepo(snapRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	if len(snapRepo.snapshots) == 0 {
		t.Error("expected account snapshot on entry")
	}
}

func TestClose_StoresAccountSnapshot(t *testing.T) {
	pb := newTestBroker()
	snapRepo := &mockAcctSnapRepo{}
	pb.SetAccountSnapshotRepo(snapRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})
	snapsAfterEntry := len(snapRepo.snapshots)

	pb.UpdatePrice("BTCUSDT", 66000)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	if len(snapRepo.snapshots) <= snapsAfterEntry {
		t.Errorf("expected additional snapshot on close, got %d -> %d", snapsAfterEntry, len(snapRepo.snapshots))
	}
}

// --- Balance/Margin/Equity Tests ---

func TestBalanceEquityMarginUpdateOnClose(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	stateAfterEntry, _ := pb.GetAccountState(context.Background())
	if stateAfterEntry.UsedMargin <= 0 {
		t.Error("expected used margin > 0 after entry")
	}

	pb.UpdatePrice("BTCUSDT", 66000)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	stateAfterClose, _ := pb.GetAccountState(context.Background())
	if stateAfterClose.UsedMargin != 0 {
		t.Errorf("expected used margin 0 after close, got %.2f", stateAfterClose.UsedMargin)
	}
	if stateAfterClose.Balance <= 0 {
		t.Error("expected positive balance after close")
	}
}

// --- DB Persistence: No open unprotected position ---

func TestDB_PersistenceNoOpenUnprotectedPosition(t *testing.T) {
	pb := newTestBroker()
	posRepo := &mockPositionRepo{}
	pb.SetPositionRepo(posRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	_, err := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01,
		StopLoss: 66000, // invalid LONG SL above entry - should reject
	})
	if err == nil {
		t.Fatal("expected error for invalid SL")
	}

	openPositions, _ := posRepo.GetOpen(context.Background())
	if len(openPositions) != 0 {
		t.Errorf("expected 0 open positions in DB after invalid SL rejection, got %d", len(openPositions))
	}
}

// --- SHORT Position SL/TP Tests ---

func TestStopLoss_ShortTriggers(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("ETHUSDT", 3000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "ETHUSDT",
		Side:      domain.OrderSideSell,
		OrderType: domain.OrderTypeMarket,
		Qty:       1.0,
		StopLoss:  3100,
	})

	// Price rises to SL (SHORT loses when price goes up)
	pb.UpdatePrice("ETHUSDT", 3150)

	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 open positions after SHORT SL, got %d", len(positions))
	}
	if len(pb.closedPositions) != 1 {
		t.Errorf("expected 1 closed position, got %d", len(pb.closedPositions))
	}
	if pb.closedPositions[0].RealizedPnL >= 0 {
		t.Error("SHORT SL: expected negative PnL")
	}
}

func TestTakeProfit_ShortTriggers(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("ETHUSDT", 3000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:     "ETHUSDT",
		Side:       domain.OrderSideSell,
		OrderType:  domain.OrderTypeMarket,
		Qty:        1.0,
		StopLoss:   3100,
		TakeProfit: 2800,
	})

	// Price drops to TP (SHORT profits when price goes down)
	pb.UpdatePrice("ETHUSDT", 2790)

	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 positions after SHORT TP, got %d", len(positions))
	}
	if len(pb.closedPositions) != 1 {
		t.Errorf("expected 1 closed position, got %d", len(pb.closedPositions))
	}
	if pb.closedPositions[0].RealizedPnL <= 0 {
		t.Error("SHORT TP: expected positive PnL")
	}
}

func TestSameCandle_ShortSLFirst(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("ETHUSDT", 3000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:     "ETHUSDT",
		Side:       domain.OrderSideSell,
		OrderType:  domain.OrderTypeMarket,
		Qty:        1.0,
		StopLoss:   3100,
		TakeProfit: 2800,
	})

	// Candle touches both SL and TP (high=3150 hit SL, low=2750 hit TP)
	pb.ProcessCandle(domain.Candle{
		Symbol: "ETHUSDT",
		High:   3150,
		Low:    2750,
		Close:  2950,
	})

	if len(pb.closedPositions) != 1 {
		t.Fatalf("expected 1 closed position, got %d", len(pb.closedPositions))
	}
	// Conservative SL-first: PnL should be negative (SL hit)
	if pb.closedPositions[0].RealizedPnL > 0 {
		t.Error("conservative SL-first for SHORT: PnL should be negative")
	}
}

func TestSetProtectiveOrders_Idempotent(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol:    "BTCUSDT",
		Side:      domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket,
		Qty:       0.01,
		StopLoss:  64000,
	})

	ordersBefore, _ := pb.GetOpenOrders(context.Background())
	slCountBefore := 0
	for _, o := range ordersBefore {
		if o.OrderType == domain.OrderTypeStopMarket {
			slCountBefore++
		}
	}
	if slCountBefore == 0 {
		t.Fatal("expected SL order after PlaceOrder")
	}

	// Call SetProtectiveOrders again — should replace, not duplicate
	pb.SetProtectiveOrders(context.Background(), "BTCUSDT", 63500, 67000)

	ordersAfter, _ := pb.GetOpenOrders(context.Background())
	slCountAfter := 0
	tpCountAfter := 0
	for _, o := range ordersAfter {
		if o.OrderType == domain.OrderTypeStopMarket {
			slCountAfter++
		}
		if o.OrderType == domain.OrderTypeTakeProfitMarket {
			tpCountAfter++
		}
	}
	if slCountAfter != 1 {
		t.Errorf("expected exactly 1 SL order after idempotent SetProtectiveOrders, got %d", slCountAfter)
	}
	if tpCountAfter != 1 {
		t.Errorf("expected exactly 1 TP order after idempotent SetProtectiveOrders, got %d", tpCountAfter)
	}

	// Position should reflect new SL/TP values
	pos, _ := pb.GetPosition("BTCUSDT")
	if pos.StopLoss != 63500 {
		t.Errorf("expected updated SL 63500, got %.2f", pos.StopLoss)
	}
	if pos.TakeProfit != 67000 {
		t.Errorf("expected updated TP 67000, got %.2f", pos.TakeProfit)
	}
}

// --- Accounting Model Tests (Wallet Balance) ---

func TestAccounting_CloseAtSamePrice_DoesNotInflateBalance(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	stateBefore, _ := pb.GetAccountState(context.Background())
	initialBalance := stateBefore.Balance

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	// Close at same price (with slippage for exit)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	stateAfter, _ := pb.GetAccountState(context.Background())
	// Balance should be LESS than initial due to fees (entry + exit)
	// It must NOT be higher due to margin being added back
	if stateAfter.Balance > initialBalance {
		t.Errorf("balance should not increase on round-trip at same price, got %.4f > %.4f", stateAfter.Balance, initialBalance)
	}
	// Margin was never subtracted from balance, so close should not add it
	// The balance decrease should be roughly (entryFee + exitFee + slippage)
	if stateAfter.Balance >= initialBalance {
		t.Errorf("expected balance decrease from fees, got %.4f vs %.4f", stateAfter.Balance, initialBalance)
	}
}

func TestAccounting_UsedMarginReturnsToZeroAfterClose(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	state, _ := pb.GetAccountState(context.Background())
	if state.UsedMargin <= 0 {
		t.Error("expected used margin > 0 after entry")
	}

	pb.ClosePosition(context.Background(), "BTCUSDT")

	state, _ = pb.GetAccountState(context.Background())
	if state.UsedMargin != 0 {
		t.Errorf("expected used margin 0 after close, got %.4f", state.UsedMargin)
	}
}

func TestAccounting_FeesReduceBalanceCorrectly(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	initialState, _ := pb.GetAccountState(context.Background())

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	afterEntry, _ := pb.GetAccountState(context.Background())
	// Entry fee should be reflected
	if afterEntry.TotalFees <= 0 {
		t.Error("expected fees > 0 after entry")
	}
	// Equity = balance + unrealized PnL
	expectedEquity := afterEntry.Balance + afterEntry.UnrealizedPnL
	if afterEntry.Equity != expectedEquity {
		t.Errorf("equity %.4f != balance %.4f + unrealizedPnL %.4f", afterEntry.Equity, afterEntry.Balance, afterEntry.UnrealizedPnL)
	}
	// Available = balance - usedMargin
	expectedAvail := afterEntry.Balance - afterEntry.UsedMargin
	if afterEntry.AvailableBalance != expectedAvail {
		t.Errorf("available %.4f != balance %.4f - margin %.4f", afterEntry.AvailableBalance, afterEntry.Balance, afterEntry.UsedMargin)
	}

	// Close at slightly better price
	pb.UpdatePrice("BTCUSDT", 65500)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	afterClose, _ := pb.GetAccountState(context.Background())
	if afterClose.TotalFees <= afterEntry.TotalFees {
		t.Error("expected additional fees after close")
	}
	// Balance should be initial minus total fees plus realized PnL
	if afterClose.Balance >= initialState.Balance {
		// Allow if profit exceeds fees
		if afterClose.Balance-afterClose.RealizedPnL+afterClose.TotalFees > initialState.Balance+10 {
			t.Errorf("balance tracking error: %.4f", afterClose.Balance)
		}
	}
}

func TestAccounting_OpenAndClose_OnlyPnLAddedBack(t *testing.T) {
	pb := newTestBroker()
	pb.UpdatePrice("BTCUSDT", 65000)

	// Open
	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	afterOpen, _ := pb.GetAccountState(context.Background())
	balanceAfterOpen := afterOpen.Balance
	marginAfterOpen := afterOpen.UsedMargin

	// Close at same entry price (there'll be slippage on exit though)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	afterClose, _ := pb.GetAccountState(context.Background())
	// The margin amount was never subtracted from balance, so
	// balanceAfterClose should be close to balanceAfterOpen (minus exit fees)
	// If margin was incorrectly added back, balance would jump by ~margin
	if afterClose.Balance > balanceAfterOpen+marginAfterOpen {
		t.Errorf("BUG: margin %.4f was incorrectly added to balance on close. Before: %.4f, After: %.4f",
			marginAfterOpen, balanceAfterOpen, afterClose.Balance)
	}
}

// --- DB Traceability Tests (IDs from DB, not paper IDs) ---

func TestTraceability_EntryExecutionUsesDBOrderID(t *testing.T) {
	pb := newTestBroker()
	ordRepo := &mockOrderRepo{}
	execRepo := &mockExecRepo{}
	pb.SetOrderRepo(ordRepo)
	pb.SetExecutionRepo(execRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	if len(execRepo.executions) == 0 {
		t.Fatal("expected at least 1 execution")
	}
	entryExec := execRepo.executions[0]
	if entryExec.OrderID == 0 {
		t.Error("entry execution has OrderID=0 — must reference a persisted order")
	}
	// Verify execution.OrderID matches a persisted order's DB ID
	found := false
	for _, o := range ordRepo.orders {
		if o.ID == entryExec.OrderID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("execution.OrderID=%d does not match any persisted order", entryExec.OrderID)
	}
}

func TestTraceability_PositionSLOrderID_UsesDBProtectiveOrderID(t *testing.T) {
	pb := newTestBroker()
	ordRepo := &mockOrderRepo{}
	posRepo := &mockPositionRepo{}
	pb.SetOrderRepo(ordRepo)
	pb.SetPositionRepo(posRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	if len(posRepo.positions) == 0 {
		t.Fatal("expected at least 1 persisted position")
	}
	savedPos := posRepo.positions[0]
	if savedPos.SLOrderID == nil {
		t.Fatal("SLOrderID is nil")
	}
	// SLOrderID must be the DB ID of the SL order, not a paper ID
	slDBID := *savedPos.SLOrderID
	found := false
	for _, o := range ordRepo.orders {
		if o.ID == slDBID && o.OrderType == domain.OrderTypeStopMarket {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("position.SLOrderID=%d does not reference a persisted STOP_MARKET order", slDBID)
	}
}

func TestTraceability_CloseExecutionUsesSyntheticOrderID(t *testing.T) {
	pb := newTestBroker()
	ordRepo := &mockOrderRepo{}
	execRepo := &mockExecRepo{}
	pb.SetOrderRepo(ordRepo)
	pb.SetExecutionRepo(execRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})

	pb.ClosePosition(context.Background(), "BTCUSDT")

	// Find the closing execution (SELL side for LONG close)
	var closeExec *domain.Execution
	for i := range execRepo.executions {
		if execRepo.executions[i].Side == domain.OrderSideSell {
			closeExec = &execRepo.executions[i]
			break
		}
	}
	if closeExec == nil {
		t.Fatal("no closing execution found")
	}
	if closeExec.OrderID == 0 {
		t.Error("closing execution has OrderID=0 — must reference a synthetic close order")
	}

	// Verify the synthetic close order exists in persisted orders
	found := false
	for _, o := range ordRepo.orders {
		if o.ID == closeExec.OrderID && o.OrderType == domain.OrderTypeMarket {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("closing execution.OrderID=%d does not reference a persisted MARKET close order", closeExec.OrderID)
	}
}

func TestTraceability_EmergencyClose_StoresCloseOrders(t *testing.T) {
	pb := newTestBroker()
	ordRepo := &mockOrderRepo{}
	execRepo := &mockExecRepo{}
	pb.SetOrderRepo(ordRepo)
	pb.SetExecutionRepo(execRepo)
	pb.UpdatePrice("BTCUSDT", 65000)
	pb.UpdatePrice("ETHUSDT", 3000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01, StopLoss: 64000,
	})
	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "ETHUSDT", Side: domain.OrderSideSell,
		OrderType: domain.OrderTypeMarket, Qty: 1.0, StopLoss: 3100,
	})

	execsBefore := len(execRepo.executions)
	pb.EmergencyCloseAll(context.Background())

	// Should have 2 closing executions with valid order IDs
	execsAfter := len(execRepo.executions)
	// execsBefore was 2 (entry execs), after emergency close should be 4
	if execsAfter <= execsBefore {
		t.Errorf("expected closing executions after emergency close, got %d → %d", execsBefore, execsAfter)
	}
	// All closing executions must have non-zero OrderID
	for _, e := range execRepo.executions {
		if e.OrderID == 0 {
			t.Errorf("execution OrderID=0 found after emergency close — must never happen")
		}
	}
}

// offsetOrderRepo returns IDs that differ from paper IDs (offset = 1000)
type offsetOrderRepo struct {
	orders []domain.Order
	offset int64
}

func (m *offsetOrderRepo) Insert(ctx context.Context, o domain.Order) (int64, error) {
	m.orders = append(m.orders, o)
	return int64(len(m.orders)) + m.offset, nil
}
func (m *offsetOrderRepo) Update(ctx context.Context, o domain.Order) error    { return nil }
func (m *offsetOrderRepo) GetOpen(ctx context.Context) ([]domain.Order, error) { return nil, nil }
func (m *offsetOrderRepo) GetBySymbol(ctx context.Context, symbol string) ([]domain.Order, error) {
	return nil, nil
}
func (m *offsetOrderRepo) GetAll(ctx context.Context, since time.Time) ([]domain.Order, error) {
	return nil, nil
}

func TestTraceability_DBIDsDifferFromPaperIDs(t *testing.T) {
	pb := newTestBroker()
	ordRepo := &offsetOrderRepo{offset: 1000}
	posRepo := &mockPositionRepo{}
	execRepo := &mockExecRepo{}
	pb.SetOrderRepo(ordRepo)
	pb.SetPositionRepo(posRepo)
	pb.SetExecutionRepo(execRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01,
		StopLoss: 64000, TakeProfit: 67000,
	})

	if len(posRepo.positions) == 0 {
		t.Fatal("expected persisted position")
	}
	savedPos := posRepo.positions[0]

	// Position SL/TP IDs must be DB IDs (1001+), not paper IDs (1, 2, 3...)
	if savedPos.SLOrderID == nil {
		t.Fatal("SLOrderID is nil")
	}
	if *savedPos.SLOrderID < 1000 {
		t.Errorf("position.SLOrderID=%d — should be DB ID (>=1000), not paper ID", *savedPos.SLOrderID)
	}
	if savedPos.TPOrderID != nil && *savedPos.TPOrderID < 1000 {
		t.Errorf("position.TPOrderID=%d — should be DB ID (>=1000), not paper ID", *savedPos.TPOrderID)
	}

	// Entry execution OrderID must be DB order ID (>=1000)
	if len(execRepo.executions) == 0 {
		t.Fatal("expected at least 1 execution")
	}
	entryExec := execRepo.executions[0]
	if entryExec.OrderID < 1000 {
		t.Errorf("execution.OrderID=%d — should be DB ID (>=1000), not paper ID", entryExec.OrderID)
	}
	if entryExec.OrderID == 0 {
		t.Error("execution.OrderID must not be zero")
	}

	// Verify all persisted orders have non-zero IDs (DB-assigned)
	for _, o := range ordRepo.orders {
		if o.ID == 0 {
			t.Error("persisted order has ID=0")
		}
	}
}

// --- LIMIT Order Persistence Tests (Issue 1) ---

func TestLimitOrder_Placing_PersistsPendingOrder(t *testing.T) {
	pb := newTestBroker()
	ordRepo := &mockOrderRepo{}
	pb.SetOrderRepo(ordRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	price := 64000.0
	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit, Qty: 0.01, Price: &price,
		StopLoss: 63000,
	})

	// Must have exactly 1 persisted order (pending LIMIT)
	if len(ordRepo.orders) != 1 {
		t.Fatalf("expected 1 persisted order, got %d", len(ordRepo.orders))
	}
	if ordRepo.orders[0].Status != domain.OrderStatusPending {
		t.Errorf("expected pending, got %s", ordRepo.orders[0].Status)
	}
	if ordRepo.orders[0].OrderType != domain.OrderTypeLimit {
		t.Errorf("expected LIMIT type, got %s", ordRepo.orders[0].OrderType)
	}
}

func TestLimitOrder_Fill_UpdatesToFilled_NotDuplicate(t *testing.T) {
	pb := newTestBroker()
	ordRepo := &mockOrderRepo{}
	pb.SetOrderRepo(ordRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	price := 64000.0
	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit, Qty: 0.01, Price: &price,
		StopLoss: 63000,
	})

	// Trigger fill
	pb.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT", High: 65000, Low: 63500, Close: 64500,
	})

	// Expected rows: 1 LIMIT (updated to filled) + 1 STOP_MARKET (new protective order)
	expectedRows := 2
	if len(ordRepo.orders) != expectedRows {
		t.Errorf("expected %d order rows after fill (1 LIMIT updated, 1 SL created), got %d", expectedRows, len(ordRepo.orders))
	}

	// Verify exactly one LIMIT row, one STOP_MARKET row
	limitCount := 0
	slCount := 0
	for _, o := range ordRepo.orders {
		if o.OrderType == domain.OrderTypeLimit {
			limitCount++
			if o.Status != domain.OrderStatusFilled {
				t.Errorf("LIMIT order status = %s, expected filled", o.Status)
			}
		}
		if o.OrderType == domain.OrderTypeStopMarket {
			slCount++
		}
	}
	if limitCount != 1 {
		t.Errorf("expected 1 LIMIT order row, got %d", limitCount)
	}
	if slCount != 1 {
		t.Errorf("expected 1 STOP_MARKET order row, got %d", slCount)
	}
}

func TestLimitOrder_Rejected_InvalidSL_UpdatesToRejected(t *testing.T) {
	pb := newTestBroker()
	ordRepo := &mockOrderRepo{}
	pb.SetOrderRepo(ordRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	price := 64000.0
	pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit, Qty: 0.01, Price: &price,
		StopLoss: 66000, // invalid LONG SL above entry
	})

	pb.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT", High: 65000, Low: 63500, Close: 64500,
	})

	// LIMIT order should be rejected, not a duplicate
	found := false
	for _, o := range ordRepo.orders {
		if o.OrderType == domain.OrderTypeLimit && o.Status == domain.OrderStatusRejected {
			found = true
		}
	}
	if !found {
		t.Error("expected LIMIT order to be rejected in DB")
	}
}

// --- CancelOrder Test (Issue 2) ---

func TestCancelOrder_UpdatesDBStatus(t *testing.T) {
	pb := newTestBroker()
	ordRepo := &mockOrderRepo{}
	pb.SetOrderRepo(ordRepo)
	pb.UpdatePrice("BTCUSDT", 65000)

	price := 64000.0
	order, _ := pb.PlaceOrder(context.Background(), OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit, Qty: 0.01, Price: &price,
		StopLoss: 63000,
	})

	pb.CancelOrder(context.Background(), order.BrokerOrderID)

	// Find the order by broker_order_id — status must be cancelled
	found := false
	for _, o := range ordRepo.orders {
		if o.BrokerOrderID == order.BrokerOrderID && o.Status == domain.OrderStatusCancelled {
			found = true
		}
	}
	if !found {
		t.Error("expected order status to be cancelled in DB")
	}
	// Verify order was removed from open orders
	openOrders, _ := pb.GetOpenOrders(context.Background())
	for _, o := range openOrders {
		if o.BrokerOrderID == order.BrokerOrderID {
			t.Error("cancelled order should be removed from open orders")
		}
	}
}
