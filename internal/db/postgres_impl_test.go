package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

const defaultTestPostgresDSN = "postgres://botsurv:botsurv@localhost:5432/botsurv?sslmode=disable"

func setupTestPostgres(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("BOTSURV_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = defaultTestPostgresDSN
	}

	database, err := Open("postgres", dsn, 1, 1, 300)
	if err != nil {
		t.Skipf("skip postgres integration test: %v\nset BOTSURV_TEST_POSTGRES_DSN to override the default DSN %q", err, defaultTestPostgresDSN)
	}
	database.SetMaxOpenConns(1)

	schema := fmt.Sprintf("botsurv_test_%d", time.Now().UnixNano())
	if _, err := database.Exec(`CREATE SCHEMA ` + schema); err != nil {
		_ = database.Close()
		t.Fatalf("create test schema: %v", err)
	}
	if _, err := database.Exec(`SET search_path TO ` + schema); err != nil {
		_ = database.Close()
		t.Fatalf("set search_path: %v", err)
	}

	t.Cleanup(func() {
		_, _ = database.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
		_ = database.Close()
	})

	return database
}

func TestOpenRejectsNonPostgresDriver(t *testing.T) {
	database, err := Open("mysql", "unused", 0, 0, 0)
	if err == nil {
		_ = database.Close()
		t.Fatal("expected non-postgres driver to be rejected")
	}
	if !strings.Contains(err.Error(), "only postgres is supported") {
		t.Fatalf("expected postgres-only error, got %v", err)
	}
}

func TestMigratePostgres(t *testing.T) {
	database := setupTestPostgres(t)
	tmpDir := t.TempDir()
	migrationPath := filepath.Join(tmpDir, "001_test.sql")
	if err := os.WriteFile(migrationPath, []byte("CREATE TABLE test_migration (id BIGSERIAL PRIMARY KEY);"), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}

	if err := Migrate(database, tmpDir); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}

	var exists bool
	if err := database.QueryRow(`SELECT to_regclass('test_migration') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("check table: %v", err)
	}
	if !exists {
		t.Fatal("expected test_migration table to exist")
	}
}

func TestMigrateIdempotent(t *testing.T) {
	database := setupTestPostgres(t)
	tmpDir := t.TempDir()
	migrationPath := filepath.Join(tmpDir, "001_test.sql")
	if err := os.WriteFile(migrationPath, []byte("CREATE TABLE test_migration (id BIGSERIAL PRIMARY KEY);"), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}

	if err := Migrate(database, tmpDir); err != nil {
		t.Fatalf("first migrate failed: %v", err)
	}
	if err := Migrate(database, tmpDir); err != nil {
		t.Fatalf("second migrate failed: %v", err)
	}
}

func TestMigrateInvalidFilename(t *testing.T) {
	database := setupTestPostgres(t)
	tmpDir := t.TempDir()
	migrationPath := filepath.Join(tmpDir, "bad_name.sql")
	if err := os.WriteFile(migrationPath, []byte("CREATE TABLE bad (id INT);"), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}

	if err := Migrate(database, tmpDir); err == nil {
		t.Fatal("expected error for invalid migration filename")
	}
}

func TestMigrateNumericOrdering(t *testing.T) {
	database := setupTestPostgres(t)
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "001_first.sql"), []byte("CREATE TABLE first (id INT);"), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "010_second.sql"), []byte("CREATE TABLE second (id INT);"), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "002_third.sql"), []byte("CREATE TABLE third (id INT);"), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}

	if err := Migrate(database, tmpDir); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}

	var firstExists, secondExists, thirdExists bool
	database.QueryRow(`SELECT to_regclass('first') IS NOT NULL`).Scan(&firstExists)
	database.QueryRow(`SELECT to_regclass('second') IS NOT NULL`).Scan(&secondExists)
	database.QueryRow(`SELECT to_regclass('third') IS NOT NULL`).Scan(&thirdExists)
	if !firstExists || !secondExists || !thirdExists {
		t.Fatalf("expected all tables to exist: first=%v second=%v third=%v", firstExists, secondExists, thirdExists)
	}
}

func TestMigrateDuplicatePrefix(t *testing.T) {
	database := setupTestPostgres(t)
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "001_first.sql"), []byte("CREATE TABLE first_dup (id INT);"), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "001_second.sql"), []byte("CREATE TABLE second_dup (id INT);"), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}

	if err := Migrate(database, tmpDir); err == nil {
		t.Fatal("expected error for duplicate migration prefix")
	}
}

func TestMigrateChecksumMismatch(t *testing.T) {
	database := setupTestPostgres(t)
	tmpDir := t.TempDir()
	migrationPath := filepath.Join(tmpDir, "001_test.sql")
	if err := os.WriteFile(migrationPath, []byte("CREATE TABLE checksum_test (id INT);"), 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}

	if err := Migrate(database, tmpDir); err != nil {
		t.Fatalf("first migrate failed: %v", err)
	}

	// tamper with the migration file
	if err := os.WriteFile(migrationPath, []byte("CREATE TABLE checksum_test_tampered (id INT);"), 0o644); err != nil {
		t.Fatalf("tamper migration: %v", err)
	}

	if err := Migrate(database, tmpDir); err == nil {
		t.Fatal("expected error for tampered migration checksum mismatch")
	}
}

func TestMigrateBackwardCompatibleNoChecksumColumn(t *testing.T) {
	database := setupTestPostgres(t)

	// Simulate old schema_migrations table without checksum column
	if _, err := database.Exec(`CREATE TABLE schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		t.Fatalf("create old schema_migrations table: %v", err)
	}

	tmpDir := t.TempDir()
	migrationPath := filepath.Join(tmpDir, "001_legacy.sql")
	content := []byte("CREATE TABLE legacy_test (id INT);")
	if err := os.WriteFile(migrationPath, content, 0o644); err != nil {
		t.Fatalf("write migration: %v", err)
	}

	// Pre-insert the migration record (as if it was already applied in the old system)
	if _, err := database.Exec(`INSERT INTO schema_migrations (filename) VALUES ($1)`, "001_legacy.sql"); err != nil {
		t.Fatalf("insert legacy migration record: %v", err)
	}

	// Migrate should add checksum column, backfill empty checksum, and succeed
	if err := Migrate(database, tmpDir); err != nil {
		t.Fatalf("migrate with legacy table failed: %v", err)
	}

	// Verify checksum was backfilled
	var cs string
	if err := database.QueryRow(`SELECT checksum FROM schema_migrations WHERE filename = $1`, "001_legacy.sql").Scan(&cs); err != nil {
		t.Fatalf("get backfilled checksum: %v", err)
	}
	if cs == "" {
		t.Fatal("expected checksum to be backfilled, got empty string")
	}

	// Running Migrate again should be idempotent
	if err := Migrate(database, tmpDir); err != nil {
		t.Fatalf("second migrate after backfill failed: %v", err)
	}
}

func TestPostgresCandleRepository(t *testing.T) {
	database := setupTestPostgres(t)
	if err := Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewPostgresRepositories(database)
	ctx := context.Background()

	c := domain.Candle{Symbol: "BTCUSDT", Timeframe: "15m", OpenTime: 1, Open: 100, High: 110, Low: 90, Close: 105, Volume: 10, Confirmed: true}
	id, err := repos.CandleRepository.Insert(ctx, c)
	if err != nil {
		t.Fatalf("insert candle: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// duplicate insert should return id, not error
	id2, err := repos.CandleRepository.Insert(ctx, c)
	if err != nil {
		t.Fatalf("insert duplicate candle: %v", err)
	}
	if id2 != id {
		t.Errorf("expected same id on conflict update, got %d vs %d", id2, id)
	}

	candles, err := repos.CandleRepository.GetBySymbolTimeframe(ctx, "BTCUSDT", "15m", 10)
	if err != nil {
		t.Fatalf("get candles: %v", err)
	}
	if len(candles) != 1 {
		t.Fatalf("expected 1 candle, got %d", len(candles))
	}
	if candles[0].Close != 105 {
		t.Errorf("expected close 105, got %f", candles[0].Close)
	}
}

func TestPostgresCandleRepository_LatestNAscending(t *testing.T) {
	database := setupTestPostgres(t)
	if err := Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewPostgresRepositories(database)
	ctx := context.Background()

	for i := int64(1); i <= 5; i++ {
		c := domain.Candle{Symbol: "ETHUSDT", Timeframe: "1H", OpenTime: i * 1000, Open: float64(i), High: float64(i), Low: float64(i), Close: float64(i), Volume: float64(i), Confirmed: true}
		if _, err := repos.CandleRepository.Insert(ctx, c); err != nil {
			t.Fatalf("insert candle %d: %v", i, err)
		}
	}

	candles, err := repos.CandleRepository.GetBySymbolTimeframe(ctx, "ETHUSDT", "1H", 3)
	if err != nil {
		t.Fatalf("get candles: %v", err)
	}
	if len(candles) != 3 {
		t.Fatalf("expected 3 candles, got %d", len(candles))
	}
	// Must be latest 3 (open_time 3000, 4000, 5000) in ascending order
	expected := []int64{3000, 4000, 5000}
	for i, c := range candles {
		if c.OpenTime != expected[i] {
			t.Errorf("candle[%d].OpenTime: expected %d, got %d", i, expected[i], c.OpenTime)
		}
		if c.Close != float64(expected[i]/1000) {
			t.Errorf("candle[%d].Close: expected %f, got %f", i, float64(expected[i]/1000), c.Close)
		}
	}
}

func TestPostgresCandleRepository_GetEmpty(t *testing.T) {
	database := setupTestPostgres(t)
	if err := Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewPostgresRepositories(database)
	ctx := context.Background()

	candles, err := repos.CandleRepository.GetBySymbolTimeframe(ctx, "UNKNOWN", "15m", 10)
	if err != nil {
		t.Fatalf("get candles: %v", err)
	}
	if len(candles) != 0 {
		t.Fatalf("expected 0 candles, got %d", len(candles))
	}
}

func TestPostgresUniverseRepository(t *testing.T) {
	database := setupTestPostgres(t)
	if err := Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewPostgresRepositories(database)
	ctx := context.Background()

	s := domain.UniverseSymbol{SymbolInfo: domain.SymbolInfo{Symbol: "ETHUSDT", Status: "Trading", QuoteAsset: "USDT"}, LiquidityScore: 85}
	if err := repos.UniverseRepository.InsertOrUpdate(ctx, s); err != nil {
		t.Fatalf("insert universe: %v", err)
	}

	all, err := repos.UniverseRepository.GetAll(ctx)
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(all))
	}

	found, err := repos.UniverseRepository.GetBySymbol(ctx, "ETHUSDT")
	if err != nil {
		t.Fatalf("get by symbol: %v", err)
	}
	if found == nil {
		t.Fatal("expected to find ETHUSDT")
	}
	if found.LiquidityScore != 85 {
		t.Errorf("expected liquidity score 85, got %f", found.LiquidityScore)
	}

	// update existing
	s.LiquidityScore = 90
	if err := repos.UniverseRepository.InsertOrUpdate(ctx, s); err != nil {
		t.Fatalf("update universe: %v", err)
	}
	found, err = repos.UniverseRepository.GetBySymbol(ctx, "ETHUSDT")
	if err != nil {
		t.Fatalf("get by symbol after update: %v", err)
	}
	if found.LiquidityScore != 90 {
		t.Errorf("expected updated liquidity score 90, got %f", found.LiquidityScore)
	}
}

func TestPostgresUniverseRepository_GetBySymbol_NotFound(t *testing.T) {
	database := setupTestPostgres(t)
	if err := Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewPostgresRepositories(database)
	ctx := context.Background()

	found, err := repos.UniverseRepository.GetBySymbol(ctx, "NOTFOUND")
	if err != nil {
		t.Fatalf("get by symbol: %v", err)
	}
	if found != nil {
		t.Fatal("expected nil for not-found symbol")
	}
}

func TestPostgresCycleRepository(t *testing.T) {
	database := setupTestPostgres(t)
	if err := Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewPostgresRepositories(database)
	ctx := context.Background()

	c := domain.Cycle{CycleID: "cycle-1", StartedAt: time.Now(), Status: "running", ReasonCodes: []string{"NO_CANDIDATE"}}
	id, err := repos.CycleRepository.Insert(ctx, c)
	if err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	latest, err := repos.CycleRepository.GetLatest(ctx)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest cycle")
	}
	if latest.CycleID != "cycle-1" {
		t.Errorf("expected cycle-1, got %s", latest.CycleID)
	}
	if len(latest.ReasonCodes) != 1 || latest.ReasonCodes[0] != "NO_CANDIDATE" {
		t.Errorf("expected reason codes [NO_CANDIDATE], got %v", latest.ReasonCodes)
	}
}

func TestPostgresCycleRepository_GetLatest_Empty(t *testing.T) {
	database := setupTestPostgres(t)
	if err := Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewPostgresRepositories(database)
	ctx := context.Background()

	latest, err := repos.CycleRepository.GetLatest(ctx)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if latest != nil {
		t.Fatal("expected nil for empty cycles")
	}
}

func TestPostgresCandidateRepository(t *testing.T) {
	database := setupTestPostgres(t)
	if err := Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewPostgresRepositories(database)
	ctx := context.Background()

	c := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			Symbol:             "BTCUSDT",
			Side:               domain.SideLong,
			SetupType:          "breakout",
			Regime:             "trend_up",
			EntryType:          domain.EntryTypeMarket,
			ProposedEntry:      50000,
			ProposedStopLoss:   49000,
			ProposedTakeProfit: 52000,
			StopLossPct:        2.0,
			TakeProfitPct:      4.0,
			RR:                 2.5,
			InvalidationLevel:  48900,
			SetupScore:         85,
			ReasonCodes:        []string{"BREAKOUT_CONFIRMED"},
		},
		CycleID:               "cycle-1",
		CandidateScore:        82,
		LiquidityScore:        80,
		ExecutionScore:        75,
		VolatilityScore:       70,
		LLMEligible:           true,
		LLMRoutingReasonCodes: []string{"SCORE_ABOVE_THRESHOLD"},
	}
	id, err := repos.CandidateRepository.Insert(ctx, c)
	if err != nil {
		t.Fatalf("insert candidate: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	candidates, err := repos.CandidateRepository.GetByCycle(ctx, "cycle-1")
	if err != nil {
		t.Fatalf("get by cycle: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	got := candidates[0]
	if got.CandidateScore != 82 {
		t.Errorf("expected candidate_score 82, got %f", got.CandidateScore)
	}
	if got.LiquidityScore != 80 {
		t.Errorf("expected liquidity_score 80, got %f", got.LiquidityScore)
	}
	if got.ExecutionScore != 75 {
		t.Errorf("expected execution_score 75, got %f", got.ExecutionScore)
	}
	if got.SetupScore != 85 {
		t.Errorf("expected setup_score 85, got %f", got.SetupScore)
	}
	if got.VolatilityScore != 70 {
		t.Errorf("expected volatility_score 70, got %f", got.VolatilityScore)
	}
	if !got.LLMEligible {
		t.Error("expected llm_eligible true")
	}
	if got.EntryType != domain.EntryTypeMarket {
		t.Errorf("expected entry_type MARKET, got %s", got.EntryType)
	}
	if got.ProposedEntry != 50000 {
		t.Errorf("expected proposed_entry 50000, got %f", got.ProposedEntry)
	}
	if got.ProposedStopLoss != 49000 {
		t.Errorf("expected proposed_stop_loss 49000, got %f", got.ProposedStopLoss)
	}
	if got.ProposedTakeProfit != 52000 {
		t.Errorf("expected proposed_take_profit 52000, got %f", got.ProposedTakeProfit)
	}
	if got.StopLossPct != 2.0 {
		t.Errorf("expected stop_loss_pct 2.0, got %f", got.StopLossPct)
	}
	if got.TakeProfitPct != 4.0 {
		t.Errorf("expected take_profit_pct 4.0, got %f", got.TakeProfitPct)
	}
	if got.RR != 2.5 {
		t.Errorf("expected rr 2.5, got %f", got.RR)
	}
	if got.InvalidationLevel != 48900 {
		t.Errorf("expected invalidation_level 48900, got %f", got.InvalidationLevel)
	}
	if len(got.LLMRoutingReasonCodes) != 1 || got.LLMRoutingReasonCodes[0] != "SCORE_ABOVE_THRESHOLD" {
		t.Errorf("expected routing reason codes [SCORE_ABOVE_THRESHOLD], got %v", got.LLMRoutingReasonCodes)
	}
	if len(got.ReasonCodes) != 1 || got.ReasonCodes[0] != "BREAKOUT_CONFIRMED" {
		t.Errorf("expected reason codes [BREAKOUT_CONFIRMED], got %v", got.ReasonCodes)
	}
}

func TestPostgresCandidateRepository_Multiple(t *testing.T) {
	database := setupTestPostgres(t)
	if err := Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewPostgresRepositories(database)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		c := domain.Candidate{
			ProposedTrade: domain.ProposedTrade{
				Symbol:    fmt.Sprintf("SYM%d", i),
				Side:      domain.SideLong,
				SetupType: "breakout",
			},
			CycleID:        "cycle-multi",
			CandidateScore: float64(80 + i),
		}
		if _, err := repos.CandidateRepository.Insert(ctx, c); err != nil {
			t.Fatalf("insert candidate %d: %v", i, err)
		}
	}

	candidates, err := repos.CandidateRepository.GetByCycle(ctx, "cycle-multi")
	if err != nil {
		t.Fatalf("get by cycle: %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}
}

func TestPostgresCandidateRepository_GetByCycle_Empty(t *testing.T) {
	database := setupTestPostgres(t)
	if err := Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewPostgresRepositories(database)
	ctx := context.Background()

	candidates, err := repos.CandidateRepository.GetByCycle(ctx, "no-such-cycle")
	if err != nil {
		t.Fatalf("get by cycle: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected 0 candidates, got %d", len(candidates))
	}
}

func TestPostgresOrderRepository_TakeProfitMarket(t *testing.T) {
	database := setupTestPostgres(t)
	if err := Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewPostgresRepositories(database)
	ctx := context.Background()

	o := domain.Order{
		Symbol:     "BTCUSDT",
		Side:       domain.OrderSideBuy,
		OrderType:  domain.OrderTypeTakeProfitMarket,
		Qty:        0.001,
		StopPrice:  floatPtr(70000),
		Status:     domain.OrderStatusPending,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		IntendedSL: 64000,
		IntendedTP: 70000,
	}

	id, err := repos.OrderRepository.Insert(ctx, o)
	if err != nil {
		t.Fatalf("insert TAKE_PROFIT_MARKET order: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	// Verify round-trip
	all, err := repos.OrderRepository.GetAll(ctx, time.Time{})
	if err != nil {
		t.Fatalf("get all orders: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 order, got %d", len(all))
	}
	if all[0].OrderType != domain.OrderTypeTakeProfitMarket {
		t.Errorf("expected order_type TAKE_PROFIT_MARKET, got %s", all[0].OrderType)
	}
	if all[0].IntendedSL != 64000 || all[0].IntendedTP != 70000 {
		t.Errorf("expected intended SL/TP round-trip, got %.2f/%.2f", all[0].IntendedSL, all[0].IntendedTP)
	}
}

func TestPostgresRiskDecisionRepository(t *testing.T) {
	database := setupTestPostgres(t)
	if err := Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewPostgresRepositories(database)
	ctx := context.Background()

	id, err := repos.RiskDecisionRepository.Insert(ctx, domain.RiskDecision{
		Approved:              false,
		FinalPositionNotional: 50,
		RequiredMargin:        10,
		EstimatedLoss:         1,
		ReasonCodes:           []string{"PORTFOLIO_RISK_LIMIT"},
		PortfolioRank:         0,
		PortfolioRejectReason: "PORTFOLIO_RISK_LIMIT",
	}, 123, "cycle-risk")
	if err != nil {
		t.Fatalf("insert risk decision: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	decisions, err := repos.RiskDecisionRepository.GetByCycle(ctx, "cycle-risk")
	if err != nil {
		t.Fatalf("get risk decisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("expected 1 risk decision, got %d", len(decisions))
	}
	got := decisions[0]
	if got.CandidateID != 123 {
		t.Fatalf("expected candidate_id 123, got %d", got.CandidateID)
	}
	if got.Approved {
		t.Fatal("expected rejected risk decision")
	}
	if got.PortfolioRejectReason != "PORTFOLIO_RISK_LIMIT" {
		t.Fatalf("expected portfolio reject reason, got %s", got.PortfolioRejectReason)
	}
	if len(got.ReasonCodes) != 1 || got.ReasonCodes[0] != "PORTFOLIO_RISK_LIMIT" {
		t.Fatalf("expected reason code round-trip, got %v", got.ReasonCodes)
	}
}

func TestPostgresLLMUsageRepository(t *testing.T) {
	database := setupTestPostgres(t)
	if err := Migrate(database, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repos := NewPostgresRepositories(database)
	ctx := context.Background()
	usageDate := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)

	if err := repos.LLMUsageRepository.IncrementCalls(ctx, usageDate, 1, 0); err != nil {
		t.Fatalf("increment usage: %v", err)
	}
	if err := repos.LLMUsageRepository.IncrementCalls(ctx, usageDate, 2, 0); err != nil {
		t.Fatalf("increment usage second time: %v", err)
	}

	state, err := repos.LLMUsageRepository.Get(ctx, usageDate)
	if err != nil {
		t.Fatalf("get usage: %v", err)
	}
	if state == nil {
		t.Fatal("expected usage state")
	}
	if state.Calls != 3 {
		t.Fatalf("expected 3 calls, got %d", state.Calls)
	}
}

func floatPtr(f float64) *float64 {
	return &f
}
