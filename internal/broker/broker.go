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
}
