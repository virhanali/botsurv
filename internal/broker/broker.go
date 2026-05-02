package broker

import (
	"context"

	"github.com/virhan/botsurv/internal/domain"
)

// OrderRequest is the input for placing an order.
type OrderRequest struct {
	Symbol     string
	Side       domain.OrderSide
	OrderType  domain.OrderType
	Qty        float64
	Price      *float64 // for LIMIT orders
	StopPrice  *float64 // for STOP_MARKET / TAKE_PROFIT_MARKET
	ReduceOnly bool
	// If set, protective SL/TP are created atomically with the position.
	// Broker must guarantee no position exists without SL.
	StopLoss   float64
	TakeProfit float64
}

// Broker abstracts order placement, position queries, and account state.
type Broker interface {
	GetAccountState(ctx context.Context) (domain.AccountState, error)
	GetOpenPositions(ctx context.Context) ([]domain.Position, error)
	GetOpenOrders(ctx context.Context) ([]domain.Order, error)
	PlaceOrder(ctx context.Context, req OrderRequest) (domain.Order, error)
	CancelOrder(ctx context.Context, orderID string) error
	ClosePosition(ctx context.Context, symbol string) error
	EmergencyCloseAll(ctx context.Context) error
	IsHalted() bool
	SetHalted(reason string)
	// SetSymbolPrice pushes the latest ticker price into the broker cache.
	// Required before PlaceOrder for MARKET orders to succeed.
	SetSymbolPrice(symbol string, price float64)
	// ProcessCandle updates price and checks pending orders against a candle.
	ProcessCandle(candle domain.Candle)
}
