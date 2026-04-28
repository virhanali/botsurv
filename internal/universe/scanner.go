package universe

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/db"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
)

// MarketDataReadonly is the subset of MarketDataService needed by the scanner.
type MarketDataReadonly interface {
	GetOrderBookSummary(ctx context.Context, symbol string) (*domain.OrderBookSummary, error)
	GetCandles(ctx context.Context, symbol, timeframe string, limit int) ([]domain.Candle, error)
	GetLatestPrice(ctx context.Context, symbol string) (float64, error)
}

// Scanner implements funnel-based universe scanning.
type Scanner struct {
	cfg      app.UniverseConfig
	strategy app.StrategyConfig
	llmRoute app.LLMRoutingConfig
	restURL  string
	md       MarketDataReadonly
	universe db.UniverseRepository
	candles  db.CandleRepository
	log      *logger.Logger
	bybit    *bybitClient

	mu        sync.RWMutex
	watchlist map[string]time.Time // external signal symbols with expiry
}

// NewScanner creates a new universe Scanner.
func NewScanner(
	cfg app.UniverseConfig,
	strategy app.StrategyConfig,
	llmRoute app.LLMRoutingConfig,
	restURL string,
	md MarketDataReadonly,
	universe db.UniverseRepository,
	candles db.CandleRepository,
	log *logger.Logger,
) *Scanner {
	return &Scanner{
		cfg:       cfg,
		strategy:  strategy,
		llmRoute:  llmRoute,
		restURL:   restURL,
		md:        md,
		universe:  universe,
		candles:   candles,
		log:       log,
		bybit:     newBybitClient(restURL),
		watchlist: make(map[string]time.Time),
	}
}

// ScanAll performs Layer 1: fetch all USDT perps, filter, rank by volume.
func (s *Scanner) ScanAll(ctx context.Context) ([]domain.UniverseSymbol, error) {
	instruments, err := s.bybit.fetchInstruments(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch instruments: %w", err)
	}

	tickers, err := s.bybit.fetchTickers(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch tickers: %w", err)
	}

	tickerMap := make(map[string]bybitTicker, len(tickers))
	for _, t := range tickers {
		tickerMap[t.Symbol] = t
	}

	blacklistSet := make(map[string]bool, len(s.cfg.Blacklist))
	for _, sym := range s.cfg.Blacklist {
		blacklistSet[sym] = true
	}

	forceSet := make(map[string]bool, len(s.cfg.ForceIncludeSymbols))
	for _, sym := range s.cfg.ForceIncludeSymbols {
		forceSet[sym] = true
	}

	minVol := s.cfg.Filters.Min24hVolumeUSD
	if minVol <= 0 {
		minVol = 1_000_000
	}

	type scored struct {
		sym      domain.UniverseSymbol
		turnover float64
	}
	var candidates []scored

	for _, inst := range instruments {
		if inst.Status != "Trading" || inst.QuoteCoin != "USDT" {
			continue
		}
		if blacklistSet[inst.Symbol] {
			continue
		}

		ticker, hasTicker := tickerMap[inst.Symbol]
		turnover, _ := strconv.ParseFloat(ticker.Turnover24h, 64)

		// Force-include symbols bypass volume filter but still need trading status
		if !forceSet[inst.Symbol] && !hasTicker {
			continue
		}
		if !forceSet[inst.Symbol] && turnover < minVol {
			continue
		}

		// Compute spread from ticker if available
		var spreadBps float64
		if hasTicker {
			bid, _ := strconv.ParseFloat(ticker.Bid1Price, 64)
			ask, _ := strconv.ParseFloat(ticker.Ask1Price, 64)
			if bid > 0 && ask > 0 && ask > bid {
				spreadBps = (ask - bid) / ((ask + bid) / 2) * 10000
			}
		}

		maxSpread := s.cfg.Filters.MaxSpreadBps
		if maxSpread <= 0 {
			maxSpread = 50
		}
		if !forceSet[inst.Symbol] && spreadBps > maxSpread {
			continue
		}

		minNotional, _ := strconv.ParseFloat(inst.MinOrderQty, 64)
		tickSize, _ := strconv.ParseFloat(inst.TickSize, 64)
		lotSize, _ := strconv.ParseFloat(inst.QtyStep, 64)
		maxLev, _ := strconv.ParseFloat(inst.MaxLeverage, 64)

		usym := domain.UniverseSymbol{
			SymbolInfo: domain.SymbolInfo{
				Symbol:      inst.Symbol,
				Status:      inst.Status,
				QuoteAsset:  inst.QuoteCoin,
				BaseAsset:   inst.BaseCoin,
				MinNotional: minNotional,
				TickSize:    tickSize,
				LotSize:     lotSize,
				MaxLeverage: maxLev,
			},
			Blacklist:    false,
			ForceInclude: forceSet[inst.Symbol],
		}

		candidates = append(candidates, scored{sym: usym, turnover: turnover})
	}

	// Sort by turnover descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].turnover > candidates[j].turnover
	})

	limit := s.cfg.LightScanMaxSymbols
	if limit <= 0 {
		limit = 100
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	result := make([]domain.UniverseSymbol, len(candidates))
	for i, c := range candidates {
		result[i] = c.sym
	}

	s.log.Info("universe layer1 scan complete", map[string]any{
		"total_instruments": len(instruments),
		"passed_filter":     len(result),
	})

	return result, nil
}

// FilterQuality performs Layer 2: market quality filtering.
func (s *Scanner) FilterQuality(ctx context.Context, symbols []domain.UniverseSymbol) ([]domain.Candidate, error) {
	maxSymbols := s.cfg.QualityFilterMaxSymbols
	if maxSymbols <= 0 {
		maxSymbols = 30
	}

	var candidates []domain.Candidate

	for _, sym := range symbols {
		if len(candidates) >= maxSymbols {
			break
		}

		ob, err := s.md.GetOrderBookSummary(ctx, sym.Symbol)
		if err != nil {
			s.log.Debug("skip symbol: no orderbook", map[string]any{"symbol": sym.Symbol, "error": err.Error()})
			continue
		}
		if ob == nil || ob.Stale {
			continue
		}

		// Spread check
		maxSpread := s.cfg.Filters.MaxSpreadBps
		if maxSpread <= 0 {
			maxSpread = 50
		}
		if !sym.ForceInclude && ob.SpreadBps > maxSpread {
			continue
		}

		// Slippage check
		if !sym.ForceInclude && ob.EstimatedSlippageBps >= 100 {
			continue
		}

		// Depth check
		if !sym.ForceInclude && ob.DepthToPositionSizeRatio <= 0 {
			continue
		}

		// Get ATR from 1H candles
		atr := s.computeATR(ctx, sym.Symbol, 14)
		price, _ := s.md.GetLatestPrice(ctx, sym.Symbol)
		if price <= 0 {
			price = ob.BestBid
		}
		if price <= 0 {
			s.log.Debug("skip symbol: no price available", map[string]any{"symbol": sym.Symbol})
			continue
		}

		// Compute scores
		liq, exec, vol := computeScores(*ob, atr, price)
		setup := 50.0 // placeholder until Phase 8 Setup Engine
		candScore := candidateScore(liq, exec, setup, vol)

		// Placeholder values until Phase 8
		rr := 2.5                // placeholder
		expectedMove := atr * 2  // placeholder
		estCost := price * 0.001 // placeholder: 10bps

		cand := domain.Candidate{
			ProposedTrade: domain.ProposedTrade{
				Symbol:             sym.Symbol,
				SetupScore:         setup,
				RR:                 rr,
				ExpectedMove:       expectedMove,
				EstimatedTotalCost: estCost,
			},
			LiquidityScore:  liq,
			ExecutionScore:  exec,
			VolatilityScore: vol,
			CandidateScore:  candScore,
		}

		// Evaluate LLM eligibility
		cand = evaluateLLMEligibility(cand, *ob, s.cfg.Filters, s.llmRoute, s.strategy)

		candidates = append(candidates, cand)
	}

	s.log.Info("universe layer2 quality filter complete", map[string]any{
		"input_symbols": len(symbols),
		"quality_pass":  len(candidates),
	})

	return candidates, nil
}

// RefreshUniverse runs the full scan pipeline and persists results.
func (s *Scanner) RefreshUniverse(ctx context.Context) error {
	symbols, err := s.ScanAll(ctx)
	if err != nil {
		return fmt.Errorf("scan all: %w", err)
	}

	// Merge force-include symbols from DB
	dbSymbols, dbErr := s.universe.GetAll(ctx)
	if dbErr != nil {
		s.log.Error("failed to load DB symbols for merge", map[string]any{"error": dbErr.Error()})
	}
	for _, dbs := range dbSymbols {
		if dbs.ForceInclude {
			found := false
			for _, s2 := range symbols {
				if s2.Symbol == dbs.Symbol {
					found = true
					break
				}
			}
			if !found {
				symbols = append(symbols, dbs)
			}
		}
	}

	// Persist Layer 1 results
	now := time.Now()
	for _, sym := range symbols {
		sym.LastScanAt = now
		if err := s.universe.InsertOrUpdate(ctx, sym); err != nil {
			s.log.Error("persist universe symbol failed", map[string]any{"symbol": sym.Symbol, "error": err.Error()})
		}
	}

	s.log.Info("universe refresh complete", map[string]any{"symbols": len(symbols)})
	return nil
}

// GetUniverse reads current universe from DB.
func (s *Scanner) GetUniverse(ctx context.Context) ([]domain.UniverseSymbol, error) {
	return s.universe.GetAll(ctx)
}

// AddForceInclude adds or updates a symbol with force_include=true.
func (s *Scanner) AddForceInclude(ctx context.Context, symbol string) error {
	existing, _ := s.universe.GetBySymbol(ctx, symbol)
	if existing != nil {
		existing.ForceInclude = true
		existing.Blacklist = false
		return s.universe.InsertOrUpdate(ctx, *existing)
	}
	return s.universe.InsertOrUpdate(ctx, domain.UniverseSymbol{
		SymbolInfo:   domain.SymbolInfo{Symbol: symbol, Status: "unknown"},
		ForceInclude: true,
	})
}

// RemoveSymbol sets blacklist=true on a symbol.
func (s *Scanner) RemoveSymbol(ctx context.Context, symbol string) error {
	existing, err := s.universe.GetBySymbol(ctx, symbol)
	if err != nil || existing == nil {
		return s.universe.InsertOrUpdate(ctx, domain.UniverseSymbol{
			SymbolInfo: domain.SymbolInfo{Symbol: symbol},
			Blacklist:  true,
		})
	}
	existing.Blacklist = true
	existing.ForceInclude = false
	return s.universe.InsertOrUpdate(ctx, *existing)
}

// AddExternalSignal adds a symbol to the external watchlist with TTL.
func (s *Scanner) AddExternalSignal(symbol string, ttlHours int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ttl := time.Duration(ttlHours) * time.Hour
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	s.watchlist[symbol] = time.Now().Add(ttl)
}

// GetExternalWatchlist returns active external signal symbols.
func (s *Scanner) GetExternalWatchlist() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var active []string
	for sym, expiry := range s.watchlist {
		if now.Before(expiry) {
			active = append(active, sym)
		} else {
			delete(s.watchlist, sym)
		}
	}
	return active
}

// computeATR computes ATR from 1H candles.
func (s *Scanner) computeATR(ctx context.Context, symbol string, period int) float64 {
	candles, err := s.candles.GetBySymbolTimeframe(ctx, symbol, "1H", period+1)
	if err != nil || len(candles) < period+1 {
		return 0
	}

	var trSum float64
	for i := 1; i < len(candles); i++ {
		high := candles[i].High
		low := candles[i].Low
		prevClose := candles[i-1].Close

		tr := high - low
		if math.Abs(high-prevClose) > tr {
			tr = math.Abs(high - prevClose)
		}
		if math.Abs(low-prevClose) > tr {
			tr = math.Abs(low - prevClose)
		}
		trSum += tr
	}

	return trSum / float64(period)
}
