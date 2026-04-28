package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		Timeframes: []string{"15m", "1H"},
	}
	svc := NewBybitWSMarketDataService(cfg, nil, nil, nil)

	topics := svc.buildTopics()
	if len(topics) != 6 {
		t.Fatalf("expected 6 topics, got %d", len(topics))
	}

	expected := []string{
		"kline.15.BTCUSDT",
		"kline.60.BTCUSDT",
		"tickers.BTCUSDT",
		"kline.15.ETHUSDT",
		"kline.60.ETHUSDT",
		"tickers.ETHUSDT",
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
			expected := []string{"kline.15.BTCUSDT", "tickers.BTCUSDT"}
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
