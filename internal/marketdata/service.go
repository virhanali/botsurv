package marketdata

import (
	"context"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

// MarketDataService provides access to market data and health status.
type MarketDataService interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	GetCandles(ctx context.Context, symbol, timeframe string, limit int) ([]domain.Candle, error)
	GetLatestPrice(ctx context.Context, symbol string) (float64, error)
	IsHealthy(symbol string) bool
	LastUpdate(symbol string) time.Time
}
