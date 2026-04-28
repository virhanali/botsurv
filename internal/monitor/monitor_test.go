package monitor

import (
	"context"
	"testing"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/broker"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
)

func newTestMonitor() (*Monitor, *broker.PaperBroker) {
	cfg := app.PaperConfig{
		StartingBalanceUSD: 1000,
		FeeTakerBps:        5.5,
		SlippageBps:        5,
		DefaultLeverage:    5,
	}
	log := logger.New(nil, logger.LevelDebug)
	pb := broker.NewPaperBroker(cfg, log)
	mon := NewMonitor(pb, nil, app.PortfolioRiskConfig{
		MaxDailyLossPct: 3,
	}, log)
	return mon, pb
}

func TestUpdate_UpdatesPrice(t *testing.T) {
	mon, pb := newTestMonitor()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), broker.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01,
  StopLoss:  100,
	})

	// Price goes up
	mon.Update(context.Background(), "BTCUSDT", 66000)

	state, _ := pb.GetAccountState(context.Background())
	if state.UnrealizedPnL <= 0 {
		t.Error("expected positive unrealized PnL")
	}
}

func TestCheckKillSwitch_Triggers(t *testing.T) {
	mon, pb := newTestMonitor()
	pb.UpdatePrice("BTCUSDT", 65000)

	// Open and close at a loss that exceeds 3% of equity
	pb.PlaceOrder(context.Background(), broker.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01,
  StopLoss:  100,
	})

	// Big drop: close at 55000 (loss ~$100)
	pb.UpdatePrice("BTCUSDT", 55000)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	// Daily loss should now exceed 3% of equity (~$900 * 3% = $27)
	// Actual loss is ~$100, well above threshold

	// Open another position to be closed by kill switch
	pb.UpdatePrice("BTCUSDT", 65000)
	pb.PlaceOrder(context.Background(), broker.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01,
  StopLoss:  100,
	})

	// Trigger kill switch
	mon.checkKillSwitch(context.Background())

	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 0 {
		t.Errorf("expected 0 positions after kill switch, got %d", len(positions))
	}
}

func TestResetDaily(t *testing.T) {
	mon, pb := newTestMonitor()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), broker.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01,
  StopLoss:  100,
	})

	pb.UpdatePrice("BTCUSDT", 64000)
	pb.ClosePosition(context.Background(), "BTCUSDT")

	state, _ := pb.GetAccountState(context.Background())
	if state.DailyLoss <= 0 {
		t.Fatal("expected daily loss > 0")
	}

	mon.ResetDaily()

	state, _ = pb.GetAccountState(context.Background())
	if state.DailyLoss != 0 {
		t.Errorf("expected daily loss 0, got %.2f", state.DailyLoss)
	}
}

func TestStatus_ReturnsInfo(t *testing.T) {
	mon, pb := newTestMonitor()
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.PlaceOrder(context.Background(), broker.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeMarket, Qty: 0.01,
  StopLoss:  100,
	})

	status := mon.Status(context.Background())
	if len(status.OpenPositions) != 1 {
		t.Errorf("expected 1 position, got %d", len(status.OpenPositions))
	}
	if status.AccountState.Balance <= 0 {
		t.Error("expected positive balance")
	}
}

func TestProcessCandle_FillsLimitOrders(t *testing.T) {
	mon, pb := newTestMonitor()
	pb.UpdatePrice("BTCUSDT", 65000)

	price := 64000.0
	pb.PlaceOrder(context.Background(), broker.OrderRequest{
		Symbol: "BTCUSDT", Side: domain.OrderSideBuy,
		OrderType: domain.OrderTypeLimit, Qty: 0.01, Price: &price,
  StopLoss:  100,
	})

	mon.ProcessCandle(domain.Candle{
		Symbol: "BTCUSDT", High: 65000, Low: 63500, Close: 64500,
	})

	orders, _ := pb.GetOpenOrders(context.Background())
	if len(orders) < 1 {
		t.Errorf("expected protective orders after limit fill, got %d", len(orders))
	}
	positions, _ := pb.GetOpenPositions(context.Background())
	if len(positions) != 1 {
		t.Errorf("expected 1 position, got %d", len(positions))
	}
}
