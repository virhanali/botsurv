package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
)

// fakeCandleRepo tracks inserts for test verification.
type fakeCandleRepo struct {
	mu      sync.Mutex
	inserts []domain.Candle
	seen    map[string]int
}

func (f *fakeCandleRepo) Insert(_ context.Context, c domain.Candle) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.seen == nil {
		f.seen = make(map[string]int)
	}
	key := fmt.Sprintf("%s|%s|%d", c.Symbol, c.Timeframe, c.OpenTime)
	if idx, ok := f.seen[key]; ok {
		f.inserts[idx] = c
		return int64(idx + 1), nil
	}
	f.inserts = append(f.inserts, c)
	f.seen[key] = len(f.inserts) - 1
	return int64(len(f.inserts)), nil
}

func (f *fakeCandleRepo) GetBySymbolTimeframe(_ context.Context, _, _ string, _ int) ([]domain.Candle, error) {
	return nil, nil
}

func (f *fakeCandleRepo) insertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.inserts)
}

func TestParseKlineMessage_ValidArray(t *testing.T) {
	payload := []byte(`{
		"topic": "kline.15.BTCUSDT",
		"data": [{
			"start": "1672324800000",
			"open": "16649.5",
			"high": "16677",
			"low": "16648",
			"close": "16677",
			"volume": "2.081",
			"turnover": "34666.4005",
			"confirm": false
		}]
	}`)

	symbol, tf, candle, err := parseKlineMessage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if symbol != "BTCUSDT" {
		t.Errorf("symbol = %s, want BTCUSDT", symbol)
	}
	if tf != "15m" {
		t.Errorf("timeframe = %s, want 15m", tf)
	}
	if candle.OpenTime != 1672324800000 {
		t.Errorf("openTime = %d, want 1672324800000", candle.OpenTime)
	}
	if candle.Open != 16649.5 {
		t.Errorf("open = %f, want 16649.5", candle.Open)
	}
	if candle.Confirmed {
		t.Error("expected confirmed = false")
	}
	if candle.TurnOver != 34666.4005 {
		t.Errorf("turnover = %f, want 34666.4005", candle.TurnOver)
	}
}

func TestParseKlineMessage_SingleObject(t *testing.T) {
	payload := []byte(`{
		"topic": "kline.1H.ETHUSDT",
		"data": {
			"start": "1672324800000",
			"open": "1200",
			"high": "1210",
			"low": "1190",
			"close": "1205",
			"volume": "100",
			"confirm": true
		}
	}`)

	symbol, tf, candle, err := parseKlineMessage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if symbol != "ETHUSDT" || tf != "1H" {
		t.Errorf("unexpected symbol/tf: %s %s", symbol, tf)
	}
	if !candle.Confirmed {
		t.Error("expected confirmed = true")
	}
}

func TestParseKlineMessage_InvalidTopic(t *testing.T) {
	payload := []byte(`{"topic": "trade.BTCUSDT", "data": []}`)
	_, _, _, err := parseKlineMessage(payload)
	if err == nil {
		t.Fatal("expected error for invalid topic")
	}
}

func TestParseKlineMessage_MalformedJSON(t *testing.T) {
	payload := []byte(`{invalid`)
	_, _, _, err := parseKlineMessage(payload)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestCandleCloseDetection(t *testing.T) {
	tests := []struct {
		name      string
		confirm   bool
		confirmed bool
	}{
		{"confirmed", true, true},
		{"unconfirmed", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := fmt.Sprintf(`{
				"topic": "kline.15.BTCUSDT",
				"data": [{
					"start": "1672324800000",
					"open": "100",
					"high": "110",
					"low": "90",
					"close": "105",
					"volume": "1",
					"confirm": %t
				}]
			}`, tt.confirm)

			_, _, candle, err := parseKlineMessage([]byte(payload))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if candle.Confirmed != tt.confirmed {
				t.Errorf("confirmed = %v, want %v", candle.Confirmed, tt.confirmed)
			}
		})
	}
}

func TestCandleCacheUpdate(t *testing.T) {
	cache := newCandleCache()

	c1 := domain.Candle{Symbol: "BTCUSDT", Timeframe: "15m", OpenTime: 100, Close: 100}
	c2 := domain.Candle{Symbol: "BTCUSDT", Timeframe: "15m", OpenTime: 200, Close: 200}
	c3 := domain.Candle{Symbol: "BTCUSDT", Timeframe: "15m", OpenTime: 100, Close: 101} // update c1

	cache.update("BTCUSDT", "15m", c2)
	cache.update("BTCUSDT", "15m", c1)
	cache.update("BTCUSDT", "15m", c3)

	got := cache.get("BTCUSDT", "15m", 10)
	if len(got) != 2 {
		t.Fatalf("expected 2 candles, got %d", len(got))
	}
	if got[0].OpenTime != 100 || got[0].Close != 101 {
		t.Errorf("first candle = %+v, want openTime=100 close=101", got[0])
	}
	if got[1].OpenTime != 200 {
		t.Errorf("second candle openTime = %d, want 200", got[1].OpenTime)
	}

	limited := cache.get("BTCUSDT", "15m", 1)
	if len(limited) != 1 || limited[0].OpenTime != 200 {
		t.Errorf("limit 1 = %+v, want openTime=200", limited)
	}
}

func TestCandleCacheBounded(t *testing.T) {
	cache := newCandleCache()

	for i := 0; i < defaultCandleCacheCap+25; i++ {
		cache.update("BTCUSDT", "15m", domain.Candle{
			Symbol:    "BTCUSDT",
			Timeframe: "15m",
			OpenTime:  int64(i),
			Close:     float64(i),
		})
	}

	got := cache.get("BTCUSDT", "15m", defaultCandleCacheCap+100)
	if len(got) != defaultCandleCacheCap {
		t.Fatalf("cache length = %d, want %d", len(got), defaultCandleCacheCap)
	}
	if got[0].OpenTime != 25 {
		t.Errorf("first retained openTime = %d, want 25", got[0].OpenTime)
	}
}

func TestDuplicateCandlePrevention(t *testing.T) {
	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 60,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
	}
	svc := NewBybitWSMarketDataService(cfg, repo, nil, nil)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	// First confirmed kline
	payload1 := []byte(`{
		"topic": "kline.15.BTCUSDT",
		"data": [{
			"start": "1672324800000",
			"open": "100",
			"high": "110",
			"low": "90",
			"close": "105",
			"volume": "1",
			"confirm": true
		}]
	}`)

	// Same candle again (duplicate)
	payload2 := []byte(`{
		"topic": "kline.15.BTCUSDT",
		"data": [{
			"start": "1672324800000",
			"open": "100",
			"high": "110",
			"low": "90",
			"close": "105",
			"volume": "1",
			"confirm": true
		}]
	}`)

	ctx := context.Background()
	if err := svc.handleKlineMessage(ctx, payload1); err != nil {
		t.Fatalf("first handle: %v", err)
	}
	if err := svc.handleKlineMessage(ctx, payload2); err != nil {
		t.Fatalf("second handle: %v", err)
	}

	if repo.insertCount() != 1 {
		t.Errorf("insert count = %d, want 1 after duplicate upsert", repo.insertCount())
	}
}

func TestStaleDataDetection(t *testing.T) {
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 60,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)

	if svc.IsHealthy("BTCUSDT") {
		t.Error("expected unhealthy with no data")
	}

	// Set price 10 seconds ago
	svc.priceCache.set("BTCUSDT", 100, time.Now().Add(-10*time.Second))
	if !svc.IsHealthy("BTCUSDT") {
		t.Error("expected healthy with recent data")
	}

	// Set price 60 seconds ago
	svc.priceCache.set("BTCUSDT", 100, time.Now().Add(-60*time.Second))
	if svc.IsHealthy("BTCUSDT") {
		t.Error("expected stale data")
	}
}

func TestIsHealthyRequiresConfiguredTimeframes(t *testing.T) {
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 30,
		Timeframes:                []string{"15m", "1H"},
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)
	now := time.Now().UTC()
	svc.priceCache.set("BTCUSDT", 100, now)
	svc.candleCache.update("BTCUSDT", "15m", domain.Candle{
		Symbol:    "BTCUSDT",
		Timeframe: "15m",
		OpenTime:  1,
		Close:     100,
	})

	if svc.IsHealthy("BTCUSDT") {
		t.Fatal("expected unhealthy until all configured timeframes have data")
	}

	svc.candleCache.update("BTCUSDT", "1H", domain.Candle{
		Symbol:    "BTCUSDT",
		Timeframe: "1H",
		OpenTime:  1,
		Close:     100,
	})
	if !svc.IsHealthy("BTCUSDT") {
		t.Fatal("expected healthy with price and all configured timeframes fresh")
	}

	svc.candleCache.lastUpdates[candleKey("BTCUSDT", "15m")] = now.Add(-60 * time.Second)
	if svc.IsHealthy("BTCUSDT") {
		t.Fatal("expected unhealthy when one configured timeframe is stale")
	}
}

func TestGetLatestPrice(t *testing.T) {
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 60,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)

	_, err := svc.GetLatestPrice(context.Background(), "BTCUSDT")
	if err == nil {
		t.Fatal("expected error for missing price")
	}

	svc.priceCache.set("BTCUSDT", 12345.6, time.Now())
	price, err := svc.GetLatestPrice(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if price != 12345.6 {
		t.Errorf("price = %f, want 12345.6", price)
	}
}

func TestParseTickerMessage(t *testing.T) {
	payload := []byte(`{
		"topic": "tickers.BTCUSDT",
		"ts": 1672324800000,
		"data": {
			"symbol": "BTCUSDT",
			"lastPrice": "16677.5"
		}
	}`)

	symbol, price, ts, hasPrice, err := parseTickerMessage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasPrice {
		t.Fatal("expected ticker payload to include price")
	}
	if symbol != "BTCUSDT" {
		t.Errorf("symbol = %s, want BTCUSDT", symbol)
	}
	if price != 16677.5 {
		t.Errorf("price = %f, want 16677.5", price)
	}
	if ts.UnixMilli() != 1672324800000 {
		t.Errorf("ts = %d, want 1672324800000", ts.UnixMilli())
	}
}

func TestParseTickerMessage_ArrayData(t *testing.T) {
	payload := []byte(`{
		"topic": "tickers.ETHUSDT",
		"data": [{
			"symbol": "ETHUSDT",
			"lastPrice": "1200"
		}]
	}`)

	symbol, price, _, hasPrice, err := parseTickerMessage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasPrice {
		t.Fatal("expected ticker payload to include price")
	}
	if symbol != "ETHUSDT" {
		t.Errorf("symbol = %s, want ETHUSDT", symbol)
	}
	if price != 1200 {
		t.Errorf("price = %f, want 1200", price)
	}
}

func TestParseTickerMessage_DeltaWithoutPriceNoop(t *testing.T) {
	payload := []byte(`{
		"topic": "tickers.BTCUSDT",
		"type": "delta",
		"data": {
			"symbol": "BTCUSDT",
			"volume24h": "123"
		}
	}`)

	symbol, _, _, hasPrice, err := parseTickerMessage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if symbol != "BTCUSDT" {
		t.Errorf("symbol = %s, want BTCUSDT", symbol)
	}
	if hasPrice {
		t.Fatal("expected missing delta price to be a no-op")
	}
}

func TestParseTickerMessage_SnapshotMissingPriceErrors(t *testing.T) {
	payload := []byte(`{
		"topic": "tickers.BTCUSDT",
		"type": "snapshot",
		"data": {
			"symbol": "BTCUSDT"
		}
	}`)

	if _, _, _, _, err := parseTickerMessage(payload); err == nil {
		t.Fatal("expected snapshot without lastPrice to fail")
	}
}

func TestBackfillRESTParser(t *testing.T) {
	body := []byte(`{
		"retCode": 0,
			"retMsg": "OK",
			"result": {
				"list": [
					["1672325700000", "105", "115", "95", "110", "20", "2000"],
					["1672324800000", "100", "110", "90", "105", "10", "1000"]
				]
			}
		}`)

	candles, err := parseKlineRESTResponse("BTCUSDT", "15m", body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(candles) != 2 {
		t.Fatalf("expected 2 candles, got %d", len(candles))
	}
	if candles[0].OpenTime != 1672324800000 {
		t.Errorf("first openTime = %d, want 1672324800000", candles[0].OpenTime)
	}
	if candles[0].Close != 105 || candles[0].TurnOver != 1000 {
		t.Errorf("first candle close/turnover = %f/%f, want 105/1000", candles[0].Close, candles[0].TurnOver)
	}
	if candles[1].OpenTime != 1672325700000 {
		t.Errorf("second openTime = %d, want 1672325700000", candles[1].OpenTime)
	}
}

func TestBackfillRESTWithMockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v5/market/kline" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"retCode": 0,
			"retMsg": "OK",
			"result": {
				"list": [
					["1672324800000", "100", "110", "90", "105", "10", "1000"]
				]
			}
		}`))
	}))
	defer server.Close()

	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		RESTURL:                   server.URL + "/",
		StaleDataThresholdSeconds: 60,
	}
	svc := NewBybitWSMarketDataService(cfg, repo, server.Client(), nil)

	ctx := context.Background()
	if err := svc.Backfill(ctx, "BTCUSDT", "15m", 1); err != nil {
		t.Fatalf("backfill error: %v", err)
	}

	if repo.insertCount() != 1 {
		t.Errorf("insert count = %d, want 1", repo.insertCount())
	}
	if !repo.inserts[0].Confirmed {
		t.Error("expected backfilled candle to be confirmed")
	}
}

func TestBackfillSkipsOpenCandle(t *testing.T) {
	now := time.Now().UTC()
	closedOpen := now.Add(-30 * time.Minute).UnixMilli()
	openOpen := now.Add(-5 * time.Minute).UnixMilli()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"retCode": 0,
			"retMsg": "OK",
			"result": {
				"list": [
					["%d", "105", "115", "95", "110", "20", "2000"],
					["%d", "100", "110", "90", "105", "10", "1000"]
				]
			}
		}`, openOpen, closedOpen)
	}))
	defer server.Close()

	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		RESTURL:                   server.URL,
		StaleDataThresholdSeconds: 60,
	}
	svc := NewBybitWSMarketDataService(cfg, repo, server.Client(), nil)

	if err := svc.Backfill(context.Background(), "BTCUSDT", "15m", 2); err != nil {
		t.Fatalf("backfill error: %v", err)
	}
	if repo.insertCount() != 1 {
		t.Fatalf("insert count = %d, want 1 closed candle only", repo.insertCount())
	}
	if repo.inserts[0].OpenTime != closedOpen {
		t.Fatalf("inserted openTime = %d, want %d", repo.inserts[0].OpenTime, closedOpen)
	}
}

func TestBackfillRESTErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode": 10001, "retMsg": "invalid symbol"}`))
	}))
	defer server.Close()

	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		RESTURL:                   server.URL,
		StaleDataThresholdSeconds: 60,
	}
	svc := NewBybitWSMarketDataService(cfg, repo, server.Client(), nil)

	ctx := context.Background()
	err := svc.Backfill(ctx, "BAD", "15m", 1)
	if err == nil {
		t.Fatal("expected error for bad response")
	}
}

func TestServiceStartStop(t *testing.T) {
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 60,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)
	ctx := context.Background()

	if err := svc.Start(ctx); err != nil {
		t.Fatalf("start error: %v", err)
	}
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("idempotent start error: %v", err)
	}
	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("stop error: %v", err)
	}
	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("idempotent stop error: %v", err)
	}
}

func TestHandleKlineMessage_PersistsConfirmed(t *testing.T) {
	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 60,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
	}
	svc := NewBybitWSMarketDataService(cfg, repo, nil, nil)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	unconfirmed := []byte(`{
		"topic": "kline.15.BTCUSDT",
		"data": [{
			"start": "1672324800000",
			"open": "100",
			"high": "110",
			"low": "90",
			"close": "105",
			"volume": "1",
			"confirm": false
		}]
	}`)

	confirmed := []byte(`{
		"topic": "kline.15.BTCUSDT",
		"data": [{
			"start": "1672324800000",
			"open": "100",
			"high": "110",
			"low": "90",
			"close": "105",
			"volume": "1",
			"confirm": true
		}]
	}`)

	ctx := context.Background()
	_ = svc.handleKlineMessage(ctx, unconfirmed)
	if repo.insertCount() != 0 {
		t.Errorf("unconfirmed insert count = %d, want 0", repo.insertCount())
	}
	cached, err := svc.GetCandles(ctx, "BTCUSDT", "15m", 1)
	if err != nil {
		t.Fatalf("get cached candle: %v", err)
	}
	if len(cached) != 1 || cached[0].Confirmed {
		t.Fatalf("cached unconfirmed candle = %+v, want one unconfirmed candle", cached)
	}

	_ = svc.handleKlineMessage(ctx, confirmed)
	if repo.insertCount() != 1 {
		t.Errorf("confirmed insert count = %d, want 1", repo.insertCount())
	}
	cached, err = svc.GetCandles(ctx, "BTCUSDT", "15m", 1)
	if err != nil {
		t.Fatalf("get cached confirmed candle: %v", err)
	}
	if len(cached) != 1 || !cached[0].Confirmed {
		t.Fatalf("cached confirmed candle = %+v, want one confirmed candle", cached)
	}
}

func TestHandlersNoopAfterStop(t *testing.T) {
	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 60,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
	}
	svc := NewBybitWSMarketDataService(cfg, repo, nil, nil)
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}

	kline := []byte(`{
		"topic": "kline.15.BTCUSDT",
		"data": [{
			"start": "1672324800000",
			"open": "100",
			"high": "110",
			"low": "90",
			"close": "105",
			"volume": "1",
			"confirm": true
		}]
	}`)
	ticker := []byte(`{
		"topic": "tickers.BTCUSDT",
		"data": {"symbol": "BTCUSDT", "lastPrice": "105"}
	}`)

	if err := svc.handleKlineMessage(ctx, kline); err != nil {
		t.Fatalf("kline after stop: %v", err)
	}
	if err := svc.handleTickerMessage(ticker); err != nil {
		t.Fatalf("ticker after stop: %v", err)
	}
	if repo.insertCount() != 0 {
		t.Fatalf("insert count after stop = %d, want 0", repo.insertCount())
	}
	if svc.IsHealthy("BTCUSDT") {
		t.Fatal("expected service to remain unhealthy after stopped handlers")
	}
}

func TestWSBuildTopics(t *testing.T) {
	cfg := app.MarketDataConfig{
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT", "ETHUSDT"},
		},
		Timeframes:     []string{"15m", "1H"},
		OrderbookDepth: 50,
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)

	topics := svc.buildTopics()
	if len(topics) != 10 {
		t.Fatalf("expected 10 topics, got %d", len(topics))
	}

	expected := []string{
		"kline.15.BTCUSDT",
		"kline.60.BTCUSDT",
		"tickers.BTCUSDT",
		"orderbook.50.BTCUSDT",
		"publicTrade.BTCUSDT",
		"kline.15.ETHUSDT",
		"kline.60.ETHUSDT",
		"tickers.ETHUSDT",
		"orderbook.50.ETHUSDT",
		"publicTrade.ETHUSDT",
	}
	for i, exp := range expected {
		if topics[i] != exp {
			t.Errorf("topic[%d] = %s, want %s", i, topics[i], exp)
		}
	}
}

func TestDispatchKlineMessage(t *testing.T) {
	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 60,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
	}
	svc := NewBybitWSMarketDataService(cfg, repo, nil, nil)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer svc.Stop(context.Background())

	payload := []byte(`{
		"topic": "kline.15.BTCUSDT",
		"data": [{
			"start": "1672324800000",
			"open": "100",
			"high": "110",
			"low": "90",
			"close": "105",
			"volume": "1",
			"confirm": true
		}]
	}`)

	if err := svc.dispatchMessage(payload); err != nil {
		t.Fatalf("dispatch kline: %v", err)
	}
	if repo.insertCount() != 1 {
		t.Errorf("insert count = %d, want 1", repo.insertCount())
	}
	cached, _ := svc.GetCandles(context.Background(), "BTCUSDT", "15m", 1)
	if len(cached) != 1 || cached[0].Close != 105 {
		t.Errorf("cached candle = %+v, want close=105", cached)
	}
}

func TestDispatchKlineMessage_MapsBybitInterval(t *testing.T) {
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 60,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer svc.Stop(context.Background())

	// Bybit sends "60" for 1H; service should map it to "1H" internally.
	payload := []byte(`{
		"topic": "kline.60.BTCUSDT",
		"data": [{
			"start": "1672324800000",
			"open": "100",
			"high": "110",
			"low": "90",
			"close": "105",
			"volume": "1",
			"confirm": false
		}]
	}`)

	if err := svc.dispatchMessage(payload); err != nil {
		t.Fatalf("dispatch kline: %v", err)
	}
	cached, _ := svc.GetCandles(context.Background(), "BTCUSDT", "1H", 1)
	if len(cached) != 1 {
		t.Fatalf("expected 1 cached candle for 1H, got %d", len(cached))
	}
}

func TestDispatchTickerMessage(t *testing.T) {
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 60,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer svc.Stop(context.Background())

	payload := []byte(`{
		"topic": "tickers.BTCUSDT",
		"ts": 1672324800000,
		"data": {
			"symbol": "BTCUSDT",
			"lastPrice": "16677.5"
		}
	}`)

	if err := svc.dispatchMessage(payload); err != nil {
		t.Fatalf("dispatch ticker: %v", err)
	}
	price, err := svc.GetLatestPrice(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("get latest price: %v", err)
	}
	if price != 16677.5 {
		t.Errorf("price = %f, want 16677.5", price)
	}
}

func TestGracefulShutdown(t *testing.T) {
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 60,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)
	ctx := context.Background()

	if err := svc.Start(ctx); err != nil {
		t.Fatalf("start error: %v", err)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.Stop(stopCtx); err != nil {
		t.Fatalf("stop error: %v", err)
	}

	if svc.running {
		t.Error("expected service to be stopped")
	}
}

func TestStartupBackfill(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v5/market/kline" {
			callCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"retCode": 0,
				"retMsg": "OK",
				"result": {
					"list": [
						["1672324800000", "100", "110", "90", "105", "10", "1000"]
					]
				}
			}`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		RESTURL:                   server.URL,
		StaleDataThresholdSeconds: 60,
		BackfillCandles:           1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m", "1H"},
	}
	svc := NewBybitWSMarketDataService(cfg, repo, server.Client(), nil)

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("start error: %v", err)
	}
	defer svc.Stop(ctx)

	// Wait for backfill goroutine to complete
	for i := 0; i < 50; i++ {
		if callCount >= 2 && repo.insertCount() >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 1 symbol x 2 timeframes = 2 backfill calls
	if callCount != 2 {
		t.Errorf("backfill call count = %d, want 2", callCount)
	}
	if repo.insertCount() != 2 {
		t.Errorf("insert count = %d, want 2", repo.insertCount())
	}

	// Cache should be warmed
	cached15, _ := svc.GetCandles(ctx, "BTCUSDT", "15m", 1)
	if len(cached15) != 1 {
		t.Errorf("15m cache = %d, want 1", len(cached15))
	}
	cached1H, _ := svc.GetCandles(ctx, "BTCUSDT", "1H", 1)
	if len(cached1H) != 1 {
		t.Errorf("1H cache = %d, want 1", len(cached1H))
	}
}

func TestBackfillNilRepoGuard(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"retCode": 0,
			"retMsg": "OK",
			"result": {
				"list": [
					["1672324800000", "100", "110", "90", "105", "10", "1000"]
				]
			}
		}`))
	}))
	defer server.Close()

	cfg := app.MarketDataConfig{
		RESTURL:                   server.URL,
		StaleDataThresholdSeconds: 60,
	}
	svc := NewBybitWSMarketDataService(cfg, nil, server.Client(), nil)

	ctx := context.Background()
	if err := svc.Backfill(ctx, "BTCUSDT", "15m", 1); err != nil {
		t.Fatalf("backfill with nil repo should not error: %v", err)
	}
}

func TestReconnectAndResubscribe(t *testing.T) {
	var connectCount int
	var mu sync.Mutex
	secondSubscribed := make(chan struct{}, 1)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		connectCount++
		cc := connectCount
		mu.Unlock()

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		defer conn.Close()

		// Read subscribe request and assert on-wire format.
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var sub struct {
			Op   string   `json:"op"`
			Args []string `json:"args"`
		}
		if err := json.Unmarshal(msg, &sub); err == nil && sub.Op == "subscribe" {
			expected := []string{"kline.15.BTCUSDT", "tickers.BTCUSDT", "publicTrade.BTCUSDT"}
			if len(sub.Args) == len(expected) {
				match := true
				for i, exp := range expected {
					if sub.Args[i] != exp {
						match = false
						break
					}
				}
				if match && cc == 2 {
					close(secondSubscribed)
				}
			}
			// Ack subscribe
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"op":"subscribe","success":true,"ret_msg":"ok","conn_id":"test"}`))
		}

		// Send a ticker on first connection, then close to force reconnect.
		// On second connection, send another ticker.
		if cc == 1 {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{
				"topic": "tickers.BTCUSDT",
				"ts": 1672324800000,
				"data": {"symbol": "BTCUSDT", "lastPrice": "100"}
			}`))
			// Close connection to trigger reconnect
			conn.Close()
			return
		}

		// Second connection: send updated price
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{
			"topic": "tickers.BTCUSDT",
			"ts": 1672324800001,
			"data": {"symbol": "BTCUSDT", "lastPrice": "200"}
		}`))

		// Keep connection open until context cancelled
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	cfg := app.MarketDataConfig{
		WSURL:                     "ws" + server.URL[4:] + "/", // convert http:// to ws://
		StaleDataThresholdSeconds: 60,
		ReconnectIntervalSeconds:  1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("start error: %v", err)
	}
	defer svc.Stop(context.Background())

	// Wait for second subscription (after reconnect)
	select {
	case <-secondSubscribed:
		// ok
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for second subscription")
	}

	mu.Lock()
	cc := connectCount
	mu.Unlock()
	if cc < 2 {
		t.Fatalf("expected at least 2 connections, got %d", cc)
	}

	// Eventually price should be 200 from second connection
	var price float64
	var err error
	for i := 0; i < 20; i++ {
		price, err = svc.GetLatestPrice(ctx, "BTCUSDT")
		if err == nil && price == 200 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("get latest price after reconnect: %v", err)
	}
	if price != 200 {
		t.Errorf("price = %f, want 200", price)
	}
}

func TestSubscribeAckRejection(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read subscribe request
		_, _, err = conn.ReadMessage()
		if err != nil {
			return
		}

		// Reject subscribe
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"op":"subscribe","success":false,"ret_msg":"invalid topic","conn_id":"test"}`))

		// Keep connection open briefly so client has time to process rejection
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	cfg := app.MarketDataConfig{
		WSURL:                     "ws" + server.URL[4:] + "/",
		StaleDataThresholdSeconds: 60,
		ReconnectIntervalSeconds:  1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("start error: %v", err)
	}
	defer svc.Stop(context.Background())

	// Give reconnect loop time to try once and fail
	time.Sleep(1500 * time.Millisecond)

	// Service should not be healthy because subscription was rejected
	if svc.IsHealthy("BTCUSDT") {
		t.Error("expected unhealthy after subscribe rejection")
	}
}

// --- Phase 3: Orderbook + Trade Flow Tests ---

func TestParseOrderBookMessage_Snapshot(t *testing.T) {
	payload := []byte(`{
		"topic": "orderbook.50.BTCUSDT",
		"type": "snapshot",
		"ts": 1672324800000,
		"data": {
			"s": "BTCUSDT",
			"b": [["16677.5","0.5"],["16677","0.3"]],
			"a": [["16678","0.3"],["16678.5","0.2"]],
			"u": 123456,
			"seq": 789012
		}
	}`)

	symbol, isSnapshot, seq, prevSeq, bids, asks, err := parseOrderBookMessage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if symbol != "BTCUSDT" {
		t.Errorf("symbol = %s, want BTCUSDT", symbol)
	}
	if !isSnapshot {
		t.Error("expected snapshot")
	}
	if seq != 789012 {
		t.Errorf("seq = %d, want 789012", seq)
	}
	if prevSeq != 0 {
		t.Errorf("prevSeq = %d, want 0 for snapshot", prevSeq)
	}
	if len(bids) != 2 {
		t.Fatalf("expected 2 bids, got %d", len(bids))
	}
	if bids[0].Price != 16677.5 || bids[0].Size != 0.5 {
		t.Errorf("bid[0] = %+v, want price=16677.5 size=0.5", bids[0])
	}
	if len(asks) != 2 {
		t.Fatalf("expected 2 asks, got %d", len(asks))
	}
	if asks[0].Price != 16678 || asks[0].Size != 0.3 {
		t.Errorf("ask[0] = %+v, want price=16678 size=0.3", asks[0])
	}
}

func TestParseOrderBookMessage_Delta(t *testing.T) {
	payload := []byte(`{
		"topic": "orderbook.50.BTCUSDT",
		"type": "delta",
		"ts": 1672324800001,
		"data": {
			"s": "BTCUSDT",
			"b": [["16677.5","0"],["16676","1.0"]],
			"a": [["16678","0.4"]],
			"u": 789012,
			"seq": 789013
		}
	}`)

	symbol, isSnapshot, seq, prevSeq, bids, asks, err := parseOrderBookMessage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if symbol != "BTCUSDT" {
		t.Errorf("symbol = %s, want BTCUSDT", symbol)
	}
	if isSnapshot {
		t.Error("expected delta")
	}
	if seq != 789013 {
		t.Errorf("seq = %d, want 789013", seq)
	}
	if prevSeq != 789012 {
		t.Errorf("prevSeq = %d, want 789012", prevSeq)
	}
	if len(bids) != 2 {
		t.Fatalf("expected 2 bid deltas, got %d", len(bids))
	}
	if bids[0].Price != 16677.5 || bids[0].Size != 0 {
		t.Errorf("bid delta[0] = %+v, want price=16677.5 size=0", bids[0])
	}
	if len(asks) != 1 {
		t.Fatalf("expected 1 ask delta, got %d", len(asks))
	}
}

func TestParseOrderBookMessage_InvalidTopic(t *testing.T) {
	payload := []byte(`{"topic": "tickers.BTCUSDT", "data": {}}`)
	_, _, _, _, _, _, err := parseOrderBookMessage(payload)
	if err == nil {
		t.Fatal("expected error for invalid topic")
	}
}

func TestParsePublicTradeMessage(t *testing.T) {
	payload := []byte(`{
		"topic": "publicTrade.BTCUSDT",
		"type": "snapshot",
		"ts": 1672324800000,
		"data": [
			{"T":1672324800000,"s":"BTCUSDT","p":"16677.5","v":"0.5","S":"Buy","L":"TickPlus"},
			{"T":1672324800001,"s":"BTCUSDT","p":"16678","v":"0.3","S":"Sell","L":"TickMinus"}
		]
	}`)

	symbol, trades, err := parsePublicTradeMessage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if symbol != "BTCUSDT" {
		t.Errorf("symbol = %s, want BTCUSDT", symbol)
	}
	if len(trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(trades))
	}
	if trades[0].Price != 16677.5 || trades[0].Size != 0.5 || trades[0].Side != "Buy" {
		t.Errorf("trade[0] = %+v", trades[0])
	}
	if trades[1].Price != 16678 || trades[1].Size != 0.3 || trades[1].Side != "Sell" {
		t.Errorf("trade[1] = %+v", trades[1])
	}
	if trades[0].Timestamp.UnixMilli() != 1672324800000 {
		t.Errorf("trade[0].timestamp = %d, want 1672324800000", trades[0].Timestamp.UnixMilli())
	}
}

func TestParsePublicTradeMessage_SingleObject(t *testing.T) {
	payload := []byte(`{
		"topic": "publicTrade.ETHUSDT",
		"data": {"T":1672324800000,"s":"ETHUSDT","p":"1200","v":"1","S":"Buy","L":"TickPlus"}
	}`)

	symbol, trades, err := parsePublicTradeMessage(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	if symbol != "ETHUSDT" {
		t.Errorf("symbol = %s, want ETHUSDT", symbol)
	}
	if trades[0].Price != 1200 {
		t.Errorf("price = %f, want 1200", trades[0].Price)
	}
}

func TestOrderBookStore_ResetAndDelta(t *testing.T) {
	store := newOrderBookStore(30 * time.Second)

	// Snapshot
	store.reset("BTCUSDT", 1, []domain.OrderBookLevel{
		{Price: 100, Size: 1},
		{Price: 99, Size: 2},
	}, []domain.OrderBookLevel{
		{Price: 101, Size: 1},
		{Price: 102, Size: 2},
	})

	summary := store.summary("BTCUSDT", 1000, "Buy")
	if summary.BestBid != 100 {
		t.Errorf("bestBid = %f, want 100", summary.BestBid)
	}
	if summary.BestAsk != 101 {
		t.Errorf("bestAsk = %f, want 101", summary.BestAsk)
	}
	if summary.Stale {
		t.Error("expected fresh orderbook")
	}

	// Delta: remove best bid, update best ask
	ok := store.applyDelta("BTCUSDT", 2, 1, []domain.OrderBookLevel{
		{Price: 100, Size: 0},
	}, []domain.OrderBookLevel{
		{Price: 101, Size: 0.5},
	})
	if !ok {
		t.Fatal("expected delta to apply")
	}

	summary = store.summary("BTCUSDT", 1000, "Buy")
	if summary.BestBid != 99 {
		t.Errorf("bestBid after delta = %f, want 99", summary.BestBid)
	}
	if summary.BestAsk != 101 {
		t.Errorf("bestAsk after delta = %f, want 101", summary.BestAsk)
	}
	if summary.BidDepth != 2 {
		t.Errorf("bidDepth = %f, want 2", summary.BidDepth)
	}
	if summary.AskDepth != 2.5 {
		t.Errorf("askDepth = %f, want 2.5", summary.AskDepth)
	}

	// Sequence mismatch: should reject and remove book
	ok = store.applyDelta("BTCUSDT", 99, 98, []domain.OrderBookLevel{
		{Price: 99, Size: 0},
	}, nil)
	if ok {
		t.Fatal("expected delta rejection on prevSeq mismatch")
	}
	summary = store.summary("BTCUSDT", 1000, "Buy")
	if !summary.Stale {
		t.Error("expected stale after sequence gap removes book")
	}
}

func TestOrderBookStore_DeltaBeforeSnapshotIgnored(t *testing.T) {
	store := newOrderBookStore(30 * time.Second)
	ok := store.applyDelta("BTCUSDT", 2, 1, []domain.OrderBookLevel{
		{Price: 100, Size: 1},
	}, []domain.OrderBookLevel{
		{Price: 101, Size: 1},
	})
	if ok {
		t.Error("expected delta to be rejected without snapshot")
	}

	summary := store.summary("BTCUSDT", 1000, "Buy")
	if !summary.Stale {
		t.Error("expected stale because no snapshot received")
	}
}

func TestOrderBookStore_SpreadCalculation(t *testing.T) {
	store := newOrderBookStore(30 * time.Second)
	store.reset("BTCUSDT", 1, []domain.OrderBookLevel{
		{Price: 10000, Size: 1},
	}, []domain.OrderBookLevel{
		{Price: 10001, Size: 1},
	})

	summary := store.summary("BTCUSDT", 1000, "Buy")
	expectedSpread := (10001.0 - 10000.0) / ((10001.0 + 10000.0) / 2) * 10000
	if math.Abs(summary.SpreadBps-expectedSpread) > 1e-6 {
		t.Errorf("spreadBps = %f, want %f", summary.SpreadBps, expectedSpread)
	}
}

func TestOrderBookStore_DepthToPositionSizeRatio(t *testing.T) {
	store := newOrderBookStore(30 * time.Second)
	store.reset("BTCUSDT", 1, []domain.OrderBookLevel{
		{Price: 100, Size: 10},
	}, []domain.OrderBookLevel{
		{Price: 101, Size: 10},
	})

	// Buy side uses ask depth only
	summary := store.summary("BTCUSDT", 100, "Buy")
	expectedRatio := (10.0 * 101.0) / 100.0
	if math.Abs(summary.DepthToPositionSizeRatio-expectedRatio) > 1e-6 {
		t.Errorf("depthToPositionSizeRatio (buy) = %f, want %f", summary.DepthToPositionSizeRatio, expectedRatio)
	}

	// Sell side uses bid depth only
	summarySell := store.summary("BTCUSDT", 100, "Sell")
	expectedRatioSell := (10.0 * 100.0) / 100.0
	if math.Abs(summarySell.DepthToPositionSizeRatio-expectedRatioSell) > 1e-6 {
		t.Errorf("depthToPositionSizeRatio (sell) = %f, want %f", summarySell.DepthToPositionSizeRatio, expectedRatioSell)
	}
}

func TestOrderBookStore_SlippageEstimate(t *testing.T) {
	store := newOrderBookStore(30 * time.Second)
	store.reset("BTCUSDT", 1, []domain.OrderBookLevel{
		{Price: 100, Size: 1},
	}, []domain.OrderBookLevel{
		{Price: 101, Size: 0.5},
		{Price: 102, Size: 1},
	})

	// Buy side: walks asks
	// At ask 101: 0.5 * 101 = 50.5 notional, remaining 49.5
	// At ask 102: buy 49.5/102 = 0.48529 size, remaining 0
	// totalSize = 0.5 + 0.48529 = 0.98529
	// avgPrice = 100 / 0.98529 = 101.492
	// mid = 100.5
	// slippage = (101.492 - 100.5) / 100.5 * 10000 = 98.7 bps
	summary := store.summary("BTCUSDT", 100, "Buy")
	expectedSlippage := (100.0/(0.5+49.5/102.0) - 100.5) / 100.5 * 10000
	if math.Abs(summary.EstimatedSlippageBps-expectedSlippage) > 1.0 {
		t.Errorf("slippage = %f, want approx %f", summary.EstimatedSlippageBps, expectedSlippage)
	}

	// Sell side: walks bids (only 1 level @ 100, so should be insufficient for 1000 notional)
	summarySell := store.summary("BTCUSDT", 1000, "Sell")
	if !math.IsInf(summarySell.EstimatedSlippageBps, 1) {
		t.Errorf("expected infinite slippage for sell due to thin bids, got %f", summarySell.EstimatedSlippageBps)
	}
}

func TestOrderBookStore_SlippageInsufficientDepth(t *testing.T) {
	store := newOrderBookStore(30 * time.Second)
	store.reset("BTCUSDT", 1, []domain.OrderBookLevel{
		{Price: 100, Size: 1},
	}, []domain.OrderBookLevel{
		{Price: 101, Size: 0.1},
	})

	summary := store.summary("BTCUSDT", 10000, "Buy")
	if !math.IsInf(summary.EstimatedSlippageBps, 1) {
		t.Errorf("expected infinite slippage for insufficient depth, got %f", summary.EstimatedSlippageBps)
	}
}

func TestOrderBookStore_StaleDetection(t *testing.T) {
	store := newOrderBookStore(100 * time.Millisecond)
	store.reset("BTCUSDT", 1, []domain.OrderBookLevel{
		{Price: 100, Size: 1},
	}, []domain.OrderBookLevel{
		{Price: 101, Size: 1},
	})

	summary := store.summary("BTCUSDT", 100, "Buy")
	if summary.Stale {
		t.Error("expected fresh immediately after reset")
	}

	time.Sleep(150 * time.Millisecond)
	summary = store.summary("BTCUSDT", 100, "Buy")
	if !summary.Stale {
		t.Error("expected stale after threshold elapsed")
	}
}

func TestTradeFlowStore_RollingWindow(t *testing.T) {
	store := newTradeFlowStore([]int{15, 60}, 30*time.Second)
	now := time.Now().UTC()

	store.add(domain.PublicTrade{Symbol: "BTCUSDT", Side: "Buy", Size: 1, Timestamp: now.Add(-10 * time.Second)})
	store.add(domain.PublicTrade{Symbol: "BTCUSDT", Side: "Sell", Size: 2, Timestamp: now.Add(-20 * time.Second)})
	store.add(domain.PublicTrade{Symbol: "BTCUSDT", Side: "Buy", Size: 3, Timestamp: now.Add(-70 * time.Second)})

	flows := store.flow("BTCUSDT")
	if len(flows) != 2 {
		t.Fatalf("expected 2 flows, got %d", len(flows))
	}

	// 15s window: only trade at -10s
	f15 := flows[0]
	if f15.WindowSeconds != 15 {
		t.Errorf("window = %d, want 15", f15.WindowSeconds)
	}
	if f15.BuyVolume != 1 {
		t.Errorf("15s buyVolume = %f, want 1", f15.BuyVolume)
	}
	if f15.SellVolume != 0 {
		t.Errorf("15s sellVolume = %f, want 0", f15.SellVolume)
	}
	if f15.TradeCount != 1 {
		t.Errorf("15s tradeCount = %d, want 1", f15.TradeCount)
	}

	// 60s window: trades at -10s and -20s
	f60 := flows[1]
	if f60.WindowSeconds != 60 {
		t.Errorf("window = %d, want 60", f60.WindowSeconds)
	}
	if f60.BuyVolume != 1 {
		t.Errorf("60s buyVolume = %f, want 1", f60.BuyVolume)
	}
	if f60.SellVolume != 2 {
		t.Errorf("60s sellVolume = %f, want 2", f60.SellVolume)
	}
	if f60.TradeCount != 2 {
		t.Errorf("60s tradeCount = %d, want 2", f60.TradeCount)
	}
	if f60.BuySellRatio != 0.5 {
		t.Errorf("60s buySellRatio = %f, want 0.5", f60.BuySellRatio)
	}
}

func TestTradeFlowStore_BuySellRatioInf(t *testing.T) {
	store := newTradeFlowStore([]int{15}, 30*time.Second)
	now := time.Now().UTC()
	store.add(domain.PublicTrade{Symbol: "BTCUSDT", Side: "Buy", Size: 5, Timestamp: now})

	flows := store.flow("BTCUSDT")
	if !math.IsInf(flows[0].BuySellRatio, 1) {
		t.Errorf("expected +inf ratio when no sell volume, got %f", flows[0].BuySellRatio)
	}
}

func TestTradeFlowStore_PruneOldTrades(t *testing.T) {
	store := newTradeFlowStore([]int{1}, 30*time.Second)
	now := time.Now().UTC()
	store.add(domain.PublicTrade{Symbol: "BTCUSDT", Side: "Buy", Size: 1, Timestamp: now.Add(-3 * time.Second)})
	store.add(domain.PublicTrade{Symbol: "BTCUSDT", Side: "Buy", Size: 2, Timestamp: now})

	flows := store.flow("BTCUSDT")
	if flows[0].BuyVolume != 2 {
		t.Errorf("buyVolume after prune = %f, want 2", flows[0].BuyVolume)
	}
	if flows[0].TradeCount != 1 {
		t.Errorf("tradeCount after prune = %d, want 1", flows[0].TradeCount)
	}
}

func TestDispatchOrderBookMessage(t *testing.T) {
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 60,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		OrderbookDepth: 50,
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer svc.Stop(context.Background())

	snapshot := []byte(`{
		"topic": "orderbook.50.BTCUSDT",
		"type": "snapshot",
		"ts": 1672324800000,
		"data": {
			"s": "BTCUSDT",
			"b": [["100","1"],["99","2"]],
			"a": [["101","1"],["102","2"]]
		}
	}`)

	if err := svc.dispatchMessage(snapshot); err != nil {
		t.Fatalf("dispatch orderbook snapshot: %v", err)
	}

	summary, err := svc.GetOrderBookSummary(context.Background(), "BTCUSDT", 1000, "Buy")
	if err != nil {
		t.Fatalf("get orderbook summary: %v", err)
	}
	if summary.BestBid != 100 {
		t.Errorf("bestBid = %f, want 100", summary.BestBid)
	}
	if summary.BestAsk != 101 {
		t.Errorf("bestAsk = %f, want 101", summary.BestAsk)
	}
	if summary.BidDepth != 3 {
		t.Errorf("bidDepth = %f, want 3", summary.BidDepth)
	}
	if summary.AskDepth != 3 {
		t.Errorf("askDepth = %f, want 3", summary.AskDepth)
	}
}

func TestDispatchPublicTradeMessage(t *testing.T) {
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 60,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		TradeFlowWindows: []int{15, 60},
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer svc.Stop(context.Background())

	now := time.Now().UTC()
	ts1 := now.Add(-5 * time.Second).UnixMilli()
	ts2 := now.Add(-3 * time.Second).UnixMilli()
	payload := fmt.Sprintf(`{
		"topic": "publicTrade.BTCUSDT",
		"type": "snapshot",
		"ts": %d,
		"data": [
			{"T":%d,"s":"BTCUSDT","p":"100","v":"1","S":"Buy","L":"TickPlus"},
			{"T":%d,"s":"BTCUSDT","p":"101","v":"2","S":"Sell","L":"TickMinus"}
		]
	}`, ts1, ts1, ts2)

	if err := svc.dispatchMessage([]byte(payload)); err != nil {
		t.Fatalf("dispatch publicTrade: %v", err)
	}

	flows, err := svc.GetTradeFlow(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("get trade flow: %v", err)
	}
	if len(flows) != 2 {
		t.Fatalf("expected 2 flows, got %d", len(flows))
	}
	if flows[0].BuyVolume != 1 {
		t.Errorf("buyVolume = %f, want 1", flows[0].BuyVolume)
	}
	if flows[0].SellVolume != 2 {
		t.Errorf("sellVolume = %f, want 2", flows[0].SellVolume)
	}
	if flows[0].TradeCount != 2 {
		t.Errorf("tradeCount = %d, want 2", flows[0].TradeCount)
	}
}

func TestHealthStatus_WithOrderBookAndTradeFlow(t *testing.T) {
	cfg := app.MarketDataConfig{
		StaleDataThresholdSeconds: 30,
		Timeframes:                []string{"15m"},
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)
	now := time.Now().UTC()

	// Only orderbook fresh
	svc.orderBooks.reset("BTCUSDT", 1, []domain.OrderBookLevel{{Price: 100, Size: 1}}, []domain.OrderBookLevel{{Price: 101, Size: 1}})
	// Manually set lastUpdate to now because reset uses time.Now
	svc.orderBooks.lastUpdate("BTCUSDT")

	status := svc.HealthStatus("BTCUSDT")
	if !status.Healthy {
		t.Errorf("expected healthy with fresh orderbook, got staleReason=%s", status.StaleReason)
	}
	if !status.OrderBookHealthy {
		t.Error("expected orderbook healthy")
	}

	// Stale orderbook
	svc.orderBooks.mu.Lock()
	if ob, ok := svc.orderBooks.books["BTCUSDT"]; ok {
		ob.lastUpdate = now.Add(-60 * time.Second)
	}
	svc.orderBooks.mu.Unlock()

	status = svc.HealthStatus("BTCUSDT")
	if status.Healthy {
		t.Error("expected unhealthy with stale orderbook")
	}
	if status.OrderBookHealthy {
		t.Error("expected orderbook unhealthy")
	}
	if !strings.Contains(status.StaleReason, "stale_orderbook") {
		t.Errorf("expected stale_orderbook in reason, got %s", status.StaleReason)
	}
}

func TestStart_BackfillAllFails_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always return error so backfill fails
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		RESTURL:                   server.URL,
		StaleDataThresholdSeconds: 60,
		BackfillCandles:           1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc := NewBybitWSMarketDataService(cfg, repo, server.Client(), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := svc.Start(ctx)
	if err == nil {
		t.Fatal("expected error when all backfills fail, got nil")
	}
	if !strings.Contains(err.Error(), "backfills failed") {
		t.Errorf("expected 'backfills failed' error, got: %v", err)
	}
}

func TestStart_BackfillAllFails_SecondStartRetries(t *testing.T) {
	failCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failCount++
		if failCount == 1 {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"retCode":0,
			"result":{
				"category":"linear",
				"list":[["1672324800000","100","110","90","105","10","1000"]]
			}
		}`))
	}))
	defer server.Close()

	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		RESTURL:                   server.URL,
		StaleDataThresholdSeconds: 60,
		BackfillCandles:           1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc := NewBybitWSMarketDataService(cfg, repo, server.Client(), nil)

	ctx := context.Background()

	// First Start should fail
	if err := svc.Start(ctx); err == nil {
		t.Fatal("expected first Start to fail")
	}

	// Second Start should retry and succeed (server now returns data)
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("expected second Start to succeed, got: %v", err)
	}

	// Verify data is actually present
	candles, _ := svc.GetCandles(ctx, "BTCUSDT", "15m", 1)
	if len(candles) == 0 {
		t.Error("expected cached candles after second Start")
	}

	svc.Stop(ctx)
}

func TestStart_Stop_StartAgain_NoRace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"retCode":0,
			"result":{
				"category":"linear",
				"list":[["1672324800000","100","110","90","105","10","1000"]]
			}
		}`))
	}))
	defer server.Close()

	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		RESTURL:                   server.URL,
		StaleDataThresholdSeconds: 60,
		BackfillCandles:           1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc := NewBybitWSMarketDataService(cfg, repo, server.Client(), nil)

	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := svc.Start(ctx); err != nil {
			t.Fatalf("iteration %d start error: %v", i, err)
		}
		if err := svc.Stop(ctx); err != nil {
			t.Fatalf("iteration %d stop error: %v", i, err)
		}
	}
}

func TestStart_AllBackfillsFail_CancelsInternalContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		RESTURL:                   server.URL,
		StaleDataThresholdSeconds: 60,
		BackfillCandles:           1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc := NewBybitWSMarketDataService(cfg, repo, server.Client(), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := svc.Start(ctx)
	if err == nil {
		t.Fatal("expected error when all backfills fail, got nil")
	}
	if !strings.Contains(err.Error(), "backfills failed") {
		t.Errorf("expected 'backfills failed' error, got: %v", err)
	}

	// Assert service is not running.
	if svc.running {
		t.Fatal("expected service to not be running after all backfills fail")
	}

	// Assert internal context is cancelled so no background goroutines leak.
	svc.mu.RLock()
	ctxErr := svc.ctx.Err()
	svc.mu.RUnlock()
	if ctxErr == nil {
		t.Fatal("expected internal context to be cancelled after all backfills fail")
	}

	// Second Start() should retry cleanly.
	// Create a new server that succeeds.
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"retCode":0,
			"result":{
				"category":"linear",
				"list":[["1672324800000","100","110","90","105","10","1000"]]
			}
		}`))
	}))
	defer server2.Close()

	// Second Start() should succeed with a working server.
	cfg2 := app.MarketDataConfig{
		RESTURL:                   server2.URL,
		StaleDataThresholdSeconds: 60,
		BackfillCandles:           1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc2 := NewBybitWSMarketDataService(cfg2, repo, server2.Client(), nil)
	if err := svc2.Start(ctx); err != nil {
		t.Fatalf("second Start should succeed after clean retry, got: %v", err)
	}
	svc2.Stop(ctx)
}

func TestStart_Timeout_CancelsInternalContext(t *testing.T) {
	// A mock HTTP server that blocks until the request context is cancelled.
	blockingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer blockingServer.Close()

	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		RESTURL:                   blockingServer.URL,
		StaleDataThresholdSeconds: 60,
		BackfillCandles:           1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc := NewBybitWSMarketDataService(cfg, repo, blockingServer.Client(), nil)
	// Override start timeout to a short value so the test doesn't block for 60s.
	svc.startTimeout = 300 * time.Millisecond

	ctx := context.Background()
	err := svc.Start(ctx)
	if err == nil {
		t.Fatal("expected timeout error from Start()")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected 'timed out' error, got: %v", err)
	}

	// Assert service is not running.
	if svc.running {
		t.Fatal("expected service to not be running after timeout")
	}

	// Assert internal context is cancelled so background goroutines cannot
	// enter the WS loop after the caller believes Start() failed.
	svc.mu.RLock()
	ctxErr := svc.ctx.Err()
	svc.mu.RUnlock()
	if ctxErr == nil {
		t.Fatal("expected internal context to be cancelled after timeout")
	}

	// Verify the background goroutine has exited by waiting on wg.
	// This proves no background WS loop can continue after Start() returns.
	done := make(chan struct{})
	go func() {
		svc.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// goroutine exited cleanly
	case <-time.After(3 * time.Second):
		t.Fatal("background goroutine did not terminate after timeout + cancel")
	}
}

func TestStart_OldFailedStartCannotCancelNewStart(t *testing.T) {
	// First server blocks indefinitely — triggers timeout in first Start.
	blockingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer blockingServer.Close()

	// Second server responds immediately — will be used by second Start.
	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"retCode":0,
			"result":{
				"category":"linear",
				"list":[["1672324800000","100","110","90","105","10","1000"]]
			}
		}`))
	}))
	defer goodServer.Close()

	repo := &fakeCandleRepo{}

	blockingCfg := app.MarketDataConfig{
		RESTURL:                   blockingServer.URL,
		StaleDataThresholdSeconds: 60,
		BackfillCandles:           1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc := NewBybitWSMarketDataService(blockingCfg, repo, blockingServer.Client(), nil)
	svc.startTimeout = 300 * time.Millisecond

	ctx := context.Background()

	// First Start — the backfill blocks, Start times out.
	err := svc.Start(ctx)
	if err == nil {
		t.Fatal("expected first Start to time out")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}

	// Second Start — uses the good server, should succeed.
	goodCfg := app.MarketDataConfig{
		RESTURL:                   goodServer.URL,
		StaleDataThresholdSeconds: 60,
		BackfillCandles:           1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc2 := NewBybitWSMarketDataService(goodCfg, repo, goodServer.Client(), nil)
	if err := svc2.Start(ctx); err != nil {
		t.Fatalf("second Start should succeed, got: %v", err)
	}

	// Assert service is running after second Start.
	if !svc2.running {
		t.Fatal("expected service to be running after second Start")
	}

	// Assert the second Start's internal context is alive (not cancelled by the old goroutine).
	svc2.mu.RLock()
	ctxErr := svc2.ctx.Err()
	svc2.mu.RUnlock()
	if ctxErr != nil {
		t.Fatalf("expected second Start's context to be alive, got: %v", ctxErr)
	}

	// Stop should succeed cleanly.
	if err := svc2.Stop(ctx); err != nil {
		t.Fatalf("Stop after second Start failed: %v", err)
	}
}

func TestStart_OldFailedStartCannotOverwriteNewStartResult(t *testing.T) {
	blockingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer blockingServer.Close()

	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"retCode":0,
			"result":{
				"category":"linear",
				"list":[["1672324800000","100","110","90","105","10","1000"]]
			}
		}`))
	}))
	defer goodServer.Close()

	repo := &fakeCandleRepo{}

	// First Start with blocking server → times out.
	blockingCfg := app.MarketDataConfig{
		RESTURL:                   blockingServer.URL,
		StaleDataThresholdSeconds: 60,
		BackfillCandles:           1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc := NewBybitWSMarketDataService(blockingCfg, repo, blockingServer.Client(), nil)
	svc.startTimeout = 300 * time.Millisecond

	ctx := context.Background()

	if err := svc.Start(ctx); err == nil {
		t.Fatal("expected first Start to time out")
	}

	// Second Start with working server → succeeds.
	goodCfg := app.MarketDataConfig{
		RESTURL:                   goodServer.URL,
		StaleDataThresholdSeconds: 60,
		BackfillCandles:           1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc2 := NewBybitWSMarketDataService(goodCfg, repo, goodServer.Client(), nil)
	if err := svc2.Start(ctx); err != nil {
		t.Fatalf("second Start should succeed, got: %v", err)
	}

	// Third Start while already running — should return nil (Start() result), not the old error.
	start3Err := svc2.Start(ctx)
	if start3Err != nil {
		t.Fatalf("third Start (while already running) should return nil, got: %v", start3Err)
	}

	// Cleanup.
	svc2.Stop(ctx)
}

func TestOrderBookStore_ResetReplacesOldData(t *testing.T) {
	store := newOrderBookStore(30 * time.Second)
	store.reset("BTCUSDT", 1, []domain.OrderBookLevel{
		{Price: 100, Size: 1},
	}, []domain.OrderBookLevel{
		{Price: 101, Size: 1},
	})

	store.reset("BTCUSDT", 2, []domain.OrderBookLevel{
		{Price: 200, Size: 2},
	}, []domain.OrderBookLevel{
		{Price: 201, Size: 2},
	})

	summary := store.summary("BTCUSDT", 100, "Buy")
	if summary.BestBid != 200 {
		t.Errorf("bestBid after reset = %f, want 200", summary.BestBid)
	}
	if summary.BestAsk != 201 {
		t.Errorf("bestAsk after reset = %f, want 201", summary.BestAsk)
	}
}

// TestStart_OldFailedStartCannotRunWSLoopAfterNewStart verifies that an old
// (failed/timed-out) Start goroutine cannot enter runWSLoop after a newer
// Start() succeeds on the same service instance.
func TestStart_OldFailedStartCannotRunWSLoopAfterNewStart(t *testing.T) {
	blockCount := 0
	var blockMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blockMu.Lock()
		blockCount++
		bc := blockCount
		blockMu.Unlock()

		if bc == 1 {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"retCode":0,
			"result":{
				"category":"linear",
				"list":[["1672324800000","100","110","90","105","10","1000"]]
			}
		}`))
	}))
	defer server.Close()

	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		RESTURL:                   server.URL,
		StaleDataThresholdSeconds: 60,
		BackfillCandles:           1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc := NewBybitWSMarketDataService(cfg, repo, server.Client(), nil)
	svc.startTimeout = 300 * time.Millisecond

	wsLoopEntered := make(chan struct{}, 1)
	svc.runWSLoopHook = func() {
		wsLoopEntered <- struct{}{}
	}

	ctx := context.Background()

	err := svc.Start(ctx)
	if err == nil {
		t.Fatal("expected first Start to time out")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}

	done := make(chan struct{})
	go func() {
		svc.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("old goroutine did not exit after first Start timeout")
	}

	select {
	case <-wsLoopEntered:
		t.Fatal("old goroutine should NOT have entered runWSLoop")
	default:
	}

	if err := svc.Start(ctx); err != nil {
		t.Fatalf("second Start should succeed, got: %v", err)
	}

	select {
	case <-wsLoopEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("second goroutine should have entered runWSLoop")
	}

	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

// TestStart_GoroutineUsesCapturedStartContext proves that the Start goroutine
// uses its per-start captured context, not the mutable s.ctx field.
// After a first Start times out and its internal context is cancelled, the
// goroutine exits because its captured startCtx is done — even if s.ctx is
// later replaced with a live context.
func TestStart_GoroutineUsesCapturedStartContext(t *testing.T) {
	blockCount := 0
	var blockMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		blockMu.Lock()
		blockCount++
		bc := blockCount
		blockMu.Unlock()

		if bc == 1 {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"retCode":0,
			"result":{
				"category":"linear",
				"list":[["1672324800000","100","110","90","105","10","1000"]]
			}
		}`))
	}))
	defer server.Close()

	repo := &fakeCandleRepo{}
	cfg := app.MarketDataConfig{
		RESTURL:                   server.URL,
		StaleDataThresholdSeconds: 60,
		BackfillCandles:           1,
		Symbols: app.SymbolsConfig{
			Mode:         "explicit",
			ExplicitList: []string{"BTCUSDT"},
		},
		Timeframes: []string{"15m"},
	}
	svc := NewBybitWSMarketDataService(cfg, repo, server.Client(), nil)
	svc.startTimeout = 300 * time.Millisecond

	ctx := context.Background()

	err := svc.Start(ctx)
	if err == nil {
		t.Fatal("expected first Start to time out")
	}

	svc.mu.Lock()
	svc.ctx, svc.cancel = context.WithCancel(context.Background())
	svc.running = true
	liveCtx := svc.ctx
	svc.mu.Unlock()

	done := make(chan struct{})
	go func() {
		svc.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("old goroutine did not exit; it may be using s.ctx instead of captured startCtx")
	}

	if liveCtx.Err() != nil {
		t.Fatal("new live context was unexpectedly cancelled; old goroutine may be using s.cancel")
	}

	svc.mu.Lock()
	svc.running = false
	svc.cancel()
	svc.mu.Unlock()
}
