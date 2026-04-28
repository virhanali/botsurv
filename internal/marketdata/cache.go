package marketdata

import (
	"sort"
	"sync"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

const defaultCandleCacheCap = 500

// candleKey builds a cache key for symbol+timeframe.
func candleKey(symbol, timeframe string) string {
	return symbol + "|" + timeframe
}

// candleCache is a thread-safe in-memory cache for candles.
type candleCache struct {
	mu          sync.RWMutex
	candles     map[string][]domain.Candle // key: symbol|timeframe
	lastUpdates map[string]time.Time       // key: symbol|timeframe
}

func newCandleCache() *candleCache {
	return &candleCache{
		candles:     make(map[string][]domain.Candle),
		lastUpdates: make(map[string]time.Time),
	}
}

// update adds or replaces a candle in the cache. It keeps candles sorted by OpenTime
// and trims the oldest entries when the per-key cap is exceeded.
func (c *candleCache) update(symbol, timeframe string, candle domain.Candle) {
	key := candleKey(symbol, timeframe)
	c.mu.Lock()
	defer c.mu.Unlock()

	list := c.candles[key]
	idx := sort.Search(len(list), func(i int) bool {
		return list[i].OpenTime >= candle.OpenTime
	})
	if idx < len(list) && list[idx].OpenTime == candle.OpenTime {
		list[idx] = candle
	} else {
		list = append(list, domain.Candle{})
		copy(list[idx+1:], list[idx:])
		list[idx] = candle
	}
	if len(list) > defaultCandleCacheCap {
		list = list[len(list)-defaultCandleCacheCap:]
	}
	c.candles[key] = list
	c.lastUpdates[key] = time.Now().UTC()
}

// get returns up to limit most recent candles.
func (c *candleCache) get(symbol, timeframe string, limit int) []domain.Candle {
	key := candleKey(symbol, timeframe)
	c.mu.RLock()
	defer c.mu.RUnlock()

	list := c.candles[key]
	if len(list) == 0 {
		return nil
	}
	if limit <= 0 || limit >= len(list) {
		out := make([]domain.Candle, len(list))
		copy(out, list)
		return out
	}
	start := len(list) - limit
	out := make([]domain.Candle, limit)
	copy(out, list[start:])
	return out
}

// lastUpdate returns the candle update time for a symbol/timeframe.
func (c *candleCache) lastUpdate(symbol, timeframe string) time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastUpdates[candleKey(symbol, timeframe)]
}

// lastUpdateAny returns the most recent candle update time for a symbol across all timeframes.
func (c *candleCache) lastUpdateAny(symbol string) time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var latest time.Time
	prefix := symbol + "|"
	for key, update := range c.lastUpdates {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix && update.After(latest) {
			latest = update
		}
	}
	return latest
}

// priceCache is a thread-safe in-memory cache for latest prices.
type priceCache struct {
	mu      sync.RWMutex
	prices  map[string]float64
	updates map[string]time.Time
}

func newPriceCache() *priceCache {
	return &priceCache{
		prices:  make(map[string]float64),
		updates: make(map[string]time.Time),
	}
}

func (p *priceCache) set(symbol string, price float64, t time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prices[symbol] = price
	p.updates[symbol] = t
}

func (p *priceCache) get(symbol string) (float64, time.Time, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	price, ok := p.prices[symbol]
	if !ok {
		return 0, time.Time{}, false
	}
	return price, p.updates[symbol], true
}

func (p *priceCache) lastUpdate(symbol string) time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.updates[symbol]
}
