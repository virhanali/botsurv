package screener

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
	"github.com/virhan/botsurv/internal/strategy"
	"github.com/virhan/botsurv/internal/universe"
)

// MarketDataProvider is the interface needed by the screener.
type MarketDataProvider interface {
	universe.MarketDataReadonly
	GetTradeFlow(ctx context.Context, symbol string) ([]domain.TradeFlow, error)
}

// CandidateContext is the compact JSON context sent to LLM for veto.
type CandidateContext struct {
	Symbol        string            `json:"symbol"`
	Side          string            `json:"side"`
	SetupType     string            `json:"setup_type"`
	Regime        string            `json:"regime"`
	EntryType     string            `json:"entry_type"`
	ProposedEntry float64           `json:"proposed_entry"`
	StopLoss      float64           `json:"stop_loss"`
	TakeProfit    float64           `json:"take_profit"`
	RR            float64           `json:"rr"`
	SetupScore    float64           `json:"setup_score"`
	ExpectedMove  float64           `json:"expected_move"`
	OrderBook     *OrderBookSummary `json:"order_book,omitempty"`
	TradeFlow     *TradeFlowSummary `json:"trade_flow,omitempty"`
	RegimeSummary *RegimeSummary    `json:"regime_summary,omitempty"`
	CostSummary   *CostSummary      `json:"cost_summary,omitempty"`
	RiskSummary   *RiskSummary      `json:"risk_summary,omitempty"`
}

type OrderBookSummary struct {
	SpreadBps   float64 `json:"spread_bps"`
	BidDepth    float64 `json:"bid_depth"`
	AskDepth    float64 `json:"ask_depth"`
	SlippageBps float64 `json:"slippage_bps"`
}

type TradeFlowSummary struct {
	BuySellRatio float64 `json:"buy_sell_ratio"`
	TradeCount   int     `json:"trade_count"`
}

type RegimeSummary struct {
	Regime     string  `json:"regime"`
	EMA200     float64 `json:"ema200"`
	PriceVsEMA float64 `json:"price_vs_ema_pct"`
	ATR        float64 `json:"atr"`
	ATRPct     float64 `json:"atr_pct"`
}

type CostSummary struct {
	EstimatedFee      float64 `json:"estimated_fee"`
	EstimatedSlippage float64 `json:"estimated_slippage"`
	TotalCost         float64 `json:"total_cost"`
	CostBps           float64 `json:"cost_bps"`
}

type RiskSummary struct {
	DailyLossPct     float64 `json:"daily_loss_pct"`
	OpenPositions    int     `json:"open_positions"`
	MaxOpenPositions int     `json:"max_open_positions"`
	DuplicateSymbol  bool    `json:"duplicate_symbol"`
}

// Screener scans quality symbols for setups and builds LLM contexts.
type Screener struct {
	cfg      app.UserConfig
	universe *universe.Scanner
	md       MarketDataProvider
	log      *logger.Logger
}

// NewScreener creates a new Screener.
func NewScreener(cfg app.UserConfig, universe *universe.Scanner, md MarketDataProvider, log *logger.Logger) *Screener {
	return &Screener{
		cfg:      cfg,
		universe: universe,
		md:       md,
		log:      log,
	}
}

// RefreshUniverse triggers a full universe refresh.
func (s *Screener) RefreshUniverse(ctx context.Context) error {
	return s.universe.RefreshUniverse(ctx)
}

// ScreenResult holds the output of the screener.
type ScreenResult struct {
	Candidates  []domain.Candidate
	LLMContexts map[string]string // symbol -> JSON context
	NonEligible []domain.Candidate
}

// Screen runs the full screening pipeline:
// 1. Get quality symbols from universe
// 2. Run setup engine on each
// 3. Compute scores and LLM eligibility
// 4. Build context for eligible candidates
func (s *Screener) Screen(ctx context.Context, cycleID string) (*ScreenResult, error) {
	// Get quality symbols from universe
	qualitySymbols, err := s.universe.GetUniverse(ctx)
	if err != nil {
		return nil, fmt.Errorf("get universe: %w", err)
	}

	// Run setup engine on each symbol
	var candidates []domain.Candidate
	for _, sym := range qualitySymbols {
		if sym.Blacklist {
			continue
		}

		cand, err := s.evaluateSymbol(ctx, sym, cycleID)
		if err != nil {
			s.log.Debug("skip symbol in screener", map[string]any{"symbol": sym.Symbol, "error": err.Error()})
			continue
		}
		candidates = append(candidates, cand)
	}

	// Separate eligible and non-eligible
	var eligible []domain.Candidate
	var nonEligible []domain.Candidate
	for _, c := range candidates {
		if c.LLMEligible {
			eligible = append(eligible, c)
		} else {
			nonEligible = append(nonEligible, c)
		}
	}

	// Build contexts for eligible candidates
	llmContexts := make(map[string]string)
	for _, c := range eligible {
		ctxJSON, err := s.buildContext(ctx, c)
		if err != nil {
			s.log.Error("build context failed", map[string]any{"symbol": c.Symbol, "error": err.Error()})
			continue
		}
		llmContexts[c.Symbol] = ctxJSON
	}

	s.log.Info("screener complete", map[string]any{
		"total_candidates": len(candidates),
		"llm_eligible":     len(eligible),
		"non_eligible":     len(nonEligible),
		"contexts_built":   len(llmContexts),
	})

	return &ScreenResult{
		Candidates:  eligible,
		LLMContexts: llmContexts,
		NonEligible: nonEligible,
	}, nil
}

func (s *Screener) evaluateSymbol(ctx context.Context, sym domain.UniverseSymbol, cycleID string) (domain.Candidate, error) {
	// Get 1H candles for regime
	candles1H, err := s.md.GetCandles(ctx, sym.Symbol, "1H", 210)
	if err != nil || len(candles1H) < 200 {
		return domain.Candidate{}, fmt.Errorf("insufficient 1H candles: %d", len(candles1H))
	}

	// Get 15m candles for setup
	candles15m, err := s.md.GetCandles(ctx, sym.Symbol, "15m", 25)
	if err != nil || len(candles15m) < 21 {
		return domain.Candidate{}, fmt.Errorf("insufficient 15m candles: %d", len(candles15m))
	}

	// Compute indicators
	atr := strategy.ATR(candles1H, s.cfg.Strategy.Indicators.ATRPeriod)
	ema200 := strategy.EMA(candles1H, s.cfg.Strategy.Indicators.EMA200Period)

	// Detect regime
	regimeCfg := strategy.RegimeConfig{
		TrendUpMinDistanceFromEMAPct:   s.cfg.Strategy.Regime.TrendUpMinDistanceFromEMAPct,
		TrendDownMaxDistanceFromEMAPct: s.cfg.Strategy.Regime.TrendDownMaxDistanceFromEMAPct,
		RangeMaxDistanceFromEMAPct:     s.cfg.Strategy.Regime.RangeMaxDistanceFromEMAPct,
	}
	regime := strategy.DetectRegime(candles1H, ema200, regimeCfg)

	// Detect setup
	setupCfg := strategy.SetupConfig{
		RangeCandles:               s.cfg.Strategy.Indicators.RangeCandles,
		VolumeSMAPeriod:            s.cfg.Strategy.Indicators.VolumeSMAPeriod,
		MinVolumeRatio:             s.cfg.Strategy.Indicators.MinVolumeRatio,
		MaxBreakoutExtensionATR:    s.cfg.Strategy.Indicators.MaxBreakoutExtensionATR,
		MaxDistanceFromBreakoutATR: s.cfg.Strategy.Indicators.MaxDistanceFromBreakoutATR,
		MinRR:                      s.cfg.Strategy.Indicators.MinRR,
		ExpectedMoveCostMultiplier: s.cfg.Strategy.Indicators.ExpectedMoveCostMultiplier,
		MinBodyRatio:               0.3,
		AtrPeriod:                  s.cfg.Strategy.Indicators.ATRPeriod,
	}
	setup := strategy.DetectSetup(candles15m, atr, ema200, regime, setupCfg)

	if setup.SetupType == strategy.SetupNone {
		return domain.Candidate{}, fmt.Errorf("no setup: %v", setup.ReasonCodes)
	}

	// Get orderbook for scoring (skip if target notional is not configured)
	targetNotional := s.cfg.ComputeTargetNotional()
	var ob domain.OrderBookSummary
	var obErr error
	if targetNotional > 0 {
		ob, obErr = s.md.GetOrderBookSummary(ctx, sym.Symbol, targetNotional, string(setup.Side))
	} else {
		obErr = fmt.Errorf("target notional is zero")
	}
	price, _ := s.md.GetLatestPrice(ctx, sym.Symbol)

	// Compute scores
	liq, exec, vol := 50.0, 50.0, 50.0
	if obErr == nil {
		liq, exec, vol = computeScoresFromOB(ob, atr, price)
	}
	candScore := liq*0.25 + exec*0.25 + setup.SetupScore*0.35 + vol*0.15

	// Estimate costs
	estFee := price * 0.00055     // taker 5.5bps
	estSlippage := price * 0.0005 // 5bps
	totalCost := estFee + estSlippage
	expectedMove := atr * 2

	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			Symbol:             sym.Symbol,
			Side:               setup.Side,
			SetupType:          string(setup.SetupType),
			Regime:             string(regime),
			EntryType:          setup.EntryType,
			ProposedEntry:      setup.ProposedEntry,
			ProposedStopLoss:   setup.StopLoss,
			ProposedTakeProfit: setup.TakeProfit,
			RR:                 setup.RR,
			SetupScore:         setup.SetupScore,
			ExpectedMove:       expectedMove,
			EstimatedTotalCost: totalCost,
			ReasonCodes:        setup.ReasonCodes,
		},
		CycleID:         cycleID,
		CandidateScore:  candScore,
		LiquidityScore:  liq,
		ExecutionScore:  exec,
		VolatilityScore: vol,
	}

	// Evaluate LLM eligibility
	if obErr == nil {
		cand = evaluateLLMEligibility(cand, ob, s.cfg.Universe.Filters, s.cfg.LLMRouting, s.cfg.Strategy)
	} else {
		cand.LLMRoutingReasonCodes = []string{"no_orderbook"}
	}

	return cand, nil
}

func (s *Screener) buildContext(ctx context.Context, cand domain.Candidate) (string, error) {
	targetNotional := s.cfg.ComputeTargetNotional()
	var ob domain.OrderBookSummary
	if targetNotional > 0 {
		ob, _ = s.md.GetOrderBookSummary(ctx, cand.Symbol, targetNotional, string(cand.Side))
	}
	tf, _ := s.md.GetTradeFlow(ctx, cand.Symbol)
	price, _ := s.md.GetLatestPrice(ctx, cand.Symbol)

	// Compute ATR and EMA for context.
	// Need 210 candles for EMA200 (same as evaluateSymbol) + ATR period.
	candles1H, _ := s.md.GetCandles(ctx, cand.Symbol, "1H", 210)
	atr := strategy.ATR(candles1H, s.cfg.Strategy.Indicators.ATRPeriod)
	ema200 := strategy.EMA(candles1H, 200)

	ctxObj := CandidateContext{
		Symbol:        cand.Symbol,
		Side:          string(cand.Side),
		SetupType:     cand.SetupType,
		Regime:        cand.Regime,
		EntryType:     string(cand.EntryType),
		ProposedEntry: cand.ProposedEntry,
		StopLoss:      cand.ProposedStopLoss,
		TakeProfit:    cand.ProposedTakeProfit,
		RR:            cand.RR,
		SetupScore:    cand.SetupScore,
		ExpectedMove:  cand.ExpectedMove,
	}

	if ob.BestBid > 0 || ob.BestAsk > 0 {
		ctxObj.OrderBook = &OrderBookSummary{
			SpreadBps:   ob.SpreadBps,
			BidDepth:    ob.BidDepth,
			AskDepth:    ob.AskDepth,
			SlippageBps: ob.EstimatedSlippageBps,
		}
	}

	if len(tf) > 0 {
		t := tf[0]
		ctxObj.TradeFlow = &TradeFlowSummary{
			BuySellRatio: t.BuySellRatio,
			TradeCount:   t.TradeCount,
		}
	}

	if price > 0 && ema200 > 0 {
		ctxObj.RegimeSummary = &RegimeSummary{
			Regime:     string(cand.Regime),
			EMA200:     ema200,
			PriceVsEMA: (price - ema200) / ema200 * 100,
			ATR:        atr,
			ATRPct:     atr / price * 100,
		}
	}

	if price > 0 {
		estFee := price * 0.00055
		estSlip := price * 0.0005
		ctxObj.CostSummary = &CostSummary{
			EstimatedFee:      estFee,
			EstimatedSlippage: estSlip,
			TotalCost:         estFee + estSlip,
			CostBps:           (estFee + estSlip) / price * 10000,
		}
	}

	ctxObj.RiskSummary = &RiskSummary{
		MaxOpenPositions: s.cfg.PortfolioRisk.MaxOpenPositions,
	}

	// Sanitize NaN/Inf values that would break JSON marshal.
	sanitizeContext(&ctxObj)

	b, err := json.Marshal(ctxObj)
	if err != nil {
		return "", fmt.Errorf("marshal context: %w", err)
	}
	return string(b), nil
}

// sanitizeContext replaces NaN and +/-Inf float64 values with 0
// to prevent json.Marshal from failing.
func sanitizeContext(c *CandidateContext) {
	c.ProposedEntry = sanitizeFloat(c.ProposedEntry)
	c.StopLoss = sanitizeFloat(c.StopLoss)
	c.TakeProfit = sanitizeFloat(c.TakeProfit)
	c.RR = sanitizeFloat(c.RR)
	c.SetupScore = sanitizeFloat(c.SetupScore)
	c.ExpectedMove = sanitizeFloat(c.ExpectedMove)

	if c.OrderBook != nil {
		c.OrderBook.SpreadBps = sanitizeFloat(c.OrderBook.SpreadBps)
		c.OrderBook.BidDepth = sanitizeFloat(c.OrderBook.BidDepth)
		c.OrderBook.AskDepth = sanitizeFloat(c.OrderBook.AskDepth)
		c.OrderBook.SlippageBps = sanitizeFloat(c.OrderBook.SlippageBps)
	}

	if c.TradeFlow != nil {
		c.TradeFlow.BuySellRatio = sanitizeFloat(c.TradeFlow.BuySellRatio)
	}

	if c.RegimeSummary != nil {
		c.RegimeSummary.EMA200 = sanitizeFloat(c.RegimeSummary.EMA200)
		c.RegimeSummary.PriceVsEMA = sanitizeFloat(c.RegimeSummary.PriceVsEMA)
		c.RegimeSummary.ATR = sanitizeFloat(c.RegimeSummary.ATR)
		c.RegimeSummary.ATRPct = sanitizeFloat(c.RegimeSummary.ATRPct)
	}

	if c.CostSummary != nil {
		c.CostSummary.EstimatedFee = sanitizeFloat(c.CostSummary.EstimatedFee)
		c.CostSummary.EstimatedSlippage = sanitizeFloat(c.CostSummary.EstimatedSlippage)
		c.CostSummary.TotalCost = sanitizeFloat(c.CostSummary.TotalCost)
		c.CostSummary.CostBps = sanitizeFloat(c.CostSummary.CostBps)
	}
}

func sanitizeFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// helpers

func computeScoresFromOB(ob domain.OrderBookSummary, atr, price float64) (liq, exec, vol float64) {
	depth := ob.BidDepth + ob.AskDepth
	liq = 30.0
	if ob.SpreadBps > 0 {
		liq += 40.0 * max(0, 1.0-ob.SpreadBps/100.0)
	}
	if depth > 0 {
		liq += 30.0 * min(1.0, depth/10000.0)
	}
	liq = clamp(liq, 0, 100)

	exec = 80.0
	if ob.EstimatedSlippageBps > 0 {
		exec = 80.0 * max(0, 1.0-ob.EstimatedSlippageBps/200.0)
	}
	exec = clamp(exec, 0, 100)

	vol = 50.0
	if price > 0 && atr > 0 {
		atrPct := atr / price * 100
		if atrPct >= 0.2 && atrPct <= 5.0 {
			vol = 50.0 + 50.0*(1.0-abs(atrPct-2.5)/2.5)
		} else if atrPct < 0.2 {
			vol = 50.0 * (atrPct / 0.2)
		}
	}
	vol = clamp(vol, 0, 100)
	return liq, exec, vol
}

func evaluateLLMEligibility(
	cand domain.Candidate,
	ob domain.OrderBookSummary,
	filters app.UniverseFiltersConfig,
	llmConfig app.LLMRoutingConfig,
	strategyCfg app.StrategyConfig,
) domain.Candidate {
	minScore := llmConfig.MinCandidateScore
	if minScore <= 0 {
		minScore = 75
	}
	minRR := strategyCfg.Indicators.MinRR
	if minRR <= 0 {
		minRR = 2.0
	}

	reasons := []string{}
	if cand.CandidateScore < minScore {
		reasons = append(reasons, "candidate_score_below_threshold")
	}
	if llmConfig.RequireExecutionOk {
		if filters.MaxSpreadBps > 0 && ob.SpreadBps > filters.MaxSpreadBps {
			reasons = append(reasons, "spread_too_wide")
		}
		if ob.EstimatedSlippageBps >= 100 {
			reasons = append(reasons, "slippage_too_high")
		}
	}
	if llmConfig.RequireLiquidityOk && ob.DepthToPositionSizeRatio <= 0 {
		reasons = append(reasons, "insufficient_depth")
	}
	if cand.RR > 0 && cand.RR < minRR {
		reasons = append(reasons, "rr_below_min")
	}
	if cand.EstimatedTotalCost > 0 {
		if cand.ExpectedMove <= 0 || cand.ExpectedMove <= 3*cand.EstimatedTotalCost {
			reasons = append(reasons, "expected_move_too_small")
		}
	}

	cand.LLMEligible = len(reasons) == 0
	cand.LLMRoutingReasonCodes = reasons
	return cand
}

func clamp(v, minV, maxV float64) float64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
