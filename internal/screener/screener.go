package screener

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
	"github.com/virhan/botsurv/internal/logger"
	"github.com/virhan/botsurv/internal/marketdata"
	"github.com/virhan/botsurv/internal/regime"
	"github.com/virhan/botsurv/internal/scoring"
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
	Symbol           string                    `json:"symbol"`
	Side             string                    `json:"side"`
	SetupType        string                    `json:"setup_type"`
	Regime           string                    `json:"regime"`
	EntryType        string                    `json:"entry_type"`
	ProposedEntry    float64                   `json:"proposed_entry"`
	StopLoss         float64                   `json:"stop_loss"`
	TakeProfit       float64                   `json:"take_profit"`
	RR               float64                   `json:"rr"`
	SetupScore       float64                   `json:"setup_score"`
	ExpectedMove     float64                   `json:"expected_move"`
	OrderBook        *OrderBookSummary         `json:"order_book,omitempty"`
	TradeFlow        *TradeFlowSummary         `json:"trade_flow,omitempty"`
	RegimeSummary    *RegimeSummary            `json:"regime_summary,omitempty"`
	CostSummary      *CostSummary              `json:"cost_summary,omitempty"`
	RiskSummary      *RiskSummary              `json:"risk_summary,omitempty"`
	WatchlistContext *scoring.WatchlistContext `json:"watchlist_context,omitempty"`
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

// StrategyRejection captures why a strategy failed for a symbol.
type StrategyRejection struct {
	Symbol   string `json:"symbol"`
	Strategy string `json:"strategy"`  // "trend_pullback" or "breakout_retest"
	Reason   string `json:"reason"`    // raw FailedOn string, e.g. "RR 1.2 < 1.4"
	Code     string `json:"code"`      // normalized code: "NO_BREAKOUT", "RR_TOO_LOW", etc.
	NearMiss bool   `json:"near_miss"`
}

// EvaluateError wraps evaluateSymbol failures with strategy rejection details.
type EvaluateError struct {
	Symbol    string
	Err       error
	Rejections []StrategyRejection
}

func (e *EvaluateError) Error() string {
	return fmt.Sprintf("evaluate %s: %s", e.Symbol, e.Err.Error())
}

func (e *EvaluateError) Unwrap() error {
	return e.Err
}

// ScreenResult holds the output of the screener.
type ScreenResult struct {
	Candidates      []domain.Candidate
	LLMContexts     map[string]string // symbol -> JSON context
	NonEligible     []domain.Candidate
	TradeCandidates map[string]strategy.TradeCandidate
	ScoreResults    map[string]scoring.ScoreResult
	RegimeSnapshots map[string]regime.MarketRegimeSnapshot
	IndicatorSnapshots15m map[string]indicator.IndicatorSnapshot
	IndicatorSnapshots1h  map[string]indicator.IndicatorSnapshot
	StrategyRejections []StrategyRejection
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
	tradeCandidates := make(map[string]strategy.TradeCandidate)
	scoreResults := make(map[string]scoring.ScoreResult)
	regimeSnapshots := make(map[string]regime.MarketRegimeSnapshot)
	var strategyRejections []StrategyRejection
	for _, sym := range qualitySymbols {
		if sym.Blacklist {
			continue
		}

		cand, tc, sr, rs, err := s.evaluateSymbol(ctx, sym, cycleID)
		if err != nil {
			s.log.Warn("skip symbol in screener", map[string]any{"symbol": sym.Symbol, "error": err.Error()})
			var ee *EvaluateError
			if errors.As(err, &ee) {
				strategyRejections = append(strategyRejections, ee.Rejections...)
			} else {
				strategyRejections = append(strategyRejections, StrategyRejection{
					Symbol:   sym.Symbol,
					Strategy: "infra",
					Reason:   err.Error(),
					Code:     rejectionCode(err.Error()),
					NearMiss: false,
				})
			}
			continue
		}
		candidates = append(candidates, cand)
		tradeCandidates[sym.Symbol] = tc
		scoreResults[sym.Symbol] = sr
		regimeSnapshots[sym.Symbol] = rs
	}

	// Build indicator snapshots for LLM routing (from cached candle data)
	snap15mMap := make(map[string]indicator.IndicatorSnapshot, len(candidates))
	snap1hMap := make(map[string]indicator.IndicatorSnapshot, len(candidates))
	setupTF := s.cfg.Strategy.Timeframes.Setup
	if setupTF == "" {
		setupTF = "15m"
	}
	contextTF := s.cfg.Strategy.Timeframes.Context
	if contextTF == "" {
		contextTF = "1H"
	}
	indCfg := s.cfg.IndicatorEngine.WithDefaults()
	snapCfg := indicator.SnapshotConfig{
		EMA20Period:             indCfg.EMA20Period,
		EMA50Period:             indCfg.EMA50Period,
		EMA200Period:            indCfg.EMA200Period,
		RSIPeriod:               indCfg.RSIPeriod,
		MACDFastPeriod:          indCfg.MACDFastPeriod,
		MACDSlowPeriod:          indCfg.MACDSlowPeriod,
		MACDSignalPeriod:        indCfg.MACDSignalPeriod,
		ATRPeriod:               indCfg.ATRPeriod,
		VolumeMAPeriod:          indCfg.VolumeMAPeriod,
		SwingLookback:           indCfg.SwingLookback,
		RecentSwingCount:        indCfg.RecentSwingCount,
		SupportResistanceATRTol: indCfg.SupportResistanceATRTol,
	}
	for _, sym := range qualitySymbols {
		if sym.Blacklist {
			continue
		}
		setupCandles, _ := s.md.GetCandles(ctx, sym.Symbol, setupTF, 250)
		if len(setupCandles) > 50 {
			if s, err := indicator.BuildSnapshot(sym.Symbol, setupTF, setupCandles, snapCfg); err == nil {
				snap15mMap[sym.Symbol] = s
			}
		}
		ctxCandles, _ := s.md.GetCandles(ctx, sym.Symbol, contextTF, 250)
		if len(ctxCandles) > 50 {
			if s, err := indicator.BuildSnapshot(sym.Symbol, contextTF, ctxCandles, snapCfg); err == nil {
				snap1hMap[sym.Symbol] = s
			}
		}
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

	// Log aggregated strategy rejections for observability
	rejectByCode := map[string]int{}
	for _, r := range strategyRejections {
		rejectByCode[r.Code]++
	}
	logReject := map[string]any{
		"total_candidates": len(candidates),
		"llm_eligible":     len(eligible),
		"non_eligible":     len(nonEligible),
		"contexts_built":   len(llmContexts),
		"strategy_rejects": len(strategyRejections),
	}
	if len(rejectByCode) > 0 {
		logReject["reject_breakdown"] = rejectByCode
	}
	s.log.Info("screener complete", logReject)

	return &ScreenResult{
		Candidates:            eligible,
		LLMContexts:           llmContexts,
		NonEligible:           nonEligible,
		TradeCandidates:       tradeCandidates,
		ScoreResults:          scoreResults,
		RegimeSnapshots:       regimeSnapshots,
		IndicatorSnapshots15m: snap15mMap,
		IndicatorSnapshots1h:  snap1hMap,
		StrategyRejections:    strategyRejections,
	}, nil
}

func (s *Screener) evaluateSymbol(ctx context.Context, sym domain.UniverseSymbol, cycleID string) (domain.Candidate, strategy.TradeCandidate, scoring.ScoreResult, regime.MarketRegimeSnapshot, error) {
	setupTF := s.cfg.Strategy.Timeframes.Setup
	if setupTF == "" {
		setupTF = "15m"
	}
	contextTF := s.cfg.Strategy.Timeframes.Context
	if contextTF == "" {
		contextTF = "1H"
	}

	validator := marketdata.NewValidator(s.cfg.DataValidation)
	minIndicatorCandles := maxInt(s.cfg.DataValidation.MinCandlesOrDefault(), 250)
	minIndicatorCandles = maxInt(minIndicatorCandles, s.cfg.IndicatorEngine.WithDefaults().EMA200Period+50)

	setupCandlesRaw, err := s.md.GetCandles(ctx, sym.Symbol, setupTF, closedCandleRawLimit(minIndicatorCandles))
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("get %s candles: %w", setupTF, err)
	}
	validatedSetup, err := validator.ValidateCandleBatch(marketdata.CandleValidationInput{
		Symbol:              sym.Symbol,
		Timeframe:           setupTF,
		Candles:             setupCandlesRaw,
		MinRequired:         minIndicatorCandles,
		MaxDataAgeSeconds:   s.cfg.DataValidation.MaxDataAgeSecondsFor(setupTF),
		RequireClosedLatest: true,
	})
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("setup candle validation failed: %w", err)
	}

	context1hRaw, err := s.md.GetCandles(ctx, sym.Symbol, contextTF, closedCandleRawLimit(minIndicatorCandles))
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("get %s candles: %w", contextTF, err)
	}
	validated1H, err := validator.ValidateCandleBatch(marketdata.CandleValidationInput{
		Symbol:              sym.Symbol,
		Timeframe:           contextTF,
		Candles:             context1hRaw,
		MinRequired:         minIndicatorCandles,
		MaxDataAgeSeconds:   s.cfg.DataValidation.MaxDataAgeSecondsFor(contextTF),
		RequireClosedLatest: true,
	})
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("context candle validation failed: %w", err)
	}

	context4hRaw, err := s.md.GetCandles(ctx, sym.Symbol, "4H", closedCandleRawLimit(minIndicatorCandles))
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("get 4H candles: %w", err)
	}
	validated4H, err := validator.ValidateCandleBatch(marketdata.CandleValidationInput{
		Symbol:              sym.Symbol,
		Timeframe:           "4H",
		Candles:             context4hRaw,
		MinRequired:         minIndicatorCandles,
		MaxDataAgeSeconds:   s.cfg.DataValidation.MaxDataAgeSecondsFor("4H"),
		RequireClosedLatest: true,
	})
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("4h candle validation failed: %w", err)
	}

	indCfg := s.cfg.IndicatorEngine.WithDefaults()
	snapCfg := indicator.SnapshotConfig{
		EMA20Period:             indCfg.EMA20Period,
		EMA50Period:             indCfg.EMA50Period,
		EMA200Period:            indCfg.EMA200Period,
		RSIPeriod:               indCfg.RSIPeriod,
		MACDFastPeriod:          indCfg.MACDFastPeriod,
		MACDSlowPeriod:          indCfg.MACDSlowPeriod,
		MACDSignalPeriod:        indCfg.MACDSignalPeriod,
		ATRPeriod:               indCfg.ATRPeriod,
		VolumeMAPeriod:          indCfg.VolumeMAPeriod,
		SwingLookback:           indCfg.SwingLookback,
		RecentSwingCount:        indCfg.RecentSwingCount,
		SupportResistanceATRTol: indCfg.SupportResistanceATRTol,
	}

	setupSnap, err := indicator.BuildSnapshot(sym.Symbol, setupTF, validatedSetup.Candles, snapCfg)
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("build %s indicator snapshot: %w", setupTF, err)
	}
	snap1H, err := indicator.BuildSnapshot(sym.Symbol, contextTF, validated1H.Candles, snapCfg)
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("build %s indicator snapshot: %w", contextTF, err)
	}
	snap4H, err := indicator.BuildSnapshot(sym.Symbol, "4H", validated4H.Candles, snapCfg)
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("build 4H indicator snapshot: %w", err)
	}
	s.log.Info("indicator snapshot computed", map[string]any{
		"symbol":             sym.Symbol,
		"timeframe":          setupTF,
		"indicator_snapshot": setupSnap,
	})

	// Regime snapshot: BTC 5m/15m used for flash-crash detection. If stale/unavailable,
	// pass empty slice — the regime filter handles it gracefully (Triggered=false).
	btc5m := marketdata.CandleValidationResult{}
	btc5mRaw, err := s.md.GetCandles(ctx, "BTCUSDT", "5m", closedCandleRawLimit(2))
	if err == nil {
		if vr, ve := validator.ValidateCandleBatch(marketdata.CandleValidationInput{
			Symbol:              "BTCUSDT",
			Timeframe:           "5m",
			Candles:             btc5mRaw,
			MinRequired:         2,
			MaxDataAgeSeconds:   s.cfg.DataValidation.MaxDataAgeSecondsFor("5m"),
			RequireClosedLatest: true,
		}); ve == nil {
			btc5m = vr
		} else {
			s.log.Warn("btc 5m validation skipped", map[string]any{"error": ve.Error()})
		}
	} else {
		s.log.Warn("btc 5m fetch skipped", map[string]any{"error": err.Error()})
	}

	btc15mRaw, err := s.md.GetCandles(ctx, "BTCUSDT", "15m", closedCandleRawLimit(2))
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("get btc 15m candles: %w", err)
	}
	btc15m, err := validator.ValidateCandleBatch(marketdata.CandleValidationInput{
		Symbol:              "BTCUSDT",
		Timeframe:           "15m",
		Candles:             btc15mRaw,
		MinRequired:         2,
		MaxDataAgeSeconds:   s.cfg.DataValidation.MaxDataAgeSecondsFor("15m"),
		RequireClosedLatest: true,
	})
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("validate btc 15m candles: %w", err)
	}
	btc1hRaw, err := s.md.GetCandles(ctx, "BTCUSDT", "1H", closedCandleRawLimit(minIndicatorCandles))
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("get btc 1H candles: %w", err)
	}
	btc1H, err := validator.ValidateCandleBatch(marketdata.CandleValidationInput{
		Symbol:              "BTCUSDT",
		Timeframe:           "1H",
		Candles:             btc1hRaw,
		MinRequired:         minIndicatorCandles,
		MaxDataAgeSeconds:   s.cfg.DataValidation.MaxDataAgeSecondsFor("1H"),
		RequireClosedLatest: true,
	})
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("validate btc 1H candles: %w", err)
	}
	btc4hRaw, err := s.md.GetCandles(ctx, "BTCUSDT", "4H", closedCandleRawLimit(minIndicatorCandles))
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("get btc 4H candles: %w", err)
	}
	btc4H, err := validator.ValidateCandleBatch(marketdata.CandleValidationInput{
		Symbol:              "BTCUSDT",
		Timeframe:           "4H",
		Candles:             btc4hRaw,
		MinRequired:         minIndicatorCandles,
		MaxDataAgeSeconds:   s.cfg.DataValidation.MaxDataAgeSecondsFor("4H"),
		RequireClosedLatest: true,
	})
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("validate btc 4H candles: %w", err)
	}

	btc1HSnap, err := indicator.BuildSnapshot("BTCUSDT", "1H", btc1H.Candles, snapCfg)
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("build btc 1H snapshot: %w", err)
	}
	btc4HSnap, err := indicator.BuildSnapshot("BTCUSDT", "4H", btc4H.Candles, snapCfg)
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("build btc 4H snapshot: %w", err)
	}

	var btcd1hCandles []domain.Candle
	var btcd1hSnap *indicator.IndicatorSnapshot
	var btcd4hSnap *indicator.IndicatorSnapshot

	if raw, e := s.md.GetCandles(ctx, "BTCD", "1H", closedCandleRawLimit(2)); e == nil {
		if vr, vErr := validator.ValidateCandleBatch(marketdata.CandleValidationInput{
			Symbol:              "BTCD",
			Timeframe:           "1H",
			Candles:             raw,
			MinRequired:         2,
			MaxDataAgeSeconds:   s.cfg.DataValidation.MaxDataAgeSecondsFor("1H"),
			RequireClosedLatest: true,
		}); vErr == nil {
			btcd1hCandles = vr.Candles
		}
	}
	if raw, e := s.md.GetCandles(ctx, "BTCD", "1H", closedCandleRawLimit(minIndicatorCandles)); e == nil {
		if vr, vErr := validator.ValidateCandleBatch(marketdata.CandleValidationInput{
			Symbol:              "BTCD",
			Timeframe:           "1H",
			Candles:             raw,
			MinRequired:         minIndicatorCandles,
			MaxDataAgeSeconds:   s.cfg.DataValidation.MaxDataAgeSecondsFor("1H"),
			RequireClosedLatest: true,
		}); vErr == nil {
			if snap, snapErr := indicator.BuildSnapshot("BTCD", "1H", vr.Candles, snapCfg); snapErr == nil {
				btcd1hSnap = &snap
			}
		}
	}
	if raw, e := s.md.GetCandles(ctx, "BTCD", "4H", closedCandleRawLimit(minIndicatorCandles)); e == nil {
		if vr, vErr := validator.ValidateCandleBatch(marketdata.CandleValidationInput{
			Symbol:              "BTCD",
			Timeframe:           "4H",
			Candles:             raw,
			MinRequired:         minIndicatorCandles,
			MaxDataAgeSeconds:   s.cfg.DataValidation.MaxDataAgeSecondsFor("4H"),
			RequireClosedLatest: true,
		}); vErr == nil {
			if snap, snapErr := indicator.BuildSnapshot("BTCD", "4H", vr.Candles, snapCfg); snapErr == nil {
				btcd4hSnap = &snap
			}
		}
	}

	regimeCfg := s.cfg.MarketRegime.WithDefaults()
	rsCfg := regimeCfg.RelativeStrength.WithDefaults()
	targetRSCandles := validated1H.Candles
	if sym.Symbol == "BTCUSDT" {
		targetRSCandles = btc1H.Candles
	}
	regimeSnap, err := regime.BuildSnapshot(regime.SnapshotInput{
		Now:             time.Now().UTC(),
		BTC5mCandles:    btc5m.Candles,
		BTC15mCandles:   btc15m.Candles,
		BTC1hCandles:    btc1H.Candles,
		BTC1hSnapshot:   btc1HSnap,
		BTC4hSnapshot:   btc4HSnap,
		Target1hCandles: targetRSCandles,
		BTCD1hCandles:   btcd1hCandles,
		BTCD1hSnapshot:  btcd1hSnap,
		BTCD4hSnapshot:  btcd4hSnap,
		Config: regime.Config{
			BTCDumpShortThresholdPct:   regimeCfg.BTCDumpShortPct,
			BTCDumpMediumThresholdPct:  regimeCfg.BTCDumpMediumPct,
			BTCNearLevelATRBuffer:      regimeCfg.BTCNearLevelATRBuffer,
			BTCDRisingFastThresholdPct: regimeCfg.BTCDRisingFastPct,
			RelativeStrength: regime.RelativeStrengthConfig{
				StrongOutperformPct:   rsCfg.StrongOutperformPct,
				StrongUnderperformPct: rsCfg.StrongUnderperformPct,
				NeutralBandPct:        rsCfg.NeutralBandPct,
				SmoothedEMAPeriod:     rsCfg.SmoothedEMAPeriod,
			},
		},
	})
	if err != nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{}, fmt.Errorf("build market regime snapshot: %w", err)
	}
	s.log.Info("market regime snapshot computed", map[string]any{
		"symbol":                 sym.Symbol,
		"market_regime_snapshot": regimeSnap,
	})

	indicatorRef := fmt.Sprintf("%s:%s:%d", sym.Symbol, setupTF, setupSnap.LastCloseTime.Unix())
	regimeRef := fmt.Sprintf("%s:%d", sym.Symbol, regimeSnap.Timestamp.Unix())

	trendCand, trendReject := strategy.GenerateTrendPullback(strategy.TrendPullbackInput{
		Now:                  time.Now().UTC(),
		Symbol:               sym.Symbol,
		Timeframe:            setupTF,
		TickSize:             sym.TickSize,
		Candles15m:           validatedSetup.Candles,
		Snapshot15m:          setupSnap,
		Snapshot1h:           snap1H,
		Snapshot4h:           snap4H,
		IndicatorSnapshotRef: indicatorRef,
		RegimeSnapshotRef:    regimeRef,
	})
	var rejections []StrategyRejection
	if trendReject != nil {
		s.log.Info("rejected_candidate", map[string]any{
			"would_have_been": trendReject.WouldHaveBeen,
			"failed_on":       trendReject.FailedOn,
			"near_miss":       trendReject.NearMiss,
		})
		rejections = append(rejections, StrategyRejection{
			Symbol:   sym.Symbol,
			Strategy: "trend_pullback",
			Reason:   trendReject.FailedOn,
			Code:     rejectionCode(trendReject.FailedOn),
			NearMiss: trendReject.NearMiss,
		})
	}
	breakoutCand, breakoutReject := strategy.GenerateBreakoutRetest(strategy.BreakoutRetestInput{
		Now:                  time.Now().UTC(),
		Symbol:               sym.Symbol,
		Timeframe:            setupTF,
		TickSize:             sym.TickSize,
		Candles15m:           validatedSetup.Candles,
		Snapshot15m:          setupSnap,
		Snapshot1h:           snap1H,
		IndicatorSnapshotRef: indicatorRef,
		RegimeSnapshotRef:    regimeRef,
	})
	if breakoutReject != nil {
		s.log.Info("rejected_candidate", map[string]any{
			"would_have_been": breakoutReject.WouldHaveBeen,
			"failed_on":       breakoutReject.FailedOn,
			"near_miss":       breakoutReject.NearMiss,
		})
		rejections = append(rejections, StrategyRejection{
			Symbol:   sym.Symbol,
			Strategy: "breakout_retest",
			Reason:   breakoutReject.FailedOn,
			Code:     rejectionCode(breakoutReject.FailedOn),
			NearMiss: breakoutReject.NearMiss,
		})
	}

	var chosen *strategy.TradeCandidate
	if trendCand != nil && breakoutCand != nil {
		if trendCand.RiskRewardRatio >= breakoutCand.RiskRewardRatio {
			chosen = trendCand
		} else {
			chosen = breakoutCand
		}
	} else if trendCand != nil {
		chosen = trendCand
	} else if breakoutCand != nil {
		chosen = breakoutCand
	}
	if chosen == nil {
		return domain.Candidate{}, strategy.TradeCandidate{}, scoring.ScoreResult{}, regime.MarketRegimeSnapshot{},
			&EvaluateError{
				Symbol:     sym.Symbol,
				Err:        fmt.Errorf("no phase3 setup candidate generated"),
				Rejections: rejections,
			}
	}

	scoreCfg := s.cfg.Scoring.WithDefaults()
	scoreRes := scoring.Score(scoring.Input{
		Candidate:      *chosen,
		Snapshot15m:    setupSnap,
		Snapshot1h:     snap1H,
		Snapshot4h:     snap4H,
		RegimeSnapshot: regimeSnap,
	}, scoreCfg)
	s.log.Info("candidate score breakdown", map[string]any{
		"symbol":          sym.Symbol,
		"strategy":        chosen.Strategy,
		"score_total":     scoreRes.ScoreTotal,
		"components":      scoreRes.Components,
		"scoring_version": scoreRes.ScoringVersion,
		"score_action":    scoreRes.Action,
	})

	targetNotional := s.cfg.ComputeTargetNotional()
	var ob domain.OrderBookSummary
	var obErr error
	if targetNotional > 0 {
		ob, obErr = s.md.GetOrderBookSummary(ctx, sym.Symbol, targetNotional, string(chosen.Side))
	} else {
		obErr = fmt.Errorf("target notional is zero")
	}
	price, _ := s.md.GetLatestPrice(ctx, sym.Symbol)
	liq, exec, vol := 50.0, 50.0, 50.0
	if obErr == nil {
		liq, exec, vol = computeScoresFromOB(ob, setupSnap.ATR14, price)
	}

	entryType := domain.EntryTypeMarket
	switch chosen.EntryType {
	case strategy.CandidateEntryLimitRetest:
		entryType = domain.EntryTypeLimitRetest
	case strategy.CandidateEntryStop:
		entryType = domain.EntryTypeMarket
	}
	tp1 := chosen.TakeProfits[0].Price
	expectedMove := math.Abs(tp1 - chosen.EntryPrice)
	estFee := chosen.EntryPrice * 0.00055
	estSlippage := chosen.EntryPrice * 0.0005
	totalCost := estFee + estSlippage

	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			Symbol:             sym.Symbol,
			Side:               chosen.Side,
			SetupType:          string(chosen.Strategy),
			Regime:             regimeSnap.RelativeStrength.Classification,
			EntryType:          entryType,
			ProposedEntry:      chosen.EntryPrice,
			ProposedStopLoss:   chosen.StopLoss,
			ProposedTakeProfit: tp1,
			RR:                 chosen.RiskRewardRatio,
			InvalidationLevel:  chosen.InvalidationLevel,
			ReasonCodes:        []string{string(scoreRes.Action), chosen.TAReasoning.Trigger},
			SetupScore:         scoreRes.Components.SetupQuality,
			ExpectedMove:       expectedMove,
			EstimatedTotalCost: totalCost,
		},
		CycleID:         cycleID,
		CandidateScore:  scoreRes.ScoreTotal,
		LiquidityScore:  liq,
		ExecutionScore:  exec,
		VolatilityScore: vol,
		LLMEligible:     scoreRes.Action != scoring.ActionReject,
	}
	if obErr == nil {
		cand = evaluateLLMEligibility(cand, ob, s.cfg.Universe.Filters, s.cfg.LLMRouting, s.cfg.Strategy)
	} else {
		cand.LLMRoutingReasonCodes = []string{"no_orderbook"}
	}
	if scoreRes.Action == scoring.ActionReject {
		cand.LLMEligible = false
		cand.LLMRoutingReasonCodes = append(cand.LLMRoutingReasonCodes, "scoring_reject")
	}
	return cand, *chosen, scoreRes, regimeSnap, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *Screener) buildContext(ctx context.Context, cand domain.Candidate) (string, error) {
	if !isFinitePositive(cand.ProposedEntry) {
		return "", fmt.Errorf("invalid proposed entry: %v", cand.ProposedEntry)
	}
	if !isFinitePositive(cand.ProposedStopLoss) {
		return "", fmt.Errorf("invalid stop loss: %v", cand.ProposedStopLoss)
	}
	if cand.ProposedTakeProfit > 0 && !isFinite(cand.ProposedTakeProfit) {
		return "", fmt.Errorf("invalid take profit: %v", cand.ProposedTakeProfit)
	}
	if !isFiniteNonNegative(cand.SetupScore) {
		return "", fmt.Errorf("invalid setup score: %v", cand.SetupScore)
	}
	if !isFiniteNonNegative(cand.ExpectedMove) {
		return "", fmt.Errorf("invalid expected move: %v", cand.ExpectedMove)
	}

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
	if !isFiniteNonNegative(atr) {
		return "", fmt.Errorf("invalid ATR: %v", atr)
	}
	if ema200 > 0 && !isFinitePositive(ema200) {
		return "", fmt.Errorf("invalid EMA200: %v", ema200)
	}
	if price > 0 && !isFinitePositive(price) {
		return "", fmt.Errorf("invalid latest price: %v", price)
	}

	// Optional watchlist context with Fibonacci confluence (built before ctxObj).
	var watchlistCtx *scoring.WatchlistContext
	if s.cfg.WatchlistContext.Enabled {
		wc := s.buildWatchlistContext(ctx, cand, atr, price)
		watchlistCtx = &wc
	}

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

	if watchlistCtx != nil {
		ctxObj.WatchlistContext = watchlistCtx
	}

	// Sanitize NaN/Inf values that would break JSON marshal.
	sanitizeContext(&ctxObj)

	b, err := json.Marshal(ctxObj)
	if err != nil {
		return "", fmt.Errorf("marshal context: %w", err)
	}
	return string(b), nil
}

// buildWatchlistContext computes optional watchlist/Fibonacci context for LLM.
// This is purely informational — it never modifies entry, SL, TP, or score.
func (s *Screener) buildWatchlistContext(ctx context.Context, cand domain.Candidate, atr, price float64) scoring.WatchlistContext {
	cfg := s.cfg.WatchlistContext.WithDefaults()

	var fibCtx indicator.FibonacciContext
	if cfg.Fibonacci.Enabled {
		setupTF := s.cfg.Strategy.Timeframes.Setup
		if setupTF == "" {
			setupTF = "15m"
		}
		lookback := cfg.Fibonacci.LookbackCandles
		if lookback <= 0 {
			lookback = 80
		}
		fibCandles, err := s.md.GetCandles(ctx, cand.Symbol, setupTF, lookback+20)
		if err == nil && len(fibCandles) >= lookback {
			fibCtx = indicator.ComputeFibonacciContext(
				fibCandles,
				cand.Side,
				lookback,
				cfg.Fibonacci.ZoneTolerancePct,
			)
		}
	}

	// Compute ATR% for chart quality if not already available
	atrPct := 0.0
	if price > 0 && atr > 0 {
		atrPct = atr / price * 100
	}

	volumeRatio := 0.0
	// Best-effort volume ratio from available indicator snapshots
	if s.cfg.Strategy.Timeframes.Setup != "" {
		volCandles, err := s.md.GetCandles(ctx, cand.Symbol, s.cfg.Strategy.Timeframes.Setup, 30)
		if err == nil && len(volCandles) > 1 {
			last := volCandles[len(volCandles)-1].Volume
			var sum float64
			count := 0
			for i := 0; i < len(volCandles)-1 && count < 20; i++ {
				idx := len(volCandles) - 2 - i
				if idx >= 0 {
					sum += volCandles[idx].Volume
					count++
				}
			}
			if count > 0 && sum > 0 {
				avg := sum / float64(count)
				if avg > 0 {
					volumeRatio = last / avg
				}
			}
		}
	}

	wc := scoring.BuildWatchlistContext(
		fibCtx,
		cand.Symbol,
		volumeRatio,
		atrPct,
		cfg,
	)
	return wc
}

// closedCandleRawLimit returns the raw candle count to request when downstream
// validation requires minRequired closed candles. Market data caches commonly
// include the current in-progress candle as the latest item; requesting one
// extra prevents a valid 250-closed-candle cache from turning into 249 after
// the validator strips the in-progress candle.
func closedCandleRawLimit(minRequired int) int {
	if minRequired <= 0 {
		return 0
	}
	return minRequired + 1
}

// rejectionCode normalizes a strategy rejection reason to a canonical code.
// This is a best-effort categorization; unknown reasons get "OTHER_REJECT".
func rejectionCode(reason string) string {
	switch {
	// Strategy-specific patterns — check before generic infra keywords
	// because strategy strings may contain "candle", "regime", etc.

	// Breakout (covers "no valid breakout in last 8 candles", retest, confirmation, etc.)
	case containsFold(reason, "no valid breakout"), containsFold(reason, "retest condition failed"),
		containsFold(reason, "closed below broken resistance"), containsFold(reason, "closed above broken support"),
		containsFold(reason, "bullish confirmation missing"), containsFold(reason, "bearish confirmation missing"):
		return "NO_BREAKOUT"
	// Pullback / structure (covers "reaction candle/structure condition failed")
	case containsFold(reason, "pullback distance"), containsFold(reason, "reaction candle"),
		containsFold(reason, "structure condition"):
		return "STRUCTURE_REJECT"
	// Volume
	case containsFold(reason, "volume") && containsFold(reason, "volume_ma"):
		return "LOW_VOLUME"
	// RR — "rr" followed by "<" indicates RR below threshold
	case containsFold(reason, "rr") && containsFold(reason, "<"):
		return "RR_TOO_LOW"
	// RSI
	case containsFold(reason, "rsi"):
		return "RSI_REJECT"
	// MACD
	case containsFold(reason, "macd"):
		return "MACD_REJECT"
	// Infrastructure errors — keywords that do NOT appear in strategy rejection strings.
	// Check before "regime" keyword to avoid matching "build market regime snapshot".
	case containsFold(reason, "validation"), containsFold(reason, "snapshot"),
		containsFold(reason, "market regime"):
		return "DATA_ERROR"
	// "get" + "candle" pattern from infra candle fetch errors
	case containsFold(reason, "get") && containsFold(reason, "candle"):
		return "DATA_ERROR"
	// Trend / regime mismatches (after infra checks so "market regime snapshot" is DATA_ERROR)
	case containsFold(reason, "htf trend"), containsFold(reason, "strongly bearish"),
		containsFold(reason, "strongly bullish"):
		return "REGIME_MISMATCH"
	case containsFold(reason, "regime"):
		return "REGIME_MISMATCH"
	// Insufficient data (must be after strategy breakout to avoid "insufficient candles" masking NO_BREAKOUT)
	case containsFold(reason, "insufficient"), containsFold(reason, "unavailable"):
		return "INSUFFICIENT_DATA"
	// Invalid risk / distance
	case containsFold(reason, "invalid risk"), containsFold(reason, "risk distance"):
		return "RISK_REJECT"
	// Generic candidate failure
	case containsFold(reason, "no phase3"), containsFold(reason, "no setup"):
		return "NO_STRATEGY_CANDIDATE"
	}
	return "OTHER_REJECT"
}

func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
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

	if c.WatchlistContext != nil {
		c.WatchlistContext.Fibonacci.SwingHigh = sanitizeFloat(c.WatchlistContext.Fibonacci.SwingHigh)
		c.WatchlistContext.Fibonacci.SwingLow = sanitizeFloat(c.WatchlistContext.Fibonacci.SwingLow)
		c.WatchlistContext.Fibonacci.Fib05 = sanitizeFloat(c.WatchlistContext.Fibonacci.Fib05)
		c.WatchlistContext.Fibonacci.Fib0618 = sanitizeFloat(c.WatchlistContext.Fibonacci.Fib0618)
		c.WatchlistContext.Fibonacci.Fib0786 = sanitizeFloat(c.WatchlistContext.Fibonacci.Fib0786)
		c.WatchlistContext.Fibonacci.CurrentPrice = sanitizeFloat(c.WatchlistContext.Fibonacci.CurrentPrice)
		c.WatchlistContext.Fibonacci.DistanceToNearestFibPct = sanitizeFloat(c.WatchlistContext.Fibonacci.DistanceToNearestFibPct)
		c.WatchlistContext.ConfluenceScoreDelta = sanitizeFloat(c.WatchlistContext.ConfluenceScoreDelta)
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
	if math.IsNaN(ob.SpreadBps) || math.IsInf(ob.SpreadBps, 0) {
		return 0, 0, 0
	}

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
	if math.IsNaN(cand.CandidateScore) || math.IsInf(cand.CandidateScore, 0) {
		reasons = append(reasons, "candidate_score_non_finite")
		cand.LLMEligible = false
		cand.LLMRoutingReasonCodes = reasons
		return cand
	}
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

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func isFinitePositive(v float64) bool {
	return isFinite(v) && v > 0
}

func isFiniteNonNegative(v float64) bool {
	return isFinite(v) && v >= 0
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
