package marketdata

import (
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

// orderBookStore maintains a local orderbook per symbol with thread-safe access.
type orderBookStore struct {
	mu             sync.RWMutex
	books          map[string]*orderBook // key: symbol
	staleThreshold time.Duration
}

// orderBook is the internal mutable orderbook for a single symbol.
type orderBook struct {
	bids       map[string]float64 // price string -> size
	asks       map[string]float64 // price string -> size
	seq        int64              // cross sequence from Bybit
	updateID   int64              // update ID (u) from Bybit; separate from seq
	lastUpdate time.Time
}

func newOrderBookStore(staleThreshold time.Duration) *orderBookStore {
	return &orderBookStore{
		books:          make(map[string]*orderBook),
		staleThreshold: staleThreshold,
	}
}

// reset rebuilds the orderbook from a snapshot.
func (s *orderBookStore) reset(symbol string, updateID, seq int64, bids, asks []domain.OrderBookLevel) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ob := &orderBook{
		bids:       make(map[string]float64, len(bids)),
		asks:       make(map[string]float64, len(asks)),
		seq:        seq,
		updateID:   updateID,
		lastUpdate: time.Now().UTC(),
	}
	for _, l := range bids {
		if l.Size > 0 {
			ob.bids[formatPrice(l.Price)] = l.Size
		}
	}
	for _, l := range asks {
		if l.Size > 0 {
			ob.asks[formatPrice(l.Price)] = l.Size
		}
	}
	s.books[symbol] = ob
}

// applyDelta applies incremental updates to the orderbook.
// Bybit V5 orderbook deltas do not provide a previous update ID or cross sequence
// that can be used to prove strict continuity, so we apply deltas without sequence
// validation. Stale detection is handled by lastUpdate timestamp.
//
// Rules:
//   - Out-of-order deltas (updateID <= stored updateID) are ignored.
//   - If no snapshot exists yet, the delta is ignored (no book to update).
func (s *orderBookStore) applyDelta(symbol string, updateID, seq int64, bids, asks []domain.OrderBookLevel) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	ob, ok := s.books[symbol]

	if !ok {
		// If no snapshot exists yet, ignore delta.
		return false
	}

	// Ignore out-of-order deltas (Bybit guarantees monotonically increasing u).
	if updateID <= ob.updateID {
		return false
	}

	for _, l := range bids {
		key := formatPrice(l.Price)
		if l.Size == 0 {
			delete(ob.bids, key)
		} else {
			ob.bids[key] = l.Size
		}
	}
	for _, l := range asks {
		key := formatPrice(l.Price)
		if l.Size == 0 {
			delete(ob.asks, key)
		} else {
			ob.asks[key] = l.Size
		}
	}
	ob.seq = seq
	ob.updateID = updateID
	ob.lastUpdate = time.Now().UTC()
	return true
}

// summary returns a computed OrderBookSummary for the symbol.
// targetNotional is used for slippage estimation and depth ratio.
// side should be "Buy" (long) or "Sell" (short) to estimate slippage on the correct side.
func (s *orderBookStore) summary(symbol string, targetNotional float64, side string) domain.OrderBookSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC()
	ob, ok := s.books[symbol]
	if !ok {
		return domain.OrderBookSummary{
			Symbol: symbol,
			Stale:  true,
		}
	}

	stale := now.Sub(ob.lastUpdate) > s.staleThreshold

	bidLevels := flattenAndSort(ob.bids, true)
	askLevels := flattenAndSort(ob.asks, false)

	bestBid := 0.0
	if len(bidLevels) > 0 {
		bestBid = bidLevels[0].Price
	}
	bestAsk := 0.0
	if len(askLevels) > 0 {
		bestAsk = askLevels[0].Price
	}

	spreadBps := 0.0
	if bestBid > 0 && bestAsk > 0 && bestAsk > bestBid {
		spreadBps = (bestAsk - bestBid) / ((bestAsk + bestBid) / 2) * 10000
	}

	bidDepth := sumDepth(bidLevels)
	askDepth := sumDepth(askLevels)

	estSlippageBps := estimateSlippage(bestBid, bestAsk, bidLevels, askLevels, targetNotional, side)

	depthToPosRatio := 0.0
	if targetNotional > 0 {
		if side == "Sell" || side == "SHORT" {
			depthToPosRatio = bidDepth * bestBid / targetNotional
		} else {
			depthToPosRatio = askDepth * bestAsk / targetNotional
		}
	}

	return domain.OrderBookSummary{
		Symbol:                   symbol,
		BestBid:                  bestBid,
		BestAsk:                  bestAsk,
		SpreadBps:                spreadBps,
		BidDepth:                 bidDepth,
		AskDepth:                 askDepth,
		DepthToPositionSizeRatio: depthToPosRatio,
		EstimatedSlippageBps:     estSlippageBps,
		LastUpdate:               ob.lastUpdate,
		Stale:                    stale,
	}
}

// lastUpdate returns the last orderbook update time for a symbol.
func (s *orderBookStore) lastUpdate(symbol string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ob, ok := s.books[symbol]; ok {
		return ob.lastUpdate
	}
	return time.Time{}
}

// flattenAndSort converts a price->size map into a sorted slice of levels.
// bids are sorted descending by price; asks ascending by price.
func flattenAndSort(m map[string]float64, descending bool) []domain.OrderBookLevel {
	levels := make([]domain.OrderBookLevel, 0, len(m))
	for priceStr, size := range m {
		price, err := parsePrice(priceStr)
		if err != nil {
			continue
		}
		levels = append(levels, domain.OrderBookLevel{Price: price, Size: size})
	}
	if descending {
		sort.Slice(levels, func(i, j int) bool {
			return levels[i].Price > levels[j].Price
		})
	} else {
		sort.Slice(levels, func(i, j int) bool {
			return levels[i].Price < levels[j].Price
		})
	}
	return levels
}

func sumDepth(levels []domain.OrderBookLevel) float64 {
	var sum float64
	for _, l := range levels {
		sum += l.Size
	}
	return sum
}

// estimateSlippage computes the estimated slippage in bps for a target notional.
// It walks the book from the mid price outward until the target notional is filled
// and computes the average execution price vs mid price.
// side should be "Buy" for longs (walks asks) or "Sell" for shorts (walks bids).
func estimateSlippage(bestBid, bestAsk float64, bidLevels, askLevels []domain.OrderBookLevel, targetNotional float64, side string) float64 {
	if targetNotional <= 0 {
		return 0
	}
	mid := 0.0
	if bestBid > 0 && bestAsk > 0 {
		mid = (bestBid + bestAsk) / 2
	} else if bestBid > 0 {
		mid = bestBid
	} else if bestAsk > 0 {
		mid = bestAsk
	}
	if mid == 0 {
		return 0
	}

	remaining := targetNotional
	totalSize := 0.0

	var levels []domain.OrderBookLevel
	if side == "Sell" || side == "SHORT" {
		levels = bidLevels
	} else {
		levels = askLevels
	}

	for _, l := range levels {
		levelNotional := l.Price * l.Size
		if levelNotional >= remaining {
			// Buy/sell just enough to fill the remaining notional
			sizeAtLevel := remaining / l.Price
			totalSize += sizeAtLevel
			remaining = 0
			break
		}
		totalSize += l.Size
		remaining -= levelNotional
	}
	if remaining > 0 {
		// Not enough depth — extremely high slippage
		return math.Inf(1)
	}
	avgPrice := targetNotional / totalSize
	slippage := math.Abs(avgPrice-mid) / mid * 10000
	return slippage
}

// formatPrice formats a float64 price as a string key.
func formatPrice(p float64) string {
	return strconv.FormatFloat(p, 'f', -1, 64)
}

// parsePrice parses a price string back to float64.
func parsePrice(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}
