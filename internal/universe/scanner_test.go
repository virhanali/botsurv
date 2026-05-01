package universe

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
)

// --- Mock MarketDataService ---

type mockMarketData struct {
	orderbooks map[string]*domain.OrderBookSummary
	candles    map[string][]domain.Candle
	prices     map[string]float64
}

func (m *mockMarketData) GetOrderBookSummary(_ context.Context, symbol string, _ float64, _ string) (domain.OrderBookSummary, error) {
	if ob, ok := m.orderbooks[symbol]; ok {
		return *ob, nil
	}
	return domain.OrderBookSummary{}, fmt.Errorf("no orderbook for %s", symbol)
}

func (m *mockMarketData) GetCandles(_ context.Context, symbol, timeframe string, limit int) ([]domain.Candle, error) {
	key := symbol + ":" + timeframe
	if c, ok := m.candles[key]; ok {
		if len(c) > limit {
			return c[len(c)-limit:], nil
		}
		return c, nil
	}
	return nil, nil
}

func (m *mockMarketData) GetLatestPrice(_ context.Context, symbol string) (float64, error) {
	if p, ok := m.prices[symbol]; ok {
		return p, nil
	}
	return 0, fmt.Errorf("no price for %s", symbol)
}

func (m *mockMarketData) Start(_ context.Context) error { return nil }
func (m *mockMarketData) Stop(_ context.Context) error  { return nil }
func (m *mockMarketData) IsHealthy(_ string) bool       { return true }
func (m *mockMarketData) LastUpdate(_ string) time.Time { return time.Now() }
func (m *mockMarketData) HealthStatus(_ context.Context, _ string) domain.MarketDataHealth {
	return domain.MarketDataHealth{Healthy: true}
}
func (m *mockMarketData) GetTradeFlow(_ context.Context, _ string) (*domain.TradeFlow, error) {
	return nil, nil
}

// --- Mock UniverseRepository ---

type mockUniverseRepo struct {
	symbols map[string]domain.UniverseSymbol
}

func newMockUniverseRepo() *mockUniverseRepo {
	return &mockUniverseRepo{symbols: make(map[string]domain.UniverseSymbol)}
}

func (m *mockUniverseRepo) InsertOrUpdate(_ context.Context, s domain.UniverseSymbol) error {
	m.symbols[s.Symbol] = s
	return nil
}

func (m *mockUniverseRepo) GetAll(_ context.Context) ([]domain.UniverseSymbol, error) {
	var result []domain.UniverseSymbol
	for _, s := range m.symbols {
		result = append(result, s)
	}
	return result, nil
}

func (m *mockUniverseRepo) GetBySymbol(_ context.Context, symbol string) (*domain.UniverseSymbol, error) {
	if s, ok := m.symbols[symbol]; ok {
		return &s, nil
	}
	return nil, nil
}

// --- Mock CandleRepository ---

type mockCandleRepo struct {
	candles []domain.Candle
}

func (m *mockCandleRepo) Insert(_ context.Context, _ domain.Candle) (int64, error) {
	return 1, nil
}

func (m *mockCandleRepo) GetBySymbolTimeframe(_ context.Context, _, _ string, limit int) ([]domain.Candle, error) {
	if len(m.candles) > limit {
		return m.candles[len(m.candles)-limit:], nil
	}
	return m.candles, nil
}

// --- Bybit REST Mock ---

func newBybitMockServer(instruments []bybitInstrument, tickers []bybitTicker) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/v5/market/instruments-info", func(w http.ResponseWriter, r *http.Request) {
		resp := bybitInstrumentsResponse{RetCode: 0}
		resp.Result.List = instruments
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/v5/market/tickers", func(w http.ResponseWriter, r *http.Request) {
		resp := bybitTickersResponse{RetCode: 0}
		resp.Result.List = tickers
		json.NewEncoder(w).Encode(resp)
	})
	return httptest.NewServer(mux)
}

func defaultTestConfig() (app.UniverseConfig, app.StrategyConfig, app.LLMRoutingConfig) {
	return app.UniverseConfig{
			LightScanMaxSymbols:     100,
			QualityFilterMaxSymbols: 30,
			SetupScanMaxSymbols:     10,
			Filters: app.UniverseFiltersConfig{
				Min24hVolumeUSD: 1_000_000,
				MaxSpreadBps:    50,
			},
			ExternalSignalWatchlist: app.ExternalSignalWatchlistConfig{
				Enabled:  true,
				TTLHours: 48,
			},
		}, app.StrategyConfig{
			Indicators: app.IndicatorsConfig{
				MinRR: 2.0,
			},
		}, app.LLMRoutingConfig{
			MinCandidateScore: 75,
		}
}

func defaultTestLogger() *logger.Logger {
	return logger.New(nil, logger.LevelDebug)
}

// --- Tests ---

func TestScanAll_FiltersByStatusAndQuote(t *testing.T) {
	server := newBybitMockServer(
		[]bybitInstrument{
			{Symbol: "BTCUSDT", Status: "Trading", QuoteCoin: "USDT", BaseCoin: "BTC", MaxLeverage: "100"},
			{Symbol: "ETHBTC", Status: "Trading", QuoteCoin: "BTC", BaseCoin: "ETH", MaxLeverage: "50"},
			{Symbol: "SOLUSDT", Status: "Unlisted", QuoteCoin: "USDT", BaseCoin: "SOL", MaxLeverage: "50"},
			{Symbol: "DOGEUSDT", Status: "Trading", QuoteCoin: "USDT", BaseCoin: "DOGE", MaxLeverage: "50"},
		},
		[]bybitTicker{
			{Symbol: "BTCUSDT", Turnover24h: "500000000", LastPrice: "65000", Bid1Price: "64999", Ask1Price: "65001"},
			{Symbol: "DOGEUSDT", Turnover24h: "2000000", LastPrice: "0.15", Bid1Price: "0.1499", Ask1Price: "0.1501"},
		},
	)
	defer server.Close()

	cfg, strategy, llmRoute := defaultTestConfig()
	universeRepo := newMockUniverseRepo()
	candleRepo := &mockCandleRepo{}
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, server.URL, nil, universeRepo, candleRepo, log, 0)
	result, err := scanner.ScanAll(context.Background())
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	// Should include BTCUSDT and DOGEUSDT (Trading + USDT + volume ok)
	// Should exclude ETHBTC (not USDT quote) and SOLUSDT (not Trading)
	symbols := make(map[string]bool)
	for _, s := range result {
		symbols[s.Symbol] = true
	}
	if !symbols["BTCUSDT"] {
		t.Error("expected BTCUSDT in result")
	}
	if !symbols["DOGEUSDT"] {
		t.Error("expected DOGEUSDT in result")
	}
	if symbols["ETHBTC"] {
		t.Error("ETHBTC should be excluded (not USDT quote)")
	}
	if symbols["SOLUSDT"] {
		t.Error("SOLUSDT should be excluded (not Trading)")
	}
}

func TestScanAll_FiltersByVolume(t *testing.T) {
	server := newBybitMockServer(
		[]bybitInstrument{
			{Symbol: "BTCUSDT", Status: "Trading", QuoteCoin: "USDT", BaseCoin: "BTC", MaxLeverage: "100"},
			{Symbol: "LOWVOLUSDT", Status: "Trading", QuoteCoin: "USDT", BaseCoin: "LOWVOL", MaxLeverage: "50"},
		},
		[]bybitTicker{
			{Symbol: "BTCUSDT", Turnover24h: "500000000", LastPrice: "65000", Bid1Price: "64999", Ask1Price: "65001"},
			{Symbol: "LOWVOLUSDT", Turnover24h: "500000", LastPrice: "1.0", Bid1Price: "0.99", Ask1Price: "1.01"},
		},
	)
	defer server.Close()

	cfg, strategy, llmRoute := defaultTestConfig()
	universeRepo := newMockUniverseRepo()
	candleRepo := &mockCandleRepo{}
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, server.URL, nil, universeRepo, candleRepo, log, 0)
	result, err := scanner.ScanAll(context.Background())
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	symbols := make(map[string]bool)
	for _, s := range result {
		symbols[s.Symbol] = true
	}
	if !symbols["BTCUSDT"] {
		t.Error("expected BTCUSDT (high volume)")
	}
	if symbols["LOWVOLUSDT"] {
		t.Error("LOWVOLUSDT should be excluded (volume below threshold)")
	}
}

func TestScanAll_BlacklistFiltering(t *testing.T) {
	server := newBybitMockServer(
		[]bybitInstrument{
			{Symbol: "BTCUSDT", Status: "Trading", QuoteCoin: "USDT", BaseCoin: "BTC", MaxLeverage: "100"},
			{Symbol: "SCAMUSDT", Status: "Trading", QuoteCoin: "USDT", BaseCoin: "SCAM", MaxLeverage: "50"},
		},
		[]bybitTicker{
			{Symbol: "BTCUSDT", Turnover24h: "500000000", LastPrice: "65000", Bid1Price: "64999", Ask1Price: "65001"},
			{Symbol: "SCAMUSDT", Turnover24h: "10000000", LastPrice: "1.0", Bid1Price: "0.99", Ask1Price: "1.01"},
		},
	)
	defer server.Close()

	cfg, strategy, llmRoute := defaultTestConfig()
	cfg.Blacklist = []string{"SCAMUSDT"}
	universeRepo := newMockUniverseRepo()
	candleRepo := &mockCandleRepo{}
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, server.URL, nil, universeRepo, candleRepo, log, 0)
	result, err := scanner.ScanAll(context.Background())
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	for _, s := range result {
		if s.Symbol == "SCAMUSDT" {
			t.Error("SCAMUSDT should be excluded by blacklist")
		}
	}
}

func TestScanAll_ForceIncludeBypassesVolume(t *testing.T) {
	server := newBybitMockServer(
		[]bybitInstrument{
			{Symbol: "BTCUSDT", Status: "Trading", QuoteCoin: "USDT", BaseCoin: "BTC", MaxLeverage: "100"},
			{Symbol: "NEWTOKENUSDT", Status: "Trading", QuoteCoin: "USDT", BaseCoin: "NEWTOKEN", MaxLeverage: "20"},
		},
		[]bybitTicker{
			{Symbol: "BTCUSDT", Turnover24h: "500000000", LastPrice: "65000", Bid1Price: "64999", Ask1Price: "65001"},
			// NEWTOKEN has no ticker (very low volume)
		},
	)
	defer server.Close()

	cfg, strategy, llmRoute := defaultTestConfig()
	cfg.ForceIncludeSymbols = []string{"NEWTOKENUSDT"}
	universeRepo := newMockUniverseRepo()
	candleRepo := &mockCandleRepo{}
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, server.URL, nil, universeRepo, candleRepo, log, 0)
	result, err := scanner.ScanAll(context.Background())
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	found := false
	for _, s := range result {
		if s.Symbol == "NEWTOKENUSDT" {
			found = true
			if !s.ForceInclude {
				t.Error("NEWTOKENUSDT should have ForceInclude=true")
			}
		}
	}
	if !found {
		t.Error("NEWTOKENUSDT should be included via force include")
	}
}

func TestScanAll_SpreadFilter(t *testing.T) {
	server := newBybitMockServer(
		[]bybitInstrument{
			{Symbol: "BTCUSDT", Status: "Trading", QuoteCoin: "USDT", BaseCoin: "BTC", MaxLeverage: "100"},
			{Symbol: "WIDEUSDT", Status: "Trading", QuoteCoin: "USDT", BaseCoin: "WIDE", MaxLeverage: "50"},
		},
		[]bybitTicker{
			{Symbol: "BTCUSDT", Turnover24h: "500000000", LastPrice: "65000", Bid1Price: "64999", Ask1Price: "65001"},
			{Symbol: "WIDEUSDT", Turnover24h: "10000000", LastPrice: "1.0", Bid1Price: "0.90", Ask1Price: "1.10"}, // ~2000 bps spread
		},
	)
	defer server.Close()

	cfg, strategy, llmRoute := defaultTestConfig()
	universeRepo := newMockUniverseRepo()
	candleRepo := &mockCandleRepo{}
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, server.URL, nil, universeRepo, candleRepo, log, 0)
	result, err := scanner.ScanAll(context.Background())
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	for _, s := range result {
		if s.Symbol == "WIDEUSDT" {
			t.Error("WIDEUSDT should be excluded (spread too wide)")
		}
	}
}

func TestFilterQuality_PassesGoodSymbols(t *testing.T) {
	md := &mockMarketData{
		orderbooks: map[string]*domain.OrderBookSummary{
			"BTCUSDT": {
				BestBid: 65000, BestAsk: 65001, SpreadBps: 1.5,
				BidDepth: 50000, AskDepth: 50000, DepthToPositionSizeRatio: 0.5,
				EstimatedSlippageBps: 5, Stale: false,
			},
		},
		prices: map[string]float64{"BTCUSDT": 65000},
	}
	cfg, strategy, llmRoute := defaultTestConfig()
	universeRepo := newMockUniverseRepo()
	candleRepo := &mockCandleRepo{}
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, "http://unused", md, universeRepo, candleRepo, log, 1000)

	input := []domain.UniverseSymbol{
		{SymbolInfo: domain.SymbolInfo{Symbol: "BTCUSDT", Status: "Trading"}},
	}

	candidates, err := scanner.FilterQuality(context.Background(), input)
	if err != nil {
		t.Fatalf("FilterQuality: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Symbol != "BTCUSDT" {
		t.Errorf("expected BTCUSDT, got %s", candidates[0].Symbol)
	}
	if candidates[0].LiquidityScore <= 0 {
		t.Error("liquidity_score should be > 0")
	}
	if candidates[0].ExecutionScore <= 0 {
		t.Error("execution_score should be > 0")
	}
	if candidates[0].CandidateScore <= 0 {
		t.Error("candidate_score should be > 0")
	}
}

func TestFilterQuality_RejectsStaleOrderbook(t *testing.T) {
	md := &mockMarketData{
		orderbooks: map[string]*domain.OrderBookSummary{
			"BTCUSDT": {Stale: true},
		},
	}
	cfg, strategy, llmRoute := defaultTestConfig()
	universeRepo := newMockUniverseRepo()
	candleRepo := &mockCandleRepo{}
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, "http://unused", md, universeRepo, candleRepo, log, 1000)

	input := []domain.UniverseSymbol{
		{SymbolInfo: domain.SymbolInfo{Symbol: "BTCUSDT"}},
	}

	candidates, err := scanner.FilterQuality(context.Background(), input)
	if err != nil {
		t.Fatalf("FilterQuality: %v", err)
	}
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates for stale orderbook, got %d", len(candidates))
	}
}

func TestFilterQuality_ForceIncludeBypassesSpread(t *testing.T) {
	md := &mockMarketData{
		orderbooks: map[string]*domain.OrderBookSummary{
			"WIDEUSDT": {
				BestBid: 1.0, BestAsk: 1.2, SpreadBps: 1800,
				BidDepth: 1000, AskDepth: 1000, DepthToPositionSizeRatio: 0.1,
				EstimatedSlippageBps: 50, Stale: false,
			},
		},
		prices: map[string]float64{"WIDEUSDT": 1.1},
	}
	cfg, strategy, llmRoute := defaultTestConfig()
	universeRepo := newMockUniverseRepo()
	candleRepo := &mockCandleRepo{}
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, "http://unused", md, universeRepo, candleRepo, log, 1000)

	input := []domain.UniverseSymbol{
		{SymbolInfo: domain.SymbolInfo{Symbol: "WIDEUSDT"}, ForceInclude: true},
	}

	candidates, err := scanner.FilterQuality(context.Background(), input)
	if err != nil {
		t.Fatalf("FilterQuality: %v", err)
	}
	if len(candidates) != 1 {
		t.Errorf("force-include should bypass spread filter, got %d candidates", len(candidates))
	}
}

func TestCandidateScore_Formula(t *testing.T) {
	// candidate_score = liquidity*0.25 + execution*0.25 + setup*0.35 + volatility*0.15
	score := candidateScore(80, 70, 90, 60)
	expected := 80*0.25 + 70*0.25 + 90*0.35 + 60*0.15
	if math.Abs(score-expected) > 0.01 {
		t.Errorf("candidateScore: got %.2f, want %.2f", score, expected)
	}
}

func TestEvaluateLLMEligibility_AllPass(t *testing.T) {
	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			RR:                 2.5,
			ExpectedMove:       100,
			EstimatedTotalCost: 10,
		},
		CandidateScore: 80,
	}
	ob := domain.OrderBookSummary{
		SpreadBps:                10,
		EstimatedSlippageBps:     5,
		DepthToPositionSizeRatio: 0.5,
	}
	cfg, strategy, llmRoute := defaultTestConfig()

	result := evaluateLLMEligibility(cand, ob, cfg.Filters, llmRoute, strategy)
	if !result.LLMEligible {
		t.Errorf("expected LLM eligible, reasons: %v", result.LLMRoutingReasonCodes)
	}
}

func TestEvaluateLLMEligibility_ScoreTooLow(t *testing.T) {
	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			RR:                 2.5,
			ExpectedMove:       100,
			EstimatedTotalCost: 10,
		},
		CandidateScore: 50, // below 75
	}
	ob := domain.OrderBookSummary{
		SpreadBps:                10,
		EstimatedSlippageBps:     5,
		DepthToPositionSizeRatio: 0.5,
	}
	cfg, strategy, llmRoute := defaultTestConfig()

	result := evaluateLLMEligibility(cand, ob, cfg.Filters, llmRoute, strategy)
	if result.LLMEligible {
		t.Error("should not be eligible with low candidate_score")
	}
	found := false
	for _, r := range result.LLMRoutingReasonCodes {
		if r == "candidate_score_below_threshold" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected candidate_score_below_threshold, got %v", result.LLMRoutingReasonCodes)
	}
}

func TestEvaluateLLMEligibility_RRTooLow(t *testing.T) {
	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			RR:                 1.0, // below min 2.0
			ExpectedMove:       100,
			EstimatedTotalCost: 10,
		},
		CandidateScore: 80,
	}
	ob := domain.OrderBookSummary{
		SpreadBps:                10,
		EstimatedSlippageBps:     5,
		DepthToPositionSizeRatio: 0.5,
	}
	cfg, strategy, llmRoute := defaultTestConfig()

	result := evaluateLLMEligibility(cand, ob, cfg.Filters, llmRoute, strategy)
	if result.LLMEligible {
		t.Error("should not be eligible with low RR")
	}
}

func TestEvaluateLLMEligibility_ExpectedMoveTooSmall(t *testing.T) {
	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			RR:                 2.5,
			ExpectedMove:       20, // < 3 * 10
			EstimatedTotalCost: 10,
		},
		CandidateScore: 80,
	}
	ob := domain.OrderBookSummary{
		SpreadBps:                10,
		EstimatedSlippageBps:     5,
		DepthToPositionSizeRatio: 0.5,
	}
	cfg, strategy, llmRoute := defaultTestConfig()

	result := evaluateLLMEligibility(cand, ob, cfg.Filters, llmRoute, strategy)
	if result.LLMEligible {
		t.Error("should not be eligible when expected move <= 3x cost")
	}
}

func TestTenGoodCandidates_AllEligible(t *testing.T) {
	// No hard cap configured, so all 10 should be eligible
	cfg, strategy, llmRoute := defaultTestConfig()
	llmRoute.HardCapCandidatesPerCycle = 0 // no hard cap (default)

	for i := 0; i < 10; i++ {
		cand := domain.Candidate{
			ProposedTrade: domain.ProposedTrade{
				Symbol:             fmt.Sprintf("SYM%dUSDT", i),
				RR:                 2.5,
				ExpectedMove:       100,
				EstimatedTotalCost: 10,
			},
			CandidateScore: 85,
		}
		ob := domain.OrderBookSummary{
			SpreadBps:                10,
			EstimatedSlippageBps:     5,
			DepthToPositionSizeRatio: 0.5,
		}

		result := evaluateLLMEligibility(cand, ob, cfg.Filters, llmRoute, strategy)
		if !result.LLMEligible {
			t.Errorf("candidate %d should be eligible, reasons: %v", i, result.LLMRoutingReasonCodes)
		}
	}
}

func TestExternalWatchlist_TTLExpiry(t *testing.T) {
	cfg, strategy, llmRoute := defaultTestConfig()
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, "http://unused", nil, nil, nil, log, 0)

	// Add with very short TTL
	scanner.AddExternalSignal("BTCUSDT", 0) // 0 -> defaults to 48h

	active := scanner.GetExternalWatchlist()
	if len(active) != 1 {
		t.Fatalf("expected 1 active, got %d", len(active))
	}

	// Manually expire by setting past time
	scanner.mu.Lock()
	scanner.watchlist["BTCUSDT"] = time.Now().Add(-1 * time.Hour)
	scanner.mu.Unlock()

	active = scanner.GetExternalWatchlist()
	if len(active) != 0 {
		t.Errorf("expected 0 after expiry, got %d", len(active))
	}
}

func TestAddForceInclude_PersistsToDB(t *testing.T) {
	universeRepo := newMockUniverseRepo()
	cfg, strategy, llmRoute := defaultTestConfig()
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, "http://unused", nil, universeRepo, nil, log, 0)

	err := scanner.AddForceInclude(context.Background(), "NEWUSDT")
	if err != nil {
		t.Fatalf("AddForceInclude: %v", err)
	}

	sym, err := universeRepo.GetBySymbol(context.Background(), "NEWUSDT")
	if err != nil || sym == nil {
		t.Fatal("symbol not found in repo")
	}
	if !sym.ForceInclude {
		t.Error("expected ForceInclude=true")
	}
	if sym.Blacklist {
		t.Error("expected Blacklist=false")
	}
}

func TestRemoveSymbol_SetsBlacklist(t *testing.T) {
	universeRepo := newMockUniverseRepo()
	universeRepo.InsertOrUpdate(context.Background(), domain.UniverseSymbol{
		SymbolInfo: domain.SymbolInfo{Symbol: "BTCUSDT", Status: "Trading"},
	})

	cfg, strategy, llmRoute := defaultTestConfig()
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, "http://unused", nil, universeRepo, nil, log, 0)

	err := scanner.RemoveSymbol(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("RemoveSymbol: %v", err)
	}

	sym, _ := universeRepo.GetBySymbol(context.Background(), "BTCUSDT")
	if !sym.Blacklist {
		t.Error("expected Blacklist=true")
	}
	if sym.ForceInclude {
		t.Error("expected ForceInclude=false")
	}
}

func TestRemoveSymbol_NotInDB(t *testing.T) {
	universeRepo := newMockUniverseRepo()
	cfg, strategy, llmRoute := defaultTestConfig()
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, "http://unused", nil, universeRepo, nil, log, 0)

	err := scanner.RemoveSymbol(context.Background(), "UNKNOWNUSDT")
	if err != nil {
		t.Fatalf("RemoveSymbol: %v", err)
	}

	sym, _ := universeRepo.GetBySymbol(context.Background(), "UNKNOWNUSDT")
	if sym == nil {
		t.Fatal("symbol should be created")
	}
	if !sym.Blacklist {
		t.Error("expected Blacklist=true")
	}
}

func TestComputeScores_HighDepthLowSpread(t *testing.T) {
	ob := domain.OrderBookSummary{
		SpreadBps:            1,
		BidDepth:             50000,
		AskDepth:             50000,
		EstimatedSlippageBps: 2,
	}
	// ATR=1625 on price=65000 => atrPct=2.5% => volatility ~100 (optimal)
	liq, exec, vol := computeScores(ob, 1625, 65000)

	if liq < 70 {
		t.Errorf("expected high liquidity score, got %.2f", liq)
	}
	if exec < 70 {
		t.Errorf("expected high execution score, got %.2f", exec)
	}
	if vol < 90 {
		t.Errorf("expected high volatility score at optimal ATR%%, got %.2f", vol)
	}
}

func TestComputeScores_LowDepthHighSpread(t *testing.T) {
	ob := domain.OrderBookSummary{
		SpreadBps:            80,
		BidDepth:             10,
		AskDepth:             10,
		EstimatedSlippageBps: 300,
	}
	liq, exec, _ := computeScores(ob, 100, 65000)

	if liq > 60 {
		t.Errorf("expected low liquidity score, got %.2f", liq)
	}
	if exec > 55 {
		t.Errorf("expected low execution score with high slippage, got %.2f", exec)
	}
}

func TestClamp(t *testing.T) {
	if clamp(5, 0, 10) != 5 {
		t.Error("clamp(5, 0, 10) should be 5")
	}
	if clamp(-1, 0, 10) != 0 {
		t.Error("clamp(-1, 0, 10) should be 0")
	}
	if clamp(15, 0, 10) != 10 {
		t.Error("clamp(15, 0, 10) should be 10")
	}
}

func TestRefreshUniverse_PersistsSymbols(t *testing.T) {
	server := newBybitMockServer(
		[]bybitInstrument{
			{Symbol: "BTCUSDT", Status: "Trading", QuoteCoin: "USDT", BaseCoin: "BTC", MaxLeverage: "100"},
		},
		[]bybitTicker{
			{Symbol: "BTCUSDT", Turnover24h: "500000000", LastPrice: "65000", Bid1Price: "64999", Ask1Price: "65001"},
		},
	)
	defer server.Close()

	universeRepo := newMockUniverseRepo()
	cfg, strategy, llmRoute := defaultTestConfig()
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, server.URL, nil, universeRepo, nil, log, 0)

	err := scanner.RefreshUniverse(context.Background())
	if err != nil {
		t.Fatalf("RefreshUniverse: %v", err)
	}

	syms, _ := universeRepo.GetAll(context.Background())
	if len(syms) != 1 {
		t.Fatalf("expected 1 symbol in DB, got %d", len(syms))
	}
	if syms[0].Symbol != "BTCUSDT" {
		t.Errorf("expected BTCUSDT, got %s", syms[0].Symbol)
	}
	if syms[0].LastScanAt.IsZero() {
		t.Error("LastScanAt should be set")
	}
}

// recordingMockMarketData records GetOrderBookSummary arguments.
type recordingMockMarketData struct {
	mockMarketData
	lastTargetNotional float64
	lastSide           string
	callCount          int
}

func (m *recordingMockMarketData) GetOrderBookSummary(_ context.Context, symbol string, targetNotional float64, side string) (domain.OrderBookSummary, error) {
	m.lastTargetNotional = targetNotional
	m.lastSide = side
	m.callCount++
	return m.mockMarketData.GetOrderBookSummary(nil, symbol, targetNotional, side)
}

func TestFilterQuality_CallsGetOrderBookSummaryWithTargetNotional(t *testing.T) {
	md := &recordingMockMarketData{
		mockMarketData: mockMarketData{
			orderbooks: map[string]*domain.OrderBookSummary{
				"BTCUSDT": {
					BestBid: 65000, BestAsk: 65001, SpreadBps: 1.5,
					BidDepth: 50000, AskDepth: 50000, DepthToPositionSizeRatio: 0.5,
					EstimatedSlippageBps: 5, Stale: false,
				},
			},
			prices: map[string]float64{"BTCUSDT": 65000},
		},
	}
	cfg, strategy, llmRoute := defaultTestConfig()
	universeRepo := newMockUniverseRepo()
	candleRepo := &mockCandleRepo{}
	log := defaultTestLogger()

	scanner := NewScanner(cfg, strategy, llmRoute, "http://unused", md, universeRepo, candleRepo, log, 500)

	input := []domain.UniverseSymbol{
		{SymbolInfo: domain.SymbolInfo{Symbol: "BTCUSDT", Status: "Trading"}},
	}

	candidates, err := scanner.FilterQuality(context.Background(), input)
	if err != nil {
		t.Fatalf("FilterQuality: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
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
