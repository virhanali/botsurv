package shadow

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"time"

	"github.com/virhan/botsurv/internal/db"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
)

// MarketDataProvider provides the latest price for a symbol.
type MarketDataProvider interface {
	GetLatestPrice(ctx context.Context, symbol string) (float64, error)
	GetCandles(ctx context.Context, symbol, timeframe string, limit int) ([]domain.Candle, error)
}

// CounterfactualTracker tracks what happened to price after a candidate was generated.
type CounterfactualTracker struct {
	md          MarketDataProvider
	outcomeRepo db.CandidateOutcomeRepository
	log         *logger.Logger
	mu          sync.Mutex
	running     bool
}

// NewCounterfactualTracker creates a new tracker.
func NewCounterfactualTracker(md MarketDataProvider, outcomeRepo db.CandidateOutcomeRepository, log *logger.Logger) *CounterfactualTracker {
	return &CounterfactualTracker{
		md:          md,
		outcomeRepo: outcomeRepo,
		log:         log,
	}
}

// TrackCandidate creates a new candidate_outcome row and starts tracking.
func (t *CounterfactualTracker) TrackCandidate(ctx context.Context, decisionID, symbol string, side domain.Side, entryPrice, stopLoss float64, takeProfits []float64) error {
	tpsJSON, err := json.Marshal(takeProfits)
	if err != nil {
		tpsJSON = []byte("[]")
	}
	outcome := domain.CandidateOutcome{
		DecisionID:  decisionID,
		Symbol:      symbol,
		Side:        side,
		EntryPrice:  entryPrice,
		StopLoss:    stopLoss,
		TakeProfits: string(tpsJSON),
		Status:      "tracking",
	}
	if t.outcomeRepo != nil {
		return t.outcomeRepo.Insert(ctx, outcome)
	}
	return nil
}

// ProcessPending fetches all tracking outcomes and updates them with current price data.
func (t *CounterfactualTracker) ProcessPending(ctx context.Context) error {
	if t.outcomeRepo == nil {
		return nil
	}
	pending, err := t.outcomeRepo.GetPending(ctx)
	if err != nil {
		return err
	}
	for _, o := range pending {
		updated, err := t.updateOutcome(ctx, o)
		if err != nil {
			t.log.Warn("failed to update outcome", map[string]any{
				"decision_id": o.DecisionID,
				"symbol":      o.Symbol,
				"error":       err.Error(),
			})
			continue
		}
		if err := t.outcomeRepo.Update(ctx, updated); err != nil {
			t.log.Warn("failed to persist outcome update", map[string]any{
				"decision_id": o.DecisionID,
				"error":       err.Error(),
			})
		}
	}
	return nil
}

func (t *CounterfactualTracker) updateOutcome(ctx context.Context, o domain.CandidateOutcome) (domain.CandidateOutcome, error) {
	now := time.Now()
	age := now.Sub(o.TrackedUntil)
	if o.TrackedUntil.IsZero() {
		// First tracking event — we use the creation time from decision_id context
		// For now, treat the tracked_until as the timestamp we set when inserting.
	}
	elapsedMinutes := age.Minutes()

	price, err := t.md.GetLatestPrice(ctx, o.Symbol)
	if err != nil || price <= 0 {
		return o, err
	}

	// Track price at specific time checkpoints
	setCheckpoint := func(targetMin float64, field **float64) {
		if *field != nil {
			return
		}
		if math.Abs(elapsedMinutes-targetMin) < 5 {
			v := price
			*field = &v
		}
	}
	setCheckpoint(15, &o.PriceAt15m)
	setCheckpoint(60, &o.PriceAt1h)
	setCheckpoint(240, &o.PriceAt4h)
	setCheckpoint(1440, &o.PriceAt24h)

	// Compute MFE/MAE
	if o.Side == domain.SideLong {
		// Favorable = price went up
		favPct := (price - o.EntryPrice) / o.EntryPrice
		if o.MaxFavorableExcursion24h == nil || favPct > *o.MaxFavorableExcursion24h {
			o.MaxFavorableExcursion24h = &favPct
		}
		advPct := (o.EntryPrice - price) / o.EntryPrice
		if o.MaxAdverseExcursion24h == nil || advPct > *o.MaxAdverseExcursion24h {
			o.MaxAdverseExcursion24h = &advPct
		}
	} else {
		// SHORT: favorable = price went down
		favPct := (o.EntryPrice - price) / o.EntryPrice
		if o.MaxFavorableExcursion24h == nil || favPct > *o.MaxFavorableExcursion24h {
			o.MaxFavorableExcursion24h = &favPct
		}
		advPct := (price - o.EntryPrice) / o.EntryPrice
		if o.MaxAdverseExcursion24h == nil || advPct > *o.MaxAdverseExcursion24h {
			o.MaxAdverseExcursion24h = &advPct
		}
	}

	// Check SL hit
	if !o.WouldHaveHitSL {
		if o.Side == domain.SideLong {
			if price <= o.StopLoss {
				o.WouldHaveHitSL = true
			}
		} else {
			if price >= o.StopLoss {
				o.WouldHaveHitSL = true
			}
		}
	}

	// Check TP hit
	if !o.WouldHaveHitTP1 {
		var tps []float64
		if err := json.Unmarshal([]byte(o.TakeProfits), &tps); err == nil && len(tps) > 0 {
			if o.Side == domain.SideLong {
				if price >= tps[0] {
					o.WouldHaveHitTP1 = true
				}
			} else {
				if price <= tps[0] {
					o.WouldHaveHitTP1 = true
				}
			}
		}
	}

	// Determine outcome
	o.TrackedUntil = now
	if o.WouldHaveHitTP1 {
		o.WouldHaveOutcome = "tp1_hit"
		o.Status = "completed"
		// Calculate R-multiple
		if o.StopLoss > 0 {
			r := math.Abs(o.EntryPrice-o.StopLoss) / o.EntryPrice
			// Simple: profit / risk
			var tps []float64
			json.Unmarshal([]byte(o.TakeProfits), &tps)
			if len(tps) > 0 {
				profitPct := math.Abs(tps[0]-o.EntryPrice) / o.EntryPrice
				if r > 0 {
					o.ResultInR = profitPct / r
				}
			}
		}
	} else if o.WouldHaveHitSL {
		o.WouldHaveOutcome = "sl_hit"
		o.Status = "completed"
		o.ResultInR = -1.0
	} else if age > 24*time.Hour {
		o.WouldHaveOutcome = "timeout"
		o.Status = "completed"
		if o.MaxFavorableExcursion24h != nil {
			o.ResultInR = *o.MaxFavorableExcursion24h / 0.01 // rough
		}
	}

	return o, nil
}

// Run starts the background tracker loop.
func (t *CounterfactualTracker) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	t.mu.Lock()
	t.running = true
	t.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	t.log.Info("counterfactual tracker started", map[string]any{"interval": interval.String()})

	for {
		select {
		case <-ctx.Done():
			t.mu.Lock()
			t.running = false
			t.mu.Unlock()
			t.log.Info("counterfactual tracker stopped", nil)
			return
		case <-ticker.C:
			if err := t.ProcessPending(ctx); err != nil {
				t.log.Error("counterfactual tracker cycle failed", map[string]any{"error": err.Error()})
			}
		}
	}
}

// IsRunning returns whether the tracker is actively running.
func (t *CounterfactualTracker) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}
