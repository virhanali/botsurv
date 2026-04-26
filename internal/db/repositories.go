package db

import (
	"context"

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
	GetLatest(ctx context.Context) (*domain.Cycle, error)
}

// CandidateRepository persists candidates.
type CandidateRepository interface {
	Insert(ctx context.Context, c domain.Candidate) (int64, error)
	GetByCycle(ctx context.Context, cycleID string) ([]domain.Candidate, error)
}

// Repositories aggregates all repository interfaces.
type Repositories struct {
	CandleRepository    CandleRepository
	UniverseRepository  UniverseRepository
	CycleRepository     CycleRepository
	CandidateRepository CandidateRepository
}
