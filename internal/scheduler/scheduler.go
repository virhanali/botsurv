package scheduler

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/virhan/botsurv/internal/alert"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/db"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/executor"
	"github.com/virhan/botsurv/internal/llm"
	"github.com/virhan/botsurv/internal/logger"
	"github.com/virhan/botsurv/internal/monitor"
	"github.com/virhan/botsurv/internal/risk"
	"github.com/virhan/botsurv/internal/screener"
)

// Scheduler orchestrates the full trading cycle.
type Scheduler struct {
	cfg       app.UserConfig
	screener  *screener.Screener
	llmClient llm.Client
	riskEng   *risk.Engine
	executor  *executor.Executor
	monitor   *monitor.Monitor
	md        screener.MarketDataProvider
	log       *logger.Logger

	mu       sync.Mutex
	running  bool
	cycleSeq int

	alertSvc         alert.Service
	llmDecisionRepo  db.LLMDecisionRepository
	riskDecisionRepo db.RiskDecisionRepository
	llmUsageRepo     db.LLMUsageRepository
	cycleRepo        db.CycleRepository
	candidateRepo    db.CandidateRepository

	// LLM daily call tracking. Persisted when llmUsageRepo is wired.
	llmCallsToday int
	llmCallsDate  string // YYYY-MM-DD
}

// NewScheduler creates a new Scheduler.
func NewScheduler(
	cfg app.UserConfig,
	screener *screener.Screener,
	llmClient llm.Client,
	riskEng *risk.Engine,
	executor *executor.Executor,
	monitor *monitor.Monitor,
	md screener.MarketDataProvider,
	log *logger.Logger,
) *Scheduler {
	return &Scheduler{
		cfg:       cfg,
		screener:  screener,
		llmClient: llmClient,
		riskEng:   riskEng,
		executor:  executor,
		monitor:   monitor,
		md:        md,
		log:       log,
	}
}

// SetAlertService sets the alert service for sending notifications.
func (s *Scheduler) SetAlertService(svc alert.Service) { s.alertSvc = svc }

// SetLLMDecisionRepo sets the LLM decision repository for persistence.
func (s *Scheduler) SetLLMDecisionRepo(repo db.LLMDecisionRepository) { s.llmDecisionRepo = repo }

// SetRiskDecisionRepo sets the risk decision repository for persistence.
func (s *Scheduler) SetRiskDecisionRepo(repo db.RiskDecisionRepository) { s.riskDecisionRepo = repo }

// SetLLMUsageRepo sets the daily LLM usage repository for persistence.
func (s *Scheduler) SetLLMUsageRepo(repo db.LLMUsageRepository) { s.llmUsageRepo = repo }

// SetCycleRepo sets the cycle repository for persistence.
func (s *Scheduler) SetCycleRepo(repo db.CycleRepository) { s.cycleRepo = repo }

// SetCandidateRepo sets the candidate repository for persistence.
func (s *Scheduler) SetCandidateRepo(repo db.CandidateRepository) { s.candidateRepo = repo }

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
}

type SkipReason struct {
	Symbol string
	Reason string
}

type riskCandidate struct {
	candidate   domain.Candidate
	decision    domain.LLMDecision
	output      risk.ValidateOutput
	candidateID int64
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

	result.Candidates = len(screenResult.Candidates)

	// Check existing positions for SL/TP before new entries.
	s.monitor.CheckAllPositions(ctx)

	// Persist all generated candidates and track DB IDs for LLM decisions.
	candidateIDs := s.persistCandidates(ctx, screenResult.Candidates, screenResult.NonEligible, cycleID)

	if len(screenResult.Candidates) == 0 {
		result.ReasonCodes = append(result.ReasonCodes, "NO_CANDIDATE")
		result.EndedAt = time.Now()
		s.log.Info("cycle complete: no candidates", map[string]any{"cycle_id": cycleID})
		return result, nil
	}

	// 4. Apply LLM call caps.
	s.loadLLMUsage(ctx)
	cappedCandidates, skippedByCap := s.applyLLMCaps(screenResult.Candidates)
	for _, sk := range skippedByCap {
		result.Skips = append(result.Skips, sk)
	}

	// 5. For each capped eligible candidate: LLM veto -> risk validation.
	var approvedForPortfolio []riskCandidate
	for _, cand := range cappedCandidates {
		ctxJSON, ok := screenResult.LLMContexts[cand.Symbol]
		if !ok {
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "NO_CONTEXT"})
			continue
		}

		// Per-cycle call cap check
		if s.cfg.LLMRouting.MaxCallsPerCycle > 0 && result.LLMCalls >= s.cfg.LLMRouting.MaxCallsPerCycle {
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "LLM_CALL_CAP_PER_CYCLE"})
			s.log.Info("candidate skipped: LLM call cap per cycle reached", map[string]any{
				"symbol":    cand.Symbol,
				"llm_calls": result.LLMCalls,
				"max_calls": s.cfg.LLMRouting.MaxCallsPerCycle,
			})
			continue
		}

		// LLM veto
		llmDecision, err := s.llmClient.VetoRequest(ctx, ctxJSON)
		if err != nil {
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "LLM_ERROR"})
			continue
		}
		result.LLMCalls++
		s.trackLLMCall(ctx)

		// Persist LLM decision with real candidate_id
		candID := candidateIDs[cand.Symbol]
		if candID == 0 {
			candID = -1
		}
		s.saveLLMDecision(ctx, llmDecision, candID, cycleID)

		if llmDecision.Decision == "BLOCK" {
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "LLM_BLOCK"})
			s.log.Info("candidate blocked by LLM", map[string]any{
				"symbol":   cand.Symbol,
				"decision": llmDecision.Decision,
				"reasons":  llmDecision.ReasonCodes,
			})
			continue
		}

		// Risk validation with real market data
		monitorStatus := s.monitor.Status(ctx)
		targetNotional := s.cfg.ComputeTargetNotional()
		ob, _ := s.md.GetOrderBookSummary(ctx, cand.Symbol, targetNotional, string(cand.Side))
		marketPrice, _ := s.md.GetLatestPrice(ctx, cand.Symbol)

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
			},
			BotState: domain.BotState{Running: true},
		}

		riskOutput := s.riskEng.Validate(riskInput)
		if !riskOutput.Approved {
			s.saveRiskDecision(ctx, riskOutput, candID, cycleID)
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "RISK_REJECTED"})
			s.log.Info("candidate rejected by risk", map[string]any{
				"symbol":  cand.Symbol,
				"reasons": riskOutput.ReasonCodes,
			})
			continue
		}

		approvedForPortfolio = append(approvedForPortfolio, riskCandidate{
			candidate:   cand,
			decision:    llmDecision,
			output:      riskOutput,
			candidateID: candID,
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
	remainingNew := maxNew - result.Executions
	if remainingNew < 0 {
		remainingNew = 0
	}

	for i, item := range approvedForPortfolio {
		cand := item.candidate
		llmDecision := item.decision
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
			continue
		}

		riskOutput.PortfolioRank = i + 1
		s.saveRiskDecision(ctx, riskOutput, item.candidateID, cycleID)

		// Execute only after final portfolio selection.
		execResult := s.executor.Execute(ctx, cand, llmDecision, riskOutput)
		if execResult.Success {
			result.Executions++
			s.log.Info("trade executed", map[string]any{
				"symbol":   cand.Symbol,
				"side":     cand.Side,
				"order_id": execResult.OrderID,
			})

			// Alert on trade executed
			if s.alertSvc != nil {
				msg := fmt.Sprintf("Trade executed: %s %s at proposed entry", cand.Symbol, cand.Side)
				if execResult.OrderID != "" {
					msg = fmt.Sprintf("Trade executed: %s %s [%s]", cand.Symbol, cand.Side, execResult.OrderID)
				}
				_ = s.alertSvc.Send(ctx, alert.AlertEvent{
					Type:      "trade_executed",
					Severity:  "info",
					Message:   msg,
					Symbol:    cand.Symbol,
					Timestamp: time.Now(),
				})
			}
		} else {
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "EXECUTION_FAILED"})
		}
	}

	result.EndedAt = time.Now()
	result.ReasonCodes = append(result.ReasonCodes, "CYCLE_COMPLETE")

	s.log.Info("cycle complete", map[string]any{
		"cycle_id":    cycleID,
		"duration_ms": result.EndedAt.Sub(result.StartedAt).Milliseconds(),
		"candidates":  result.Candidates,
		"llm_calls":   result.LLMCalls,
		"executions":  result.Executions,
		"skips":       len(result.Skips),
	})

	return result, nil
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
			return ctx.Err()
		case <-ticker.C:
			// Add buffer to avoid racing with candle close
			time.Sleep(buffer)
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
	if err := s.llmUsageRepo.IncrementCalls(ctx, usageDate, 1); err != nil {
		s.log.Error("failed to persist LLM usage", map[string]any{"error": err.Error()})
	}
}
