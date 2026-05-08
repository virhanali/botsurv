package risk

import (
	"math"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

func TestBlockIfPriceInvalid(t *testing.T) {
	if blocked, _ := BlockIfPriceInvalid(100); blocked {
		t.Fatal("expected valid price to pass")
	}
	if blocked, _ := BlockIfPriceInvalid(0); !blocked {
		t.Fatal("expected zero price to block")
	}
}

func TestBlockIfNaNOrInf(t *testing.T) {
	pass := Snapshot{Price: 1, SpreadPct: 0.1, FundingRatePct: 0.01}
	if blocked, _ := BlockIfNaNOrInf(pass); blocked {
		t.Fatal("expected finite snapshot to pass")
	}
	fail := Snapshot{Price: math.NaN(), SpreadPct: 0.1, FundingRatePct: 0.01}
	if blocked, _ := BlockIfNaNOrInf(fail); !blocked {
		t.Fatal("expected NaN snapshot to block")
	}
}

func TestBlockIfStaleData(t *testing.T) {
	fresh := Snapshot{LastDataAt: time.Now().Add(-10 * time.Second)}
	if blocked, _ := BlockIfStaleData(fresh, 30*time.Second); blocked {
		t.Fatal("expected fresh data to pass")
	}
	stale := Snapshot{LastDataAt: time.Now().Add(-2 * time.Minute)}
	if blocked, _ := BlockIfStaleData(stale, 30*time.Second); !blocked {
		t.Fatal("expected stale data to block")
	}
}

func TestBlockIfInsufficientCandles(t *testing.T) {
	candles := make([]domain.Candle, 10)
	if blocked, _ := BlockIfInsufficientCandles(candles, 10); blocked {
		t.Fatal("expected exact minimum to pass")
	}
	if blocked, _ := BlockIfInsufficientCandles(candles, 11); !blocked {
		t.Fatal("expected short candle batch to block")
	}
}

func TestBlockIfSpreadTooWide(t *testing.T) {
	okOB := domain.OrderBookSummary{SpreadBps: 10} // 0.10%
	if blocked, _ := BlockIfSpreadTooWide(okOB, 0.15); blocked {
		t.Fatal("expected spread below threshold to pass")
	}
	badOB := domain.OrderBookSummary{SpreadBps: 20} // 0.20%
	if blocked, _ := BlockIfSpreadTooWide(badOB, 0.15); !blocked {
		t.Fatal("expected spread above threshold to block")
	}
}

func TestBlockIfFundingExtreme(t *testing.T) {
	if blocked, _ := BlockIfFundingExtreme(0.2, 0.5); blocked {
		t.Fatal("expected normal funding to pass")
	}
	if blocked, _ := BlockIfFundingExtreme(0.7, 0.5); !blocked {
		t.Fatal("expected extreme funding to block")
	}
}

func TestBlockIfDuplicateOrder(t *testing.T) {
	orders := []domain.Order{{Symbol: "BTCUSDT", Status: domain.OrderStatusPending}}
	if blocked, _ := BlockIfDuplicateOrder("ETHUSDT", orders); blocked {
		t.Fatal("expected different symbol to pass")
	}
	if blocked, _ := BlockIfDuplicateOrder("BTCUSDT", orders); !blocked {
		t.Fatal("expected duplicate pending order to block")
	}
}

func TestBlockIfPositionAlreadyOpen(t *testing.T) {
	positions := []domain.Position{{Symbol: "BTCUSDT", Status: domain.PositionStatusOpen}}
	if blocked, _ := BlockIfPositionAlreadyOpen("ETHUSDT", positions); blocked {
		t.Fatal("expected symbol without open position to pass")
	}
	if blocked, _ := BlockIfPositionAlreadyOpen("BTCUSDT", positions); !blocked {
		t.Fatal("expected existing open position to block")
	}
}

func TestBlockIfDailyLossReached(t *testing.T) {
	if blocked, _ := BlockIfDailyLossReached(-20, 3, 1000); blocked {
		t.Fatal("expected daily loss below cap to pass")
	}
	if blocked, _ := BlockIfDailyLossReached(-30, 3, 1000); !blocked {
		t.Fatal("expected daily loss at cap to block")
	}
}

func TestBlockIfConsecutiveLossLimit(t *testing.T) {
	trades := []TradeOutcome{{PnL: -1}, {PnL: -2}, {PnL: 1}, {PnL: -1}}
	if blocked, _ := BlockIfConsecutiveLossLimit(trades, 2); blocked {
		t.Fatal("expected one latest loss to pass")
	}
	trades = []TradeOutcome{{PnL: 1}, {PnL: -1}, {PnL: -2}, {PnL: -3}}
	if blocked, _ := BlockIfConsecutiveLossLimit(trades, 3); !blocked {
		t.Fatal("expected 3 consecutive losses to block")
	}
}

func TestBlockIfCooldownActive(t *testing.T) {
	lastTrade := time.Now().Add(-20 * time.Minute)
	cfg := TradeCooldownConfig{Enabled: true, AfterLoss: 30 * time.Minute, LastTradeWasLoss: true}
	if blocked, _ := BlockIfCooldownActive(&lastTrade, cfg); !blocked {
		t.Fatal("expected active cooldown to block")
	}
	cfg = TradeCooldownConfig{Enabled: true, AfterLoss: 10 * time.Minute, LastTradeWasLoss: true}
	if blocked, _ := BlockIfCooldownActive(&lastTrade, cfg); blocked {
		t.Fatal("expected expired cooldown to pass")
	}
}

func TestBlockIfEmergencyStopActive(t *testing.T) {
	if blocked, _ := BlockIfEmergencyStopActive(EmergencyStopState{Active: false}); blocked {
		t.Fatal("expected inactive emergency stop to pass")
	}
	if blocked, _ := BlockIfEmergencyStopActive(EmergencyStopState{Active: true, Reason: "manual"}); !blocked {
		t.Fatal("expected active emergency stop to block")
	}
}

func TestBlockIfBTCFlashCrash(t *testing.T) {
	if blocked, _ := BlockIfBTCFlashCrash(-1.0, -2.5); blocked {
		t.Fatal("expected mild BTC move to pass")
	}
	if blocked, _ := BlockIfBTCFlashCrash(-3.0, -2.5); !blocked {
		t.Fatal("expected BTC flash crash to block")
	}
}

func TestBlockIfPositionMismatch(t *testing.T) {
	local := []PositionState{{Symbol: "BTCUSDT", Side: domain.SideLong, Size: 1}}
	exchange := []PositionState{{Symbol: "BTCUSDT", Side: domain.SideLong, Size: 1}}
	if blocked, _ := BlockIfPositionMismatch(local, exchange); blocked {
		t.Fatal("expected matching states to pass")
	}
	exchange = []PositionState{{Symbol: "BTCUSDT", Side: domain.SideShort, Size: 1}}
	if blocked, _ := BlockIfPositionMismatch(local, exchange); !blocked {
		t.Fatal("expected side mismatch to block")
	}
}

func TestBlockIfSymbolInSLCooldown(t *testing.T) {
	// Nil map should not block.
	if blocked, _ := BlockIfSymbolInSLCooldown("BTCUSDT", nil); blocked {
		t.Fatal("expected nil cooldown map to pass")
	}

	// Empty map should not block.
	if blocked, _ := BlockIfSymbolInSLCooldown("BTCUSDT", map[string]time.Time{}); blocked {
		t.Fatal("expected empty cooldown map to pass")
	}

	// Symbol not in cooldown map should pass.
	cooldowns := map[string]time.Time{"ETHUSDT": time.Now().Add(30 * time.Minute)}
	if blocked, _ := BlockIfSymbolInSLCooldown("BTCUSDT", cooldowns); blocked {
		t.Fatal("expected symbol not in cooldown to pass")
	}

	// Symbol in active cooldown should block.
	future := time.Now().Add(30 * time.Minute)
	cooldowns = map[string]time.Time{"BTCUSDT": future}
	blocked, reason := BlockIfSymbolInSLCooldown("BTCUSDT", cooldowns)
	if !blocked {
		t.Fatal("expected symbol in cooldown to block")
	}
	if reason == "" {
		t.Fatal("expected non-empty block reason")
	}

	// Expired cooldown should pass.
	past := time.Now().Add(-1 * time.Minute)
	cooldowns = map[string]time.Time{"BTCUSDT": past}
	if blocked, _ := BlockIfSymbolInSLCooldown("BTCUSDT", cooldowns); blocked {
		t.Fatal("expected expired cooldown to pass")
	}
}

func TestCleanExpiredSLCooldowns(t *testing.T) {
	now := time.Now()
	cooldowns := map[string]time.Time{
		"BTCUSDT": now.Add(30 * time.Minute), // active
		"ETHUSDT": now.Add(-1 * time.Minute),  // expired
		"SOLUSDT": now.Add(1 * time.Hour),     // active
	}

	cleaned := CleanExpiredSLCooldowns(cooldowns)
	if len(cleaned) != 2 {
		t.Fatalf("expected 2 active cooldowns, got %d", len(cleaned))
	}
	if _, ok := cleaned["BTCUSDT"]; !ok {
		t.Fatal("expected BTCUSDT to remain in cooldown map")
	}
	if _, ok := cleaned["SOLUSDT"]; !ok {
		t.Fatal("expected SOLUSDT to remain in cooldown map")
	}
	if _, ok := cleaned["ETHUSDT"]; ok {
		t.Fatal("expected ETHUSDT to be removed from cooldown map")
	}

	// Nil map returns nil.
	if result := CleanExpiredSLCooldowns(nil); result != nil {
		t.Fatal("expected nil for nil input")
	}
}
