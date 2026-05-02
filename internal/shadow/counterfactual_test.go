package shadow

import (
	"context"
	"testing"

	"github.com/virhan/botsurv/internal/domain"
)

func TestNewUUID(t *testing.T) {
	u1 := NewUUID()
	u2 := NewUUID()
	if u1 == u2 {
		t.Error("UUIDs should be unique")
	}
	if len(u1) != 36 {
		t.Errorf("expected UUID length 36, got %d", len(u1))
	}
}

type mockPriceProvider struct {
	price float64
	err   error
}

func (m *mockPriceProvider) GetLatestPrice(ctx context.Context, symbol string) (float64, error) {
	return m.price, m.err
}

func (m *mockPriceProvider) GetCandles(ctx context.Context, symbol, timeframe string, limit int) ([]domain.Candle, error) {
	return nil, nil
}

func TestCounterfactualTracker_TrackCandidate(t *testing.T) {
	md := &mockPriceProvider{price: 100.0}
	tracker := NewCounterfactualTracker(md, nil, nil)

	// Track without repo (should not error)
	err := tracker.TrackCandidate(context.Background(), "test-decision-id", "BTCUSDT", domain.SideLong, 100.0, 95.0, []float64{110.0, 120.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCounterfactualTracker_ProcessPending_NoRepo(t *testing.T) {
	md := &mockPriceProvider{price: 100.0}
	tracker := NewCounterfactualTracker(md, nil, nil)

	err := tracker.ProcessPending(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCounterfactualTracker_UpdateOutcome_TP1Hit(t *testing.T) {
	md := &mockPriceProvider{price: 115.0} // Above TP1 110.0
	tracker := NewCounterfactualTracker(md, nil, nil)

	o := domain.CandidateOutcome{
		DecisionID:  "test-1",
		Symbol:      "BTCUSDT",
		Side:        domain.SideLong,
		EntryPrice:  100.0,
		StopLoss:    95.0,
		TakeProfits: `[110.0, 120.0]`,
		Status:      "tracking",
	}

	updated, err := tracker.updateOutcome(context.Background(), o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !updated.WouldHaveHitTP1 {
		t.Error("expected TP1 hit")
	}
	if updated.WouldHaveOutcome != "tp1_hit" {
		t.Errorf("expected tp1_hit, got %s", updated.WouldHaveOutcome)
	}
	if updated.Status != "completed" {
		t.Errorf("expected completed, got %s", updated.Status)
	}
}

func TestCounterfactualTracker_UpdateOutcome_SLHit(t *testing.T) {
	md := &mockPriceProvider{price: 94.0} // Below SL 95.0
	tracker := NewCounterfactualTracker(md, nil, nil)

	o := domain.CandidateOutcome{
		DecisionID:  "test-2",
		Symbol:      "BTCUSDT",
		Side:        domain.SideLong,
		EntryPrice:  100.0,
		StopLoss:    95.0,
		TakeProfits: `[110.0, 120.0]`,
		Status:      "tracking",
	}

	updated, err := tracker.updateOutcome(context.Background(), o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !updated.WouldHaveHitSL {
		t.Error("expected SL hit")
	}
	if updated.WouldHaveOutcome != "sl_hit" {
		t.Errorf("expected sl_hit, got %s", updated.WouldHaveOutcome)
	}
	if updated.Status != "completed" {
		t.Errorf("expected completed, got %s", updated.Status)
	}
	if updated.ResultInR != -1.0 {
		t.Errorf("expected -1.0 R, got %.2f", updated.ResultInR)
	}
}

func TestCounterfactualTracker_UpdateOutcome_Short_TP1Hit(t *testing.T) {
	md := &mockPriceProvider{price: 85.0} // Below TP 90.0
	tracker := NewCounterfactualTracker(md, nil, nil)

	o := domain.CandidateOutcome{
		DecisionID:  "test-3",
		Symbol:      "BTCUSDT",
		Side:        domain.SideShort,
		EntryPrice:  100.0,
		StopLoss:    105.0,
		TakeProfits: `[90.0, 80.0]`,
		Status:      "tracking",
	}

	updated, err := tracker.updateOutcome(context.Background(), o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !updated.WouldHaveHitTP1 {
		t.Error("expected TP1 hit for short")
	}
	if updated.WouldHaveOutcome != "tp1_hit" {
		t.Errorf("expected tp1_hit, got %s", updated.WouldHaveOutcome)
	}
}

func TestCounterfactualTracker_UpdateOutcome_Short_SLHit(t *testing.T) {
	md := &mockPriceProvider{price: 106.0} // Above SL 105.0
	tracker := NewCounterfactualTracker(md, nil, nil)

	o := domain.CandidateOutcome{
		DecisionID:  "test-4",
		Symbol:      "BTCUSDT",
		Side:        domain.SideShort,
		EntryPrice:  100.0,
		StopLoss:    105.0,
		TakeProfits: `[90.0, 80.0]`,
		Status:      "tracking",
	}

	updated, err := tracker.updateOutcome(context.Background(), o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !updated.WouldHaveHitSL {
		t.Error("expected SL hit for short")
	}
	if updated.WouldHaveOutcome != "sl_hit" {
		t.Errorf("expected sl_hit, got %s", updated.WouldHaveOutcome)
	}
}

func TestCounterfactualTracker_MFE_MAE(t *testing.T) {
	md := &mockPriceProvider{price: 108.0} // 8% above entry
	tracker := NewCounterfactualTracker(md, nil, nil)

	o := domain.CandidateOutcome{
		DecisionID:  "test-5",
		Symbol:      "BTCUSDT",
		Side:        domain.SideLong,
		EntryPrice:  100.0,
		StopLoss:    95.0,
		TakeProfits: `[110.0]`,
		Status:      "tracking",
	}

	updated, err := tracker.updateOutcome(context.Background(), o)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.MaxFavorableExcursion24h == nil {
		t.Error("expected MFE to be set")
	} else if *updated.MaxFavorableExcursion24h < 0.07 {
		t.Errorf("expected MFE >= 0.08, got %.4f", *updated.MaxFavorableExcursion24h)
	}
}
