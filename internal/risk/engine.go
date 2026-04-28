package risk

import (
	"fmt"
	"math"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
)

// MarketState holds current market data for risk evaluation.
type MarketState struct {
	Symbol      string
	Price       float64
	SpreadBps   float64
	SlippageBps float64
	DepthRatio  float64
	LastUpdate  time.Time
	Stale       bool
}

// PortfolioState holds current portfolio info.
type PortfolioState struct {
	OpenPositions         []domain.Position
	OpenOrders            []domain.Order
	NewPositionsThisCycle int
	DailyLoss             float64
	ConsecutiveLosses     int
	CooldownUntil         *time.Time
}

// Engine is the deterministic risk authority.
type Engine struct {
	cfg app.UserConfig
}

// NewEngine creates a new Risk Engine.
func NewEngine(cfg app.UserConfig) *Engine {
	return &Engine{cfg: cfg}
}

// ValidateInput holds all inputs for risk validation.
type ValidateInput struct {
	ProposedTrade domain.ProposedTrade
	LLMDecision   domain.LLMDecision
	AccountState  domain.AccountState
	MarketState   MarketState
	Portfolio     PortfolioState
	BotState      domain.BotState
}

// ValidateOutput is the result of risk validation.
type ValidateOutput struct {
	Approved              bool
	FinalPositionNotional float64
	RequiredMargin        float64
	EstimatedLoss         float64
	ReasonCodes           []string
	PortfolioRank         int
	PortfolioRejectReason string
}

// Validate performs all risk checks and returns APPROVED or REJECTED.
func (e *Engine) Validate(input ValidateInput) ValidateOutput {
	var reasons []string

	// 0. LLM decision validation (Risk Engine is final authority)
	if !isValidLLMDecision(input.LLMDecision) {
		reasons = append(reasons, "INVALID_LLM_DECISION")
	}
	if input.LLMDecision.Confidence > 0 && input.LLMDecision.Confidence < 0.6 {
		reasons = append(reasons, "LLM_LOW_CONFIDENCE")
	}
	if input.LLMDecision.Decision == "REDUCE_SIZE" {
		validMultipliers := map[float64]bool{1.0: true, 0.75: true, 0.5: true, 0.25: true, 0.0: true}
		if !validMultipliers[input.LLMDecision.SizeMultiplier] {
			reasons = append(reasons, "INVALID_SIZE_MULTIPLIER")
		}
	}

	// 1. Bot state checks
	if input.BotState.Halted {
		reasons = append(reasons, "BOT_HALTED")
	}

	// 2. Market data freshness
	if input.MarketState.Stale {
		reasons = append(reasons, "MARKET_DATA_STALE")
	}
	staleThreshold := time.Duration(e.cfg.MarketData.StaleDataThresholdSeconds) * time.Second
	if !input.MarketState.LastUpdate.IsZero() && time.Since(input.MarketState.LastUpdate) > staleThreshold {
		reasons = append(reasons, "MARKET_DATA_STALE")
	}

	// 3. Spread check
	maxSpread := e.cfg.Universe.Filters.MaxSpreadBps
	if maxSpread <= 0 {
		maxSpread = 50
	}
	if input.MarketState.SpreadBps > maxSpread {
		reasons = append(reasons, "SPREAD_TOO_WIDE")
	}

	// 4. Slippage check
	if input.MarketState.SlippageBps >= 100 {
		reasons = append(reasons, "SLIPPAGE_TOO_HIGH")
	}

	// 5. Depth check
	if input.MarketState.DepthRatio <= 0 {
		reasons = append(reasons, "INSUFFICIENT_DEPTH")
	}

	// 6. Daily loss check
	maxDailyLossPct := e.cfg.PortfolioRisk.MaxDailyLossPct
	if maxDailyLossPct > 0 {
		maxDailyLoss := input.AccountState.Equity * maxDailyLossPct / 100
		if input.Portfolio.DailyLoss >= maxDailyLoss {
			reasons = append(reasons, "DAILY_LOSS_BREACHED")
		}
	}

	// 7. Max open positions
	if len(input.Portfolio.OpenPositions) >= e.cfg.PortfolioRisk.MaxOpenPositions {
		reasons = append(reasons, "MAX_OPEN_POSITIONS")
	}

	// 8. Max new positions per cycle
	if input.Portfolio.NewPositionsThisCycle >= e.cfg.PortfolioRisk.MaxNewPositionsPerCycle {
		reasons = append(reasons, "MAX_NEW_POSITIONS_PER_CYCLE")
	}

	// 9. Max total exposure
	var totalExposure float64
	for _, pos := range input.Portfolio.OpenPositions {
		totalExposure += pos.EntryPrice * pos.Size
	}
	estimatedNewNotional := e.computeBaseNotional(input)
	if totalExposure+estimatedNewNotional > e.cfg.PortfolioRisk.MaxTotalExposureUSD {
		reasons = append(reasons, "MAX_TOTAL_EXPOSURE")
	}

	// 10. Max margin used
	maxMarginPct := e.cfg.PortfolioRisk.MaxTotalMarginUsedPct
	if maxMarginPct > 0 && input.AccountState.Equity > 0 {
		maxMargin := input.AccountState.Equity * maxMarginPct / 100
		if input.AccountState.UsedMargin > maxMargin {
			reasons = append(reasons, "MAX_MARGIN_USED")
		}
	}

	// 11. Duplicate symbol
	var hasExisting bool
	for _, pos := range input.Portfolio.OpenPositions {
		if pos.Symbol == input.ProposedTrade.Symbol && pos.Status == domain.PositionStatusOpen {
			hasExisting = true
			break
		}
	}
	if hasExisting {
		reasons = append(reasons, "DUPLICATE_SYMBOL")
	}

	// 12. Same direction positions
	sameDirectionCount := 0
	for _, pos := range input.Portfolio.OpenPositions {
		if pos.Side == input.ProposedTrade.Side && pos.Status == domain.PositionStatusOpen {
			sameDirectionCount++
		}
	}
	if sameDirectionCount >= e.cfg.PortfolioRisk.MaxSameDirectionPositions {
		reasons = append(reasons, "TOO_MANY_SAME_DIRECTION")
	}

	// 13. Cooldown check
	if input.Portfolio.CooldownUntil != nil && time.Now().Before(*input.Portfolio.CooldownUntil) {
		reasons = append(reasons, "IN_COOLDOWN")
	}

	// 14. Consecutive losses cooldown
	cooldownCfg := e.cfg.PortfolioRisk.CooldownAfterLosses
	if cooldownCfg.Enabled && input.Portfolio.ConsecutiveLosses >= cooldownCfg.ConsecutiveLosses {
		reasons = append(reasons, "CONSECUTIVE_LOSSES_COOLDOWN")
	}

	// 15. SL correct side
	if input.ProposedTrade.ProposedStopLoss > 0 {
		if input.ProposedTrade.Side == domain.SideLong && input.ProposedTrade.ProposedStopLoss >= input.ProposedTrade.ProposedEntry {
			reasons = append(reasons, "SL_WRONG_SIDE")
		}
		if input.ProposedTrade.Side == domain.SideShort && input.ProposedTrade.ProposedStopLoss <= input.ProposedTrade.ProposedEntry {
			reasons = append(reasons, "SL_WRONG_SIDE")
		}
	} else {
		reasons = append(reasons, "SL_MISSING")
	}

	// 16. TP correct side (if provided)
	if input.ProposedTrade.ProposedTakeProfit > 0 {
		if input.ProposedTrade.Side == domain.SideLong && input.ProposedTrade.ProposedTakeProfit <= input.ProposedTrade.ProposedEntry {
			reasons = append(reasons, "TP_WRONG_SIDE")
		}
		if input.ProposedTrade.Side == domain.SideShort && input.ProposedTrade.ProposedTakeProfit >= input.ProposedTrade.ProposedEntry {
			reasons = append(reasons, "TP_WRONG_SIDE")
		}
	}

	// 17. RR check
	minRR := e.cfg.Strategy.Indicators.MinRR
	if minRR <= 0 {
		minRR = 2.0
	}
	if input.ProposedTrade.RR < minRR {
		reasons = append(reasons, "RR_BELOW_MIN")
	}

	// 18. Expected move > 3x cost
	costMult := e.cfg.Strategy.Indicators.ExpectedMoveCostMultiplier
	if costMult <= 0 {
		costMult = 3.0
	}
	if input.ProposedTrade.ExpectedMove > 0 && input.ProposedTrade.EstimatedTotalCost > 0 {
		if input.ProposedTrade.ExpectedMove <= costMult*input.ProposedTrade.EstimatedTotalCost {
			reasons = append(reasons, "EXPECTED_MOVE_TOO_SMALL")
		}
	}

	// 19. Min notional
	notional := e.estimateNotional(input)
	if notional < e.cfg.PortfolioRisk.MinNotionalUSD {
		reasons = append(reasons, "BELOW_MIN_NOTIONAL")
	}

	// 20. Leverage check
	leverage := e.cfg.Sizing.MaxLeverage
	if leverage <= 0 {
		leverage = e.cfg.Broker.Paper.DefaultLeverage
	}
	symbolMaxLev := 100.0 // would come from symbol info
	if leverage > symbolMaxLev {
		reasons = append(reasons, "LEVERAGE_EXCEEDS_MAX")
	}

	// Sizing
	baseNotional := e.computeBaseNotional(input)
	finalNotional := baseNotional

	// LLM REDUCE_SIZE
	if input.LLMDecision.Decision == "REDUCE_SIZE" && input.LLMDecision.SizeMultiplier > 0 {
		finalNotional = baseNotional * input.LLMDecision.SizeMultiplier
	}

	margin := finalNotional / leverage
	slPct := e.stopLossPercent(input.ProposedTrade)
	estimatedLoss := finalNotional * slPct / 100

	approved := len(reasons) == 0

	return ValidateOutput{
		Approved:              approved,
		FinalPositionNotional: finalNotional,
		RequiredMargin:        margin,
		EstimatedLoss:         estimatedLoss,
		ReasonCodes:           reasons,
	}
}

// RankAndSelectPortfolio takes multiple approved candidates and selects within portfolio limits.
func (e *Engine) RankAndSelectPortfolio(candidates []ValidateInput) []ValidateOutput {
	// Score and rank candidates
	type scored struct {
		input ValidateInput
		score float64
		idx   int
	}
	var scored_list []scored
	for i, c := range candidates {
		score := c.ProposedTrade.SetupScore // use setup_score as ranking key
		scored_list = append(scored_list, scored{input: c, score: score, idx: i})
	}

	// Sort by score descending (simple bubble for small N)
	for i := 0; i < len(scored_list); i++ {
		for j := i + 1; j < len(scored_list); j++ {
			if scored_list[j].score > scored_list[i].score {
				scored_list[i], scored_list[j] = scored_list[j], scored_list[i]
			}
		}
	}

	results := make([]ValidateOutput, len(candidates))
	approvedCount := 0
	maxNew := e.cfg.PortfolioRisk.MaxNewPositionsPerCycle

	for _, s := range scored_list {
		output := e.Validate(s.input)
		if output.Approved && approvedCount < maxNew {
			output.PortfolioRank = approvedCount + 1
			approvedCount++
		} else if output.Approved {
			output.Approved = false
			output.PortfolioRejectReason = "PORTFOLIO_RISK_LIMIT"
			output.ReasonCodes = append(output.ReasonCodes, "PORTFOLIO_RISK_LIMIT")
		}
		results[s.idx] = output
	}

	return results
}

func (e *Engine) estimateNotional(input ValidateInput) float64 {
	if input.ProposedTrade.ProposedEntry > 0 {
		// Estimate based on sizing
		leverage := e.cfg.Sizing.MaxLeverage
		if leverage <= 0 {
			leverage = e.cfg.Broker.Paper.DefaultLeverage
		}
		marginPerTrade := e.cfg.Sizing.MarginPerTradeUSD
		if marginPerTrade <= 0 {
			marginPerTrade = e.cfg.PortfolioRisk.MarginPerTradeUSD
		}
		return marginPerTrade * leverage
	}
	return 0
}

func (e *Engine) computeBaseNotional(input ValidateInput) float64 {
	leverage := e.cfg.Sizing.MaxLeverage
	if leverage <= 0 {
		leverage = e.cfg.Broker.Paper.DefaultLeverage
	}
	marginPerTrade := e.cfg.Sizing.MarginPerTradeUSD
	if marginPerTrade <= 0 {
		marginPerTrade = e.cfg.PortfolioRisk.MarginPerTradeUSD
	}

	positionByMargin := marginPerTrade * leverage

	riskPct := e.cfg.Sizing.MaxRiskPerTradePct
	if riskPct <= 0 {
		riskPct = e.cfg.PortfolioRisk.MaxRiskPerTradePct
	}
	riskAmount := input.AccountState.Equity * riskPct / 100
	slPct := e.stopLossPercent(input.ProposedTrade)
	positionByRisk := math.MaxFloat64
	if slPct > 0 {
		positionByRisk = riskAmount / (slPct / 100)
	}

	return math.Min(positionByMargin, positionByRisk)
}

func (e *Engine) stopLossPercent(pt domain.ProposedTrade) float64 {
	if pt.StopLossPct > 0 {
		return pt.StopLossPct
	}
	if pt.ProposedEntry > 0 && pt.ProposedStopLoss > 0 {
		return math.Abs(pt.ProposedEntry-pt.ProposedStopLoss) / pt.ProposedEntry * 100
	}
	return 0
}

func isValidLLMDecision(d domain.LLMDecision) bool {
	valid := map[string]bool{
		"ALLOW_MARKET": true, "ALLOW_LIMIT_RETEST": true,
		"REDUCE_SIZE": true, "BLOCK": true,
	}
	return valid[d.Decision]
}

// GetRejectionReason returns a human-readable rejection reason for a code.
func GetRejectionReason(code string) string {
	reasons := map[string]string{
		"BOT_HALTED":                  "Bot is halted",
		"MARKET_DATA_STALE":           "Market data is stale",
		"SPREAD_TOO_WIDE":             "Spread exceeds threshold",
		"SLIPPAGE_TOO_HIGH":           "Slippage estimate too high",
		"INSUFFICIENT_DEPTH":          "Insufficient orderbook depth",
		"DAILY_LOSS_BREACHED":         "Daily loss limit breached",
		"MAX_OPEN_POSITIONS":          "Maximum open positions reached",
		"MAX_NEW_POSITIONS_PER_CYCLE": "Maximum new positions per cycle reached",
		"MAX_TOTAL_EXPOSURE":          "Maximum total exposure reached",
		"MAX_MARGIN_USED":             "Maximum margin usage reached",
		"DUPLICATE_SYMBOL":            "Position already exists for this symbol",
		"TOO_MANY_SAME_DIRECTION":     "Too many positions in same direction",
		"IN_COOLDOWN":                 "In cooldown period",
		"CONSECUTIVE_LOSSES_COOLDOWN": "Consecutive losses cooldown active",
		"SL_WRONG_SIDE":               "Stop loss on wrong side",
		"SL_MISSING":                  "Stop loss is missing",
		"TP_WRONG_SIDE":               "Take profit on wrong side",
		"RR_BELOW_MIN":                "Risk/Reward below minimum",
		"EXPECTED_MOVE_TOO_SMALL":     "Expected move too small relative to cost",
		"BELOW_MIN_NOTIONAL":          "Position below minimum notional",
		"LEVERAGE_EXCEEDS_MAX":        "Leverage exceeds maximum",
		"PORTFOLIO_RISK_LIMIT":        "Portfolio risk limit reached",
	}
	if r, ok := reasons[code]; ok {
		return r
	}
	return fmt.Sprintf("Unknown reason: %s", code)
}
