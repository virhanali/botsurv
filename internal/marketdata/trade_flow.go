package marketdata

import (
	"math"
	"sync"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

// tradeFlowStore maintains rolling trade flow windows per symbol.
type tradeFlowStore struct {
	mu             sync.RWMutex
	trades         map[string][]domain.PublicTrade // key: symbol
	windows        []int                           // e.g. [15, 60, 300]
	maxWindow      time.Duration
	staleThreshold time.Duration
}

func newTradeFlowStore(windows []int, staleThreshold time.Duration) *tradeFlowStore {
	maxSec := 0
	for _, w := range windows {
		if w > maxSec {
			maxSec = w
		}
	}
	return &tradeFlowStore{
		trades:         make(map[string][]domain.PublicTrade),
		windows:        append([]int(nil), windows...),
		maxWindow:      time.Duration(maxSec) * time.Second,
		staleThreshold: staleThreshold,
	}
}

// add inserts a public trade and prunes trades older than the max window.
func (s *tradeFlowStore) add(trade domain.PublicTrade) {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := s.trades[trade.Symbol]
	list = append(list, trade)
	cutoff := time.Now().UTC().Add(-s.maxWindow)
	// Prune old trades. Since trades are generally in order, find first valid.
	idx := len(list)
	for i, t := range list {
		if t.Timestamp.After(cutoff) || t.Timestamp.Equal(cutoff) {
			idx = i
			break
		}
	}
	list = list[idx:]
	s.trades[trade.Symbol] = list
}

// flow returns trade flow summaries for all configured windows.
func (s *tradeFlowStore) flow(symbol string) []domain.TradeFlow {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC()
	list := s.trades[symbol]
	lastUpdate := time.Time{}
	if len(list) > 0 {
		lastUpdate = list[len(list)-1].Timestamp
	}
	stale := now.Sub(lastUpdate) > s.staleThreshold

	flows := make([]domain.TradeFlow, 0, len(s.windows))
	for _, winSec := range s.windows {
		window := time.Duration(winSec) * time.Second
		cutoff := now.Add(-window)
		var buyVol, sellVol float64
		var count int
		for _, t := range list {
			if t.Timestamp.Before(cutoff) {
				continue
			}
			if t.Side == "Buy" {
				buyVol += t.Size
			} else {
				sellVol += t.Size
			}
			count++
		}
		ratio := 0.0
		if sellVol > 0 {
			ratio = buyVol / sellVol
		} else if buyVol > 0 {
			ratio = math.Inf(1)
		}
		flows = append(flows, domain.TradeFlow{
			Symbol:        symbol,
			WindowSeconds: winSec,
			BuyVolume:     buyVol,
			SellVolume:    sellVol,
			BuySellRatio:  ratio,
			TradeCount:    count,
			LastUpdate:    lastUpdate,
			Stale:         stale,
		})
	}
	return flows
}

// lastUpdate returns the timestamp of the most recent trade for a symbol.
func (s *tradeFlowStore) lastUpdate(symbol string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if list, ok := s.trades[symbol]; ok && len(list) > 0 {
		return list[len(list)-1].Timestamp
	}
	return time.Time{}
}
