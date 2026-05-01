package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

// --- In-memory repositories for E2E ---

type memPositionRepo struct {
	positions []domain.Position
}

func (m *memPositionRepo) Insert(_ context.Context, p domain.Position) (int64, error) {
	m.positions = append(m.positions, p)
	return int64(len(m.positions)), nil
}
func (m *memPositionRepo) Update(_ context.Context, p domain.Position) error {
	for i := range m.positions {
		if m.positions[i].ID == p.ID {
			m.positions[i] = p
			return nil
		}
	}
	return nil
}
func (m *memPositionRepo) GetOpen(_ context.Context) ([]domain.Position, error) {
	var out []domain.Position
	for _, p := range m.positions {
		if p.Status == domain.PositionStatusOpen {
			out = append(out, p)
		}
	}
	return out, nil
}
func (m *memPositionRepo) GetClosed(_ context.Context, _ time.Time) ([]domain.Position, error) {
	return nil, nil
}
func (m *memPositionRepo) GetBySymbol(_ context.Context, symbol string) (*domain.Position, error) {
	for _, p := range m.positions {
		if p.Symbol == symbol && p.Status == domain.PositionStatusOpen {
			return &p, nil
		}
	}
	return nil, nil
}

type memOrderRepo struct {
	orders []domain.Order
}

func (m *memOrderRepo) Insert(_ context.Context, o domain.Order) (int64, error) {
	m.orders = append(m.orders, o)
	return int64(len(m.orders)), nil
}
func (m *memOrderRepo) Update(_ context.Context, o domain.Order) error {
	for i := range m.orders {
		if m.orders[i].ID == o.ID {
			m.orders[i] = o
			return nil
		}
	}
	return nil
}
func (m *memOrderRepo) GetOpen(_ context.Context) ([]domain.Order, error) {
	var out []domain.Order
	for _, o := range m.orders {
		if o.Status == domain.OrderStatusPending || o.Status == domain.OrderStatusPartiallyFilled {
			out = append(out, o)
		}
	}
	return out, nil
}
func (m *memOrderRepo) GetBySymbol(_ context.Context, _ string) ([]domain.Order, error) {
	return nil, nil
}
func (m *memOrderRepo) GetAll(_ context.Context, _ time.Time) ([]domain.Order, error) {
	return nil, nil
}

type memExecutionRepo struct {
	executions []domain.Execution
}

func (m *memExecutionRepo) Insert(_ context.Context, e domain.Execution) (int64, error) {
	m.executions = append(m.executions, e)
	return int64(len(m.executions)), nil
}
func (m *memExecutionRepo) GetByOrder(_ context.Context, orderID int64) ([]domain.Execution, error) {
	var out []domain.Execution
	for _, e := range m.executions {
		if e.OrderID == orderID {
			out = append(out, e)
		}
	}
	return out, nil
}
func (m *memExecutionRepo) GetAll(_ context.Context, _ time.Time) ([]domain.Execution, error) {
	return m.executions, nil
}

type memAccountSnapshotRepo struct {
	snapshots []domain.AccountState
}

func (m *memAccountSnapshotRepo) Insert(_ context.Context, a domain.AccountState) (int64, error) {
	m.snapshots = append(m.snapshots, a)
	return int64(len(m.snapshots)), nil
}
func (m *memAccountSnapshotRepo) GetLatest(_ context.Context) (*domain.AccountState, error) {
	if len(m.snapshots) == 0 {
		return nil, nil
	}
	return &m.snapshots[len(m.snapshots)-1], nil
}
func (m *memAccountSnapshotRepo) GetLatestBefore(_ context.Context, _ time.Time) (*domain.AccountState, error) {
	return nil, nil
}
func (m *memAccountSnapshotRepo) GetAll(_ context.Context, _ time.Time) ([]domain.AccountState, error) {
	return m.snapshots, nil
}

// e2eMarketData produces deterministic breakout setup candles.
type e2eMarketData struct {
	mockMarketData
}

func (m *e2eMarketData) GetOrderBookSummary(_ context.Context, _ string, targetNotional float64, _ string) (domain.OrderBookSummary, error) {
	return domain.OrderBookSummary{
		BestBid: 65000, BestAsk: 65001, SpreadBps: 1,
		BidDepth: 100000, AskDepth: 100000, EstimatedSlippageBps: 5,
		DepthToPositionSizeRatio: 10, Stale: false,
	}, nil
}

// newMockBybitServer creates an httptest server that mimics Bybit REST endpoints
// for universe scanning. This eliminates UNIVERSE_REFRESH_FAILED in E2E tests.
func newMockBybitServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "instruments-info"):
			json.NewEncoder(w).Encode(map[string]any{
				"retCode": 0,
				"result": map[string]any{
					"list": []map[string]any{
						{
							"symbol":      "BTCUSDT",
							"status":      "Trading",
							"baseCoin":    "BTC",
							"quoteCoin":   "USDT",
							"minOrderQty": "0.001",
							"tickSize":    "0.01",
							"qtyStep":     "0.001",
							"maxLeverage": "100",
						},
					},
				},
			})
		case strings.Contains(r.URL.Path, "tickers"):
			json.NewEncoder(w).Encode(map[string]any{
				"retCode": 0,
				"result": map[string]any{
					"list": []map[string]any{
						{
							"symbol":      "BTCUSDT",
							"turnover24h": "500000000",
							"lastPrice":   "65000",
							"bid1Price":   "64999",
							"ask1Price":   "65001",
						},
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestEndToEnd_PaperTradingCycle runs a full scheduler cycle with repository-backed persistence.
func TestEndToEnd_PaperTradingCycle(t *testing.T) {
	ctx := context.Background()
	log := logger.New(nil, logger.LevelDebug)

	cfg := app.UserConfig{
		App: app.AppConfig{
			Mode:                 "paper",
			CycleIntervalSeconds: 900,
		},
		Universe: app.UniverseConfig{
			Mode: "all_usdt_perpetual",
			Filters: app.UniverseFiltersConfig{
				MaxSpreadBps: 50,
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
			Mode:               "dynamic",
			MinCandidateScore:  1.0,
			RequireExecutionOk: false,
			RequireLiquidityOk: false,
		},
		PortfolioRisk: app.PortfolioRiskConfig{
			MaxOpenPositions:          5,
			MaxNewPositionsPerCycle:   2,
			MaxTotalExposureUSD:       100000,
			MaxTotalMarginUsedPct:     50.0,
			MaxRiskPerTradePct:        1.0,
			MaxDailyLossPct:           3.0,
			MaxLeverage:               10.0,
			MinNotionalUSD:            10.0,
			MarginPerTradeUSD:         100.0,
			MaxSameDirectionPositions: 3,
		},
		Sizing: app.SizingConfig{
			Method:             "fixed_margin",
			MarginPerTradeUSD:  100.0,
			MaxLeverage:        10.0,
			MaxRiskPerTradePct: 1.0,
		},
		Broker: app.BrokerConfig{
			Provider: "paper",
			Paper: app.PaperConfig{
				StartingBalanceUSD: 10000,
				FeeMakerBps:        2,
				FeeTakerBps:        5.5,
				SlippageBps:        5,
				DefaultLeverage:    10,
			},
		},
		LLM: app.LLMConfig{Enabled: false},
	}

	bybitSrv := newMockBybitServer()
	defer bybitSrv.Close()

	md := &e2eMarketData{}

	universeRepo := &mockUniverseRepo{
		symbols: []domain.UniverseSymbol{
			{SymbolInfo: domain.SymbolInfo{Symbol: "BTCUSDT", Status: "Trading", QuoteAsset: "USDT", MinNotional: 10}},
		},
	}
	candleRepo := &mockCandleRepo{}

	universeScanner := universe.NewScanner(cfg.Universe, cfg.Strategy, cfg.LLMRouting, bybitSrv.URL, md, universeRepo, candleRepo, log, cfg.ComputeTargetNotional())
	screenerSvc := screener.NewScreener(cfg, universeScanner, md, log)

	pb := broker.NewPaperBroker(cfg.Broker.Paper, log)
	pb.UpdatePrice("BTCUSDT", 65000)

	// Wire in-memory repositories for persistence verification
	posRepo := &memPositionRepo{}
	ordRepo := &memOrderRepo{}
	execRepo := &memExecutionRepo{}
	snapRepo := &memAccountSnapshotRepo{}
	pb.SetPositionRepo(posRepo)
	pb.SetOrderRepo(ordRepo)
	pb.SetExecutionRepo(execRepo)
	pb.SetAccountSnapshotRepo(snapRepo)

	riskEng := risk.NewEngine(cfg)
	exec := executor.NewExecutor(pb, log)
	mon := monitor.NewMonitor(pb, md, cfg.PortfolioRisk, log)

	// Mock LLM always returns ALLOW_MARKET
	llmClient := llm.NewMockClient(domain.LLMDecision{
		Decision:       "ALLOW_MARKET",
		Confidence:     0.9,
		SizeMultiplier: 1.0,
	}, nil)

	sched := NewScheduler(cfg, screenerSvc, llmClient, riskEng, exec, mon, md, log)

	cycleRepo := &mockCycleRepo{}
	candidateRepo := &mockCandidateRepo{}
	llmDecisionRepo := &mockLLMDecisionRepo{}

	sched.SetCycleRepo(cycleRepo)
	sched.SetCandidateRepo(candidateRepo)
	sched.SetLLMDecisionRepo(llmDecisionRepo)

	result, err := sched.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	// 0. Universe refresh must not fail
	for _, rc := range result.ReasonCodes {
		if rc == "UNIVERSE_REFRESH_FAILED" {
			t.Fatal("unexpected UNIVERSE_REFRESH_FAILED in result")
		}
	}

	// 1. At least one candidate became LLM eligible
	if result.Candidates == 0 {
		t.Fatal("expected at least one candidate")
	}

	// 2. Mock LLM was called
	if result.LLMCalls == 0 {
		t.Fatal("expected at least one LLM call")
	}

	// 3. Execution occurred
	if result.Executions == 0 {
		t.Fatalf("expected at least one execution, skips: %v", result.Skips)
	}

	// 4. PaperBroker created position
	openPositions, _ := pb.GetOpenPositions(ctx)
	if len(openPositions) == 0 {
		t.Fatal("expected open position")
	}
	pos := openPositions[0]

	// 5. Protective SL/TP orders exist
	openOrders, _ := pb.GetOpenOrders(ctx)
	var hasSL, hasTP bool
	for _, o := range openOrders {
		if o.Symbol == pos.Symbol {
			if o.OrderType == domain.OrderTypeStopMarket {
				hasSL = true
			}
			if o.OrderType == domain.OrderTypeTakeProfitMarket {
				hasTP = true
			}
		}
	}
	if !hasSL {
		t.Fatal("expected protective SL order to exist")
	}
	if !hasTP {
		t.Fatal("expected protective TP order to exist")
	}

	// 6. No position exists without SL
	if pos.StopLoss <= 0 {
		t.Fatal("position must have StopLoss")
	}

	// 7. Execution persisted
	if len(execRepo.executions) == 0 {
		t.Fatal("expected execution to be persisted")
	}

	// 8. Account snapshot persisted
	if len(snapRepo.snapshots) == 0 {
		t.Fatal("expected account snapshot to be persisted")
	}

	// 9. Cycle persisted
	if len(cycleRepo.cycles) == 0 {
		t.Fatal("expected cycle to be persisted")
	}
	cycle := cycleRepo.cycles[0]
	if cycle.CycleID != result.CycleID {
		t.Errorf("expected cycle_id %s, got %s", result.CycleID, cycle.CycleID)
	}

	// 10. Candidate persisted
	if len(candidateRepo.candidates) == 0 {
		t.Fatal("expected candidates to be persisted")
	}
	var foundEligible bool
	for _, c := range candidateRepo.candidates {
		if c.LLMEligible {
			foundEligible = true
			break
		}
	}
	if !foundEligible {
		t.Fatal("expected at least one LLM-eligible candidate to be persisted")
	}

	// 11. LLM decision persisted with candidate_id
	if len(llmDecisionRepo.decisions) == 0 {
		t.Fatal("expected LLM decisions to be persisted")
	}
	rec := llmDecisionRepo.decisions[0]
	if rec.candidateID <= 0 {
		t.Errorf("expected positive candidate_id in LLM decision, got %d", rec.candidateID)
	}
	if rec.cycleID != result.CycleID {
		t.Errorf("expected cycle_id %s in LLM decision, got %s", result.CycleID, rec.cycleID)
	}
	if rec.decision.Decision != "ALLOW_MARKET" {
		t.Errorf("expected ALLOW_MARKET in persisted decision, got %s", rec.decision.Decision)
	}
}

// TestEndToEnd_Postgres runs a full scheduler cycle with real Postgres repositories.
// Skipped unless BOTSURV_TEST_POSTGRES_DSN is set.
func TestEndToEnd_Postgres(t *testing.T) {
	dsn := os.Getenv("BOTSURV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BOTSURV_TEST_POSTGRES_DSN not set; skipping Postgres E2E test")
	}

	ctx := context.Background()
	log := logger.New(nil, logger.LevelDebug)

	// Setup real Postgres connection with isolated schema
	database, err := db.Open("postgres", dsn, 1, 1, 300)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	database.SetMaxOpenConns(1)

	schema := fmt.Sprintf("botsurv_e2e_%d", time.Now().UnixNano())
	if _, err := database.Exec(`CREATE SCHEMA ` + schema); err != nil {
		_ = database.Close()
		t.Fatalf("create schema: %v", err)
	}
	if _, err := database.Exec(`SET search_path TO ` + schema); err != nil {
		_ = database.Close()
		t.Fatalf("set search_path: %v", err)
	}
	defer func() {
		_, _ = database.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = database.Close()
	}()

	if err := db.Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	repos := db.NewPostgresRepositories(database)

	cfg := app.UserConfig{
		App: app.AppConfig{
			Mode:                 "paper",
			CycleIntervalSeconds: 900,
		},
		Universe: app.UniverseConfig{
			Mode: "all_usdt_perpetual",
			Filters: app.UniverseFiltersConfig{
				MaxSpreadBps: 50,
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
			Mode:               "dynamic",
			MinCandidateScore:  1.0,
			RequireExecutionOk: false,
			RequireLiquidityOk: false,
		},
		PortfolioRisk: app.PortfolioRiskConfig{
			MaxOpenPositions:          5,
			MaxNewPositionsPerCycle:   2,
			MaxTotalExposureUSD:       100000,
			MaxTotalMarginUsedPct:     50.0,
			MaxRiskPerTradePct:        1.0,
			MaxDailyLossPct:           3.0,
			MaxLeverage:               10.0,
			MinNotionalUSD:            10.0,
			MarginPerTradeUSD:         100.0,
			MaxSameDirectionPositions: 3,
		},
		Sizing: app.SizingConfig{
			Method:             "fixed_margin",
			MarginPerTradeUSD:  100.0,
			MaxLeverage:        10.0,
			MaxRiskPerTradePct: 1.0,
		},
		Broker: app.BrokerConfig{
			Provider: "paper",
			Paper: app.PaperConfig{
				StartingBalanceUSD: 10000,
				FeeMakerBps:        2,
				FeeTakerBps:        5.5,
				SlippageBps:        5,
				DefaultLeverage:    10,
			},
		},
		LLM: app.LLMConfig{Enabled: false},
	}

	bybitSrv := newMockBybitServer()
	defer bybitSrv.Close()

	md := &e2eMarketData{}

	// Pre-seed universe with BTCUSDT
	if err := repos.UniverseRepository.InsertOrUpdate(ctx, domain.UniverseSymbol{
		SymbolInfo: domain.SymbolInfo{Symbol: "BTCUSDT", Status: "Trading", QuoteAsset: "USDT", MinNotional: 10},
	}); err != nil {
		t.Fatalf("seed universe: %v", err)
	}

	candleRepo := &mockCandleRepo{}
	universeScanner := universe.NewScanner(cfg.Universe, cfg.Strategy, cfg.LLMRouting, bybitSrv.URL, md, repos.UniverseRepository, candleRepo, log, cfg.ComputeTargetNotional())
	screenerSvc := screener.NewScreener(cfg, universeScanner, md, log)

	pb := broker.NewPaperBroker(cfg.Broker.Paper, log)
	pb.UpdatePrice("BTCUSDT", 65000)

	pb.SetPositionRepo(repos.PositionRepository)
	pb.SetOrderRepo(repos.OrderRepository)
	pb.SetExecutionRepo(repos.ExecutionRepository)
	pb.SetAccountSnapshotRepo(repos.AccountSnapshotRepository)

	riskEng := risk.NewEngine(cfg)
	exec := executor.NewExecutor(pb, log)
	mon := monitor.NewMonitor(pb, md, cfg.PortfolioRisk, log)

	llmClient := llm.NewMockClient(domain.LLMDecision{
		Decision:       "ALLOW_MARKET",
		Confidence:     0.9,
		SizeMultiplier: 1.0,
	}, nil)

	sched := NewScheduler(cfg, screenerSvc, llmClient, riskEng, exec, mon, md, log)
	sched.SetCycleRepo(repos.CycleRepository)
	sched.SetCandidateRepo(repos.CandidateRepository)
	sched.SetLLMDecisionRepo(repos.LLMDecisionRepository)
	sched.SetRiskDecisionRepo(repos.RiskDecisionRepository)
	sched.SetLLMUsageRepo(repos.LLMUsageRepository)

	result, err := sched.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected result")
	}

	// 0. Universe refresh must not fail
	for _, rc := range result.ReasonCodes {
		if rc == "UNIVERSE_REFRESH_FAILED" {
			t.Fatal("unexpected UNIVERSE_REFRESH_FAILED in result")
		}
	}

	if result.Candidates == 0 {
		t.Fatal("expected at least one candidate")
	}
	if result.LLMCalls == 0 {
		t.Fatal("expected at least one LLM call")
	}
	if result.Executions == 0 {
		t.Fatalf("expected at least one execution, skips: %v", result.Skips)
	}

	// --- Assert Postgres persistence ---

	// 1. Cycle persisted
	latestCycle, err := repos.CycleRepository.GetLatest(ctx)
	if err != nil {
		t.Fatalf("get latest cycle: %v", err)
	}
	if latestCycle == nil {
		t.Fatal("expected cycle to be persisted in Postgres")
	}
	if latestCycle.CycleID != result.CycleID {
		t.Errorf("expected cycle_id %s, got %s", result.CycleID, latestCycle.CycleID)
	}

	// 2. Candidates persisted with LLM-eligible record
	candidates, err := repos.CandidateRepository.GetByCycle(ctx, result.CycleID)
	if err != nil {
		t.Fatalf("get candidates: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected candidates to be persisted in Postgres")
	}
	var foundEligible bool
	for _, c := range candidates {
		if c.LLMEligible {
			foundEligible = true
			break
		}
	}
	if !foundEligible {
		t.Fatal("expected at least one LLM-eligible candidate in Postgres")
	}

	// 3. LLM decisions persisted with positive candidate_id
	llmDecisions, err := repos.LLMDecisionRepository.GetByCycle(ctx, result.CycleID)
	if err != nil {
		t.Fatalf("get llm decisions: %v", err)
	}
	if len(llmDecisions) == 0 {
		t.Fatal("expected LLM decisions to be persisted in Postgres")
	}
	var foundDecision bool
	for _, d := range llmDecisions {
		if d.CandidateID > 0 {
			foundDecision = true
			break
		}
	}
	if !foundDecision {
		t.Fatal("expected at least one LLM decision with positive candidate_id in Postgres")
	}

	// 4. Risk decisions persisted with final approval
	riskDecisions, err := repos.RiskDecisionRepository.GetByCycle(ctx, result.CycleID)
	if err != nil {
		t.Fatalf("get risk decisions: %v", err)
	}
	if len(riskDecisions) == 0 {
		t.Fatal("expected risk decisions to be persisted in Postgres")
	}
	var foundApprovedRisk bool
	for _, d := range riskDecisions {
		if d.CandidateID > 0 && d.Approved {
			foundApprovedRisk = true
			break
		}
	}
	if !foundApprovedRisk {
		t.Fatal("expected at least one approved risk decision with candidate_id in Postgres")
	}

	usageDate, _ := time.Parse("2006-01-02", time.Now().Format("2006-01-02"))
	usage, err := repos.LLMUsageRepository.Get(ctx, usageDate)
	if err != nil {
		t.Fatalf("get llm usage: %v", err)
	}
	if usage == nil || usage.Calls == 0 {
		t.Fatalf("expected persisted LLM usage calls, got %#v", usage)
	}

	// 5. Orders persisted: MARKET, STOP_MARKET, TAKE_PROFIT_MARKET
	allOrders, err := repos.OrderRepository.GetAll(ctx, time.Time{})
	if err != nil {
		t.Fatalf("get all orders: %v", err)
	}
	var hasMarket, hasStopMarket, hasTakeProfitMarket bool
	for _, o := range allOrders {
		switch o.OrderType {
		case domain.OrderTypeMarket:
			hasMarket = true
		case domain.OrderTypeStopMarket:
			hasStopMarket = true
		case domain.OrderTypeTakeProfitMarket:
			hasTakeProfitMarket = true
		}
	}
	if !hasMarket {
		t.Fatal("expected MARKET order persisted in Postgres")
	}
	if !hasStopMarket {
		t.Fatal("expected STOP_MARKET order persisted in Postgres")
	}
	if !hasTakeProfitMarket {
		t.Fatal("expected TAKE_PROFIT_MARKET order persisted in Postgres")
	}

	// 6. Open position persisted
	openPositions, err := repos.PositionRepository.GetOpen(ctx)
	if err != nil {
		t.Fatalf("get open positions: %v", err)
	}
	if len(openPositions) == 0 {
		t.Fatal("expected open position persisted in Postgres")
	}

	// 7. Execution persisted
	executions, err := repos.ExecutionRepository.GetAll(ctx, time.Time{})
	if err != nil {
		t.Fatalf("get executions: %v", err)
	}
	if len(executions) == 0 {
		t.Fatal("expected execution persisted in Postgres")
	}

	// 8. Account snapshot persisted
	snapshots, err := repos.AccountSnapshotRepository.GetAll(ctx, time.Time{})
	if err != nil {
		t.Fatalf("get account snapshots: %v", err)
	}
	if len(snapshots) == 0 {
		t.Fatal("expected account snapshot persisted in Postgres")
	}
}
