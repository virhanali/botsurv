package risk

import (
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

func TestEvaluateHardBlocks_MultipleTriggered(t *testing.T) {
	now := time.Now()
	lastTrade := now.Add(-5 * time.Minute)
	result := EvaluateHardBlocks(BlockEvaluationInput{
		Symbol:             "BTCUSDT",
		Price:              0, // invalid
		Snapshot:           Snapshot{Price: 0, LastDataAt: now.Add(-5 * time.Minute)},
		Candles:            make([]domain.Candle, 10),
		MinRequiredCandles: 20,
		OrderBook:          domain.OrderBookSummary{SpreadBps: 25}, // 0.25%
		MaxSpreadPct:       0.15,
		PendingOrders: []domain.Order{
			{Symbol: "BTCUSDT", Status: domain.OrderStatusPending},
		},
		CurrentPositions: []domain.Position{
			{Symbol: "BTCUSDT", Status: domain.PositionStatusOpen},
		},
		TodayPnL:             -40,
		DailyMaxLossPct:      3,
		Equity:               1000,
		RecentTrades:         []TradeOutcome{{PnL: -1}, {PnL: -2}, {PnL: -3}},
		MaxConsecutiveLosses: 3,
		LastTradeTime:        &lastTrade,
		Cooldown: TradeCooldownConfig{
			Enabled:          true,
			AfterLoss:        30 * time.Minute,
			LastTradeWasLoss: true,
		},
		EmergencyStop:      EmergencyStopState{Active: true, Reason: "manual stop"},
		BTC5mReturnPct:     -3,
		BTCFlashCrash5mPct: -2.5,
		LocalPositions: []PositionState{
			{Symbol: "BTCUSDT", Side: domain.SideLong, Size: 1},
		},
		ExchangePositions: []PositionState{
			{Symbol: "BTCUSDT", Side: domain.SideShort, Size: 1},
		},
		MaxDataAge: 90 * time.Second,
	})

	if !result.Blocked {
		t.Fatal("expected blocked=true")
	}
	if len(result.BlocksTriggered) < 2 {
		t.Fatalf("expected multiple blocks, got %d", len(result.BlocksTriggered))
	}
	if result.FirstBlockReason == "" {
		t.Fatal("expected first block reason to be populated")
	}
	if len(result.AllBlockReasons) != len(result.BlocksTriggered) {
		t.Fatalf("all reasons count mismatch: reasons=%d blocks=%d", len(result.AllBlockReasons), len(result.BlocksTriggered))
	}
}
