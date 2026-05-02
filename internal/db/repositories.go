package db

import (
	"context"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

// CandleRepository persists candles.
type CandleRepository interface {
	Insert(ctx context.Context, c domain.Candle) (int64, error)
	GetBySymbolTimeframe(ctx context.Context, symbol, timeframe string, limit int) ([]domain.Candle, error)
}

// UniverseRepository persists universe symbols.
type UniverseRepository interface {
	InsertOrUpdate(ctx context.Context, s domain.UniverseSymbol) error
	GetAll(ctx context.Context) ([]domain.UniverseSymbol, error)
	GetBySymbol(ctx context.Context, symbol string) (*domain.UniverseSymbol, error)
}

// CycleRepository persists trading cycles.
type CycleRepository interface {
	Insert(ctx context.Context, c domain.Cycle) (int64, error)
	Update(ctx context.Context, c domain.Cycle) error
	GetLatest(ctx context.Context) (*domain.Cycle, error)
}

// CandidateRepository persists candidates.
type CandidateRepository interface {
	Insert(ctx context.Context, c domain.Candidate) (int64, error)
	GetByCycle(ctx context.Context, cycleID string) ([]domain.Candidate, error)
}

// PositionRepository persists positions.
type PositionRepository interface {
	Insert(ctx context.Context, p domain.Position) (int64, error)
	Update(ctx context.Context, p domain.Position) error
	GetOpen(ctx context.Context) ([]domain.Position, error)
	GetClosed(ctx context.Context, since time.Time) ([]domain.Position, error)
	GetBySymbol(ctx context.Context, symbol string) (*domain.Position, error)
}

// OrderRepository persists orders.
type OrderRepository interface {
	Insert(ctx context.Context, o domain.Order) (int64, error)
	Update(ctx context.Context, o domain.Order) error
	GetOpen(ctx context.Context) ([]domain.Order, error)
	GetBySymbol(ctx context.Context, symbol string) ([]domain.Order, error)
	GetAll(ctx context.Context, since time.Time) ([]domain.Order, error)
}

// ExecutionRepository persists executions.
type ExecutionRepository interface {
	Insert(ctx context.Context, e domain.Execution) (int64, error)
	GetByOrder(ctx context.Context, orderID int64) ([]domain.Execution, error)
	GetAll(ctx context.Context, since time.Time) ([]domain.Execution, error)
}

// AccountSnapshotRepository persists account snapshots.
type AccountSnapshotRepository interface {
	Insert(ctx context.Context, a domain.AccountState) (int64, error)
	GetLatest(ctx context.Context) (*domain.AccountState, error)
	GetLatestBefore(ctx context.Context, before time.Time) (*domain.AccountState, error)
	GetAll(ctx context.Context, since time.Time) ([]domain.AccountState, error)
}

// LLMDecisionRepository persists LLM decisions.
type LLMDecisionRepository interface {
	Insert(ctx context.Context, d domain.LLMDecision, candidateID int64, cycleID string) (int64, error)
	GetByCycle(ctx context.Context, cycleID string) ([]domain.LLMDecision, error)
}

// RiskDecisionRepository persists deterministic risk decisions.
type RiskDecisionRepository interface {
	Insert(ctx context.Context, d domain.RiskDecision, candidateID int64, cycleID string) (int64, error)
	GetByCycle(ctx context.Context, cycleID string) ([]domain.RiskDecision, error)
}

// LLMUsageRepository persists daily LLM usage counters.
type LLMUsageRepository interface {
	Get(ctx context.Context, usageDate time.Time) (*domain.LLMUsageState, error)
	IncrementCalls(ctx context.Context, usageDate time.Time, calls int) error
}

// DecisionLogRepository persists decision log entries.
type DecisionLogRepository interface {
	Insert(ctx context.Context, d domain.DecisionLog) error
	GetByCycle(ctx context.Context, cycleID string) ([]domain.DecisionLog, error)
	GetRecent(ctx context.Context, limit int) ([]domain.DecisionLog, error)
	GetBySymbol(ctx context.Context, symbol string, since time.Time) ([]domain.DecisionLog, error)
	CountByAction(ctx context.Context, action string, since time.Time) (int, error)
}

// CandidateOutcomeRepository persists counterfactual outcomes.
type CandidateOutcomeRepository interface {
	Insert(ctx context.Context, o domain.CandidateOutcome) error
	Update(ctx context.Context, o domain.CandidateOutcome) error
	GetPending(ctx context.Context) ([]domain.CandidateOutcome, error)
	GetByDecision(ctx context.Context, decisionID string) (*domain.CandidateOutcome, error)
	GetCompleted(ctx context.Context, since time.Time) ([]domain.CandidateOutcome, error)
}

// PaperTradeRepository persists paper mode trades.
type PaperTradeRepository interface {
	Insert(ctx context.Context, t domain.PaperTrade) error
	Update(ctx context.Context, t domain.PaperTrade) error
	GetOpen(ctx context.Context) ([]domain.PaperTrade, error)
	GetByDecision(ctx context.Context, decisionID string) (*domain.PaperTrade, error)
	GetAll(ctx context.Context, since time.Time) ([]domain.PaperTrade, error)
	CountByExitReason(ctx context.Context, reason string, since time.Time) (int, error)
}

// PaperAccountStateRepository persists paper account state.
type PaperAccountStateRepository interface {
	Get(ctx context.Context) (*domain.PaperAccountState, error)
	Update(ctx context.Context, s domain.PaperAccountState) error
}

// Repositories aggregates all repository interfaces.
type Repositories struct {
	CandleRepository             CandleRepository
	UniverseRepository           UniverseRepository
	CycleRepository              CycleRepository
	CandidateRepository          CandidateRepository
	PositionRepository           PositionRepository
	OrderRepository              OrderRepository
	ExecutionRepository          ExecutionRepository
	AccountSnapshotRepository    AccountSnapshotRepository
	LLMDecisionRepository        LLMDecisionRepository
	RiskDecisionRepository       RiskDecisionRepository
	LLMUsageRepository           LLMUsageRepository
	DecisionLogRepository        DecisionLogRepository
	CandidateOutcomeRepository   CandidateOutcomeRepository
	PaperTradeRepository         PaperTradeRepository
	PaperAccountStateRepository  PaperAccountStateRepository
}
