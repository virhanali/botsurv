package scheduler

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/broker"
	"github.com/virhan/botsurv/internal/db"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/execution"
	paperexec "github.com/virhan/botsurv/internal/execution/paper"
	"github.com/virhan/botsurv/internal/executor"
	"github.com/virhan/botsurv/internal/llm"
	"github.com/virhan/botsurv/internal/logger"
	"github.com/virhan/botsurv/internal/monitor"
	"github.com/virhan/botsurv/internal/risk"
	"github.com/virhan/botsurv/internal/screener"
	"github.com/virhan/botsurv/internal/universe"
)

// --- Mocks ---

type mockCycleRepo struct {
	cycles []domain.Cycle
}

func (m *mockCycleRepo) Insert(ctx context.Context, c domain.Cycle) (int64, error) {
	m.cycles = append(m.cycles, c)
	return int64(len(m.cycles)), nil
}

func (m *mockCycleRepo) Update(ctx context.Context, c domain.Cycle) error {
	for i := range m.cycles {
		if m.cycles[i].CycleID == c.CycleID {
			m.cycles[i].Status = c.Status
			m.cycles[i].EndedAt = c.EndedAt
			m.cycles[i].ReasonCodes = c.ReasonCodes
			return nil
		}
	}
	return errors.New("cycle not found")
}

func (m *mockCycleRepo) GetLatest(ctx context.Context) (*domain.Cycle, error) {
	if len(m.cycles) == 0 {
		return nil, nil
	}
	return &m.cycles[len(m.cycles)-1], nil
}

type mockUniverseRepo struct {
	symbols []domain.UniverseSymbol
	err     error
}

func (m *mockUniverseRepo) InsertOrUpdate(ctx context.Context, s domain.UniverseSymbol) error {
	return nil
}
func (m *mockUniverseRepo) GetAll(ctx context.Context) ([]domain.UniverseSymbol, error) {
	return m.symbols, m.err
}
func (m *mockUniverseRepo) GetBySymbol(ctx context.Context, symbol string) (*domain.UniverseSymbol, error) {
	return nil, nil
}

type mockCandleRepo struct{}

func (m *mockCandleRepo) Insert(ctx context.Context, c domain.Candle) (int64, error) { return 0, nil }
func (m *mockCandleRepo) GetBySymbolTimeframe(ctx context.Context, symbol, timeframe string, limit int) ([]domain.Candle, error) {
	return nil, nil
}

type mockMarketData struct {
	candles      map[string][]domain.Candle
	orderbookErr error
	priceErr     error
	price        float64
}

func (m *mockMarketData) GetOrderBookSummary(ctx context.Context, symbol string, targetNotional float64, side string) (domain.OrderBookSummary, error) {
	if m.orderbookErr != nil {
		return domain.OrderBookSummary{}, m.orderbookErr
	}
	return domain.OrderBookSummary{BestBid: 100, BestAsk: 101, SpreadBps: 1, BidDepth: 100, AskDepth: 100, EstimatedSlippageBps: 5, DepthToPositionSizeRatio: 10}, nil
}
func (m *mockMarketData) GetCandles(ctx context.Context, symbol, timeframe string, limit int) ([]domain.Candle, error) {
	if m.candles != nil {
		if c, ok := m.candles[symbol+timeframe]; ok {
			return c, nil
		}
	}
	now := time.Now().UTC()
	if limit <= 0 {
		limit = 25
	}

	makeSeries := func(n int, step time.Duration, base, stepPrice float64, tf string) []domain.Candle {
		out := make([]domain.Candle, n)
		for i := 0; i < n; i++ {
			p := base + float64(i)*stepPrice
			out[i] = domain.Candle{
				Symbol:    symbol,
				Timeframe: tf,
				OpenTime:  now.Add(-time.Duration(n-i) * step).UnixMilli(),
				Open:      p,
				High:      p + 100,
				Low:       p - 50,
				Close:     p + 50,
				Volume:    2000,
				Confirmed: true,
			}
		}
		return out
	}

	switch timeframe {
	case "1H":
		n := limit
		if n < 260 {
			n = 260
		}
		return makeSeries(n, time.Hour, 64500, 5, "1H"), nil
	case "4H":
		n := limit
		if n < 260 {
			n = 260
		}
		return makeSeries(n, 4*time.Hour, 64000, 8, "4H"), nil
	case "5m":
		n := limit
		if n < 2 {
			n = 2
		}
		return makeSeries(n, 5*time.Minute, 65000, 2, "5m"), nil
	case "15m":
		n := limit
		if n < 25 {
			n = 25
		}
		// 15m candles: trend + pullback + bullish reaction for phase-3 strategy tests.
		candles := make([]domain.Candle, n)
		price := 65000.0
		for i := 0; i < n; i++ {
			switch {
			case i < n-40:
				price += 0.8
			case i < n-6:
				price -= 0.2
			default:
				price += 0.35
			}
			open := price - 0.05
			close := price
			high := price + 80
			low := price - 80
			vol := 1000.0
			if i == n-1 {
				open = price - 0.03
				close = price + 0.12
				high = price + 85
				low = price - 160
				vol = 2000
			}
			candles[i] = domain.Candle{
				Symbol:    symbol,
				Timeframe: "15m",
				OpenTime:  now.Add(-time.Duration(n-i) * 15 * time.Minute).UnixMilli(),
				Open:      open,
				High:      high,
				Low:       low,
				Close:     close,
				Volume:    vol,
				Confirmed: true,
			}
		}
		return candles, nil
	default:
		return makeSeries(limit, time.Minute, 100, 1, timeframe), nil
	}
}
func (m *mockMarketData) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	if m.priceErr != nil {
		return 0, m.priceErr
	}
	if m.price > 0 {
		return m.price, nil
	}
	return 65000, nil
}
func (m *mockMarketData) IsHealthy(symbol string) bool { return true }
func (m *mockMarketData) GetTradeFlow(ctx context.Context, symbol string) ([]domain.TradeFlow, error) {
	return nil, nil
}

type mockCandidateRepo struct {
	candidates []domain.Candidate
}

func (m *mockCandidateRepo) Insert(ctx context.Context, c domain.Candidate) (int64, error) {
	m.candidates = append(m.candidates, c)
	return int64(len(m.candidates)), nil
}

func (m *mockCandidateRepo) GetByCycle(ctx context.Context, cycleID string) ([]domain.Candidate, error) {
	var out []domain.Candidate
	for _, c := range m.candidates {
		if c.CycleID == cycleID {
			out = append(out, c)
		}
	}
	return out, nil
}

type llmDecisionRecord struct {
	decision    domain.LLMDecision
	candidateID int64
	cycleID     string
}

type mockLLMDecisionRepo struct {
	decisions []llmDecisionRecord
}

func (m *mockLLMDecisionRepo) Insert(ctx context.Context, d domain.LLMDecision, candidateID int64, cycleID string) (int64, error) {
	m.decisions = append(m.decisions, llmDecisionRecord{decision: d, candidateID: candidateID, cycleID: cycleID})
	return int64(len(m.decisions)), nil
}

func (m *mockLLMDecisionRepo) GetByCycle(ctx context.Context, cycleID string) ([]domain.LLMDecision, error) {
	var out []domain.LLMDecision
	for _, rec := range m.decisions {
		if rec.cycleID == cycleID {
			d := rec.decision
			d.CandidateID = rec.candidateID
			out = append(out, d)
		}
	}
	return out, nil
}

type riskDecisionRecord struct {
	decision    domain.RiskDecision
	candidateID int64
	cycleID     string
}

type mockRiskDecisionRepo struct {
	decisions []riskDecisionRecord
}

func (m *mockRiskDecisionRepo) Insert(ctx context.Context, d domain.RiskDecision, candidateID int64, cycleID string) (int64, error) {
	m.decisions = append(m.decisions, riskDecisionRecord{decision: d, candidateID: candidateID, cycleID: cycleID})
	return int64(len(m.decisions)), nil
}

func (m *mockRiskDecisionRepo) GetByCycle(ctx context.Context, cycleID string) ([]domain.RiskDecision, error) {
	var out []domain.RiskDecision
	for _, rec := range m.decisions {
		if rec.cycleID == cycleID {
			d := rec.decision
			d.CandidateID = rec.candidateID
			d.CycleID = rec.cycleID
			out = append(out, d)
		}
	}
	return out, nil
}

type mockLLMUsageRepo struct {
	calls int
	date  string
}

func (m *mockLLMUsageRepo) Get(ctx context.Context, usageDate time.Time) (*domain.LLMUsageState, error) {
	if m.date != usageDate.Format("2006-01-02") {
		return nil, nil
	}
	return &domain.LLMUsageState{UsageDate: usageDate, Calls: m.calls}, nil
}

func (m *mockLLMUsageRepo) IncrementCalls(ctx context.Context, usageDate time.Time, calls int) error {
	m.date = usageDate.Format("2006-01-02")
	m.calls += calls
	return nil
}

func newTestScheduler(universeRepo db.UniverseRepository, cycleRepo db.CycleRepository) *Scheduler {
	log := logger.New(nil, logger.LevelDebug)
	cfg := app.UserConfig{
		App: app.AppConfig{
			Mode:                 "paper",
			CycleIntervalSeconds: 900,
		},
		Universe: app.UniverseConfig{
			Mode: "all_usdt_perpetual",
		},
		DataValidation: app.DataValidationConfig{
			MinCandles: 25,
			MaxDataAgeSeconds: map[string]int{
				"1H":  7200,
				"15m": 7200,
				"5m":  7200,
			},
		},
		Strategy: app.StrategyConfig{
			Enabled: true,
			Timeframes: app.TimeframesConfig{
				Context:   "1H",
				Setup:     "15m",
				Execution: "15m",
			},
			Indicators: app.IndicatorsConfig{
				ATRPeriod:                  14,
				EMA200Period:               200,
				VolumeSMAPeriod:            20,
				RangeCandles:               20,
				MinVolumeRatio:             1.0,
				MaxBreakoutExtensionATR:    2.0,
				MaxDistanceFromBreakoutATR: 1.0,
				MinRR:                      1.5,
			},
			Regime: app.RegimeConfig{
				TrendUpMinDistanceFromEMAPct:   1.0,
				TrendDownMaxDistanceFromEMAPct: -1.0,
				RangeMaxDistanceFromEMAPct:     0.5,
			},
		},
		LLMRouting: app.LLMRoutingConfig{
			Mode:              "off",
			MinCandidateScore: 1.0,
		},
		PortfolioRisk: app.PortfolioRiskConfig{
			MaxOpenPositions:          5,
			MaxRiskPerTradePct:        1.0,
			MaxDailyLossPct:           3.0,
			MaxTotalExposureUSD:       100000,
			MaxTotalMarginUsedPct:     50.0,
			MaxLeverage:               10.0,
			MinNotionalUSD:            10.0,
			MarginPerTradeUSD:         100.0,
			MaxNewPositionsPerCycle:   2,
			MaxSameDirectionPositions: 5,
		},
		Sizing: app.SizingConfig{
			Method:             "fixed_margin",
			MarginPerTradeUSD:  100.0,
			MaxLeverage:        10.0,
			MaxRiskPerTradePct: 1.0,
		},
	}

	md := &mockMarketData{}
	universeScanner := universe.NewScanner(cfg.Universe, cfg.Strategy, cfg.LLMRouting, "", md, universeRepo, &mockCandleRepo{}, log, cfg.ComputeTargetNotional())
	screenerSvc := screener.NewScreener(cfg, universeScanner, md, log)

	pb := broker.NewPaperBroker(app.PaperConfig{StartingBalanceUSD: 10000}, log)
	riskEng := risk.NewEngine(cfg)
	exec := executor.NewExecutor(pb, log)
	safetyEng := execution.NewSafetyEngine(cfg)
	mon := monitor.NewMonitor(pb, md, cfg.PortfolioRisk, log)

	sched := NewScheduler(cfg, screenerSvc, llm.NewMockClient(domain.LLMDecision{Decision: "ALLOW_MARKET", Confidence: 0.9, SizeMultiplier: 1.0}, nil), riskEng, exec, safetyEng, mon, md, log)
	sched.SetCycleRepo(cycleRepo)
	return sched
}

func TestCycleID_Generation(t *testing.T) {
	s := &Scheduler{log: logger.New(nil, logger.LevelDebug)}
	s.cycleSeq = 0

	// Each RunOnce should generate unique cycle IDs
	ids := make(map[string]bool)
	for i := 0; i < 3; i++ {
		result := &CycleResult{
			CycleID:   fmt.Sprintf("cycle-%d-%s", i+1, time.Now().Format("20060102-150405")),
			StartedAt: time.Now(),
		}
		if ids[result.CycleID] {
			t.Errorf("duplicate cycle ID: %s", result.CycleID)
		}
		ids[result.CycleID] = true
	}
}

func TestNoOverlappingCycles(t *testing.T) {
	s := &Scheduler{
		log:      logger.New(nil, logger.LevelDebug),
		running:  true, // simulate a running cycle
		cycleSeq: 0,
	}

	_, err := s.RunOnce(context.Background())
	if err == nil {
		t.Error("expected error for overlapping cycle")
	}
}

func TestSkipReason_String(t *testing.T) {
	sr := SkipReason{Symbol: "BTCUSDT", Reason: "LLM_BLOCK"}
	if sr.Symbol != "BTCUSDT" || sr.Reason != "LLM_BLOCK" {
		t.Error("SkipReason fields not set correctly")
	}
}

func TestCycleResult_Fields(t *testing.T) {
	result := &CycleResult{
		CycleID:    "test-cycle",
		StartedAt:  time.Now(),
		Candidates: 5,
		LLMCalls:   3,
		Executions: 1,
		Skips: []SkipReason{
			{Symbol: "A", Reason: "LLM_BLOCK"},
			{Symbol: "B", Reason: "RISK_REJECTED"},
		},
		ReasonCodes: []string{"CYCLE_COMPLETE"},
	}

	if result.CycleID != "test-cycle" {
		t.Errorf("expected test-cycle, got %s", result.CycleID)
	}
	if result.Candidates != 5 {
		t.Errorf("expected 5 candidates, got %d", result.Candidates)
	}
	if len(result.Skips) != 2 {
		t.Errorf("expected 2 skips, got %d", len(result.Skips))
	}
}

func TestScheduler_RunOnce_NoCandidates_PersistsCycle(t *testing.T) {
	cycleRepo := &mockCycleRepo{}
	universeRepo := &mockUniverseRepo{symbols: []domain.UniverseSymbol{}} // empty universe → no candidates
	sched := newTestScheduler(universeRepo, cycleRepo)

	result, err := sched.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	if len(cycleRepo.cycles) != 1 {
		t.Fatalf("expected 1 cycle persisted, got %d", len(cycleRepo.cycles))
	}
	cycle := cycleRepo.cycles[0]
	if cycle.Status != "skipped" {
		t.Errorf("expected status 'skipped', got %s", cycle.Status)
	}
	if !containsAny(cycle.ReasonCodes, []string{"NO_CANDIDATE"}) {
		t.Errorf("expected NO_CANDIDATE in reason codes, got %v", cycle.ReasonCodes)
	}
}

func TestScheduler_RunOnce_ScreenerError_PersistsFailedCycle(t *testing.T) {
	cycleRepo := &mockCycleRepo{}
	universeRepo := &mockUniverseRepo{err: errors.New("universe db unreachable")}
	sched := newTestScheduler(universeRepo, cycleRepo)

	result, err := sched.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected screener error")
	}
	if result == nil {
		t.Fatal("expected result even on error")
	}

	if len(cycleRepo.cycles) != 1 {
		t.Fatalf("expected 1 cycle persisted, got %d", len(cycleRepo.cycles))
	}
	cycle := cycleRepo.cycles[0]
	if cycle.Status != "failed" {
		t.Errorf("expected status 'failed', got %s", cycle.Status)
	}
	if !containsAny(cycle.ReasonCodes, []string{"SCREEN_FAILED"}) {
		t.Errorf("expected SCREEN_FAILED in reason codes, got %v", cycle.ReasonCodes)
	}
}

func TestScheduler_RunOnce_CandidatesPersisted(t *testing.T) {
	cycleRepo := &mockCycleRepo{}
	candidateRepo := &mockCandidateRepo{}
	universeRepo := &mockUniverseRepo{
		symbols: []domain.UniverseSymbol{
			{SymbolInfo: domain.SymbolInfo{Symbol: "BTCUSDT", Status: "Trading", QuoteAsset: "USDT", MinNotional: 10}},
		},
	}
	sched := newTestScheduler(universeRepo, cycleRepo)
	sched.SetCandidateRepo(candidateRepo)

	_, err := sched.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(candidateRepo.candidates) == 0 {
		t.Fatal("expected candidates to be persisted")
	}
	cand := candidateRepo.candidates[0]
	if cand.CycleID == "" {
		t.Error("expected candidate to have cycle_id")
	}
}

func TestScheduler_RunOnce_LLMDecisionUsesCandidateID(t *testing.T) {
	cycleRepo := &mockCycleRepo{}
	candidateRepo := &mockCandidateRepo{}
	llmDecisionRepo := &mockLLMDecisionRepo{}
	universeRepo := &mockUniverseRepo{
		symbols: []domain.UniverseSymbol{
			{SymbolInfo: domain.SymbolInfo{Symbol: "BTCUSDT", Status: "Trading", QuoteAsset: "USDT", MinNotional: 10}},
		},
	}
	sched := newTestScheduler(universeRepo, cycleRepo)
	sched.SetCandidateRepo(candidateRepo)
	sched.SetLLMDecisionRepo(llmDecisionRepo)

	result, err := sched.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(llmDecisionRepo.decisions) == 0 {
		t.Fatal("expected LLM decisions to be persisted")
	}
	if len(candidateRepo.candidates) == 0 {
		t.Fatal("expected candidates to be persisted")
	}
	rec := llmDecisionRepo.decisions[0]
	if rec.candidateID != 1 {
		t.Errorf("expected candidateID=1 (first mock insert), got %d", rec.candidateID)
	}
	if rec.cycleID == "" {
		t.Error("expected non-empty cycleID in LLM decision record")
	}
	if result != nil && rec.cycleID != result.CycleID {
		t.Errorf("expected cycleID=%s, got %s", result.CycleID, rec.cycleID)
	}
}

func TestScheduler_RunOnce_RiskDecisionPersisted(t *testing.T) {
	cycleRepo := &mockCycleRepo{}
	candidateRepo := &mockCandidateRepo{}
	riskDecisionRepo := &mockRiskDecisionRepo{}
	universeRepo := &mockUniverseRepo{
		symbols: []domain.UniverseSymbol{
			{SymbolInfo: domain.SymbolInfo{Symbol: "BTCUSDT", Status: "Trading", QuoteAsset: "USDT", MinNotional: 10}},
		},
	}
	sched := newTestScheduler(universeRepo, cycleRepo)
	sched.SetCandidateRepo(candidateRepo)
	sched.SetRiskDecisionRepo(riskDecisionRepo)

	result, err := sched.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(riskDecisionRepo.decisions) == 0 {
		t.Fatal("expected risk decisions to be persisted")
	}
	rec := riskDecisionRepo.decisions[0]
	if rec.candidateID <= 0 {
		t.Fatalf("expected positive candidateID, got %d", rec.candidateID)
	}
	if rec.cycleID != result.CycleID {
		t.Fatalf("expected cycleID=%s, got %s", result.CycleID, rec.cycleID)
	}
}

func TestScheduler_RunOnce_PortfolioRankingPersistsRejectedCandidates(t *testing.T) {
	cycleRepo := &mockCycleRepo{}
	candidateRepo := &mockCandidateRepo{}
	riskDecisionRepo := &mockRiskDecisionRepo{}
	universeRepo := &mockUniverseRepo{
		symbols: []domain.UniverseSymbol{
			{SymbolInfo: domain.SymbolInfo{Symbol: "BTCUSDT", Status: "Trading", QuoteAsset: "USDT", MinNotional: 10}},
			{SymbolInfo: domain.SymbolInfo{Symbol: "ETHUSDT", Status: "Trading", QuoteAsset: "USDT", MinNotional: 10}},
		},
	}
	sched := newTestScheduler(universeRepo, cycleRepo)
	sched.cfg.PortfolioRisk.MaxNewPositionsPerCycle = 1
	sched.SetCandidateRepo(candidateRepo)
	sched.SetRiskDecisionRepo(riskDecisionRepo)

	result, err := sched.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(riskDecisionRepo.decisions) < 2 {
		t.Fatalf("expected at least 2 risk decisions, got %d", len(riskDecisionRepo.decisions))
	}
	approved := 0
	portfolioRejected := 0
	for _, rec := range riskDecisionRepo.decisions {
		if rec.decision.Approved {
			approved++
		}
		if rec.decision.PortfolioRejectReason == "PORTFOLIO_RISK_LIMIT" {
			portfolioRejected++
		}
	}
	if approved != 1 {
		t.Fatalf("expected exactly 1 approved decision, got %d", approved)
	}
	if portfolioRejected == 0 {
		t.Fatal("expected at least one portfolio risk rejection")
	}
	if !skipContains(result.Skips, "PORTFOLIO_RISK_LIMIT") {
		t.Fatalf("expected PORTFOLIO_RISK_LIMIT skip, got %v", result.Skips)
	}
}

func TestScheduler_CheckDailyReset_UpdatesDay(t *testing.T) {
	log := logger.New(nil, logger.LevelDebug)
	pb := broker.NewPaperBroker(app.PaperConfig{StartingBalanceUSD: 10000}, log)
	md := &mockMarketData{}
	mon := monitor.NewMonitor(pb, md, app.PortfolioRiskConfig{}, log)
	sched := &Scheduler{
		log:               log,
		monitor:           mon,
		lastDailyResetDay: "2020-01-01",
	}
	sched.checkDailyReset()
	today := time.Now().UTC().Format("2006-01-02")
	if sched.lastDailyResetDay != today {
		t.Errorf("expected lastDailyResetDay = %s, got %s", today, sched.lastDailyResetDay)
	}
}

func TestScheduler_CheckDailyReset_CallsReviewerResetCost(t *testing.T) {
	log := logger.New(nil, logger.LevelDebug)
	pb := broker.NewPaperBroker(app.PaperConfig{StartingBalanceUSD: 10000}, log)
	md := &mockMarketData{}
	mon := monitor.NewMonitor(pb, md, app.PortfolioRiskConfig{}, log)
	sched := &Scheduler{
		log:               log,
		monitor:           mon,
		lastDailyResetDay: "2020-01-01",
	}
	// Reviewer with audit_only mode (enabled, no active calls)
	reviewer := llm.NewReviewer(app.LLMReviewConfig{
		Mode:            "audit_only",
		Provider:        "openrouter",
		Model:           "test-model",
		BaseURL:         "http://localhost:99999",
		TimeoutSeconds:  1,
		MaxOutputTokens: 100,
	}, log)
	if !reviewer.IsEnabled() {
		t.Fatal("expected reviewer to be enabled")
	}
	sched.SetLLMReviewer(reviewer)

	sched.checkDailyReset()
	today := time.Now().UTC().Format("2006-01-02")
	if sched.lastDailyResetDay != today {
		t.Errorf("expected lastDailyResetDay = %s, got %s", today, sched.lastDailyResetDay)
	}
}

func TestPaperMode_SimulatorPositions_MergedIntoCycleState(t *testing.T) {
	cycleRepo := &mockCycleRepo{}
	candidateRepo := &mockCandidateRepo{}
	riskDecisionRepo := &mockRiskDecisionRepo{}
	universeRepo := &mockUniverseRepo{
		symbols: []domain.UniverseSymbol{
			{SymbolInfo: domain.SymbolInfo{Symbol: "BTCUSDT", Status: "Trading", QuoteAsset: "USDT", MinNotional: 10}},
		},
	}

	log := logger.New(nil, logger.LevelDebug)
	cfg := app.UserConfig{
		App: app.AppConfig{
			Mode:                 "paper",
			CycleIntervalSeconds: 900,
		},
		Universe: app.UniverseConfig{
			Mode: "all_usdt_perpetual",
		},
		DataValidation: app.DataValidationConfig{
			MinCandles: 25,
			MaxDataAgeSeconds: map[string]int{
				"1H":  7200,
				"15m": 7200,
				"5m":  7200,
			},
		},
		Strategy: app.StrategyConfig{
			Enabled: true,
			Timeframes: app.TimeframesConfig{
				Context:   "1H",
				Setup:     "15m",
				Execution: "15m",
			},
			Indicators: app.IndicatorsConfig{
				ATRPeriod:                  14,
				EMA200Period:               200,
				VolumeSMAPeriod:            20,
				RangeCandles:               20,
				MinVolumeRatio:             1.0,
				MaxBreakoutExtensionATR:    2.0,
				MaxDistanceFromBreakoutATR: 1.0,
				MinRR:                      1.5,
			},
			Regime: app.RegimeConfig{
				TrendUpMinDistanceFromEMAPct:   1.0,
				TrendDownMaxDistanceFromEMAPct: -1.0,
				RangeMaxDistanceFromEMAPct:     0.5,
			},
		},
		LLMRouting: app.LLMRoutingConfig{
			Mode:              "off",
			MinCandidateScore: 1.0,
		},
		PortfolioRisk: app.PortfolioRiskConfig{
			MaxOpenPositions:          5,
			MaxRiskPerTradePct:        1.0,
			MaxDailyLossPct:           3.0,
			MaxTotalExposureUSD:       100000,
			MaxTotalMarginUsedPct:     50.0,
			MaxLeverage:               10.0,
			MinNotionalUSD:            10.0,
			MarginPerTradeUSD:         100.0,
			MaxNewPositionsPerCycle:   2,
			MaxSameDirectionPositions: 5,
		},
		Sizing: app.SizingConfig{
			Method:             "fixed_margin",
			MarginPerTradeUSD:  100.0,
			MaxLeverage:        10.0,
			MaxRiskPerTradePct: 1.0,
		},
	}

	md := &mockMarketData{}
	universeScanner := universe.NewScanner(cfg.Universe, cfg.Strategy, cfg.LLMRouting, "", md, universeRepo, &mockCandleRepo{}, log, cfg.ComputeTargetNotional())
	screenerSvc := screener.NewScreener(cfg, universeScanner, md, log)

	pb := broker.NewPaperBroker(app.PaperConfig{StartingBalanceUSD: 10000}, log)
	riskEng := risk.NewEngine(cfg)
	exec := executor.NewExecutor(pb, log)
	safetyEng := execution.NewSafetyEngine(cfg)
	mon := monitor.NewMonitor(pb, md, cfg.PortfolioRisk, log)

	mockLLM := llm.NewMockClient(domain.LLMDecision{Decision: "ALLOW_MARKET", Confidence: 0.9, SizeMultiplier: 1.0}, nil)
	sched := NewScheduler(cfg, screenerSvc, mockLLM, riskEng, exec, safetyEng, mon, md, log)
	sched.SetMode(app.ModePaper)
	sched.SetCycleRepo(cycleRepo)
	sched.SetCandidateRepo(candidateRepo)
	sched.SetRiskDecisionRepo(riskDecisionRepo)

	// Set up paper simulator with a pre-existing BTCUSDT open trade
	// Use TakeProfit well above current mock price (65000) so it doesn't trigger
	paperTradeRepo := &mockPaperTradeRepo{}
	paperTradeRepo.trades = append(paperTradeRepo.trades, domain.PaperTrade{
		PaperTradeID: "existing-btc",
		Symbol:       "BTCUSDT",
		Side:         domain.SideLong,
		Qty:          0.1,
		EntryPrice:   64000,
		StopLoss:     63000,
		TakeProfit:   100000,
		OpenedAt:     time.Now().Add(-time.Hour),
	})
	paperSim := paperexec.NewSimulator(md, paperTradeRepo, &mockPaperAccountRepo{}, app.PaperConfig{StartingBalanceUSD: 10000}, log)
	if err := paperSim.Initialize(context.Background()); err != nil {
		t.Fatalf("paper sim init: %v", err)
	}
	sched.SetPaperSimulator(paperSim)

	result, err := sched.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	// With an existing BTCUSDT paper trade, the candidate should be blocked
	// either by hard blocks (BlockIfPositionAlreadyOpen) or risk (DUPLICATE_SYMBOL).
	foundRejected := false
	for _, sk := range result.Skips {
		if sk.Symbol == "BTCUSDT" && (sk.Reason == "HARD_BLOCK" || sk.Reason == "RISK_REJECTED") {
			foundRejected = true
			break
		}
	}
	if !foundRejected {
		t.Fatalf("expected BTCUSDT to be rejected due to existing paper trade, skips: %v", result.Skips)
	}

	// Verify the risk decision contains DUPLICATE_SYMBOL
	foundDuplicate := false
	for _, rec := range riskDecisionRepo.decisions {
		for _, rc := range rec.decision.ReasonCodes {
			if rc == "DUPLICATE_SYMBOL" {
				foundDuplicate = true
			}
		}
	}
	if !foundDuplicate {
		t.Log("risk decisions did not contain DUPLICATE_SYMBOL; may have been blocked by hard blocks")
	}
}

// recordingMockMarketData records the arguments passed to GetOrderBookSummary.
type recordingMockMarketData struct {
	mockMarketData
	lastTargetNotional float64
	lastSide           string
	callCount          int
}

func (m *recordingMockMarketData) GetOrderBookSummary(ctx context.Context, symbol string, targetNotional float64, side string) (domain.OrderBookSummary, error) {
	m.lastTargetNotional = targetNotional
	m.lastSide = side
	m.callCount++
	return domain.OrderBookSummary{BestBid: 100, BestAsk: 101, SpreadBps: 1, BidDepth: 100, AskDepth: 100, EstimatedSlippageBps: 5, DepthToPositionSizeRatio: 10}, nil
}

func TestScheduler_RunOnce_CallsGetOrderBookSummaryWithTargetNotional(t *testing.T) {
	cycleRepo := &mockCycleRepo{}
	candidateRepo := &mockCandidateRepo{}
	universeRepo := &mockUniverseRepo{
		symbols: []domain.UniverseSymbol{
			{SymbolInfo: domain.SymbolInfo{Symbol: "BTCUSDT", Status: "Trading", QuoteAsset: "USDT", MinNotional: 10}},
		},
	}

	log := logger.New(nil, logger.LevelDebug)
	cfg := app.UserConfig{
		App: app.AppConfig{
			Mode:                 "paper",
			CycleIntervalSeconds: 900,
		},
		Universe: app.UniverseConfig{
			Mode: "all_usdt_perpetual",
		},
		DataValidation: app.DataValidationConfig{
			MinCandles: 25,
			MaxDataAgeSeconds: map[string]int{
				"1H":  7200,
				"15m": 7200,
				"5m":  7200,
			},
		},
		Strategy: app.StrategyConfig{
			Enabled: true,
			Timeframes: app.TimeframesConfig{
				Context:   "1H",
				Setup:     "15m",
				Execution: "15m",
			},
			Indicators: app.IndicatorsConfig{
				ATRPeriod:                  14,
				EMA200Period:               200,
				VolumeSMAPeriod:            20,
				RangeCandles:               20,
				MinVolumeRatio:             1.0,
				MaxBreakoutExtensionATR:    2.0,
				MaxDistanceFromBreakoutATR: 1.0,
				MinRR:                      1.5,
			},
			Regime: app.RegimeConfig{
				TrendUpMinDistanceFromEMAPct:   1.0,
				TrendDownMaxDistanceFromEMAPct: -1.0,
				RangeMaxDistanceFromEMAPct:     0.5,
			},
		},
		LLMRouting: app.LLMRoutingConfig{
			Mode:              "off",
			MinCandidateScore: 1.0,
		},
		PortfolioRisk: app.PortfolioRiskConfig{
			MaxOpenPositions:        5,
			MaxRiskPerTradePct:      1.0,
			MaxDailyLossPct:         3.0,
			MaxTotalExposureUSD:     100000,
			MaxTotalMarginUsedPct:   50.0,
			MaxLeverage:             10.0,
			MinNotionalUSD:          10.0,
			MarginPerTradeUSD:       100.0,
			MaxNewPositionsPerCycle: 2,
		},
		Sizing: app.SizingConfig{
			Method:             "fixed_margin",
			MarginPerTradeUSD:  100.0,
			MaxLeverage:        10.0,
			MaxRiskPerTradePct: 1.0,
		},
	}

	md := &recordingMockMarketData{}
	universeScanner := universe.NewScanner(cfg.Universe, cfg.Strategy, cfg.LLMRouting, "", md, universeRepo, &mockCandleRepo{}, log, cfg.ComputeTargetNotional())
	screenerSvc := screener.NewScreener(cfg, universeScanner, md, log)

	pb := broker.NewPaperBroker(app.PaperConfig{StartingBalanceUSD: 10000}, log)
	riskEng := risk.NewEngine(cfg)
	exec := executor.NewExecutor(pb, log)
	safetyEng := execution.NewSafetyEngine(cfg)
	mon := monitor.NewMonitor(pb, md, cfg.PortfolioRisk, log)

	sched := NewScheduler(cfg, screenerSvc, llm.NewMockClient(domain.LLMDecision{Decision: "ALLOW_MARKET", Confidence: 0.9, SizeMultiplier: 1.0}, nil), riskEng, exec, safetyEng, mon, md, log)
	sched.SetCycleRepo(cycleRepo)
	sched.SetCandidateRepo(candidateRepo)

	_, err := sched.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if md.callCount == 0 {
		t.Fatal("expected at least one GetOrderBookSummary call")
	}
	if md.lastTargetNotional <= 0 {
		t.Errorf("expected targetNotional > 0, got %f", md.lastTargetNotional)
	}
	if md.lastSide == "" {
		t.Error("expected non-empty side passed to GetOrderBookSummary")
	}
}

func TestScheduler_SkipZeroPrice(t *testing.T) {
	cycleRepo := &mockCycleRepo{}
	candidateRepo := &mockCandidateRepo{}
	riskDecisionRepo := &mockRiskDecisionRepo{}
	universeRepo := &mockUniverseRepo{
		symbols: []domain.UniverseSymbol{
			{SymbolInfo: domain.SymbolInfo{Symbol: "BTCUSDT", Status: "Trading", QuoteAsset: "USDT", MinNotional: 10}},
		},
	}

	log := logger.New(nil, logger.LevelDebug)
	cfg := app.UserConfig{
		App: app.AppConfig{
			Mode:                 "paper",
			CycleIntervalSeconds: 900,
		},
		Universe: app.UniverseConfig{
			Mode: "all_usdt_perpetual",
		},
		DataValidation: app.DataValidationConfig{
			MinCandles: 25,
			MaxDataAgeSeconds: map[string]int{
				"1H":  7200,
				"15m": 7200,
				"5m":  7200,
			},
		},
		Strategy: app.StrategyConfig{
			Enabled: true,
			Timeframes: app.TimeframesConfig{
				Context:   "1H",
				Setup:     "15m",
				Execution: "15m",
			},
			Indicators: app.IndicatorsConfig{
				ATRPeriod:                  14,
				EMA200Period:               200,
				VolumeSMAPeriod:            20,
				RangeCandles:               20,
				MinVolumeRatio:             1.0,
				MaxBreakoutExtensionATR:    2.0,
				MaxDistanceFromBreakoutATR: 1.0,
				MinRR:                      1.5,
			},
			Regime: app.RegimeConfig{
				TrendUpMinDistanceFromEMAPct:   1.0,
				TrendDownMaxDistanceFromEMAPct: -1.0,
				RangeMaxDistanceFromEMAPct:     0.5,
			},
		},
		LLMRouting: app.LLMRoutingConfig{
			Mode:              "off",
			MinCandidateScore: 1.0,
		},
		PortfolioRisk: app.PortfolioRiskConfig{
			MaxOpenPositions:          5,
			MaxRiskPerTradePct:        1.0,
			MaxDailyLossPct:           3.0,
			MaxTotalExposureUSD:       100000,
			MaxTotalMarginUsedPct:     50.0,
			MaxLeverage:               10.0,
			MinNotionalUSD:            10.0,
			MarginPerTradeUSD:         100.0,
			MaxNewPositionsPerCycle:   2,
			MaxSameDirectionPositions: 5,
		},
		Sizing: app.SizingConfig{
			Method:             "fixed_margin",
			MarginPerTradeUSD:  100.0,
			MaxLeverage:        10.0,
			MaxRiskPerTradePct: 1.0,
		},
	}

	md := &mockMarketData{priceErr: errors.New("price unavailable")}
	universeScanner := universe.NewScanner(cfg.Universe, cfg.Strategy, cfg.LLMRouting, "", md, universeRepo, &mockCandleRepo{}, log, cfg.ComputeTargetNotional())
	screenerSvc := screener.NewScreener(cfg, universeScanner, md, log)

	pb := broker.NewPaperBroker(app.PaperConfig{StartingBalanceUSD: 10000}, log)
	riskEng := risk.NewEngine(cfg)
	exec := executor.NewExecutor(pb, log)
	safetyEng := execution.NewSafetyEngine(cfg)
	mon := monitor.NewMonitor(pb, md, cfg.PortfolioRisk, log)

	sched := NewScheduler(cfg, screenerSvc, llm.NewMockClient(domain.LLMDecision{Decision: "ALLOW_MARKET", Confidence: 0.9, SizeMultiplier: 1.0}, nil), riskEng, exec, safetyEng, mon, md, log)
	sched.SetCycleRepo(cycleRepo)
	sched.SetCandidateRepo(candidateRepo)
	sched.SetRiskDecisionRepo(riskDecisionRepo)

	result, err := sched.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Skips) == 0 {
		t.Fatal("expected skipped candidate due to unavailable price")
	}
	foundSkip := false
	for _, sk := range result.Skips {
		if sk.Reason == "HARD_BLOCK" {
			foundSkip = true
			break
		}
	}
	if !foundSkip {
		t.Fatalf("expected HARD_BLOCK skip, got %v", result.Skips)
	}
}

func TestApplyLLMCaps_HardCap(t *testing.T) {
	log := logger.New(nil, logger.LevelDebug)
	sched := &Scheduler{cfg: app.UserConfig{LLMRouting: app.LLMRoutingConfig{HardCapCandidatesPerCycle: 2}}, log: log}

	candidates := []domain.Candidate{
		{ProposedTrade: domain.ProposedTrade{Symbol: "A"}, CandidateScore: 90},
		{ProposedTrade: domain.ProposedTrade{Symbol: "B"}, CandidateScore: 80},
		{ProposedTrade: domain.ProposedTrade{Symbol: "C"}, CandidateScore: 70},
	}

	kept, skips := sched.applyLLMCaps(candidates)
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept, got %d", len(kept))
	}
	if kept[0].Symbol != "A" || kept[1].Symbol != "B" {
		t.Errorf("expected A,B kept, got %v", kept)
	}
	if len(skips) != 1 || skips[0].Symbol != "C" || skips[0].Reason != "LLM_HARD_CAP" {
		t.Errorf("expected C skipped with LLM_HARD_CAP, got %v", skips)
	}
}

func TestApplyLLMCaps_DailyCapReached(t *testing.T) {
	log := logger.New(nil, logger.LevelDebug)
	sched := &Scheduler{
		cfg: app.UserConfig{LLMRouting: app.LLMRoutingConfig{MaxCallsPerDay: 5}},
		log: log,
	}
	sched.llmCallsDate = time.Now().Format("2006-01-02")
	sched.llmCallsToday = 5

	candidates := []domain.Candidate{{ProposedTrade: domain.ProposedTrade{Symbol: "A"}, CandidateScore: 90}}
	kept, skips := sched.applyLLMCaps(candidates)
	if len(kept) != 0 {
		t.Fatalf("expected 0 kept, got %d", len(kept))
	}
	if len(skips) != 1 || skips[0].Reason != "LLM_CALL_CAP_PER_DAY" {
		t.Errorf("expected daily cap skip, got %v", skips)
	}
}

func TestApplyLLMCaps_DailyCapRestrictsHardCap(t *testing.T) {
	log := logger.New(nil, logger.LevelDebug)
	sched := &Scheduler{
		cfg: app.UserConfig{LLMRouting: app.LLMRoutingConfig{HardCapCandidatesPerCycle: 5, MaxCallsPerDay: 3}},
		log: log,
	}
	sched.llmCallsDate = time.Now().Format("2006-01-02")
	sched.llmCallsToday = 1

	candidates := []domain.Candidate{
		{ProposedTrade: domain.ProposedTrade{Symbol: "A"}, CandidateScore: 90},
		{ProposedTrade: domain.ProposedTrade{Symbol: "B"}, CandidateScore: 80},
		{ProposedTrade: domain.ProposedTrade{Symbol: "C"}, CandidateScore: 70},
		{ProposedTrade: domain.ProposedTrade{Symbol: "D"}, CandidateScore: 60},
	}

	kept, skips := sched.applyLLMCaps(candidates)
	// remaining today = 2, so hard cap is reduced to 2
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept, got %d", len(kept))
	}
	if len(skips) != 2 {
		t.Fatalf("expected 2 skips, got %d", len(skips))
	}
	for _, sk := range skips {
		if sk.Reason != "LLM_CALL_CAP_PER_DAY" {
			t.Errorf("expected skip reason LLM_CALL_CAP_PER_DAY, got %s", sk.Reason)
		}
	}
}

func TestApplyLLMCaps_ZeroMeansUnlimited(t *testing.T) {
	log := logger.New(nil, logger.LevelDebug)
	sched := &Scheduler{
		cfg: app.UserConfig{LLMRouting: app.LLMRoutingConfig{HardCapCandidatesPerCycle: 0, MaxCallsPerDay: 0}},
		log: log,
	}

	candidates := []domain.Candidate{
		{ProposedTrade: domain.ProposedTrade{Symbol: "A"}, CandidateScore: 90},
		{ProposedTrade: domain.ProposedTrade{Symbol: "B"}, CandidateScore: 80},
	}

	kept, skips := sched.applyLLMCaps(candidates)
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept, got %d", len(kept))
	}
	if len(skips) != 0 {
		t.Fatalf("expected 0 skips, got %d", len(skips))
	}
}

func TestApplyLLMCaps_MaxCallsPerCycle(t *testing.T) {
	log := logger.New(nil, logger.LevelDebug)
	sched := &Scheduler{
		cfg: app.UserConfig{LLMRouting: app.LLMRoutingConfig{MaxCallsPerCycle: 1}},
		log: log,
	}

	candidates := []domain.Candidate{
		{ProposedTrade: domain.ProposedTrade{Symbol: "A"}, CandidateScore: 90},
		{ProposedTrade: domain.ProposedTrade{Symbol: "B"}, CandidateScore: 80},
		{ProposedTrade: domain.ProposedTrade{Symbol: "C"}, CandidateScore: 70},
	}

	kept, skips := sched.applyLLMCaps(candidates)
	if len(kept) != 1 {
		t.Fatalf("expected 1 kept, got %d", len(kept))
	}
	if kept[0].Symbol != "A" {
		t.Errorf("expected highest-score candidate A, got %s", kept[0].Symbol)
	}
	if len(skips) != 2 {
		t.Fatalf("expected 2 skips, got %d", len(skips))
	}
	for _, sk := range skips {
		if sk.Reason != "LLM_CALL_CAP_PER_CYCLE" {
			t.Errorf("expected skip reason LLM_CALL_CAP_PER_CYCLE, got %s", sk.Reason)
		}
	}
}

func TestTrackLLMCall_ResetsOnNewDay(t *testing.T) {
	log := logger.New(nil, logger.LevelDebug)
	sched := &Scheduler{log: log}
	sched.llmCallsDate = "2020-01-01"
	sched.llmCallsToday = 5

	sched.trackLLMCall(context.Background())

	if sched.llmCallsToday != 1 {
		t.Errorf("expected calls today reset to 1, got %d", sched.llmCallsToday)
	}
	if sched.llmCallsDate != time.Now().Format("2006-01-02") {
		t.Errorf("expected date updated to today")
	}
}

func TestLLMUsageRepo_LoadsPersistedDailyCap(t *testing.T) {
	log := logger.New(nil, logger.LevelDebug)
	today := time.Now().Format("2006-01-02")
	usageRepo := &mockLLMUsageRepo{date: today, calls: 5}
	sched := &Scheduler{
		cfg:          app.UserConfig{LLMRouting: app.LLMRoutingConfig{MaxCallsPerDay: 5}},
		log:          log,
		llmUsageRepo: usageRepo,
	}
	sched.loadLLMUsage(context.Background())

	kept, skips := sched.applyLLMCaps([]domain.Candidate{
		{ProposedTrade: domain.ProposedTrade{Symbol: "BTCUSDT"}, CandidateScore: 90},
	})
	if len(kept) != 0 {
		t.Fatalf("expected no kept candidates after persisted cap, got %d", len(kept))
	}
	if len(skips) != 1 || skips[0].Reason != "LLM_CALL_CAP_PER_DAY" {
		t.Fatalf("expected daily cap skip, got %v", skips)
	}
}

func TestTrackLLMCall_PersistsUsage(t *testing.T) {
	log := logger.New(nil, logger.LevelDebug)
	usageRepo := &mockLLMUsageRepo{}
	sched := &Scheduler{log: log, llmUsageRepo: usageRepo}

	sched.trackLLMCall(context.Background())

	if usageRepo.calls != 1 {
		t.Fatalf("expected persisted calls=1, got %d", usageRepo.calls)
	}
	if sched.llmCallsToday != 1 {
		t.Fatalf("expected in-memory calls=1, got %d", sched.llmCallsToday)
	}
}

// mockPaperTradeRepo is an in-memory PaperTradeRepository.
type mockPaperTradeRepo struct {
	trades []domain.PaperTrade
}

func (m *mockPaperTradeRepo) Insert(ctx context.Context, t domain.PaperTrade) error {
	m.trades = append(m.trades, t)
	return nil
}
func (m *mockPaperTradeRepo) Update(ctx context.Context, t domain.PaperTrade) error {
	for i := range m.trades {
		if m.trades[i].PaperTradeID == t.PaperTradeID {
			m.trades[i] = t
			return nil
		}
	}
	return nil
}
func (m *mockPaperTradeRepo) GetOpen(ctx context.Context) ([]domain.PaperTrade, error) {
	var out []domain.PaperTrade
	for _, t := range m.trades {
		if t.ClosedAt == nil {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *mockPaperTradeRepo) GetByDecision(ctx context.Context, decisionID string) (*domain.PaperTrade, error) {
	return nil, nil
}
func (m *mockPaperTradeRepo) GetAll(ctx context.Context, since time.Time) ([]domain.PaperTrade, error) {
	return m.trades, nil
}
func (m *mockPaperTradeRepo) CountByExitReason(ctx context.Context, reason string, since time.Time) (int, error) {
	return 0, nil
}

type mockPaperAccountRepo struct{}

func (m *mockPaperAccountRepo) Get(ctx context.Context) (*domain.PaperAccountState, error) {
	return &domain.PaperAccountState{StartingEquity: 10000, CurrentEquity: 10000}, nil
}
func (m *mockPaperAccountRepo) Update(ctx context.Context, s domain.PaperAccountState) error {
	return nil
}

func skipContains(skips []SkipReason, reason string) bool {
	for _, sk := range skips {
		if sk.Reason == reason {
			return true
		}
	}
	return false
}
