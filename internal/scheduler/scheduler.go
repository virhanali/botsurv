package scheduler

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/virhan/botsurv/internal/alert"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/db"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/execution"
	paperexec "github.com/virhan/botsurv/internal/execution/paper"
	"github.com/virhan/botsurv/internal/executor"
	"github.com/virhan/botsurv/internal/llm"
	"github.com/virhan/botsurv/internal/logger"
	"github.com/virhan/botsurv/internal/monitor"
	"github.com/virhan/botsurv/internal/regime"
	"github.com/virhan/botsurv/internal/risk"
	"github.com/virhan/botsurv/internal/routing"
	"github.com/virhan/botsurv/internal/screener"
	"github.com/virhan/botsurv/internal/shadow"
	"github.com/virhan/botsurv/internal/strategy"
)

// Scheduler orchestrates the full trading cycle.
type Scheduler struct {
	cfg         app.UserConfig
	screener    *screener.Screener
	llmClient   llm.Client
	llmReviewer *llm.Reviewer
	riskEng     *risk.Engine
	executor    *executor.Executor
	safetyEng   *execution.SafetyEngine
	monitor     *monitor.Monitor
	md          screener.MarketDataProvider
	log         *logger.Logger

	mu       sync.Mutex
	running  bool
	cycleSeq int

	mode app.BotMode

	alertSvc         alert.Service
	llmDecisionRepo  db.LLMDecisionRepository
	riskDecisionRepo db.RiskDecisionRepository
	llmUsageRepo     db.LLMUsageRepository
	cycleRepo        db.CycleRepository
	candidateRepo    db.CandidateRepository

	// Phase 5
	decisionLogRepo       db.DecisionLogRepository
	outcomeRepo           db.CandidateOutcomeRepository
	paperSim              *paperexec.Simulator
	counterfactualTracker *shadow.CounterfactualTracker

	// LLM daily call tracking. Persisted when llmUsageRepo is wired.
	llmCallsToday int
	llmCallsDate  string // YYYY-MM-DD

	// Daily reset tracking.
	lastDailyResetDay string // YYYY-MM-DD

	// BTC 5m staleness tracker.
	btc5mStalenessTracker *regime.StalenessTracker

	// Loss tracking state.
	consecutiveLosses   int
	cooldownUntil       *time.Time
	perSymbolSLCooldown map[string]time.Time
}

// NewScheduler creates a new Scheduler.
func NewScheduler(
	cfg app.UserConfig,
	screener *screener.Screener,
	llmClient llm.Client,
	riskEng *risk.Engine,
	executor *executor.Executor,
	safetyEng *execution.SafetyEngine,
	monitor *monitor.Monitor,
	md screener.MarketDataProvider,
	log *logger.Logger,
) *Scheduler {
	stalenessCfg := regime.Btc5mStalenessConfig{
		WarnCycles:     cfg.MarketRegime.Btc5mStaleness.WarnCycles,
		CriticalCycles: cfg.MarketRegime.Btc5mStaleness.CriticalCycles,
	}
	return &Scheduler{
		cfg:                    cfg,
		screener:               screener,
		llmClient:              llmClient,
		riskEng:                riskEng,
		executor:               executor,
		safetyEng:              safetyEng,
		monitor:                monitor,
		md:                     md,
		log:                    log,
		btc5mStalenessTracker:  regime.NewStalenessTracker(stalenessCfg),
	}
}

// SetMode sets the operational mode.
func (s *Scheduler) SetMode(mode app.BotMode) { s.mode = mode }

// effectiveLeverage returns the configured leverage for the reviewer.
func (s *Scheduler) effectiveLeverage(out risk.ValidateOutput) float64 {
	cfgLev := s.cfg.Sizing.MaxLeverage
	if cfgLev <= 0 {
		cfgLev = s.cfg.PortfolioRisk.MaxLeverage
	}
	if cfgLev <= 0 {
		cfgLev = s.cfg.Broker.Paper.DefaultLeverage
	}
	if cfgLev <= 0 {
		cfgLev = 3.0
	}
	return cfgLev
}

// SetAlertService sets the alert service for sending notifications.
func (s *Scheduler) SetAlertService(svc alert.Service) { s.alertSvc = svc }

// SetLLMDecisionRepo sets the LLM decision repository for persistence.
func (s *Scheduler) SetLLMDecisionRepo(repo db.LLMDecisionRepository) { s.llmDecisionRepo = repo }

// SetLLMReviewer sets the LLM Reviewer (Phase 6).
func (s *Scheduler) SetLLMReviewer(reviewer *llm.Reviewer) { s.llmReviewer = reviewer }

// SetRiskDecisionRepo sets the risk decision repository for persistence.
func (s *Scheduler) SetRiskDecisionRepo(repo db.RiskDecisionRepository) { s.riskDecisionRepo = repo }

// SetLLMUsageRepo sets the daily LLM usage repository for persistence.
func (s *Scheduler) SetLLMUsageRepo(repo db.LLMUsageRepository) { s.llmUsageRepo = repo }

// SetCycleRepo sets the cycle repository for persistence.
func (s *Scheduler) SetCycleRepo(repo db.CycleRepository) { s.cycleRepo = repo }

// SetCandidateRepo sets the candidate repository for persistence.
func (s *Scheduler) SetCandidateRepo(repo db.CandidateRepository) { s.candidateRepo = repo }

// SetDecisionLogRepo sets the decision log repository.
func (s *Scheduler) SetDecisionLogRepo(repo db.DecisionLogRepository) { s.decisionLogRepo = repo }

// SetOutcomeRepo sets the candidate outcome repository.
func (s *Scheduler) SetOutcomeRepo(repo db.CandidateOutcomeRepository) { s.outcomeRepo = repo }

// SetPaperSimulator sets the paper mode simulator.
func (s *Scheduler) SetPaperSimulator(sim *paperexec.Simulator) { s.paperSim = sim }

// SetCounterfactualTracker sets the counterfactual tracker.
func (s *Scheduler) SetCounterfactualTracker(t *shadow.CounterfactualTracker) {
	s.counterfactualTracker = t
}

// CycleResult holds the outcome of a trading cycle.
type CycleResult struct {
	CycleID     string
	StartedAt   time.Time
	EndedAt     time.Time
	Candidates  int
	LLMCalls    int
	Executions  int
	Skips       []SkipReason
	ReasonCodes []string

	// Phase 3: per-cycle breakdown
	RejectedPreLLM      int `json:"rejected_pre_llm"`
	LLMSkippedHighScore int `json:"llm_skipped_high_score"`
	LLMVetoCalled       int `json:"llm_veto_called"`
	LLMApprove          int `json:"llm_approve"`
	LLMReject           int `json:"llm_reject"`
	LLMReduceSize       int `json:"llm_reduce_size"`
	RiskRejected        int `json:"risk_rejected"`
	SafetyRejected      int `json:"safety_rejected"`
}

type SkipReason struct {
	Symbol string
	Reason string
}

type riskCandidate struct {
	candidate     domain.Candidate
	decision      domain.LLMDecision
	output        risk.ValidateOutput
	candidateID   int64
	monitorStatus monitor.MonitorStatus
	marketPrice   float64
	ob            domain.OrderBookSummary
}

// RunOnce executes a single trading cycle.
func (s *Scheduler) RunOnce(ctx context.Context) (result *CycleResult, err error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil, fmt.Errorf("cycle already running")
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	s.cycleSeq++
	cycleID := fmt.Sprintf("cycle-%d-%s", s.cycleSeq, time.Now().Format("20060102-150405"))
	startedAt := time.Now()

	result = &CycleResult{
		CycleID:   cycleID,
		StartedAt: startedAt,
	}

	// Insert cycle as "running" at start, update to final status at end.
	s.insertCycleRunning(ctx, cycleID, startedAt)
	defer func() {
		if result != nil {
			if result.EndedAt.IsZero() {
				result.EndedAt = time.Now()
			}
			s.updateCycleFinal(result, err)
		}
	}()

	s.log.Info("cycle started", map[string]any{"cycle_id": cycleID})

	// 1. Check bot state
	if s.isHalted() {
		result.ReasonCodes = append(result.ReasonCodes, "BOT_HALTED")
		result.EndedAt = time.Now()
		s.log.Info("cycle skipped: bot halted", map[string]any{"cycle_id": cycleID})
		return result, nil
	}

	// 1b. Check daily reset (day boundary transition)
	s.checkDailyReset()

	// 2. Refresh universe if needed
	if refreshErr := s.refreshUniverseIfNeeded(ctx); refreshErr != nil {
		s.log.Error("universe refresh failed", map[string]any{"error": refreshErr.Error()})
		result.ReasonCodes = append(result.ReasonCodes, "UNIVERSE_REFRESH_FAILED")
	}

	// 3. Run screener
	screenResult, screenErr := s.screener.Screen(ctx, cycleID)
	if screenErr != nil {
		result.ReasonCodes = append(result.ReasonCodes, "SCREEN_FAILED")
		result.EndedAt = time.Now()
		s.log.Error("screener failed", map[string]any{"error": screenErr.Error()})
		return result, screenErr
	}

	// Inject BTC 5m staleness warning into regime snapshots.
	if s.btc5mStalenessTracker != nil && s.btc5mStalenessTracker.ShouldCritical() {
		s.log.Error("BTC 5m data CRITICAL: stale for too many cycles — halting new positions", map[string]any{
			"stale_counter": s.btc5mStalenessTracker.Counter(),
		})
		result.ReasonCodes = append(result.ReasonCodes, "BTC_STALE_CRITICAL")
		result.EndedAt = time.Now()
		return result, nil
	}
	for sym, rs := range screenResult.RegimeSnapshots {
		if s.btc5mStalenessTracker != nil {
			s.btc5mStalenessTracker.InjectWarning(&rs)
			if s.btc5mStalenessTracker.Counter() > 0 {
				rs.BTCDataStale = true
				if !containsString(rs.Warnings, "BTC_DATA_STALE") {
					rs.Warnings = append(rs.Warnings, "BTC_DATA_STALE")
				}
			}
			screenResult.RegimeSnapshots[sym] = rs
		}
	}

	result.Candidates = len(screenResult.Candidates)

	// Log decisions for non-eligible candidates
	setupTF := s.cfg.Strategy.Timeframes.Setup
	if setupTF == "" {
		setupTF = "15m"
	}
	modeStr := s.mode.String()
	for _, nc := range screenResult.NonEligible {
		fields := &DecisionLogFields{}
		fields.WithDataValidationResult("passed")
		fields.WithCandidate(buildTradeCandidateFromDomain(nc))
		if tc, ok := screenResult.TradeCandidates[nc.Symbol]; ok {
			fields.WithCandidate(tc)
			if sr, ok2 := screenResult.ScoreResults[nc.Symbol]; ok2 {
				fields.WithScoreBreakdown(sr)
				fields.ScoringVersion = sr.ScoringVersion
			}
		}
		log := fields.ToDomain(result.CycleID, modeStr, nc.Symbol, setupTF, 0, "REJECTED_SCORE", "candidate below LLM eligibility threshold")
		s.saveDecisionLog(ctx, log)
	}

	// Check existing positions for SL/TP before new entries.
	// In paper mode, the simulator is the sole closer for paper positions;
	// skip the broker-level monitor check to avoid double-close races (C1).
	if !s.mode.IsPaper() {
		s.monitor.CheckAllPositions(ctx)
	}

	// Persist all generated candidates and track DB IDs for LLM decisions.
	candidateIDs := s.persistCandidates(ctx, screenResult.Candidates, screenResult.NonEligible, cycleID)

	if len(screenResult.Candidates) == 0 {
		reasons := []string{"NO_CANDIDATE"}
		codeSeen := map[string]bool{}
		for _, r := range screenResult.StrategyRejections {
			if !codeSeen[r.Code] {
				reasons = append(reasons, r.Code)
				codeSeen[r.Code] = true
			}
		}
		result.ReasonCodes = append(result.ReasonCodes, reasons...)
		result.EndedAt = time.Now()
		logDetails := map[string]any{
			"cycle_id":          cycleID,
			"reason_codes":      reasons,
			"strategy_rejects":  len(screenResult.StrategyRejections),
		}
		s.log.Info("cycle complete: no candidates", logDetails)
		return result, nil
	}

	// Paper mode: check open positions
	if s.mode.IsPaper() && s.paperSim != nil {
		s.paperSim.CheckOpenPositions(ctx)
		s.updatePostTradeState(ctx)
	}

	// 4. Apply LLM call caps.
	s.loadLLMUsage(ctx)
	cappedCandidates, skippedByCap := s.applyLLMCaps(screenResult.Candidates)
	for _, sk := range skippedByCap {
		result.Skips = append(result.Skips, sk)
	}

	// 5. Snapshot broker state once for the entire cycle to avoid races
	// between WS-driven position closures and scheduler state reads.
	cycleMonitorStatus := s.monitor.Status(ctx)

	// Paper mode: merge simulator open positions into cycle state so risk
	// checks (duplicate symbol, max open positions, same direction, exposure)
	// see paper trades opened by the simulator.
	if s.mode.IsPaper() && s.paperSim != nil {
		paperPositions, simErr := s.paperSim.GetOpenPositions(ctx)
		if simErr != nil {
			s.log.Error("paper simulator state unavailable", map[string]any{"error": simErr.Error()})
			result.ReasonCodes = append(result.ReasonCodes, "PAPER_STATE_UNAVAILABLE")
			result.EndedAt = time.Now()
			return result, nil
		}
		if len(paperPositions) > 0 {
			cycleMonitorStatus.OpenPositions = append(cycleMonitorStatus.OpenPositions, paperPositions...)
		}
	}

	// In paper mode, override the account equity with paper simulator equity
	// so risk sizing uses paper capital, not broker equity.
	if s.mode.IsPaper() && s.paperSim != nil {
		paperAccState := s.paperSim.AccountState()
		cycleMonitorStatus.AccountState.Equity = paperAccState.Equity
		cycleMonitorStatus.AccountState.Balance = paperAccState.Balance
		cycleMonitorStatus.AccountState.RealizedPnL = paperAccState.RealizedPnL
		if cycleMonitorStatus.AccountState.AvailableBalance <= 0 {
			cycleMonitorStatus.AccountState.AvailableBalance = paperAccState.Equity - paperAccState.UsedMargin
		}
	}

	// 6. Phase 1: Pre-LLM processing — collect candidates that pass hard blocks and routing
	type candidateCtx struct {
		cand             domain.Candidate
		ctxJSON          string
		routeResult      routing.RouteResult
		ob               domain.OrderBookSummary
		obErr            error
		marketPrice      float64
		priceErr         error
		candlesForBlocks []domain.Candle
		setupTF          string
		llmDecision      domain.LLMDecision
		llmErr           error
	}

	var approvedForPortfolio []riskCandidate
	var preLLMPass []*candidateCtx
	var vetoTasks []*candidateCtx

	for _, cand := range cappedCandidates {
		ctxJSON, ok := screenResult.LLMContexts[cand.Symbol]
		if !ok {
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "NO_CONTEXT"})
			continue
		}

		monitorStatus := cycleMonitorStatus
		targetNotional := s.cfg.ComputeTargetNotional()
		ob, obErr := s.md.GetOrderBookSummary(ctx, cand.Symbol, targetNotional, string(cand.Side))
		if obErr != nil {
			s.log.Warn("orderbook unavailable for hard blocks", map[string]any{
				"symbol": cand.Symbol,
				"error":  obErr.Error(),
			})
		}
		marketPrice, priceErr := s.md.GetLatestPrice(ctx, cand.Symbol)
		if priceErr != nil {
			s.log.Warn("price unavailable for hard blocks", map[string]any{
				"symbol": cand.Symbol,
				"error":  priceErr.Error(),
			})
		}
		setupTF := s.cfg.Strategy.Timeframes.Setup
		if setupTF == "" {
			setupTF = "15m"
		}
		minCandles := s.cfg.DataValidation.MinCandlesOrDefault()
		candlesForBlocks, candlesErr := s.md.GetCandles(ctx, cand.Symbol, setupTF, minCandles)
		if candlesErr != nil {
			s.log.Warn("candles unavailable for hard blocks", map[string]any{
				"symbol":    cand.Symbol,
				"timeframe": setupTF,
				"error":     candlesErr.Error(),
			})
		}
		lastDataAt := ob.LastUpdate
		if lastDataAt.IsZero() {
			lastDataAt = latestCandleCloseTime(candlesForBlocks, setupTF)
		}
		btc5mReturn := s.computeBTC5mReturn(ctx)

		localPositions := toPositionState(monitorStatus.OpenPositions)
		blockEval := risk.EvaluateHardBlocks(risk.BlockEvaluationInput{
			Symbol: cand.Symbol,
			Price:  marketPrice,
			Snapshot: risk.Snapshot{
				Price:          marketPrice,
				SpreadPct:      ob.SpreadBps / 100.0,
				FundingRatePct: 0,
				LastDataAt:     lastDataAt,
			},
			Candles:              candlesForBlocks,
			MinRequiredCandles:   minCandles,
			OrderBook:            ob,
			MaxSpreadPct:         s.cfg.HardBlocks.MaxSpreadPctOrDefault(),
			FundingRatePct:       0,
			MaxFundingAbsPct:     s.cfg.HardBlocks.MaxFundingAbsPctOrDefault(),
			PendingOrders:        monitorStatus.OpenOrders,
			CurrentPositions:     monitorStatus.OpenPositions,
			TodayPnL:             -monitorStatus.AccountState.DailyLoss,
			DailyMaxLossPct:      s.cfg.HardBlocks.DailyMaxLossPctOrDefault(),
			Equity:               monitorStatus.AccountState.Equity,
			RecentTrades:         nil,
			MaxConsecutiveLosses: s.cfg.HardBlocks.MaxConsecutiveLossesOrDefault(),
			LastTradeTime:        nil,
			Cooldown: risk.TradeCooldownConfig{
				Enabled:          s.cfg.HardBlocks.CooldownAfterLossMin > 0 || s.cfg.HardBlocks.CooldownAfterWinMin > 0,
				AfterLoss:        time.Duration(s.cfg.HardBlocks.CooldownAfterLossMin) * time.Minute,
				AfterWin:         time.Duration(s.cfg.HardBlocks.CooldownAfterWinMin) * time.Minute,
				LastTradeWasLoss: false,
			},
			EmergencyStop: risk.EmergencyStopState{
				Active: s.monitor.IsHalted(),
				Reason: "monitor halted",
			},
			BTC5mReturnPct:     btc5mReturn,
			BTCFlashCrash5mPct: s.cfg.HardBlocks.BTCFlashCrash5mPctOrDefault(),
			LocalPositions:     localPositions,
			ExchangePositions:  localPositions, // exchange state not available in phase 1.
			MaxDataAge:         time.Duration(s.cfg.DataValidation.MaxDataAgeSecondsFor(setupTF)) * time.Second,
			PerSymbolSLCooldown: s.perSymbolSLCooldown,
		})
		if blockEval.Blocked {
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "HARD_BLOCK"})
			s.log.Info("candidate blocked by hard block evaluator", map[string]any{
				"symbol":            cand.Symbol,
				"blocks_triggered":  blockEval.BlocksTriggered,
				"first_block":       blockEval.FirstBlockReason,
				"all_block_reasons": blockEval.AllBlockReasons,
			})
			fields := &DecisionLogFields{}
			fields.WithDataValidationResult("blocks_triggered")
			fields.WithBlocksTriggered(blockEval.BlocksTriggered)
			if tc, ok := screenResult.TradeCandidates[cand.Symbol]; ok {
				fields.WithCandidate(tc)
				if sr, ok2 := screenResult.ScoreResults[cand.Symbol]; ok2 {
					fields.WithScoreBreakdown(sr)
					fields.ScoringVersion = sr.ScoringVersion
				}
			}
			dl := fields.ToDomain(cycleID, modeStr, cand.Symbol, setupTF, len(candlesForBlocks), "BLOCKED_HARD", blockEval.FirstBlockReason)
			s.saveDecisionLog(ctx, dl)
			s.trackCounterfactual(ctx, dl.DecisionID, cand)
			continue
		}

		// Detect ambiguity flags and route candidate
		score := cand.CandidateScore
		flags := routing.AmbiguityFlags{}
		tc, tcOk := screenResult.TradeCandidates[cand.Symbol]
		sr, _ := screenResult.ScoreResults[cand.Symbol]
		rs, rsOk := screenResult.RegimeSnapshots[cand.Symbol]
		snap15m, snap15mOk := screenResult.IndicatorSnapshots15m[cand.Symbol]
		snap1h, snap1hOk := screenResult.IndicatorSnapshots1h[cand.Symbol]
		if tcOk && rsOk && snap15mOk && snap1hOk {
			flags = routing.DetectAmbiguity(snap15m, snap1h, rs, sr, tc)
		}

		routingEng := routing.DefaultRoutingEngine()
		maxLLM := s.cfg.LLMRouting.MaxCallsPerCycle
		marketIsRanging := rs.IsRanging
		btcDataStale := rs.BTCDataStale
		routeResult := routingEng.RouteCandidate(score, flags, result.LLMCalls, maxLLM, marketIsRanging, btcDataStale)

		cctx := &candidateCtx{
			cand:             cand,
			ctxJSON:          ctxJSON,
			routeResult:      routeResult,
			ob:               ob,
			obErr:            obErr,
			marketPrice:      marketPrice,
			priceErr:         priceErr,
			candlesForBlocks: candlesForBlocks,
			setupTF:          setupTF,
		}

		switch routeResult.Route {
		case routing.RouteRejectPreLLM:
			result.RejectedPreLLM++
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "PRE_LLM_REJECTED:" + routeResult.Reason})
			s.log.Info("candidate rejected before LLM", map[string]any{
				"symbol": cand.Symbol,
				"score":  score,
				"flags":  routeResult.FlagCount,
				"reason": routeResult.Reason,
			})
			continue

		case routing.RouteSkipLLM:
			result.LLMSkippedHighScore++
			cctx.llmDecision = domain.LLMDecision{
				Decision: func() string {
					if cand.EntryType == domain.EntryTypeLimitRetest {
						return "ALLOW_LIMIT_RETEST"
					}
					return "ALLOW_MARKET"
				}(),
				Confidence:       1.0,
				SizeMultiplier:   1.0,
				ReasonCodes:      []string{"HIGH_SCORE_SKIP_LLM"},
				ValidationStatus: "high_score_skip",
			}
			s.log.Info("candidate skipped LLM veto", map[string]any{
				"symbol": cand.Symbol,
				"score":  score,
				"flags":  routeResult.FlagCount,
				"reason": routeResult.Reason,
			})
			preLLMPass = append(preLLMPass, cctx)

		default: // LLM_VETO_REQUIRED
			// Per-cycle call cap: only collect up to MaxCallsPerCycle veto tasks
			if s.cfg.LLMRouting.MaxCallsPerCycle > 0 && len(vetoTasks) >= s.cfg.LLMRouting.MaxCallsPerCycle {
				result.RejectedPreLLM++
				result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "LLM_CALL_CAP_PER_CYCLE"})
				s.log.Info("candidate skipped: LLM call cap per cycle reached", map[string]any{
					"symbol":    cand.Symbol,
					"llm_calls": len(vetoTasks),
					"max_calls": s.cfg.LLMRouting.MaxCallsPerCycle,
				})
				continue
			}
			result.LLMVetoCalled++
			vetoTasks = append(vetoTasks, cctx)
			preLLMPass = append(preLLMPass, cctx)
		}
	}

	// Phase 2: Parallel LLM veto calls
	if len(vetoTasks) > 0 {
		var wg sync.WaitGroup
		for _, vt := range vetoTasks {
			wg.Add(1)
			go func(t *candidateCtx) {
				defer wg.Done()
				t.llmDecision, t.llmErr = s.llmClient.VetoRequest(ctx, t.ctxJSON)
			}(vt)
		}
		wg.Wait()
	}

	// Phase 3: Post-LLM processing — sequential for DB safety
	monitorStatus := cycleMonitorStatus
	for _, cctx := range preLLMPass {
		cand := cctx.cand
		llmDecision := cctx.llmDecision
		llmErr := cctx.llmErr
		ob := cctx.ob
		obErr := cctx.obErr
		marketPrice := cctx.marketPrice
		priceErr := cctx.priceErr
		candlesForBlocks := cctx.candlesForBlocks
		setupTF := cctx.setupTF

		if cctx.routeResult.Route == routing.RouteLLMVeto {
			if llmErr != nil {
				result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "LLM_ERROR"})
				continue
			}
			result.LLMCalls++
			s.trackLLMCall(ctx)
		}

		// Persist LLM decision with real candidate_id
		candID := candidateIDs[cand.Symbol]
		if candID == 0 {
			candID = -1
		}
		s.saveLLMDecision(ctx, llmDecision, candID, cycleID)

		if llmDecision.Decision == "BLOCK" {
			result.LLMReject++
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "LLM_BLOCK"})
			s.log.Info("candidate blocked by LLM", map[string]any{
				"symbol":   cand.Symbol,
				"decision": llmDecision.Decision,
				"reasons":  llmDecision.ReasonCodes,
			})
			fields := &DecisionLogFields{}
			fields.WithDataValidationResult("passed")
			if tc, ok := screenResult.TradeCandidates[cand.Symbol]; ok {
				fields.WithCandidate(tc)
			}
			if sr, ok := screenResult.ScoreResults[cand.Symbol]; ok {
				fields.WithScoreBreakdown(sr)
				fields.ScoringVersion = sr.ScoringVersion
			}
			if rs, ok := screenResult.RegimeSnapshots[cand.Symbol]; ok {
				fields.WithRegimeSnapshot(rs)
			}
			llmJSON := marshalLLMDecision(llmDecision)
			dl := fields.ToDomain(cycleID, modeStr, cand.Symbol, setupTF, len(candlesForBlocks), "REJECTED_LLM", llmJSON)
			s.saveDecisionLog(ctx, dl)
			s.trackCounterfactual(ctx, dl.DecisionID, cand)
			continue
		}

		// Risk validation with real market data
		if obErr != nil {
			s.log.Warn("orderbook unavailable", map[string]any{
				"symbol": cand.Symbol,
				"error":  obErr.Error(),
			})
			riskOut := risk.ValidateOutput{
				Approved:    false,
				ReasonCodes: []string{"MARKET_DATA_UNAVAILABLE"},
			}
			s.saveRiskDecision(ctx, riskOut, candID, cycleID)
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "RISK_REJECTED"})
			fields := buildBaseFields(screenResult, cand)
			dl := fields.ToDomain(cycleID, modeStr, cand.Symbol, setupTF, len(candlesForBlocks), "REJECTED_RISK", "MARKET_DATA_UNAVAILABLE")
			s.saveDecisionLog(ctx, dl)
			s.trackCounterfactual(ctx, dl.DecisionID, cand)
			continue
		}

		if priceErr != nil || marketPrice <= 0 || math.IsNaN(marketPrice) || math.IsInf(marketPrice, 0) {
			errMsg := ""
			if priceErr != nil {
				errMsg = priceErr.Error()
			}
			s.log.Warn("price unavailable", map[string]any{
				"symbol": cand.Symbol,
				"error":  errMsg,
				"price":  marketPrice,
			})
			riskOut := risk.ValidateOutput{
				Approved:    false,
				ReasonCodes: []string{"PRICE_UNAVAILABLE"},
			}
			s.saveRiskDecision(ctx, riskOut, candID, cycleID)
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "RISK_REJECTED"})
			fields := buildBaseFields(screenResult, cand)
			dl := fields.ToDomain(cycleID, modeStr, cand.Symbol, setupTF, len(candlesForBlocks), "REJECTED_RISK", "PRICE_UNAVAILABLE")
			s.saveDecisionLog(ctx, dl)
			s.trackCounterfactual(ctx, dl.DecisionID, cand)
			continue
		}

		riskInput := risk.ValidateInput{
			ProposedTrade: cand.ProposedTrade,
			LLMDecision:   llmDecision,
			AccountState:  monitorStatus.AccountState,
			MarketState: risk.MarketState{
				Symbol:      cand.Symbol,
				Price:       marketPrice,
				SpreadBps:   ob.SpreadBps,
				SlippageBps: ob.EstimatedSlippageBps,
				DepthRatio:  ob.DepthToPositionSizeRatio,
				LastUpdate:  ob.LastUpdate,
				Stale:       ob.Stale,
			},
			Portfolio: risk.PortfolioState{
				OpenPositions:         monitorStatus.OpenPositions,
				OpenOrders:            monitorStatus.OpenOrders,
				NewPositionsThisCycle: result.Executions,
				ConsecutiveLosses:     s.consecutiveLosses,
				CooldownUntil:        s.cooldownUntil,
				PerSymbolSLCooldown:   s.perSymbolSLCooldown,
			},
			BotState: domain.BotState{Running: true},
		}

		riskOutput := s.riskEng.Validate(riskInput)
		if !riskOutput.Approved {
			result.RiskRejected++
			s.saveRiskDecision(ctx, riskOutput, candID, cycleID)
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "RISK_REJECTED"})
			s.log.Info("candidate rejected by risk", map[string]any{
				"symbol":  cand.Symbol,
				"reasons": riskOutput.ReasonCodes,
			})
			fields := buildBaseFields(screenResult, cand)
			dl := fields.ToDomain(cycleID, modeStr, cand.Symbol, setupTF, len(candlesForBlocks), "REJECTED_RISK", joinReasons(riskOutput.ReasonCodes))
			s.saveDecisionLog(ctx, dl)
			s.trackCounterfactual(ctx, dl.DecisionID, cand)
			continue
		}

		// Phase 6: LLM Reviewer (runs on every approved candidate)
		reviewOutcome := s.runLLMReview(ctx, cand, screenResult, riskOutput, cycleID, setupTF)
		if reviewOutcome != nil {
			switch reviewOutcome.Action {
			case llm.ActionReject:
				result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "LLM_REVIEW_REJECTED"})
				s.log.Info("candidate rejected by LLM reviewer", map[string]any{
					"symbol":            cand.Symbol,
					"review_action":     reviewOutcome.Action,
					"review_confidence": reviewOutcome.Confidence,
					"review_quality":    reviewOutcome.SetupQuality,
					"review_reason":     reviewOutcome.ReasonSummary,
				})
				fields := buildBaseFields(screenResult, cand)
				fields.WithLLMReview(reviewOutcome)
				dl := fields.ToDomain(cycleID, modeStr, cand.Symbol, setupTF, len(candlesForBlocks), "REJECTED_LLM_REVIEW", reviewOutcome.ReasonSummary)
				s.saveDecisionLog(ctx, dl)
				s.trackCounterfactual(ctx, dl.DecisionID, cand)
				continue
			case llm.ActionReduceSize:
				s.log.Info("LLM reviewer reduced size", map[string]any{
					"symbol":        cand.Symbol,
					"multiplier":    reviewOutcome.SizeMultiplier,
					"review_reason": reviewOutcome.ReasonSummary,
				})
			case llm.ActionApproveRetestOnly:
				s.log.Info("LLM reviewer forced retest-only entry", map[string]any{
					"symbol":        cand.Symbol,
					"review_reason": reviewOutcome.ReasonSummary,
				})
			}
		}

		approvedForPortfolio = append(approvedForPortfolio, riskCandidate{
			candidate:     cand,
			decision:      llmDecision,
			output:        riskOutput,
			candidateID:   candID,
			monitorStatus: monitorStatus,
			marketPrice:   marketPrice,
			ob:            ob,
		})
	}

	// 6. Rank all risk-approved candidates before execution. This prevents
	// sequential candidate order from deciding portfolio allocation.
	sort.SliceStable(approvedForPortfolio, func(i, j int) bool {
		return approvedForPortfolio[i].candidate.CandidateScore > approvedForPortfolio[j].candidate.CandidateScore
	})
	maxNew := s.cfg.PortfolioRisk.MaxNewPositionsPerCycle
	if maxNew <= 0 {
		maxNew = len(approvedForPortfolio)
	}
	anyRanging := false
	for _, rs := range screenResult.RegimeSnapshots {
		if rs.IsRanging {
			anyRanging = true
			break
		}
	}
	if anyRanging {
		regimeCfg := s.cfg.MarketRegime.WithDefaults()
		maxNew = routing.ComputeReduceNewPositions(maxNew, 1, anyRanging, regimeCfg.ReduceNewPositionsWhenRanging)
	}
	remainingNew := maxNew - result.Executions
	if remainingNew < 0 {
		remainingNew = 0
	}

	for i, item := range approvedForPortfolio {
		cand := item.candidate
		riskOutput := item.output
		if i >= remainingNew {
			riskOutput.Approved = false
			riskOutput.PortfolioRejectReason = "PORTFOLIO_RISK_LIMIT"
			riskOutput.ReasonCodes = append(riskOutput.ReasonCodes, "PORTFOLIO_RISK_LIMIT")
			s.saveRiskDecision(ctx, riskOutput, item.candidateID, cycleID)
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "PORTFOLIO_RISK_LIMIT"})
			s.log.Info("candidate rejected by portfolio ranking", map[string]any{
				"symbol": cand.Symbol,
				"score":  cand.CandidateScore,
			})
			fields := buildBaseFields(screenResult, cand)
			dl := fields.ToDomain(cycleID, modeStr, cand.Symbol, setupTF, 0, "REJECTED_RISK", "PORTFOLIO_RISK_LIMIT")
			s.saveDecisionLog(ctx, dl)
			s.trackCounterfactual(ctx, dl.DecisionID, cand)
			continue
		}

		riskOutput.PortfolioRank = i + 1
		s.saveRiskDecision(ctx, riskOutput, item.candidateID, cycleID)

		// Phase 4: deterministic candidate risk validation → order plan
		tc, tcOk := screenResult.TradeCandidates[cand.Symbol]
		sr, srOk := screenResult.ScoreResults[cand.Symbol]
		rs, rsOk := screenResult.RegimeSnapshots[cand.Symbol]
		var phase4Result risk.RiskValidationResult
		if tcOk && srOk && rsOk {
			si, siOk := screenResult.SymbolInfos[cand.Symbol]
			if !siOk {
				si = domain.SymbolInfo{
					Symbol:      cand.Symbol,
					TickSize:    0.01,
					LotSize:     0.001,
					MinNotional: 10,
					MaxLeverage: 100,
				}
			}
			phase4Result = s.riskEng.ValidateCandidate(risk.CandidateRiskInput{
				Candidate:      tc,
				ScoreResult:    sr,
				RegimeSnapshot: rs,
				SymbolInfo:     si,
				AccountState: item.monitorStatus.AccountState,
				Portfolio: risk.PortfolioState{
					OpenPositions:         item.monitorStatus.OpenPositions,
					OpenOrders:            item.monitorStatus.OpenOrders,
					NewPositionsThisCycle: result.Executions,
					ConsecutiveLosses:     s.consecutiveLosses,
					CooldownUntil:        s.cooldownUntil,
					PerSymbolSLCooldown:   s.perSymbolSLCooldown,
				},
				BotState: domain.BotState{Running: true},
			})
		} else {
			phase4Result = risk.RiskValidationResult{
				CandidateID:      cand.Symbol,
				Approved:         false,
				RejectionReasons: []string{"MISSING_PHASE3_DATA"},
			}
		}
		if !phase4Result.Approved {
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "PHASE4_RISK_REJECTED"})
			s.log.Info("candidate rejected by Phase 4 risk engine", map[string]any{
				"symbol":  cand.Symbol,
				"reasons": phase4Result.RejectionReasons,
			})
			fields := buildBaseFields(screenResult, cand)
			fields.WithRiskValidation(phase4Result)
			dl := fields.ToDomain(cycleID, modeStr, cand.Symbol, setupTF, 0, "REJECTED_RISK", joinReasons(phase4Result.RejectionReasons))
			s.saveDecisionLog(ctx, dl)
			s.trackCounterfactual(ctx, dl.DecisionID, cand)
			continue
		}

		// Phase 4: execution safety checks
		safetyResult := s.safetyEng.EvaluateExecutionSafety(ctx, *phase4Result.OrderPlan, execution.MarketState{
			Symbol:          cand.Symbol,
			Price:           item.marketPrice,
			OrderBook:       item.ob,
			SpreadPct:       item.ob.SpreadBps / 100.0,
			SlippagePct:     item.ob.EstimatedSlippageBps / 100.0,
			ExchangeHealthy: true,
		}, execution.AccountState{
			OpenPositions: item.monitorStatus.OpenPositions,
			OpenOrders:    item.monitorStatus.OpenOrders,
		})
		if !safetyResult.Safe {
			result.SafetyRejected++
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "EXECUTION_SAFETY_FAILED"})
			s.log.Info("candidate blocked by execution safety", map[string]any{
				"symbol":             cand.Symbol,
				"failed_checks":      safetyResult.FailedChecks,
				"reasons":            safetyResult.Reasons,
				"recommended_action": safetyResult.RecommendedAction,
			})
			fields := buildBaseFields(screenResult, cand)
			fields.WithRiskValidation(phase4Result)
			fields.WithSafetyValidation(false, safetyResult.FailedChecks, safetyResult.Reasons)
			fields.WithOrderPlan(phase4Result.OrderPlan)
			dl := fields.ToDomain(cycleID, modeStr, cand.Symbol, setupTF, 0, "REJECTED_SAFETY", joinReasons(safetyResult.Reasons))
			s.saveDecisionLog(ctx, dl)
			s.trackCounterfactual(ctx, dl.DecisionID, cand)
			continue
		}

		// Phase 5: Route based on mode
		fields := buildBaseFields(screenResult, cand)
		fields.WithRiskValidation(phase4Result)
		fields.WithSafetyValidation(true, nil, nil)
		fields.WithOrderPlan(phase4Result.OrderPlan)
		fields.RiskConfigVersion = phase4Result.RiskConfigVersion

		if s.mode.IsShadow() {
			// Shadow mode: log only, no submission
			dl := fields.ToDomain(cycleID, modeStr, cand.Symbol, setupTF, 0, "EXECUTED_SHADOW", "shadow mode: order plan validated, not submitted")
			s.saveDecisionLog(ctx, dl)
			s.trackCounterfactual(ctx, dl.DecisionID, cand)
			result.Executions++
			s.log.Info("shadow mode: order plan validated", map[string]any{
				"symbol":    cand.Symbol,
				"side":      cand.Side,
				"plan":      phase4Result.OrderPlan,
				"modifiers": phase4Result.ModifiersApplied,
			})
		} else if s.mode.IsPaper() && s.paperSim != nil {
			// Paper mode: simulate fill
			dl := fields.ToDomain(cycleID, modeStr, cand.Symbol, setupTF, 0, "EXECUTED_PAPER", "paper mode: simulated fill")
			s.saveDecisionLog(ctx, dl)
			trade, err := s.paperSim.SimulateFill(ctx, dl.DecisionID, *phase4Result.OrderPlan)
			if err != nil {
				s.log.Error("paper simulator failed to open position", map[string]any{
					"symbol": cand.Symbol,
					"error":  err.Error(),
				})
				result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "PAPER_SIM_FAILED"})
				continue
			}
			s.trackCounterfactual(ctx, dl.DecisionID, cand)
			result.Executions++
			s.log.Info("paper mode: position opened", map[string]any{
				"symbol":   cand.Symbol,
				"side":     cand.Side,
				"trade_id": trade.PaperTradeID,
				"entry":    trade.EntryPrice,
				"sl":       trade.StopLoss,
				"tp":       trade.TakeProfit,
			})
		} else {
			// Default: log plan (Phase 4 behavior)
			execResult := s.executor.ExecutePlan(ctx, phase4Result, safetyResult)
			if execResult.Success {
				result.Executions++
				s.log.Info("order plan validated and logged", map[string]any{
					"symbol":    cand.Symbol,
					"side":      cand.Side,
					"plan":      phase4Result.OrderPlan,
					"modifiers": phase4Result.ModifiersApplied,
				})
				dl := fields.ToDomain(cycleID, modeStr, cand.Symbol, setupTF, 0, "EXECUTED_SHADOW", "order plan logged (no simulator)")
				s.saveDecisionLog(ctx, dl)
				s.trackCounterfactual(ctx, dl.DecisionID, cand)
			} else {
				result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "EXECUTION_FAILED"})
			}
		}

		// Alert on validated plan (non-paper fallback)
		if s.mode.IsShadow() || s.paperSim == nil {
			if s.alertSvc != nil {
				msg := fmt.Sprintf("Order plan validated: %s %s qty=%.4f entry=%.2f (Phase 4 log only)",
					cand.Symbol, cand.Side, phase4Result.OrderPlan.Qty, phase4Result.OrderPlan.EntryPrice)
				_ = s.alertSvc.Send(ctx, alert.AlertEvent{
					Type:      "order_plan_validated",
					Severity:  "info",
					Message:   msg,
					Symbol:    cand.Symbol,
					Timestamp: time.Now(),
				})
			}
		}
	}

	result.EndedAt = time.Now()
	result.ReasonCodes = append(result.ReasonCodes, "CYCLE_COMPLETE")

	s.log.Info("cycle complete", map[string]any{
		"cycle_id":            cycleID,
		"duration_ms":         result.EndedAt.Sub(result.StartedAt).Milliseconds(),
		"candidates":          result.Candidates,
		"llm_calls":           result.LLMCalls,
		"executions":          result.Executions,
		"skips":               len(result.Skips),
		"rejected_pre_llm":    result.RejectedPreLLM,
		"llm_skipped_hiscore": result.LLMSkippedHighScore,
		"llm_veto_calls":      result.LLMVetoCalled,
		"llm_approve":         result.LLMApprove,
		"llm_reject":          result.LLMReject,
		"llm_reduce_size":     result.LLMReduceSize,
		"risk_rejected":       result.RiskRejected,
		"safety_rejected":     result.SafetyRejected,
	})

	return result, nil
}

func latestCandleCloseTime(candles []domain.Candle, timeframe string) time.Time {
	if len(candles) == 0 {
		return time.Time{}
	}
	interval, err := timeframeDuration(timeframe)
	if err != nil {
		return time.Time{}
	}
	latest := candles[len(candles)-1]
	return time.UnixMilli(latest.OpenTime).UTC().Add(interval)
}

func timeframeDuration(timeframe string) (time.Duration, error) {
	switch timeframe {
	case "1m":
		return time.Minute, nil
	case "5m":
		return 5 * time.Minute, nil
	case "15m":
		return 15 * time.Minute, nil
	case "30m":
		return 30 * time.Minute, nil
	case "1H":
		return time.Hour, nil
	case "4H":
		return 4 * time.Hour, nil
	case "1d":
		return 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported timeframe %q", timeframe)
	}
}

func (s *Scheduler) computeBTC5mReturn(ctx context.Context) float64 {
	candles, err := s.md.GetCandles(ctx, "BTCUSDT", "5m", 2)
	if err != nil || len(candles) < 2 {
		if s.btc5mStalenessTracker != nil {
			s.btc5mStalenessTracker.RecordFailure()
		}
		s.log.Warn("btc 5m data staleness: failure", map[string]any{
			"stale_counter": s.btc5mStalenessCounter(),
		})
		return 0
	}
	if s.btc5mStalenessTracker != nil {
		s.btc5mStalenessTracker.RecordSuccess()
	}
	prev := candles[len(candles)-2].Close
	last := candles[len(candles)-1].Close
	if prev <= 0 {
		return 0
	}
	return ((last - prev) / prev) * 100
}

func (s *Scheduler) btc5mStalenessCounter() int {
	if s.btc5mStalenessTracker == nil {
		return 0
	}
	return s.btc5mStalenessTracker.Counter()
}

func toPositionState(positions []domain.Position) []risk.PositionState {
	out := make([]risk.PositionState, 0, len(positions))
	for _, p := range positions {
		out = append(out, risk.PositionState{
			Symbol: p.Symbol,
			Side:   p.Side,
			Size:   p.Size,
		})
	}
	return out
}

// updatePostTradeState syncs post-trade state (consecutive losses, cooldown,
// per-symbol blackout) from the paper simulator and recent trade events.
func (s *Scheduler) updatePostTradeState(ctx context.Context) {
	if s.paperSim == nil {
		return
	}

	slCooldownCfg := s.cfg.PortfolioRisk.PerSymbolSLCooldown
	lossCooldownCfg := s.cfg.PortfolioRisk.CooldownAfterLosses

	// Read consecutive losses from simulator (single source of truth, DB-persisted).
	s.consecutiveLosses = s.paperSim.ConsecutiveLosses()

	// Check cooldown thresholds based on current count.
	if lossCooldownCfg.Enabled && s.consecutiveLosses >= lossCooldownCfg.ConsecutiveLosses && s.cooldownUntil == nil {
		cooldownDur := time.Duration(lossCooldownCfg.CooldownMinutes) * time.Minute
		until := time.Now().Add(cooldownDur)
		s.cooldownUntil = &until
		s.log.Info("consecutive loss cooldown activated", map[string]any{
			"consecutive_losses": s.consecutiveLosses,
			"cooldown_until":     until.Format(time.RFC3339),
		})
	}
	if s.consecutiveLosses == 0 {
		s.cooldownUntil = nil
	}

	// Per-symbol SL blackout from recent closed trades.
	if slCooldownCfg.Enabled {
		recentTrades, err := s.paperSim.GetRecentClosedTrades(ctx, time.Now().Add(-1*time.Hour))
		if err != nil {
			s.log.Warn("failed to query recent trades for symbol cooldown", map[string]any{"error": err.Error()})
			return
		}
		cooldownDur := time.Duration(slCooldownCfg.CooldownMinutes) * time.Minute
		if s.perSymbolSLCooldown == nil {
			s.perSymbolSLCooldown = make(map[string]time.Time)
		}
		for _, trade := range recentTrades {
			if trade.ExitReason != "sl" || trade.ClosedAt == nil {
				continue
			}
			// Only set if not already covered.
			if until, ok := s.perSymbolSLCooldown[trade.Symbol]; !ok || time.Now().After(until) {
				s.perSymbolSLCooldown[trade.Symbol] = time.Now().Add(cooldownDur)
				s.log.Info("per-symbol SL cooldown set", map[string]any{
					"symbol":         trade.Symbol,
					"cooldown_until": s.perSymbolSLCooldown[trade.Symbol].Format(time.RFC3339),
				})
			}
		}
	}

	// Clean expired per-symbol cooldown entries.
	now := time.Now()
	for sym, until := range s.perSymbolSLCooldown {
		if !now.Before(until) {
			delete(s.perSymbolSLCooldown, sym)
		}
	}
}

// Run starts the main loop with time-based scheduling.
func (s *Scheduler) Run(ctx context.Context) error {
	interval := time.Duration(s.cfg.App.CycleIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	buffer := 5 * time.Second

	s.log.Info("scheduler started", map[string]any{
		"interval": interval.String(),
	})

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run immediately on start
	if _, err := s.RunOnce(ctx); err != nil {
		s.log.Error("initial cycle failed", map[string]any{"error": err.Error()})
	}

	for {
		select {
		case <-ctx.Done():
			s.log.Info("scheduler stopped", nil)
			return nil
		case <-ticker.C:
			// Add buffer to avoid racing with candle close
			select {
			case <-ctx.Done():
				s.log.Info("scheduler stopped", nil)
				return nil
			case <-time.After(buffer):
			}
			if _, err := s.RunOnce(ctx); err != nil {
				s.log.Error("cycle failed", map[string]any{"error": err.Error()})
			}
		}
	}
}

func (s *Scheduler) isHalted() bool {
	if s == nil {
		return true
	}
	return s.monitor.IsHalted()
}

func (s *Scheduler) checkDailyReset() {
	today := time.Now().UTC().Format("2006-01-02")
	if s.lastDailyResetDay == today {
		return
	}
	s.lastDailyResetDay = today
	s.monitor.ResetDaily()
	if s.llmReviewer != nil && s.llmReviewer.IsEnabled() {
		s.llmReviewer.ResetDailyCost()
	}
	s.log.Info("daily reset triggered", map[string]any{"date": today})
}

func (s *Scheduler) refreshUniverseIfNeeded(ctx context.Context) error {
	return s.screener.RefreshUniverse(ctx)
}

func (s *Scheduler) persistCandidates(ctx context.Context, eligible, nonEligible []domain.Candidate, cycleID string) map[string]int64 {
	ids := make(map[string]int64)
	if s.candidateRepo == nil {
		return ids
	}
	all := make([]domain.Candidate, 0, len(eligible)+len(nonEligible))
	all = append(all, eligible...)
	all = append(all, nonEligible...)
	for _, c := range all {
		id, err := s.candidateRepo.Insert(ctx, c)
		if err != nil {
			s.log.Error("failed to save candidate", map[string]any{"symbol": c.Symbol, "error": err.Error()})
			continue
		}
		ids[c.Symbol] = id
	}
	return ids
}

func (s *Scheduler) saveLLMDecision(ctx context.Context, d domain.LLMDecision, candidateID int64, cycleID string) {
	if s.llmDecisionRepo == nil {
		return
	}
	if _, err := s.llmDecisionRepo.Insert(ctx, d, candidateID, cycleID); err != nil {
		s.log.Error("failed to save LLM decision", map[string]any{"error": err.Error()})
	}
}

func (s *Scheduler) saveRiskDecision(ctx context.Context, d risk.ValidateOutput, candidateID int64, cycleID string) {
	if s.riskDecisionRepo == nil {
		return
	}
	decision := domain.RiskDecision{
		Approved:              d.Approved,
		FinalPositionNotional: d.FinalPositionNotional,
		RequiredMargin:        d.RequiredMargin,
		EstimatedLoss:         d.EstimatedLoss,
		ReasonCodes:           d.ReasonCodes,
		PortfolioRank:         d.PortfolioRank,
		PortfolioRejectReason: d.PortfolioRejectReason,
	}
	if _, err := s.riskDecisionRepo.Insert(ctx, decision, candidateID, cycleID); err != nil {
		s.log.Error("failed to save risk decision", map[string]any{"error": err.Error()})
	}
}

func (s *Scheduler) insertCycleRunning(ctx context.Context, cycleID string, startedAt time.Time) {
	if s.cycleRepo == nil {
		return
	}
	cycle := domain.Cycle{
		CycleID:   cycleID,
		StartedAt: startedAt,
		Status:    "running",
	}
	if _, err := s.cycleRepo.Insert(ctx, cycle); err != nil {
		s.log.Error("failed to insert running cycle", map[string]any{"error": err.Error()})
	}
}

func (s *Scheduler) updateCycleFinal(result *CycleResult, runErr error) {
	if s.cycleRepo == nil {
		return
	}
	status := "completed"
	if runErr != nil {
		status = "failed"
	} else if containsAny(result.ReasonCodes, []string{"BOT_HALTED", "NO_CANDIDATE"}) {
		status = "skipped"
	}
	cycle := domain.Cycle{
		CycleID:     result.CycleID,
		StartedAt:   result.StartedAt,
		Status:      status,
		ReasonCodes: result.ReasonCodes,
	}
	if !result.EndedAt.IsZero() {
		cycle.EndedAt = &result.EndedAt
	}
	if err := s.cycleRepo.Update(context.Background(), cycle); err != nil {
		// Fallback to insert if update fails (e.g. cycle row not found).
		if _, insErr := s.cycleRepo.Insert(context.Background(), cycle); insErr != nil {
			s.log.Error("failed to save cycle", map[string]any{"error": insErr.Error()})
		}
	}
}

func containsAny(haystack []string, needles []string) bool {
	for _, h := range haystack {
		for _, n := range needles {
			if h == n {
				return true
			}
		}
	}
	return false
}

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}

// RefreshUniverse exposes universe refresh for CLI.
func (s *Scheduler) RefreshUniverse(ctx context.Context) error {
	return s.screener.RefreshUniverse(ctx)
}

// applyLLMCaps enforces the strictest positive cap among:
//   - hard_cap_candidates_per_cycle
//   - max_calls_per_cycle
//   - remaining max_calls_per_day
//
// 0 means unlimited for each field.
// Candidates are sorted by candidate_score descending before truncation.
func (s *Scheduler) applyLLMCaps(candidates []domain.Candidate) ([]domain.Candidate, []SkipReason) {
	if len(candidates) == 0 {
		return candidates, nil
	}

	// Ensure daily counter is on the correct date.
	today := time.Now().Format("2006-01-02")
	if s.llmCallsDate != today {
		s.llmCallsDate = today
		s.llmCallsToday = 0
	}

	// Check daily cap first — if exhausted, block everything.
	if s.cfg.LLMRouting.MaxCallsPerDay > 0 && s.llmCallsToday >= s.cfg.LLMRouting.MaxCallsPerDay {
		var skips []SkipReason
		for _, c := range candidates {
			skips = append(skips, SkipReason{Symbol: c.Symbol, Reason: "LLM_CALL_CAP_PER_DAY"})
		}
		s.log.Warn("LLM daily call cap reached", map[string]any{
			"llm_calls_today": s.llmCallsToday,
			"max_calls_day":   s.cfg.LLMRouting.MaxCallsPerDay,
		})
		return nil, skips
	}

	// Determine the strictest positive effective cap and keep the reason tied to
	// the cap that actually limited the candidate set.
	effectiveCap := 0
	capReason := "LLM_HARD_CAP"

	setCap := func(candidateCap int, reason string) {
		if candidateCap > 0 {
			if effectiveCap == 0 || candidateCap < effectiveCap {
				effectiveCap = candidateCap
				capReason = reason
			}
		}
	}

	setCap(s.cfg.LLMRouting.HardCapCandidatesPerCycle, "LLM_HARD_CAP")
	setCap(s.cfg.LLMRouting.MaxCallsPerCycle, "LLM_CALL_CAP_PER_CYCLE")

	if s.cfg.LLMRouting.MaxCallsPerDay > 0 {
		remainingToday := s.cfg.LLMRouting.MaxCallsPerDay - s.llmCallsToday
		if remainingToday > 0 {
			setCap(remainingToday, "LLM_CALL_CAP_PER_DAY")
		} else {
			// Should have been caught above, but guard anyway.
			var skips []SkipReason
			for _, c := range candidates {
				skips = append(skips, SkipReason{Symbol: c.Symbol, Reason: "LLM_CALL_CAP_PER_DAY"})
			}
			return nil, skips
		}
	}

	// No cap configured — return all candidates.
	if effectiveCap <= 0 {
		return candidates, nil
	}

	// Sort by candidate_score descending (bubble sort is fine for small N).
	sorted := make([]domain.Candidate, len(candidates))
	copy(sorted, candidates)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].CandidateScore > sorted[i].CandidateScore {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	if len(sorted) <= effectiveCap {
		return sorted, nil
	}

	kept := sorted[:effectiveCap]
	dropped := sorted[effectiveCap:]
	var skips []SkipReason
	for _, c := range dropped {
		skips = append(skips, SkipReason{Symbol: c.Symbol, Reason: capReason})
	}

	s.log.Info("LLM cap applied", map[string]any{
		"eligible_before": len(candidates),
		"eligible_after":  len(kept),
		"effective_cap":   effectiveCap,
		"hard_cap":        s.cfg.LLMRouting.HardCapCandidatesPerCycle,
		"call_cap":        s.cfg.LLMRouting.MaxCallsPerCycle,
		"daily_remaining": s.cfg.LLMRouting.MaxCallsPerDay - s.llmCallsToday,
	})

	return kept, skips
}

func (s *Scheduler) loadLLMUsage(ctx context.Context) {
	today := time.Now().Format("2006-01-02")
	if s.llmUsageRepo == nil {
		if s.llmCallsDate != today {
			s.llmCallsDate = today
			s.llmCallsToday = 0
		}
		return
	}

	usageDate, err := time.Parse("2006-01-02", today)
	if err != nil {
		return
	}
	state, err := s.llmUsageRepo.Get(ctx, usageDate)
	if err != nil {
		s.log.Warn("failed to load LLM usage state", map[string]any{"error": err.Error()})
		if s.llmCallsDate != today {
			s.llmCallsDate = today
			s.llmCallsToday = 0
		}
		return
	}
	s.llmCallsDate = today
	if state == nil {
		s.llmCallsToday = 0
		return
	}
	s.llmCallsToday = state.Calls
}

func (s *Scheduler) trackLLMCall(ctx context.Context) {
	today := time.Now().Format("2006-01-02")
	if s.llmCallsDate != today {
		s.llmCallsDate = today
		s.llmCallsToday = 0
	}
	s.llmCallsToday++
	if s.llmUsageRepo == nil {
		return
	}
	usageDate, err := time.Parse("2006-01-02", today)
	if err != nil {
		return
	}
	cost := 0.0
	if s.llmClient != nil {
		cost = s.llmClient.DailyCost()
	}
	if err := s.llmUsageRepo.IncrementCalls(ctx, usageDate, 1, cost); err != nil {
		s.log.Error("failed to persist LLM usage", map[string]any{"error": err.Error()})
	}
}

// --- Phase 5 helpers ---

func (s *Scheduler) saveDecisionLog(ctx context.Context, dl domain.DecisionLog) {
	if s.decisionLogRepo == nil {
		return
	}
	if err := s.decisionLogRepo.Insert(ctx, dl); err != nil {
		s.log.Error("failed to save decision log", map[string]any{
			"decision_id":  dl.DecisionID,
			"symbol":       dl.Symbol,
			"final_action": dl.FinalAction,
			"error":        err.Error(),
		})
	}
}

func (s *Scheduler) trackCounterfactual(ctx context.Context, decisionID string, cand domain.Candidate) {
	if s.counterfactualTracker == nil {
		return
	}
	tps := []float64{cand.ProposedTakeProfit}
	if err := s.counterfactualTracker.TrackCandidate(ctx, decisionID, cand.Symbol, cand.Side, cand.ProposedEntry, cand.ProposedStopLoss, tps); err != nil {
		s.log.Warn("failed to track counterfactual", map[string]any{
			"decision_id": decisionID,
			"symbol":      cand.Symbol,
			"error":       err.Error(),
		})
	}
}

func buildBaseFields(sr *screener.ScreenResult, cand domain.Candidate) *DecisionLogFields {
	fields := &DecisionLogFields{}
	fields.WithDataValidationResult("passed")
	if tc, ok := sr.TradeCandidates[cand.Symbol]; ok {
		fields.WithCandidate(tc)
	}
	if s, ok := sr.ScoreResults[cand.Symbol]; ok {
		fields.WithScoreBreakdown(s)
		fields.ScoringVersion = s.ScoringVersion
	}
	if rs, ok := sr.RegimeSnapshots[cand.Symbol]; ok {
		fields.WithRegimeSnapshot(rs)
	}
	return fields
}

func buildTradeCandidateFromDomain(c domain.Candidate) strategy.TradeCandidate {
	return strategy.TradeCandidate{
		Symbol:     c.Symbol,
		Side:       c.Side,
		EntryPrice: c.ProposedEntry,
		StopLoss:   c.ProposedStopLoss,
	}
}

func marshalLLMDecision(d domain.LLMDecision) string {
	return fmt.Sprintf("LLM_BLOCK reason=%v confidence=%.2f", d.ReasonCodes, d.Confidence)
}

func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	result := reasons[0]
	for i := 1; i < len(reasons); i++ {
		result += "; " + reasons[i]
	}
	return result
}

// runLLMReview runs the Phase 6 LLM Reviewer on a risk-approved candidate.
// Returns the review outcome, or nil if reviewer is disabled.
func (s *Scheduler) runLLMReview(
	ctx context.Context,
	cand domain.Candidate,
	screenResult *screener.ScreenResult,
	riskOutput risk.ValidateOutput,
	cycleID, setupTF string,
) *llm.ReviewOutcome {
	if s.llmReviewer == nil || !s.llmReviewer.IsEnabled() {
		return nil
	}

	// Build indicator summary for the reviewer
	rs, hasRegime := screenResult.RegimeSnapshots[cand.Symbol]
	sr, hasScore := screenResult.ScoreResults[cand.Symbol]

	input := llm.ReviewCandidateInput{
		Symbol:       cand.Symbol,
		Side:         string(cand.Side),
		StrategyName: cand.SetupType,
		Timeframe:    setupTF,
		EntryType:    string(cand.EntryType),
		EntryPrice:   cand.ProposedEntry,
		StopLoss:     cand.ProposedStopLoss,
		TakeProfits: []llm.ReviewTP{
			{Price: cand.ProposedTakeProfit, SizePct: 100},
		},
		RRRatio:          cand.RR,
		ModifiersApplied: riskOutput.ReasonCodes,
		Qty:              riskOutput.FinalPositionNotional / cand.ProposedEntry,
		Leverage:         s.effectiveLeverage(riskOutput),
		RiskAmountUSD:    riskOutput.EstimatedLoss,
		RiskPct:          0.5,
	}

	if hasRegime {
		input.BTCFiltersTrig = rs.BTCFiltersTriggered
		input.BTCDAvailable = rs.BTCDAvailable
		input.BTCDTrend = classifyBTCDTrend(rs)
		input.RelativeStrength = rs.RelativeStrength.RS4H
		input.RSClass = rs.RelativeStrength.Classification
	}

	if hasScore {
		input.TotalScore = sr.ScoreTotal
		input.ScoringVersion = sr.ScoringVersion
	}

	outcome, err := s.llmReviewer.Review(ctx, input)
	if err != nil {
		s.log.Error("LLM reviewer call failed", map[string]any{
			"symbol": cand.Symbol,
			"error":  err.Error(),
		})
		return nil
	}

	return outcome
}

func classifyBTCDTrend(rs regime.MarketRegimeSnapshot) string {
	for _, f := range rs.BTCDFiltersTriggered {
		if f == "BTCDominanceRisingFast" {
			return "rising"
		}
		if f == "BTCDominanceFalling" {
			return "falling"
		}
	}
	return "stable"
}
