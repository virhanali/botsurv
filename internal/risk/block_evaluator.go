package risk

import (
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

// BlockEvaluationInput is the complete context for hard-block evaluation.
type BlockEvaluationInput struct {
	Symbol               string
	Price                float64
	Snapshot             Snapshot
	Candles              []domain.Candle
	MinRequiredCandles   int
	OrderBook            domain.OrderBookSummary
	MaxSpreadPct         float64
	FundingRatePct       float64
	MaxFundingAbsPct     float64
	PendingOrders        []domain.Order
	CurrentPositions     []domain.Position
	TodayPnL             float64
	DailyMaxLossPct      float64
	Equity               float64
	RecentTrades         []TradeOutcome
	MaxConsecutiveLosses int
	LastTradeTime        *time.Time
	Cooldown             TradeCooldownConfig
	EmergencyStop        EmergencyStopState
	BTC5mReturnPct       float64
	BTCFlashCrash5mPct   float64
	LocalPositions       []PositionState
	ExchangePositions    []PositionState
	MaxDataAge           time.Duration
}

// BlockEvaluationResult is the structured output from hard-block evaluation.
type BlockEvaluationResult struct {
	Blocked          bool     `json:"blocked"`
	BlocksTriggered  []string `json:"blocks_triggered"`
	FirstBlockReason string   `json:"first_block_reason"`
	AllBlockReasons  []string `json:"all_block_reasons"`
}

// EvaluateHardBlocks runs all hard blocks and collects every triggered reason.
func EvaluateHardBlocks(in BlockEvaluationInput) BlockEvaluationResult {
	result := BlockEvaluationResult{}

	record := func(name string, blocked bool, reason string) {
		if !blocked {
			return
		}
		result.Blocked = true
		result.BlocksTriggered = append(result.BlocksTriggered, name)
		result.AllBlockReasons = append(result.AllBlockReasons, reason)
		if result.FirstBlockReason == "" {
			result.FirstBlockReason = reason
		}
	}

	snapshot := in.Snapshot
	if snapshot.Numbers == nil {
		snapshot.Numbers = map[string]float64{}
	}
	snapshot.Numbers["orderbook_spread_bps"] = in.OrderBook.SpreadBps
	snapshot.Numbers["orderbook_slippage_bps"] = in.OrderBook.EstimatedSlippageBps

	blocked, reason := BlockIfPriceInvalid(in.Price)
	record("BlockIfPriceInvalid", blocked, reason)

	blocked, reason = BlockIfNaNOrInf(snapshot)
	record("BlockIfNaNOrInf", blocked, reason)

	blocked, reason = BlockIfStaleData(in.Snapshot, in.MaxDataAge)
	record("BlockIfStaleData", blocked, reason)

	blocked, reason = BlockIfInsufficientCandles(in.Candles, in.MinRequiredCandles)
	record("BlockIfInsufficientCandles", blocked, reason)

	blocked, reason = BlockIfSpreadTooWide(in.OrderBook, in.MaxSpreadPct)
	record("BlockIfSpreadTooWide", blocked, reason)

	blocked, reason = BlockIfFundingExtreme(in.FundingRatePct, in.MaxFundingAbsPct)
	record("BlockIfFundingExtreme", blocked, reason)

	blocked, reason = BlockIfDuplicateOrder(in.Symbol, in.PendingOrders)
	record("BlockIfDuplicateOrder", blocked, reason)

	blocked, reason = BlockIfPositionAlreadyOpen(in.Symbol, in.CurrentPositions)
	record("BlockIfPositionAlreadyOpen", blocked, reason)

	blocked, reason = BlockIfDailyLossReached(in.TodayPnL, in.DailyMaxLossPct, in.Equity)
	record("BlockIfDailyLossReached", blocked, reason)

	blocked, reason = BlockIfConsecutiveLossLimit(in.RecentTrades, in.MaxConsecutiveLosses)
	record("BlockIfConsecutiveLossLimit", blocked, reason)

	blocked, reason = BlockIfCooldownActive(in.LastTradeTime, in.Cooldown)
	record("BlockIfCooldownActive", blocked, reason)

	blocked, reason = BlockIfEmergencyStopActive(in.EmergencyStop)
	record("BlockIfEmergencyStopActive", blocked, reason)

	blocked, reason = BlockIfBTCFlashCrash(in.BTC5mReturnPct, in.BTCFlashCrash5mPct)
	record("BlockIfBTCFlashCrash", blocked, reason)

	blocked, reason = BlockIfPositionMismatch(in.LocalPositions, in.ExchangePositions)
	record("BlockIfPositionMismatch", blocked, reason)

	return result
}
