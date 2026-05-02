package risk

import (
	"fmt"
	"math"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

// Snapshot contains numeric market data used by hard blocks.
type Snapshot struct {
	Price          float64
	SpreadPct      float64
	FundingRatePct float64
	LastDataAt     time.Time
	Numbers        map[string]float64
}

// TradeOutcome is a minimal representation used for consecutive-loss checks.
type TradeOutcome struct {
	PnL float64
}

// TradeCooldownConfig controls cooldown block behavior.
type TradeCooldownConfig struct {
	Enabled          bool
	AfterLoss        time.Duration
	AfterWin         time.Duration
	LastTradeWasLoss bool
}

// EmergencyStopState is a minimal emergency stop state.
type EmergencyStopState struct {
	Active bool
	Reason string
}

// PositionState is a normalized position snapshot for mismatch checks.
type PositionState struct {
	Symbol string
	Side   domain.Side
	Size   float64
}

// BlockIfPriceInvalid blocks when price is NaN/Inf/non-positive.
func BlockIfPriceInvalid(price float64) (bool, string) {
	if math.IsNaN(price) || math.IsInf(price, 0) || price <= 0 {
		return true, fmt.Sprintf("price is invalid: %.8f", price)
	}
	return false, ""
}

// BlockIfNaNOrInf blocks when snapshot numbers contain NaN/Inf values.
func BlockIfNaNOrInf(snapshot Snapshot) (bool, string) {
	values := map[string]float64{
		"price":            snapshot.Price,
		"spread_pct":       snapshot.SpreadPct,
		"funding_rate_pct": snapshot.FundingRatePct,
	}
	for k, v := range snapshot.Numbers {
		values[k] = v
	}
	for name, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return true, fmt.Sprintf("snapshot contains NaN/Inf: %s=%v", name, v)
		}
	}
	return false, ""
}

// BlockIfStaleData blocks when data age exceeds max allowed age.
func BlockIfStaleData(snapshot Snapshot, maxAge time.Duration) (bool, string) {
	if maxAge <= 0 {
		return false, ""
	}
	if snapshot.LastDataAt.IsZero() {
		return true, "market data timestamp is missing"
	}
	age := time.Since(snapshot.LastDataAt)
	if age > maxAge {
		return true, fmt.Sprintf("market data is %s old, max allowed %s", age.Truncate(time.Second), maxAge)
	}
	return false, ""
}

// BlockIfInsufficientCandles blocks when candle count is below minimum.
func BlockIfInsufficientCandles(candles []domain.Candle, minRequired int) (bool, string) {
	if minRequired <= 0 {
		return false, ""
	}
	if len(candles) < minRequired {
		return true, fmt.Sprintf("insufficient candles: have %d, need %d", len(candles), minRequired)
	}
	return false, ""
}

// BlockIfSpreadTooWide blocks when orderbook spread exceeds threshold.
func BlockIfSpreadTooWide(orderbook domain.OrderBookSummary, maxSpreadPct float64) (bool, string) {
	if maxSpreadPct <= 0 {
		return false, ""
	}
	spreadPct := orderbook.SpreadBps / 100.0
	if spreadPct > maxSpreadPct {
		return true, fmt.Sprintf("spread too wide: %.4f%% > %.4f%%", spreadPct, maxSpreadPct)
	}
	return false, ""
}

// BlockIfFundingExtreme blocks when funding absolute value exceeds threshold.
func BlockIfFundingExtreme(fundingRate, maxAbs float64) (bool, string) {
	if maxAbs <= 0 {
		return false, ""
	}
	if math.Abs(fundingRate) > maxAbs {
		return true, fmt.Sprintf("funding rate extreme: %.4f%% exceeds max_abs %.4f%%", fundingRate, maxAbs)
	}
	return false, ""
}

// BlockIfDuplicateOrder blocks if there is a pending order for the same symbol.
func BlockIfDuplicateOrder(symbol string, pendingOrders []domain.Order) (bool, string) {
	for _, o := range pendingOrders {
		if o.Symbol == symbol && o.Status == domain.OrderStatusPending {
			return true, fmt.Sprintf("duplicate pending order for symbol %s", symbol)
		}
	}
	return false, ""
}

// BlockIfPositionAlreadyOpen blocks when an open position for the symbol already exists.
func BlockIfPositionAlreadyOpen(symbol string, currentPositions []domain.Position) (bool, string) {
	for _, p := range currentPositions {
		if p.Symbol == symbol && p.Status == domain.PositionStatusOpen {
			return true, fmt.Sprintf("position already open for symbol %s", symbol)
		}
	}
	return false, ""
}

// BlockIfDailyLossReached blocks when today's loss reaches the configured limit.
func BlockIfDailyLossReached(todayPnL, maxLossPct, equity float64) (bool, string) {
	if maxLossPct <= 0 {
		return false, ""
	}
	if equity <= 0 {
		return true, "equity is invalid for daily loss check"
	}
	if todayPnL >= 0 {
		return false, ""
	}
	lossAbs := -todayPnL
	maxLossAbs := equity * maxLossPct / 100.0
	if lossAbs >= maxLossAbs {
		return true, fmt.Sprintf("daily loss reached: %.2f >= %.2f (%.2f%%)", lossAbs, maxLossAbs, maxLossPct)
	}
	return false, ""
}

// BlockIfConsecutiveLossLimit blocks when recent consecutive losses hit threshold.
func BlockIfConsecutiveLossLimit(recentTrades []TradeOutcome, maxLosses int) (bool, string) {
	if maxLosses <= 0 {
		return false, ""
	}
	consecutive := 0
	for i := len(recentTrades) - 1; i >= 0; i-- {
		if recentTrades[i].PnL < 0 {
			consecutive++
			continue
		}
		break
	}
	if consecutive >= maxLosses {
		return true, fmt.Sprintf("consecutive loss limit reached: %d >= %d", consecutive, maxLosses)
	}
	return false, ""
}

// BlockIfCooldownActive blocks when cooldown period is still active.
func BlockIfCooldownActive(lastTradeTime *time.Time, cooldownConfig TradeCooldownConfig) (bool, string) {
	if !cooldownConfig.Enabled || lastTradeTime == nil {
		return false, ""
	}
	cooldown := cooldownConfig.AfterWin
	if cooldownConfig.LastTradeWasLoss {
		cooldown = cooldownConfig.AfterLoss
	}
	if cooldown <= 0 {
		return false, ""
	}
	elapsed := time.Since(*lastTradeTime)
	if elapsed < cooldown {
		remaining := cooldown - elapsed
		return true, fmt.Sprintf("cooldown active: %s remaining", remaining.Truncate(time.Second))
	}
	return false, ""
}

// BlockIfEmergencyStopActive blocks when emergency stop is active.
func BlockIfEmergencyStopActive(state EmergencyStopState) (bool, string) {
	if state.Active {
		if state.Reason != "" {
			return true, fmt.Sprintf("emergency stop active: %s", state.Reason)
		}
		return true, "emergency stop active"
	}
	return false, ""
}

// BlockIfBTCFlashCrash blocks when BTC 5m return breaches crash threshold.
func BlockIfBTCFlashCrash(btc5mReturn, threshold float64) (bool, string) {
	if threshold == 0 {
		return false, ""
	}
	if threshold < 0 && btc5mReturn <= threshold {
		return true, fmt.Sprintf("BTC flash crash detected: %.4f%% <= %.4f%%", btc5mReturn, threshold)
	}
	if threshold > 0 && btc5mReturn >= threshold {
		return true, fmt.Sprintf("BTC flash spike detected: %.4f%% >= %.4f%%", btc5mReturn, threshold)
	}
	return false, ""
}

// BlockIfPositionMismatch blocks when local and exchange states diverge.
func BlockIfPositionMismatch(localState, exchangeState []PositionState) (bool, string) {
	local := make(map[string]PositionState, len(localState))
	exchange := make(map[string]PositionState, len(exchangeState))
	for _, p := range localState {
		local[p.Symbol] = p
	}
	for _, p := range exchangeState {
		exchange[p.Symbol] = p
	}

	if len(local) != len(exchange) {
		return true, fmt.Sprintf("position mismatch: local=%d exchange=%d", len(local), len(exchange))
	}

	const eps = 1e-9
	for sym, lp := range local {
		ep, ok := exchange[sym]
		if !ok {
			return true, fmt.Sprintf("position mismatch: symbol %s missing on exchange state", sym)
		}
		if lp.Side != ep.Side {
			return true, fmt.Sprintf("position mismatch: symbol %s side local=%s exchange=%s", sym, lp.Side, ep.Side)
		}
		if math.Abs(lp.Size-ep.Size) > eps {
			return true, fmt.Sprintf("position mismatch: symbol %s size local=%.8f exchange=%.8f", sym, lp.Size, ep.Size)
		}
	}
	return false, ""
}
