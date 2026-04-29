package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/db"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
)

// BybitWSMarketDataService implements MarketDataService for Bybit public WebSocket + REST.
type BybitWSMarketDataService struct {
	config     app.MarketDataConfig
	candleRepo db.CandleRepository
	httpClient *http.Client
	logger     *logger.Logger

	candleCache *candleCache
	priceCache  *priceCache
	orderBooks  *orderBookStore
	tradeFlows  *tradeFlowStore

	mu      sync.RWMutex
	running bool

	// ws control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	wsConn *websocket.Conn
	wsMu   sync.Mutex

	staleThreshold time.Duration

	// orderbookGapLogLast tracks the last time we logged a delta rejection
	// per symbol to avoid flooding logs.
	orderbookGapLogLast   map[string]time.Time
	orderbookGapLogLastMu sync.Mutex

	// dynamic symbols for all_usdt_perpetual mode
	muDynamic      sync.RWMutex
	dynamicSymbols []string

	// readiness signals when initial backfill completes.
	ready       chan struct{}
	startResult error // result of the last Start(), protected by mu

	startTimeout time.Duration // max time to wait for initial backfill readiness

	// runWSLoopHook is a test-only hook called just before entering runWSLoop.
	// Set to nil for production.
	runWSLoopHook func()
}

// NewBybitWSMarketDataService creates a new Bybit market data service.
func NewBybitWSMarketDataService(
	config app.MarketDataConfig,
	candleRepo db.CandleRepository,
	httpClient *http.Client,
	log *logger.Logger,
) *BybitWSMarketDataService {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	if log == nil {
		log = logger.Default()
	}
	stale := time.Duration(config.StaleDataThresholdSeconds) * time.Second
	startTimeout := 60 * time.Second
	if config.StartTimeoutSeconds > 0 {
		startTimeout = time.Duration(config.StartTimeoutSeconds) * time.Second
	}
	return &BybitWSMarketDataService{
		config:              config,
		candleRepo:          candleRepo,
		httpClient:          httpClient,
		logger:              log,
		candleCache:         newCandleCache(),
		priceCache:          newPriceCache(),
		orderBooks:          newOrderBookStore(stale),
		tradeFlows:          newTradeFlowStore(config.TradeFlowWindows, stale),
		staleThreshold:      stale,
		ready:               make(chan struct{}),
		startTimeout:        startTimeout,
		orderbookGapLogLast: make(map[string]time.Time),
	}
}

// Start begins the market data service: backfills candles, then starts the WebSocket loop.
// Blocks until initial backfill completes or the provided context is cancelled.
// Returns an error if backfill fails for all symbol/timeframe pairs.
func (s *BybitWSMarketDataService) Start(ctx context.Context) error {
	if len(s.symbols()) == 0 {
		return fmt.Errorf("no symbols configured for market data")
	}

	s.mu.Lock()
	if s.running {
		ready := s.ready
		s.mu.Unlock()
		// Already running; wait for the same ready channel and return its result.
		select {
		case <-ready:
			s.mu.RLock()
			err := s.startResult
			s.mu.RUnlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.running = true
	s.startResult = nil
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.ready = make(chan struct{})
	readyCh := s.ready
	cancel := s.cancel
	startCtx := s.ctx
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		backfillOK := 0
		backfillTotal := 0

		// Backfill first so that older REST data does not overwrite live WS updates.
		if s.config.BackfillCandles > 0 {
			for _, symbol := range s.symbols() {
				for _, tf := range s.config.Timeframes {
					backfillTotal++
					backfillCtx, cancel := context.WithTimeout(startCtx, 30*time.Second)
					if err := s.backfillAndWarm(backfillCtx, symbol, tf, s.config.BackfillCandles); err != nil {
						s.logger.Warn("backfill failed", map[string]any{
							"symbol":    symbol,
							"timeframe": tf,
							"error":     err.Error(),
						})
					} else {
						backfillOK++
					}
					cancel()
				}
			}
		}

		// Fail closed if every backfill failed and we expected some data.
		if backfillTotal > 0 && backfillOK == 0 {
			s.failStartIfCurrent(readyCh, cancel, fmt.Errorf("market data start failed: all %d backfills failed", backfillTotal))
			close(readyCh)
			return
		}

		// Signal readiness after backfill completes.
		close(readyCh)

		if startCtx.Err() != nil {
			return
		}

		if s.runWSLoopHook != nil {
			s.runWSLoopHook()
		}
		s.runWSLoop()
	}()

	// Wait for initial backfill readiness (or timeout).
	select {
	case <-readyCh:
		s.mu.RLock()
		err := s.startResult
		s.mu.RUnlock()
		return err
	case <-ctx.Done():
		err := fmt.Errorf("market data start cancelled: %w", ctx.Err())
		s.failStartIfCurrent(readyCh, cancel, err)
		return err
	case <-time.After(s.startTimeout):
		err := fmt.Errorf("market data start timed out waiting for initial backfill")
		s.failStartIfCurrent(readyCh, cancel, err)
		return err
	}
}

// Stop shuts down the market data service gracefully.
func (s *BybitWSMarketDataService) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.cancel()
	s.startResult = nil
	// Reset readiness channel so a future Start() can wait again.
	// The old goroutine may still reference the old channel; closing
	// a closed channel panics, so we only swap the field here.
	s.ready = make(chan struct{})
	s.mu.Unlock()

	s.closeWS()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.logger.Error("graceful shutdown timed out", map[string]any{"error": ctx.Err().Error()})
		s.closeWS()
		<-done
		return ctx.Err()
	}
}

// GetCandles returns candles from cache first, falling back to repository.
func (s *BybitWSMarketDataService) GetCandles(ctx context.Context, symbol, timeframe string, limit int) ([]domain.Candle, error) {
	cached := s.candleCache.get(symbol, timeframe, limit)
	if len(cached) > 0 {
		return cached, nil
	}
	if s.candleRepo != nil {
		return s.candleRepo.GetBySymbolTimeframe(ctx, symbol, timeframe, limit)
	}
	return nil, nil
}

// GetLatestPrice returns the latest cached price for a symbol.
func (s *BybitWSMarketDataService) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	price, _, ok := s.priceCache.get(symbol)
	if !ok {
		return 0, fmt.Errorf("no price available for %s", symbol)
	}
	return price, nil
}

// GetOrderBookSummary returns a computed orderbook summary for a symbol.
func (s *BybitWSMarketDataService) GetOrderBookSummary(ctx context.Context, symbol string, targetNotional float64, side string) (domain.OrderBookSummary, error) {
	summary := s.orderBooks.summary(symbol, targetNotional, side)
	return summary, nil
}

// GetTradeFlow returns trade flow summaries for all configured windows for a symbol.
func (s *BybitWSMarketDataService) GetTradeFlow(ctx context.Context, symbol string) ([]domain.TradeFlow, error) {
	return s.tradeFlows.flow(symbol), nil
}

// HealthStatus returns detailed market data health for a symbol.
func (s *BybitWSMarketDataService) HealthStatus(symbol string) domain.MarketDataHealth {
	now := time.Now()
	priceUpdate := s.priceCache.lastUpdate(symbol)
	candleUpdate := s.candleCache.lastUpdateAny(symbol)
	orderBookUpdate := s.orderBooks.lastUpdate(symbol)
	tradeFlowUpdate := s.tradeFlows.lastUpdate(symbol)

	hasPrice := !priceUpdate.IsZero()
	hasCandle := !candleUpdate.IsZero()
	hasOrderBook := !orderBookUpdate.IsZero()
	hasTradeFlow := !tradeFlowUpdate.IsZero()

	priceHealthy := !hasPrice || now.Sub(priceUpdate) <= s.staleThreshold
	candlesHealthy := !hasCandle || now.Sub(candleUpdate) <= s.staleThreshold
	orderBookHealthy := !hasOrderBook || now.Sub(orderBookUpdate) <= s.staleThreshold
	tradeFlowHealthy := !hasTradeFlow || now.Sub(tradeFlowUpdate) <= s.staleThreshold

	// Candles require all configured timeframes to be fresh if any candle data exists.
	if hasCandle {
		for _, tf := range s.config.Timeframes {
			update := s.candleCache.lastUpdate(symbol, tf)
			if update.IsZero() || now.Sub(update) > s.staleThreshold {
				candlesHealthy = false
				break
			}
		}
	}

	healthy := (hasPrice || hasCandle || hasOrderBook) && priceHealthy && candlesHealthy && orderBookHealthy && tradeFlowHealthy

	staleReason := ""
	if !healthy {
		reasons := []string{}
		if hasPrice && !priceHealthy {
			reasons = append(reasons, "stale_price")
		}
		if hasCandle && !candlesHealthy {
			reasons = append(reasons, "stale_candles")
		}
		if hasOrderBook && !orderBookHealthy {
			reasons = append(reasons, "stale_orderbook")
		}
		if hasTradeFlow && !tradeFlowHealthy {
			reasons = append(reasons, "stale_trade_flow")
		}
		if !hasPrice && !hasCandle && !hasOrderBook {
			reasons = append(reasons, "no_data")
		}
		staleReason = strings.Join(reasons, ",")
	}

	lastUpdate := priceUpdate
	if candleUpdate.After(lastUpdate) {
		lastUpdate = candleUpdate
	}
	if orderBookUpdate.After(lastUpdate) {
		lastUpdate = orderBookUpdate
	}
	if tradeFlowUpdate.After(lastUpdate) {
		lastUpdate = tradeFlowUpdate
	}

	return domain.MarketDataHealth{
		Symbol:           symbol,
		Healthy:          healthy,
		LastUpdate:       lastUpdate,
		StaleReason:      staleReason,
		CandlesHealthy:   candlesHealthy,
		OrderBookHealthy: orderBookHealthy,
		PriceHealthy:     priceHealthy,
	}
}

// IsHealthy reports whether market data for a symbol is not stale.
// It considers price, candle, and orderbook freshness.
func (s *BybitWSMarketDataService) IsHealthy(symbol string) bool {
	return s.HealthStatus(symbol).Healthy
}

// LastUpdate returns the most recent update time for a symbol across all data types.
func (s *BybitWSMarketDataService) LastUpdate(symbol string) time.Time {
	priceUpdate := s.priceCache.lastUpdate(symbol)
	candleUpdate := s.candleCache.lastUpdateAny(symbol)
	orderBookUpdate := s.orderBooks.lastUpdate(symbol)
	tradeFlowUpdate := s.tradeFlows.lastUpdate(symbol)

	latest := priceUpdate
	if candleUpdate.After(latest) {
		latest = candleUpdate
	}
	if orderBookUpdate.After(latest) {
		latest = orderBookUpdate
	}
	if tradeFlowUpdate.After(latest) {
		latest = tradeFlowUpdate
	}
	return latest
}

// runWSLoop manages the WebSocket connection lifecycle with auto-reconnect.
func (s *BybitWSMarketDataService) runWSLoop() {
	maxReconnectInterval := time.Duration(s.config.ReconnectIntervalSeconds) * time.Second
	if maxReconnectInterval < time.Second {
		maxReconnectInterval = time.Second
	}
	backoff := time.Second
	consecutiveRejections := 0

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		connectStart := time.Now()
		err := s.connectAndServe()
		if err == nil {
			backoff = time.Second
			consecutiveRejections = 0
		} else {
			s.logger.Error("websocket error", map[string]any{"error": err.Error()})
			if isSubscribeRejection(err) {
				consecutiveRejections++
				if consecutiveRejections >= 5 {
					backoff = 5 * time.Minute
				}
			} else {
				consecutiveRejections = 0
			}
		}
		if time.Since(connectStart) > 60*time.Second {
			backoff = time.Second
			consecutiveRejections = 0
		}

		select {
		case <-s.ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff = time.Duration(float64(backoff) * 1.5)
		backoff += time.Duration(rand.Intn(500)) * time.Millisecond
		ceiling := maxReconnectInterval
		if consecutiveRejections >= 5 {
			ceiling = max(maxReconnectInterval, 5*time.Minute)
		}
		if backoff > ceiling {
			backoff = ceiling
		}
	}
}

func isSubscribeRejection(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "subscribe rejected")
}

// connectAndServe dials the WebSocket, subscribes, and reads messages until disconnect.
func (s *BybitWSMarketDataService) connectAndServe() error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(s.ctx, s.config.WSURL, nil)
	if err != nil {
		return fmt.Errorf("dial ws: %w", err)
	}
	if s.ctx.Err() != nil {
		conn.Close()
		return fmt.Errorf("context cancelled after dial")
	}

	s.setWSConn(conn)
	defer func() {
		s.setWSConn(nil)
		conn.Close()
	}()

	if err := s.subscribe(conn); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	preAck, err := s.waitForSubscribeAck(conn)
	if err != nil {
		return fmt.Errorf("subscribe ack: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
		return fmt.Errorf("set read deadline: %w", err)
	}

	// Dispatch any frames received before the subscribe ack.
	for _, payload := range preAck {
		if err := s.dispatchMessage(payload); err != nil {
			s.logger.Warn("dispatch pre-ack message error", map[string]any{"error": err.Error()})
		}
	}

	// Ping goroutine scoped to this connection.
	pingCtx, pingCancel := context.WithCancel(s.ctx)
	var pingWg sync.WaitGroup
	pingWg.Add(1)
	defer func() {
		pingCancel()
		pingWg.Wait()
	}()
	go func() {
		defer pingWg.Done()
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-ticker.C:
				if err := s.pingConn(); err != nil {
					return
				}
			}
		}
	}()

	for {
		select {
		case <-s.ctx.Done():
			return nil
		default:
		}

		_, payload, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			if s.ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read message: %w", err)
		}

		if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
			return fmt.Errorf("set read deadline: %w", err)
		}

		if err := s.dispatchMessage(payload); err != nil {
			s.logger.Warn("dispatch message error", map[string]any{"error": err.Error()})
		}
	}
}

// waitForSubscribeAck reads frames until the subscribe ack is received and validated.
// It returns any non-ack payloads received before the ack so they can be dispatched
// once the subscription is confirmed.
func (s *BybitWSMarketDataService) waitForSubscribeAck(conn *websocket.Conn) ([][]byte, error) {
	var preAck [][]byte
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		if err := conn.SetReadDeadline(time.Now().Add(remaining)); err != nil {
			return nil, err
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return nil, err
		}
		var ack struct {
			Op      string `json:"op"`
			Success bool   `json:"success"`
			RetMsg  string `json:"ret_msg"`
		}
		if err := json.Unmarshal(payload, &ack); err == nil && ack.Op == "subscribe" {
			if !ack.Success {
				return nil, fmt.Errorf("subscribe rejected: %s", ack.RetMsg)
			}
			return preAck, nil
		}
		preAck = append(preAck, payload)
	}
	return nil, fmt.Errorf("subscribe ack timeout")
}

// subscribe sends a Bybit public WebSocket subscription request.
func (s *BybitWSMarketDataService) subscribe(conn *websocket.Conn) error {
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	defer conn.SetWriteDeadline(time.Time{})

	topics := s.buildTopics()
	msg := map[string]interface{}{
		"op":     "subscribe",
		"args":   topics,
		"req_id": "sub-" + strconv.FormatInt(time.Now().UnixNano(), 10),
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal subscribe: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("write subscribe: %w", err)
	}
	s.logger.Info("subscribed", map[string]any{"topics": len(topics)})
	return nil
}

// buildTopics constructs subscription topics from config symbols and timeframes.
func (s *BybitWSMarketDataService) buildTopics() []string {
	symbols := s.symbols()
	var topics []string
	for _, symbol := range symbols {
		for _, tf := range s.config.Timeframes {
			topics = append(topics, fmt.Sprintf("kline.%s.%s", mapTimeframeToBybit(tf), symbol))
		}
		topics = append(topics, "tickers."+symbol)
		if s.config.OrderbookDepth > 0 {
			topics = append(topics, fmt.Sprintf("orderbook.%d.%s", s.config.OrderbookDepth, symbol))
		}
		topics = append(topics, "publicTrade."+symbol)
	}
	return topics
}

// dispatchMessage routes a raw WebSocket payload to the appropriate handler.
func (s *BybitWSMarketDataService) dispatchMessage(payload []byte) error {
	var msg struct {
		Topic string `json:"topic"`
		Op    string `json:"op"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return err
	}
	if msg.Op == "pong" {
		return nil
	}
	if msg.Topic == "" {
		return nil
	}

	parts := splitTopic(msg.Topic)
	if len(parts) == 0 {
		return nil
	}

	switch parts[0] {
	case "kline":
		return s.handleKlineMessage(s.ctx, payload)
	case "tickers":
		return s.handleTickerMessage(payload)
	case "orderbook":
		return s.handleOrderBookMessage(payload)
	case "publicTrade":
		return s.handlePublicTradeMessage(payload)
	default:
		return nil
	}
}

// handleKlineMessage processes a raw Bybit kline WebSocket message.
func (s *BybitWSMarketDataService) handleKlineMessage(ctx context.Context, payload []byte) error {
	s.mu.RLock()
	if !s.running {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	symbol, timeframe, candles, err := parseKlineMessages(payload)
	if err != nil {
		return err
	}
	if s.ctx.Err() != nil {
		return nil
	}
	for _, candle := range candles {
		s.candleCache.update(symbol, timeframe, candle)
		if candle.Confirmed && s.candleRepo != nil {
			_, err := s.candleRepo.Insert(ctx, candle)
			if err != nil {
				s.logger.Warn("persist confirmed candle failed", map[string]any{
					"symbol":    symbol,
					"timeframe": timeframe,
					"openTime":  candle.OpenTime,
					"error":     err.Error(),
				})
			}
		}
	}
	return nil
}

// handleTickerMessage processes a raw Bybit ticker WebSocket message.
func (s *BybitWSMarketDataService) handleTickerMessage(payload []byte) error {
	s.mu.RLock()
	if !s.running {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	symbol, price, ts, hasPrice, err := parseTickerMessage(payload)
	if err != nil {
		return err
	}
	if !hasPrice {
		return nil
	}
	s.priceCache.set(symbol, price, ts)
	return nil
}

// handleOrderBookMessage processes a raw Bybit orderbook WebSocket message.
func (s *BybitWSMarketDataService) handleOrderBookMessage(payload []byte) error {
	s.mu.RLock()
	if !s.running {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	symbol, isSnapshot, updateID, seq, bids, asks, err := parseOrderBookMessage(payload)
	if err != nil {
		return err
	}
	if isSnapshot {
		s.orderBooks.reset(symbol, updateID, seq, bids, asks)
	} else {
		ok := s.orderBooks.applyDelta(symbol, updateID, seq, bids, asks)
		if !ok {
			// Throttle per-symbol to avoid flooding logs when deltas arrive before snapshot.
			s.orderbookGapLogLastMu.Lock()
			last, exists := s.orderbookGapLogLast[symbol]
			shouldLog := !exists || time.Since(last) > 5*time.Second
			if shouldLog {
				s.orderbookGapLogLast[symbol] = time.Now()
			}
			s.orderbookGapLogLastMu.Unlock()
			if shouldLog {
				s.logger.Debug("orderbook delta rejected (no snapshot)", map[string]any{
					"symbol": symbol,
				})
			}
		}
	}
	return nil
}

// handlePublicTradeMessage processes a raw Bybit publicTrade WebSocket message.
func (s *BybitWSMarketDataService) handlePublicTradeMessage(payload []byte) error {
	s.mu.RLock()
	if !s.running {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	symbol, trades, err := parsePublicTradeMessage(payload)
	if err != nil {
		return err
	}
	for _, t := range trades {
		if t.Symbol == "" {
			t.Symbol = symbol
		}
		s.tradeFlows.add(t)
	}
	return nil
}

// Backfill performs REST backfill for a symbol and timeframe and warms the cache.
func (s *BybitWSMarketDataService) Backfill(ctx context.Context, symbol, timeframe string, limit int) error {
	return s.backfillAndWarm(ctx, symbol, timeframe, limit)
}

// backfillAndWarm backfills candles and warms the in-memory cache.
func (s *BybitWSMarketDataService) backfillAndWarm(ctx context.Context, symbol, timeframe string, limit int) error {
	candles, err := backfillCandles(ctx, s.httpClient, s.config.RESTURL, symbol, timeframe, limit, s.candleRepo)
	if err != nil {
		return err
	}
	for _, c := range candles {
		s.candleCache.update(symbol, timeframe, c)
	}
	s.logger.Info("backfill warmed cache", map[string]any{
		"symbol":    symbol,
		"timeframe": timeframe,
		"candles":   len(candles),
	})
	return nil
}

// symbols returns the configured symbols to track.
func (s *BybitWSMarketDataService) symbols() []string {
	if s.config.Symbols.Mode == "explicit" {
		return s.config.Symbols.ExplicitList
	}
	s.muDynamic.RLock()
	defer s.muDynamic.RUnlock()
	return s.dynamicSymbols
}

// SetSymbols sets the symbols to track (used for all_usdt_perpetual mode).
func (s *BybitWSMarketDataService) SetSymbols(symbols []string) {
	s.muDynamic.Lock()
	defer s.muDynamic.Unlock()
	s.dynamicSymbols = symbols
}

// failStartIfCurrent records a fatal start error and cancels the captured context,
// but only mutates s.startResult and s.running if s.ready still matches readyCh
// (i.e. no newer Start() has replaced the readiness channel).
func (s *BybitWSMarketDataService) failStartIfCurrent(readyCh chan struct{}, cancel context.CancelFunc, err error) {
	s.mu.Lock()
	if s.ready == readyCh {
		s.startResult = err
		s.running = false
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *BybitWSMarketDataService) setWSConn(conn *websocket.Conn) {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	s.wsConn = conn
}

func (s *BybitWSMarketDataService) getWSConn() *websocket.Conn {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	return s.wsConn
}

func (s *BybitWSMarketDataService) closeWS() {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	if s.wsConn != nil {
		s.wsConn.Close()
		s.wsConn = nil
	}
}

func (s *BybitWSMarketDataService) pingConn() error {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	if s.wsConn == nil {
		return fmt.Errorf("no ws connection")
	}
	if err := s.wsConn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return s.wsConn.WriteMessage(websocket.TextMessage, []byte(`{"op":"ping"}`))
}
