package executor

import (
	"context"
	"math"
	"testing"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/broker"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
	"github.com/virhan/botsurv/internal/risk"
)

func newTestExecutor() (*Executor, *broker.PaperBroker) {
	cfg := app.PaperConfig{
		StartingBalanceUSD: 10000,
		FeeMakerBps:        2,
		FeeTakerBps:        5.5,
		SlippageBps:        5,
		DefaultLeverage:    5,
	}
	log := logger.New(nil, logger.LevelDebug)
	pb := broker.NewPaperBroker(cfg, log)
	ex := NewExecutor(pb, log)
	return ex, pb
}

func TestExecute_MarketOrder(t *testing.T) {
	ex, pb := newTestExecutor()
	pb.UpdatePrice("BTCUSDT", 65000)

	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			Symbol:             "BTCUSDT",
			Side:               domain.SideLong,
			EntryType:          domain.EntryTypeMarket,
			ProposedEntry:      65000,
			ProposedStopLoss:   64000,
			ProposedTakeProfit: 67000,
		},
	}

	decision := domain.LLMDecision{Decision: "ALLOW_MARKET", Confidence: 0.9}
	riskOut := risk.ValidateOutput{
		Approved:              true,
		FinalPositionNotional: 100,
	}

	result := ex.Execute(context.Background(), cand, decision, riskOut, 65000)
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.OrderID == "" {
		t.Error("expected order ID")
	}

	// Verify position exists
	pos, ok := pb.GetPosition("BTCUSDT")
	if !ok {
		t.Fatal("expected position to exist")
	}
	if pos.Side != domain.SideLong {
		t.Errorf("expected LONG, got %s", pos.Side)
	}
}

func TestExecute_SetsProtectiveOrders(t *testing.T) {
	ex, pb := newTestExecutor()
	pb.UpdatePrice("BTCUSDT", 65000)

	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			Symbol:             "BTCUSDT",
			Side:               domain.SideLong,
			EntryType:          domain.EntryTypeMarket,
			ProposedEntry:      65000,
			ProposedStopLoss:   64000,
			ProposedTakeProfit: 67000,
		},
	}

	riskOut := risk.ValidateOutput{
		Approved:              true,
		FinalPositionNotional: 100,
	}

	result := ex.Execute(context.Background(), cand, domain.LLMDecision{}, riskOut, 65000)
	if !result.Success {
		t.Fatalf("expected success: %s", result.Error)
	}

	// Position should have SL set
	pos, ok := pb.GetPosition("BTCUSDT")
	if !ok {
		t.Fatal("expected position")
	}
	if pos.SLOrderID == nil {
		t.Error("expected SL order ID to be set")
	}
}

func TestExecute_ShortPosition(t *testing.T) {
	ex, pb := newTestExecutor()
	pb.UpdatePrice("ETHUSDT", 3000)

	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			Symbol:             "ETHUSDT",
			Side:               domain.SideShort,
			EntryType:          domain.EntryTypeMarket,
			ProposedEntry:      3000,
			ProposedStopLoss:   3100,
			ProposedTakeProfit: 2800,
		},
	}

	riskOut := risk.ValidateOutput{
		Approved:              true,
		FinalPositionNotional: 500,
	}

	result := ex.Execute(context.Background(), cand, domain.LLMDecision{}, riskOut, 3000)
	if !result.Success {
		t.Errorf("expected success: %s", result.Error)
	}

	pos, ok := pb.GetPosition("ETHUSDT")
	if !ok {
		t.Fatal("expected position")
	}
	if pos.Side != domain.SideShort {
		t.Errorf("expected SHORT, got %s", pos.Side)
	}
}

func TestExecute_ZeroQuantity(t *testing.T) {
	ex, _ := newTestExecutor()

	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			Symbol:        "BTCUSDT",
			Side:          domain.SideLong,
			ProposedEntry: 0, // zero entry = zero qty
		},
	}

	riskOut := risk.ValidateOutput{
		Approved:              true,
		FinalPositionNotional: 100,
	}

	result := ex.Execute(context.Background(), cand, domain.LLMDecision{}, riskOut, 65000)
	if result.Success {
		t.Error("expected failure for zero quantity")
	}
}

func TestExecute_InsufficientBalance(t *testing.T) {
	ex, _ := newTestExecutor()

	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			Symbol:             "BTCUSDT",
			Side:               domain.SideLong,
			EntryType:          domain.EntryTypeMarket,
			ProposedEntry:      65000,
			ProposedStopLoss:   64000,
			ProposedTakeProfit: 67000,
		},
	}

	riskOut := risk.ValidateOutput{
		Approved:              true,
		FinalPositionNotional: 1000000, // way more than balance
	}

	result := ex.Execute(context.Background(), cand, domain.LLMDecision{}, riskOut, 65000)
	if result.Success {
		t.Error("expected failure for insufficient balance")
	}
}

func TestExecutor_RejectNaNQuantity(t *testing.T) {
	ex, pb := newTestExecutor()
	pb.UpdatePrice("BTCUSDT", 65000)

	cand := domain.Candidate{
		ProposedTrade: domain.ProposedTrade{
			Symbol:             "BTCUSDT",
			Side:               domain.SideLong,
			EntryType:          domain.EntryTypeMarket,
			ProposedEntry:      math.NaN(),
			ProposedStopLoss:   64000,
			ProposedTakeProfit: 67000,
		},
	}
	riskOut := risk.ValidateOutput{
		Approved:              true,
		FinalPositionNotional: 100,
	}

	result := ex.Execute(context.Background(), cand, domain.LLMDecision{}, riskOut, 65000)
	if result.Success {
		t.Fatal("expected rejection for NaN quantity")
	}
	if result.Error == "" {
		t.Fatal("expected error message for invalid quantity")
	}
}
