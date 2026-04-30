package scheduler

import (
	"context"
	"fmt"
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

	alertSvc        alert.Service
	llmDecisionRepo db.LLMDecisionRepository
	cycleRepo       db.CycleRepository
	candidateRepo   db.CandidateRepository
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

	// 4. For each eligible candidate: LLM veto -> risk -> execute
	for _, cand := range screenResult.Candidates {
		ctxJSON, ok := screenResult.LLMContexts[cand.Symbol]
		if !ok {
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "NO_CONTEXT"})
			continue
		}

		// LLM veto
		llmDecision, err := s.llmClient.VetoRequest(ctx, ctxJSON)
		if err != nil {
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "LLM_ERROR"})
			continue
		}
		result.LLMCalls++

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
		ob, _ := s.md.GetOrderBookSummary(ctx, cand.Symbol, 0, "")
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
			result.Skips = append(result.Skips, SkipReason{Symbol: cand.Symbol, Reason: "RISK_REJECTED"})
			s.log.Info("candidate rejected by risk", map[string]any{
				"symbol":  cand.Symbol,
				"reasons": riskOutput.ReasonCodes,
			})
			continue
		}

		// Execute
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
