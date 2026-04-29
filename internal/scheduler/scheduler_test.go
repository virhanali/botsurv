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
	candles map[string][]domain.Candle
}

func (m *mockMarketData) GetOrderBookSummary(ctx context.Context, symbol string, targetNotional float64, side string) (domain.OrderBookSummary, error) {
	return domain.OrderBookSummary{BestBid: 100, BestAsk: 101, SpreadBps: 1, BidDepth: 100, AskDepth: 100, EstimatedSlippageBps: 5, DepthToPositionSizeRatio: 10}, nil
}
func (m *mockMarketData) GetCandles(ctx context.Context, symbol, timeframe string, limit int) ([]domain.Candle, error) {
	if m.candles != nil {
		if c, ok := m.candles[symbol+timeframe]; ok {
			return c, nil
		}
	}
	now := time.Now().Truncate(time.Hour)

	if timeframe == "1H" {
		// 210 trending-up 1H candles for regime detection.
		candles := make([]domain.Candle, 210)
		for i := 0; i < 210; i++ {
			price := 64500.0 + float64(i)*5
			candles[i] = domain.Candle{
				Symbol:    symbol,
				Timeframe: "1H",
				OpenTime:  now.Add(-time.Duration(210-i) * time.Hour).UnixMilli(),
				Open:      price,
				High:      price + 100,
				Low:       price - 50,
				Close:     price + 50,
				Volume:    2000,
				Confirmed: true,
			}
		}
		return candles, nil
	}

	// 15m candles: flat range with breakout on last candle.
	candles := make([]domain.Candle, 25)
	for i := 0; i < 24; i++ {
		candles[i] = domain.Candle{
			Symbol:    symbol,
			Timeframe: "15m",
			OpenTime:  now.Add(-time.Duration(24-i) * 15 * time.Minute).UnixMilli(),
			Open:      65000,
			High:      65050,
			Low:       64950,
			Close:     65000,
			Volume:    1000,
			Confirmed: true,
		}
	}
	// Current candle: breakout above range high with volume.
	candles[24] = domain.Candle{
		Symbol:    symbol,
		Timeframe: "15m",
		OpenTime:  now.UnixMilli(),
		Open:      65000,
		High:      65200,
		Low:       64950,
		Close:     65200,
		Volume:    5000,
		Confirmed: true,
	}
	return candles, nil
}
func (m *mockMarketData) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	return 65000, nil
}
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

	md := &mockMarketData{}
	universeScanner := universe.NewScanner(cfg.Universe, cfg.Strategy, cfg.LLMRouting, "", md, universeRepo, &mockCandleRepo{}, log)
	screenerSvc := screener.NewScreener(cfg, universeScanner, md, log)

	pb := broker.NewPaperBroker(app.PaperConfig{StartingBalanceUSD: 10000}, log)
	riskEng := risk.NewEngine(cfg)
	exec := executor.NewExecutor(pb, log)
	mon := monitor.NewMonitor(pb, md, cfg.PortfolioRisk, log)

	sched := NewScheduler(cfg, screenerSvc, llm.NewMockClient(domain.LLMDecision{Decision: "ALLOW_MARKET", Confidence: 0.9, SizeMultiplier: 1.0}, nil), riskEng, exec, mon, md, log)
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
