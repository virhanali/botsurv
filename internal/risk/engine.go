package risk

import (
	"fmt"
	"math"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/regime"
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
	SymbolInfo    domain.SymbolInfo
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

	if !isFinitePositive(input.AccountState.Equity) {
		return ValidateOutput{Approved: false, ReasonCodes: []string{"INVALID_EQUITY"}}
	}
	if !isFinitePositive(input.ProposedTrade.ProposedEntry) {
		return ValidateOutput{Approved: false, ReasonCodes: []string{"INVALID_ENTRY"}}
	}
	if input.ProposedTrade.ProposedStopLoss <= 0 {
		return ValidateOutput{Approved: false, ReasonCodes: []string{"SL_MISSING"}}
	}
	if !isFinitePositive(input.ProposedTrade.ProposedStopLoss) {
		return ValidateOutput{Approved: false, ReasonCodes: []string{"INVALID_STOP_LOSS"}}
	}
	if !isFiniteNonNegative(input.ProposedTrade.SetupScore) {
		return ValidateOutput{Approved: false, ReasonCodes: []string{"INVALID_SETUP_SCORE"}}
	}
	if !isFiniteNonNegative(input.ProposedTrade.ExpectedMove) {
		return ValidateOutput{Approved: false, ReasonCodes: []string{"INVALID_EXPECTED_MOVE"}}
	}
	if !isFiniteNonNegative(input.ProposedTrade.EstimatedTotalCost) {
		return ValidateOutput{Approved: false, ReasonCodes: []string{"INVALID_ESTIMATED_COST"}}
	}
	if !isFiniteNonNegative(input.MarketState.SpreadBps) {
		return ValidateOutput{Approved: false, ReasonCodes: []string{"INVALID_SPREAD"}}
	}
	if !isFiniteNonNegative(input.MarketState.SlippageBps) {
		return ValidateOutput{Approved: false, ReasonCodes: []string{"INVALID_SLIPPAGE"}}
	}
	if !isFiniteNonNegative(input.MarketState.DepthRatio) {
		return ValidateOutput{Approved: false, ReasonCodes: []string{"INVALID_DEPTH"}}
	}
	if !isFinitePositive(input.MarketState.Price) {
		return ValidateOutput{Approved: false, ReasonCodes: []string{"INVALID_MARKET_PRICE"}}
	}

	// 0. LLM decision validation (Risk Engine is final authority)
	if !isValidLLMDecision(input.LLMDecision) {
		reasons = append(reasons, "INVALID_LLM_DECISION")
	}
	if input.LLMDecision.Decision == "BLOCK" {
		reasons = append(reasons, "LLM_DECISION_BLOCK")
	}
	if input.LLMDecision.Confidence < 0.6 {
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

	// 16b. TP/SL must not be equal or too close
	if input.ProposedTrade.ProposedTakeProfit > 0 && input.ProposedTrade.ProposedStopLoss > 0 {
		minSep := input.SymbolInfo.TickSize
		if minSep <= 0 {
			minSep = 0.0001
		}
		if math.Abs(input.ProposedTrade.ProposedTakeProfit-input.ProposedTrade.ProposedStopLoss) < minSep {
			reasons = append(reasons, "TP_SL_TOO_CLOSE")
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
	symbolMaxLev := input.SymbolInfo.MaxLeverage
	if symbolMaxLev <= 0 {
		symbolMaxLev = 100.0
	}
	if leverage > symbolMaxLev {
		reasons = append(reasons, "LEVERAGE_EXCEEDS_MAX")
	}
	if leverage <= 0 {
		reasons = append(reasons, "INVALID_LEVERAGE")
	}

	// Sizing
	baseNotional := e.computeBaseNotional(input)
	finalNotional := baseNotional

	// LLM REDUCE_SIZE
	if input.LLMDecision.Decision == "REDUCE_SIZE" && input.LLMDecision.SizeMultiplier > 0 {
		finalNotional = baseNotional * input.LLMDecision.SizeMultiplier
	}

	margin := 0.0
	if leverage > 0 {
		margin = finalNotional / leverage
	}
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
	if !isFinitePositive(input.ProposedTrade.ProposedEntry) || !isFinitePositive(input.ProposedTrade.ProposedStopLoss) {
		return 0
	}

	leverage := e.cfg.Sizing.MaxLeverage
	if leverage <= 0 {
		leverage = e.cfg.Broker.Paper.DefaultLeverage
	}
	if !isFinitePositive(leverage) {
		return 0
	}
	marginPerTrade := e.cfg.Sizing.MarginPerTradeUSD
	if marginPerTrade <= 0 {
		marginPerTrade = e.cfg.PortfolioRisk.MarginPerTradeUSD
	}
	if !isFinitePositive(marginPerTrade) {
		return 0
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
	out := math.Min(positionByMargin, positionByRisk)
	if math.IsNaN(out) || math.IsInf(out, 0) || out < 0 {
		return 0
	}
	return out
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

func isFinitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

func isFiniteNonNegative(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
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
		"TP_SL_TOO_CLOSE":             "Take profit and stop loss are too close",
		"RR_BELOW_MIN":                "Risk/Reward below minimum",
		"EXPECTED_MOVE_TOO_SMALL":     "Expected move too small relative to cost",
		"BELOW_MIN_NOTIONAL":          "Position below minimum notional",
		"LEVERAGE_EXCEEDS_MAX":        "Leverage exceeds maximum",
		"PORTFOLIO_RISK_LIMIT":        "Portfolio risk limit reached",
		"RISK_TOO_SMALL":              "Risk amount too small after modifiers",
		"MAX_CORRELATED_ALT_POSITIONS": "Maximum correlated alt LONG positions reached",
		"INVALID_QTY":                 "Invalid quantity after lot size rounding",
		"MARGIN_EXCEEDS_AVAILABLE":    "Margin required exceeds available balance",
	}
	if r, ok := reasons[code]; ok {
		return r
	}
	return fmt.Sprintf("Unknown reason: %s", code)
}

// --- Phase 4 Candidate Risk Validation ---

// ValidateCandidate performs Phase 4 deterministic risk validation on a scored candidate.
func (e *Engine) ValidateCandidate(input CandidateRiskInput) RiskValidationResult {
	cfg := e.cfg.Risk.WithDefaults()
	var reasons []string
	var modifiers []string

	cand := input.Candidate
	acc := input.AccountState

	// Basic sanity checks
	if !isFinitePositive(acc.Equity) {
		return RiskValidationResult{CandidateID: cand.CandidateID, Approved: false, RejectionReasons: []string{"INVALID_EQUITY"}, RiskConfigVersion: cfg.RiskConfigVersion}
	}
	if !isFinitePositive(cand.EntryPrice) {
		return RiskValidationResult{CandidateID: cand.CandidateID, Approved: false, RejectionReasons: []string{"INVALID_ENTRY"}, RiskConfigVersion: cfg.RiskConfigVersion}
	}
	if cand.StopLoss <= 0 || !isFinitePositive(cand.StopLoss) {
		return RiskValidationResult{CandidateID: cand.CandidateID, Approved: false, RejectionReasons: []string{"SL_MISSING"}, RiskConfigVersion: cfg.RiskConfigVersion}
	}

	// 1. Determine base risk pct (cap at max)
	riskPct := cfg.BaseRiskPerTradePct
	if riskPct > cfg.MaxRiskPerTradePct {
		riskPct = cfg.MaxRiskPerTradePct
	}

	// 2. Apply modifiers in order
	// Modifier 1: REDUCE_SIZE from scoring
	if input.ScoreResult.Action == "REDUCE_SIZE" {
		riskPct *= 0.5
		modifiers = append(modifiers, "scoring_reduce_size_0.5x")
	}

	// Modifier 2: BTC bearish HTF + LONG
	if cand.Side == domain.SideLong && hasBTCBearishHTF(input.RegimeSnapshot) {
		riskPct *= 0.7
		modifiers = append(modifiers, "btc_bearish_htf_0.7x")
	}

	// Modifier 3: strong_underperform + LONG
	if cand.Side == domain.SideLong && input.RegimeSnapshot.RelativeStrength.Classification == "strong_underperform" {
		riskPct *= 0.6
		modifiers = append(modifiers, "strong_underperform_0.6x")
	}

	// 3. Check minimum risk after modifiers
	if riskPct < cfg.MinRiskPerTradePct {
		return RiskValidationResult{
			CandidateID:       cand.CandidateID,
			Approved:          false,
			RejectionReasons:  []string{"RISK_TOO_SMALL"},
			ModifiersApplied:  modifiers,
			RiskConfigVersion: cfg.RiskConfigVersion,
		}
	}

	// 4. Sizing
	leverage := cfg.PreferredLeverage
	if leverage > cfg.MaxLeverage {
		leverage = cfg.MaxLeverage
	}
	if leverage > input.SymbolInfo.MaxLeverage && input.SymbolInfo.MaxLeverage > 0 {
		leverage = input.SymbolInfo.MaxLeverage
	}

	riskAmount := acc.Equity * riskPct / 100
	riskPerUnit := math.Abs(cand.EntryPrice - cand.StopLoss)
	qtyRaw := riskAmount / riskPerUnit
	qty := roundToLotSize(qtyRaw, input.SymbolInfo.LotSize)
	positionValue := qty * cand.EntryPrice
	marginRequired := positionValue / leverage

	// 5. Validations
	// LONG: SL below entry, TPs above entry
	// SHORT: SL above entry, TPs below entry
	if cand.Side == domain.SideLong {
		if cand.StopLoss >= cand.EntryPrice {
			reasons = append(reasons, "SL_WRONG_SIDE")
		}
		for _, tp := range cand.TakeProfits {
			if tp.Price <= cand.EntryPrice {
				reasons = append(reasons, "TP_WRONG_SIDE")
				break
			}
		}
	} else {
		if cand.StopLoss <= cand.EntryPrice {
			reasons = append(reasons, "SL_WRONG_SIDE")
		}
		for _, tp := range cand.TakeProfits {
			if tp.Price >= cand.EntryPrice {
				reasons = append(reasons, "TP_WRONG_SIDE")
				break
			}
		}
	}

	// RR >= min_rr (against TP1)
	if len(cand.TakeProfits) > 0 {
		rr := cand.RiskRewardRatio
		if rr < cfg.MinRR {
			reasons = append(reasons, "RR_BELOW_MIN")
		}
	}

	// TP/SL must not be equal or too close
	if len(cand.TakeProfits) > 0 {
		minSep := input.SymbolInfo.TickSize
		if minSep <= 0 {
			minSep = 0.0001
		}
		if math.Abs(cand.TakeProfits[0].Price-cand.StopLoss) < minSep {
			reasons = append(reasons, "TP_SL_TOO_CLOSE")
		}
	}

	// Margin <= available_balance * 0.95
	availableBalance := acc.AvailableBalance
	if availableBalance <= 0 {
		availableBalance = acc.Balance - acc.UsedMargin
	}
	if availableBalance > 0 && marginRequired > availableBalance*0.95 {
		reasons = append(reasons, "MARGIN_EXCEEDS_AVAILABLE")
	}

	// Qty > min order size
	if qty <= 0 || (input.SymbolInfo.LotSize > 0 && qty < input.SymbolInfo.LotSize) {
		reasons = append(reasons, "INVALID_QTY")
	}
	if input.SymbolInfo.MinNotional > 0 && positionValue < input.SymbolInfo.MinNotional {
		reasons = append(reasons, "BELOW_MIN_NOTIONAL")
	}

	// Price rounded to tick size
	entryPrice := roundToTickSize(cand.EntryPrice, input.SymbolInfo.TickSize)
	slPrice := roundToTickSize(cand.StopLoss, input.SymbolInfo.TickSize)

	// Max open positions
	openPosCount := 0
	for _, p := range input.Portfolio.OpenPositions {
		if p.Status == domain.PositionStatusOpen {
			openPosCount++
		}
	}
	if openPosCount >= cfg.MaxOpenPositions {
		reasons = append(reasons, "MAX_OPEN_POSITIONS")
	}

	// Max correlated positions (only 1 alt LONG at a time if BTC-correlated)
	if cand.Side == domain.SideLong && cand.Symbol != "BTCUSDT" {
		altLongCount := 0
		for _, p := range input.Portfolio.OpenPositions {
			if p.Status == domain.PositionStatusOpen && p.Side == domain.SideLong && p.Symbol != "BTCUSDT" {
				altLongCount++
			}
		}
		if altLongCount >= cfg.MaxCorrelatedPositions {
			reasons = append(reasons, "MAX_CORRELATED_ALT_POSITIONS")
		}
	}

	// Bot state
	if input.BotState.Halted {
		reasons = append(reasons, "BOT_HALTED")
	}

	// Build order plan
	var tpPlans []TakeProfitPlan
	for _, tp := range cand.TakeProfits {
		tpPlans = append(tpPlans, TakeProfitPlan{
			Price: roundToTickSize(tp.Price, input.SymbolInfo.TickSize),
			Qty:   roundToLotSize(qty*tp.SizePct/100, input.SymbolInfo.LotSize),
		})
	}

	// Estimate fees (taker fee for entry + taker fee for exit)
	feeRate := e.cfg.Broker.Paper.FeeTakerBps / 10000.0
	estimatedFees := positionValue * feeRate * 2 // entry + exit

	plan := &OrderPlan{
		Symbol:         cand.Symbol,
		Side:           cand.Side,
		Qty:            qty,
		EntryPrice:     entryPrice,
		EntryType:      string(cand.EntryType),
		StopLoss:       slPrice,
		TakeProfits:    tpPlans,
		Leverage:       leverage,
		MarginRequired: marginRequired,
		EstimatedFees:  estimatedFees,
		RiskAmountUSD:  riskAmount,
		RiskPctUsed:    riskPct,
	}

	approved := len(reasons) == 0
	return RiskValidationResult{
		CandidateID:       cand.CandidateID,
		Approved:          approved,
		RejectionReasons:  reasons,
		OrderPlan:         plan,
		ModifiersApplied:  modifiers,
		RiskConfigVersion: cfg.RiskConfigVersion,
	}
}

func hasBTCBearishHTF(snap regime.MarketRegimeSnapshot) bool {
	for _, f := range snap.BTCFiltersTriggered {
		if f == "BTCStronglyBearishHTF" {
			return true
		}
	}
	return false
}

func roundToTickSize(price, tickSize float64) float64 {
	if tickSize <= 0 || math.IsNaN(tickSize) || math.IsInf(tickSize, 0) {
		return price
	}
	return math.Round(price/tickSize) * tickSize
}

func roundToLotSize(qty, lotSize float64) float64 {
	if lotSize <= 0 || math.IsNaN(lotSize) || math.IsInf(lotSize, 0) {
		return qty
	}
	return math.Round(qty/lotSize) * lotSize
}
