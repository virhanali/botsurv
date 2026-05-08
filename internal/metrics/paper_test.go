package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

func TestPaperMetrics_Compute_Empty(t *testing.T) {
	metrics, err := ComputePaperMetrics(context.Background(), nil, nil, "all_time")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metrics.TotalTrades != 0 {
		t.Errorf("expected 0 trades, got %d", metrics.TotalTrades)
	}
}

func TestPaperMetrics_Compute_WithTrades(t *testing.T) {
	now := time.Now()
	pnl1 := 50.0
	pnl2 := -25.0
	pnl3 := 75.0
	r1 := 2.0
	r2 := -1.0
	r3 := 3.0

	mockTrades := []domain.PaperTrade{
		{
			PaperTradeID: "t1",
			DecisionID:   "d1",
			Symbol:       "BTCUSDT",
			Side:         domain.SideLong,
			Qty:          1.0,
			Leverage:     5,
			EntryPrice:   100.0,
			ExitPrice:    &[]float64{105.0}[0],
			PnLNet:       &pnl1,
			RMultiple:    &r1,
			ClosedAt:     &now,
			ExitReason:   "tp1",
		},
		{
			PaperTradeID: "t2",
			DecisionID:   "d2",
			Symbol:       "ETHUSDT",
			Side:         domain.SideShort,
			Qty:          1.0,
			Leverage:     5,
			EntryPrice:   200.0,
			ExitPrice:    &[]float64{205.0}[0],
			PnLNet:       &pnl2,
			RMultiple:    &r2,
			ClosedAt:     &now,
			ExitReason:   "sl",
		},
		{
			PaperTradeID: "t3",
			DecisionID:   "d3",
			Symbol:       "BTCUSDT",
			Side:         domain.SideLong,
			Qty:          1.0,
			Leverage:     5,
			EntryPrice:   100.0,
			ExitPrice:    &[]float64{107.5}[0],
			PnLNet:       &pnl3,
			RMultiple:    &r3,
			ClosedAt:     &now,
			ExitReason:   "tp2",
		},
	}

	mockRepo := &mockPaperTradeRepo{trades: mockTrades}
	metrics, err := ComputePaperMetrics(context.Background(), mockRepo, nil, "all_time")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if metrics.TotalTrades != 3 {
		t.Errorf("expected 3 trades, got %d", metrics.TotalTrades)
	}
	if metrics.WinRate < 66.0 {
		t.Errorf("expected win rate ~66.7%%, got %.2f", metrics.WinRate)
	}
	if metrics.ProfitFactor <= 1.0 {
		t.Errorf("expected profit factor > 1.0, got %.2f", metrics.ProfitFactor)
	}
	if metrics.Expectancy <= 0 {
		t.Errorf("expected positive expectancy, got %.4f", metrics.Expectancy)
	}
}

func TestPaperMetrics_Compute_AllLosses(t *testing.T) {
	now := time.Now()
	pnl1 := -100.0
	pnl2 := -50.0
	r1 := -1.0
	r2 := -1.0

	mockTrades := []domain.PaperTrade{
		{
			PaperTradeID: "t1",
			DecisionID:   "d1",
			Symbol:       "BTCUSDT",
			Side:         domain.SideLong,
			Qty:          1.0,
			ClosedAt:     &now,
			PnLNet:       &pnl1,
			RMultiple:    &r1,
			ExitReason:   "sl",
		},
		{
			PaperTradeID: "t2",
			DecisionID:   "d2",
			Symbol:       "BTCUSDT",
			Side:         domain.SideLong,
			Qty:          1.0,
			ClosedAt:     &now,
			PnLNet:       &pnl2,
			RMultiple:    &r2,
			ExitReason:   "sl",
		},
	}

	mockRepo := &mockPaperTradeRepo{trades: mockTrades}
	metrics, err := ComputePaperMetrics(context.Background(), mockRepo, nil, "all_time")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if metrics.TotalTrades != 2 {
		t.Errorf("expected 2 trades, got %d", metrics.TotalTrades)
	}
	if metrics.WinRate != 0 {
		t.Errorf("expected 0%% win rate, got %.2f", metrics.WinRate)
	}
	if metrics.ProfitFactor != 0 {
		t.Errorf("expected 0 profit factor, got %.2f", metrics.ProfitFactor)
	}
	if metrics.Expectancy >= 0 {
		t.Errorf("expected negative expectancy, got %.4f", metrics.Expectancy)
	}
}

func TestPeriodStart(t *testing.T) {
	today := periodStart("today")
	if today.IsZero() {
		t.Error("today should not be zero")
	}
	if today.Hour() != 0 || today.Minute() != 0 {
		t.Error("today should be midnight")
	}

	week := periodStart("week")
	allTime := periodStart("all_time")
	if week.IsZero() {
		t.Error("week should not be zero")
	}
	if !allTime.IsZero() {
		t.Error("all_time should be zero time")
	}
}

// mockPaperTradeRepo implements db.PaperTradeRepository for testing
type mockPaperTradeRepo struct {
	trades []domain.PaperTrade
}

func (m *mockPaperTradeRepo) Insert(ctx context.Context, t domain.PaperTrade) error { return nil }
func (m *mockPaperTradeRepo) Update(ctx context.Context, t domain.PaperTrade) error { return nil }
func (m *mockPaperTradeRepo) GetOpen(ctx context.Context) ([]domain.PaperTrade, error) { return nil, nil }
func (m *mockPaperTradeRepo) GetByDecision(ctx context.Context, decisionID string) (*domain.PaperTrade, error) {
	return nil, nil
}
func (m *mockPaperTradeRepo) GetAll(ctx context.Context, since time.Time) ([]domain.PaperTrade, error) {
	return m.trades, nil
}
func (m *mockPaperTradeRepo) CountByExitReason(ctx context.Context, reason string, since time.Time) (int, error) {
	return 0, nil
}

func TestPaperMetrics_ExplicitWinsLosses(t *testing.T) {
	now := time.Now()
	pnl1 := 50.0
	pnl2 := -25.0
	pnl3 := 75.0
	pnl4 := -30.0
	rMult := 1.0

	mockTrades := []domain.PaperTrade{
		{PaperTradeID: "t1", Symbol: "A", Side: domain.SideLong, PnLNet: &pnl1, RMultiple: &rMult, ClosedAt: &now, ExitReason: "tp"},
		{PaperTradeID: "t2", Symbol: "B", Side: domain.SideLong, PnLNet: &pnl2, RMultiple: &rMult, ClosedAt: &now, ExitReason: "sl"},
		{PaperTradeID: "t3", Symbol: "C", Side: domain.SideLong, PnLNet: &pnl3, RMultiple: &rMult, ClosedAt: &now, ExitReason: "tp"},
		{PaperTradeID: "t4", Symbol: "D", Side: domain.SideLong, PnLNet: &pnl4, RMultiple: &rMult, ClosedAt: &now, ExitReason: "sl"},
	}

	mockRepo := &mockPaperTradeRepo{trades: mockTrades}
	metrics, err := ComputePaperMetrics(context.Background(), mockRepo, nil, "all_time")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if metrics.Wins != 2 {
		t.Errorf("expected 2 wins, got %d", metrics.Wins)
	}
	if metrics.Losses != 2 {
		t.Errorf("expected 2 losses, got %d", metrics.Losses)
	}
	if metrics.GetWins() != 2 {
		t.Errorf("GetWins() expected 2, got %d", metrics.GetWins())
	}
	if metrics.GetLosses() != 2 {
		t.Errorf("GetLosses() expected 2, got %d", metrics.GetLosses())
	}
	if metrics.Wins+metrics.Losses != metrics.TotalTrades {
		t.Errorf("wins(%d)+losses(%d) != total(%d)", metrics.Wins, metrics.Losses, metrics.TotalTrades)
	}
}
