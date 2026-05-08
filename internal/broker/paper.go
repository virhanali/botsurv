package broker

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/virhan/botsurv/internal/alert"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
)

// PaperBroker simulates futures trading in paper mode.
//
// Rehydrate reloads persisted paper state on restart so open positions and
// protective orders remain active after a service restart.
type PaperBroker struct {
	mu sync.RWMutex

	cfg app.PaperConfig
	log *logger.Logger

	// Account state
	startingBalance float64
	balance         float64
	usedMargin      float64
	realizedPnL     float64
	unrealizedPnL   float64
	totalFees       float64
	totalSlippage   float64
	dailyLoss       float64

	// Positions and orders
	openPositions   map[string]*domain.Position // symbol -> position
	openOrders      map[string]*domain.Order    // orderID -> order
	closedPositions []domain.Position

	// ID counters
	nextOrderID    int64
	nextPositionID int64
	nextExecID     int64

	// Latest prices for unrealized PnL
	prices map[string]float64

	// Halted state
	halted     bool
	haltReason string

	// DB repositories (optional, nil = no persistence)
	posRepo  PositionRepository
	ordRepo  OrderRepository
	execRepo ExecutionRepository
	snapRepo AccountSnapshotRepository

	// Alert service (optional, nil = no alerts)
	alert alert.Service

	// Last snapshot time for throttling
	lastSnapshotAt time.Time
}

// PositionRepository is the minimal interface for position persistence.
type PositionRepository interface {
	Insert(ctx context.Context, p domain.Position) (int64, error)
	Update(ctx context.Context, p domain.Position) error
	GetOpen(ctx context.Context) ([]domain.Position, error)
}

// OrderRepository is the minimal interface for order persistence.
type OrderRepository interface {
	Insert(ctx context.Context, o domain.Order) (int64, error)
	Update(ctx context.Context, o domain.Order) error
	GetOpen(ctx context.Context) ([]domain.Order, error)
	GetBySymbol(ctx context.Context, symbol string) ([]domain.Order, error)
	GetAll(ctx context.Context, since time.Time) ([]domain.Order, error)
}

// ExecutionRepository is the minimal interface for execution persistence.
type ExecutionRepository interface {
	Insert(ctx context.Context, e domain.Execution) (int64, error)
}

// AccountSnapshotRepository is the minimal interface for account snapshot persistence.
type AccountSnapshotRepository interface {
	Insert(ctx context.Context, a domain.AccountState) (int64, error)
	GetLatest(ctx context.Context) (*domain.AccountState, error)
	GetLatestBefore(ctx context.Context, before time.Time) (*domain.AccountState, error)
}

// NewPaperBroker creates a new PaperBroker.
func NewPaperBroker(cfg app.PaperConfig, log *logger.Logger) *PaperBroker {
	return &PaperBroker{
		cfg:             cfg,
		log:             log,
		startingBalance: cfg.StartingBalanceUSD,
		balance:         cfg.StartingBalanceUSD,
		openPositions:   make(map[string]*domain.Position),
		openOrders:      make(map[string]*domain.Order),
		prices:          make(map[string]float64),
		nextOrderID:     1,
		nextPositionID:  1,
		nextExecID:      1,
	}
}

// SetPositionRepo sets an optional position repository for DB persistence.
func (pb *PaperBroker) SetPositionRepo(repo PositionRepository) { pb.posRepo = repo }

// SetOrderRepo sets an optional order repository for DB persistence.
func (pb *PaperBroker) SetOrderRepo(repo OrderRepository) { pb.ordRepo = repo }

// SetExecutionRepo sets an optional execution repository for DB persistence.
func (pb *PaperBroker) SetExecutionRepo(repo ExecutionRepository) { pb.execRepo = repo }

// SetAccountSnapshotRepo sets an optional account snapshot repository for DB persistence.
func (pb *PaperBroker) SetAccountSnapshotRepo(repo AccountSnapshotRepository) { pb.snapRepo = repo }

// SetAlertSender sets an optional alert sender.
func (pb *PaperBroker) SetAlertSender(svc alert.Service) { pb.alert = svc }

// Rehydrate reloads open paper positions, open orders, and the latest account
// snapshot from persistence. If a persisted open position has an SL price but
// no active STOP_MARKET order, the paper broker repairs the missing protective
// order before accepting new entries.
func (pb *PaperBroker) Rehydrate(ctx context.Context) error {
	if pb.posRepo == nil || pb.ordRepo == nil {
		return nil
	}

	positions, err := pb.posRepo.GetOpen(ctx)
	if err != nil {
		return fmt.Errorf("load open positions: %w", err)
	}
	orders, err := pb.ordRepo.GetOpen(ctx)
	if err != nil {
		return fmt.Errorf("load open orders: %w", err)
	}

	var snapshot *domain.AccountState
	if pb.snapRepo != nil {
		snapshot, err = pb.snapRepo.GetLatest(ctx)
		if err != nil {
			return fmt.Errorf("load latest account snapshot: %w", err)
		}
	}

	openPositions := make(map[string]*domain.Position, len(positions))
	openOrders := make(map[string]*domain.Order, len(orders))
	var ordersToUpdate []domain.Order
	nextOrderID := int64(1)
	nextPositionID := int64(1)
	usedMargin := 0.0

	for _, p := range positions {
		if p.ID >= nextPositionID {
			nextPositionID = p.ID + 1
		}
		if p.StopLoss <= 0 {
			pb.SetHalted(fmt.Sprintf("persisted open position %s has no stop loss", p.Symbol))
			return fmt.Errorf("persisted open position %s has no stop loss", p.Symbol)
		}
		if err := validatePositionStopLoss(p); err != nil {
			pb.SetHalted(err.Error())
			return err
		}
		cp := p
		openPositions[p.Symbol] = &cp
		usedMargin += p.Margin
	}

	for _, o := range orders {
		if o.ID >= nextOrderID {
			nextOrderID = o.ID + 1
		}
		if o.BrokerOrderID == "" {
			o.BrokerOrderID = fmt.Sprintf("paper-db-%d", o.ID)
			ordersToUpdate = append(ordersToUpdate, o)
		}
		if o.OrderType == domain.OrderTypeLimit && o.IntendedSL <= 0 {
			o.Status = domain.OrderStatusCancelled
			o.UpdatedAt = time.Now()
			ordersToUpdate = append(ordersToUpdate, o)
			pb.log.Warn("cancelled rehydrated LIMIT order without intended SL", map[string]any{
				"symbol": o.Symbol,
				"order":  o.BrokerOrderID,
			})
			continue
		}
		co := o
		openOrders[o.BrokerOrderID] = &co
	}

	for _, o := range ordersToUpdate {
		if err := pb.ordRepo.Update(ctx, o); err != nil {
			return fmt.Errorf("update rehydrated order %s: %w", o.BrokerOrderID, err)
		}
	}

	pb.mu.Lock()
	if snapshot != nil {
		pb.balance = snapshot.Balance
		pb.realizedPnL = snapshot.RealizedPnL
		pb.unrealizedPnL = snapshot.UnrealizedPnL
		pb.totalFees = snapshot.TotalFees
		pb.totalSlippage = snapshot.TotalSlippage
		pb.dailyLoss = snapshot.DailyLoss
	}
	pb.usedMargin = usedMargin
	pb.openPositions = openPositions
	pb.openOrders = openOrders
	pb.nextOrderID = maxInt64(nextOrderID, pb.nextOrderID)
	pb.nextPositionID = maxInt64(nextPositionID, pb.nextPositionID)

	repaired := 0
	for _, pos := range pb.openPositions {
		if pb.hasOpenStopOrderLocked(pos.Symbol) {
			continue
		}
		if !pb.createProtectiveOrdersForPosition(pos, pos.StopLoss, pos.TakeProfit) {
			pb.mu.Unlock()
			pb.SetHalted(fmt.Sprintf("failed to repair protective stop for %s", pos.Symbol))
			return fmt.Errorf("failed to repair protective stop for %s", pos.Symbol)
		}
		pb.saveProtectiveOrdersForPosition(pos)
		pb.updatePositionInDB(pos)
		repaired++
	}
	pb.mu.Unlock()

	pb.log.Info("paper broker rehydrated", map[string]any{
		"open_positions": len(positions),
		"open_orders":    len(openOrders),
		"used_margin":    usedMargin,
		"repaired_sl":    repaired,
	})
	return nil
}

// GetAccountState returns the current account state.
func (pb *PaperBroker) GetAccountState(_ context.Context) (domain.AccountState, error) {
	pb.mu.RLock()
	defer pb.mu.RUnlock()

	equity := pb.balance + pb.unrealizedPnL
	return domain.AccountState{
		Balance:          pb.balance,
		AvailableBalance: pb.balance - pb.usedMargin,
		UsedMargin:       pb.usedMargin,
		Equity:           equity,
		RealizedPnL:      pb.realizedPnL,
		UnrealizedPnL:    pb.unrealizedPnL,
		TotalFees:        pb.totalFees,
		TotalSlippage:    pb.totalSlippage,
		DailyLoss:        pb.dailyLoss,
		RecordedAt:       time.Now(),
	}, nil
}

// GetOpenPositions returns all open positions.
func (pb *PaperBroker) GetOpenPositions(_ context.Context) ([]domain.Position, error) {
	pb.mu.RLock()
	defer pb.mu.RUnlock()

	positions := make([]domain.Position, 0, len(pb.openPositions))
	for _, p := range pb.openPositions {
		positions = append(positions, *p)
	}
	return positions, nil
}

// GetOpenOrders returns all open orders.
func (pb *PaperBroker) GetOpenOrders(_ context.Context) ([]domain.Order, error) {
	pb.mu.RLock()
	defer pb.mu.RUnlock()

	orders := make([]domain.Order, 0, len(pb.openOrders))
	for _, o := range pb.openOrders {
		orders = append(orders, *o)
	}
	return orders, nil
}

// IsHalted returns whether the broker is halted.
func (pb *PaperBroker) IsHalted() bool {
	pb.mu.RLock()
	defer pb.mu.RUnlock()
	return pb.halted
}

// SetHalted halts the broker (prevents new entries).
func (pb *PaperBroker) SetHalted(reason string) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.halted = true
	pb.haltReason = reason
	pb.log.Error("broker halted", map[string]any{"reason": reason})
}

// PlaceOrder places a new order.
func (pb *PaperBroker) PlaceOrder(_ context.Context, req OrderRequest) (domain.Order, error) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if req.Qty <= 0 {
		return domain.Order{}, fmt.Errorf("qty must be > 0")
	}
	if req.Symbol == "" {
		return domain.Order{}, fmt.Errorf("symbol is required")
	}

	// Halted: block new entries
	if pb.halted && req.StopPrice == nil && !req.ReduceOnly && (req.OrderType == domain.OrderTypeMarket || req.OrderType == domain.OrderTypeLimit) {
		return domain.Order{}, fmt.Errorf("broker halted: %s", pb.haltReason)
	}

	// Enforce no-position-without-SL: all entry orders must have StopLoss
	if !req.ReduceOnly && (req.OrderType == domain.OrderTypeMarket || req.OrderType == domain.OrderTypeLimit) {
		if req.StopLoss <= 0 {
			return domain.Order{}, fmt.Errorf("entry order rejected: StopLoss is required (no position without SL)")
		}
	}

	order := domain.Order{
		ID:            pb.nextOrderID,
		BrokerOrderID: fmt.Sprintf("paper-%d", pb.nextOrderID),
		Symbol:        req.Symbol,
		Side:          req.Side,
		OrderType:     req.OrderType,
		Qty:           req.Qty,
		Price:         req.Price,
		StopPrice:     req.StopPrice,
		Status:        domain.OrderStatusPending,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		IntendedSL:    req.StopLoss,
		IntendedTP:    req.TakeProfit,
	}
	pb.nextOrderID++

	// Market orders fill immediately
	if req.OrderType == domain.OrderTypeMarket {
		price, ok := pb.prices[req.Symbol]
		if !ok || price <= 0 {
			return domain.Order{}, fmt.Errorf("no price available for %s", req.Symbol)
		}

		slippageBps := pb.cfg.SlippageBps
		takerFeeBps := pb.cfg.FeeTakerBps

		var fillPrice float64
		var slippageAmt float64
		if req.Side == domain.OrderSideBuy {
			slippageAmt = price * slippageBps / 10000
			fillPrice = price + slippageAmt
		} else {
			slippageAmt = price * slippageBps / 10000
			fillPrice = price - slippageAmt
		}

		// Validate SL side BEFORE mutating any broker state (H3 fix).
		if !req.ReduceOnly && (req.OrderType == domain.OrderTypeMarket || req.OrderType == domain.OrderTypeLimit) {
			if req.StopLoss > 0 {
				if err := pb.validateStopLoss(req.Side, fillPrice, req.StopLoss); err != nil {
					order.Status = domain.OrderStatusRejected
					return order, err
				}
			}
		}

		notional := fillPrice * req.Qty
		fee := notional * takerFeeBps / 10000
		margin := notional / pb.cfg.DefaultLeverage

		// Check sufficient balance
		if pb.balance-pb.usedMargin < margin+fee {
			order.Status = domain.OrderStatusRejected
			pb.log.Error("order rejected: insufficient margin", map[string]any{
				"symbol":    req.Symbol,
				"required":  margin + fee,
				"available": pb.balance - pb.usedMargin,
			})
			return order, fmt.Errorf("insufficient margin: need %.2f, have %.2f", margin+fee, pb.balance-pb.usedMargin)
		}

		// Check for existing position
		existing, hasPos := pb.openPositions[req.Symbol]
		if hasPos && !req.ReduceOnly {
			// Close existing position first (opposite side)
			pb.closePositionInternalWithOrder(existing, fillPrice, time.Now(), nil)
			hasPos = false
		}

		if hasPos && req.ReduceOnly {
			// Reduce existing position
			pb.reducePosition(existing, req.Side, req.Qty, fillPrice, fee, slippageAmt)
		} else {
			// Open new position
			pos := &domain.Position{
				ID:         pb.nextPositionID,
				Symbol:     req.Symbol,
				Side:       pb.sideFromOrderSide(req.Side),
				EntryPrice: fillPrice,
				Size:       req.Qty,
				Leverage:   pb.cfg.DefaultLeverage,
				Margin:     margin,
				Status:     domain.PositionStatusOpen,
				Source:     "paper",
				OpenedAt:   time.Now(),
			}
			pb.nextPositionID++
			pb.openPositions[req.Symbol] = pos
			pb.usedMargin += margin
			pb.balance -= fee
			pb.totalFees += fee
			pb.totalSlippage += slippageAmt * req.Qty

			// Atomic protective orders: create first (in-memory), then persist position with SL/TP.
			if req.StopLoss > 0 || req.TakeProfit > 0 {
				if !pb.createProtectiveOrdersForPosition(pos, req.StopLoss, req.TakeProfit) {
					// Position was emergency-closed due to invalid SL.
					order.Status = domain.OrderStatusRejected
					pb.saveOrder(&order)
					return order, fmt.Errorf("position rejected: invalid StopLoss price for %s side", pos.Side)
				}
			}
			// Set SL/TP on position only after protective orders are confirmed.
			pos.StopLoss = req.StopLoss
			pos.TakeProfit = req.TakeProfit

			order.Status = domain.OrderStatusFilled
			order.UpdatedAt = time.Now()

			// DB persistence order: main order → protective orders → position → execution
			// This ensures all cross-referencing IDs are DB IDs, not paper IDs.
			pb.saveOrder(&order)
			pb.saveProtectiveOrdersForPosition(pos)
			pb.savePosition(pos)
			// Save entry execution with DB main order ID
			pb.saveExecution(&domain.Execution{
				ID:         pb.nextExecID,
				OrderID:    order.ID,
				Symbol:     req.Symbol,
				Side:       req.Side,
				Qty:        req.Qty,
				Price:      fillPrice,
				Fee:        fee,
				Slippage:   slippageAmt,
				ExecutedAt: time.Now(),
			})
			pb.nextExecID++
			// Snapshot account state after entry.
			pb.saveAccountSnapshotNow()
		}

		pb.log.Info("market order filled", map[string]any{
			"symbol":     req.Symbol,
			"side":       req.Side,
			"qty":        req.Qty,
			"fill_price": fillPrice,
			"fee":        fee,
			"slippage":   slippageAmt,
		})
	}

	// Protective orders (SL/TP) are stored and checked on price updates
	if req.OrderType == domain.OrderTypeStopMarket || req.OrderType == domain.OrderTypeTakeProfitMarket {
		if req.StopPrice == nil || *req.StopPrice <= 0 {
			return domain.Order{}, fmt.Errorf("stop_price required for %s", req.OrderType)
		}
		order.Status = domain.OrderStatusPending
		pb.openOrders[order.BrokerOrderID] = &order
	}

	// Limit orders are stored as pending and persisted immediately.
	if req.OrderType == domain.OrderTypeLimit {
		if req.Price == nil || *req.Price <= 0 {
			return domain.Order{}, fmt.Errorf("price required for LIMIT order")
		}
		order.Status = domain.OrderStatusPending
		pb.openOrders[order.BrokerOrderID] = &order
		pb.saveOrder(&order)
	}

	return order, nil
}

// CancelOrder cancels a pending order.
func (pb *PaperBroker) CancelOrder(_ context.Context, orderID string) error {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	order, ok := pb.openOrders[orderID]
	if !ok {
		return fmt.Errorf("order %s not found", orderID)
	}
	order.Status = domain.OrderStatusCancelled
	order.UpdatedAt = time.Now()
	pb.updateOrderInDB(order)
	delete(pb.openOrders, orderID)
	return nil
}

// ClosePosition closes an open position at market price.
func (pb *PaperBroker) ClosePosition(_ context.Context, symbol string) error {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	pos, ok := pb.openPositions[symbol]
	if !ok {
		return fmt.Errorf("no open position for %s", symbol)
	}

	price, ok := pb.prices[symbol]
	if !ok || price <= 0 {
		return fmt.Errorf("no price available for %s", symbol)
	}

	pb.closePositionInternalWithOrder(pos, price, time.Now(), nil)
	return nil
}

// EmergencyCloseAll closes all positions and cancels all orders.
func (pb *PaperBroker) EmergencyCloseAll(_ context.Context) error {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	now := time.Now()

	// Capture counts before loops
	cancelledCount := len(pb.openOrders)
	closedCount := len(pb.openPositions)

	// Cancel all open orders
	for id, order := range pb.openOrders {
		order.Status = domain.OrderStatusCancelled
		order.UpdatedAt = now
		delete(pb.openOrders, id)
	}

	// Close all positions
	for symbol, pos := range pb.openPositions {
		price, ok := pb.prices[symbol]
		if !ok || price <= 0 {
			price = pos.EntryPrice // fallback: close at entry (no PnL)
			pb.log.Error("emergency close: no price, using entry price", map[string]any{"symbol": symbol})
		}
		pb.closePositionInternalWithOrder(pos, price, now, nil)
	}

	pb.log.Error("EMERGENCY CLOSE ALL executed", map[string]any{
		"positions_closed": closedCount,
		"orders_cancelled": cancelledCount,
	})

	// Alert on emergency close
	pb.sendAlert(alert.AlertEvent{
		Type:      "emergency_close",
		Severity:  "danger",
		Message:   fmt.Sprintf("Emergency close all: %d positions closed, %d orders cancelled", closedCount, cancelledCount),
		Timestamp: now,
	})

	return nil
}

// UpdatePrice updates the latest price and recalculates unrealized PnL.
// This also checks protective orders (SL/TP) for fills.
func (pb *PaperBroker) UpdatePrice(symbol string, price float64) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	pb.setPriceLocked(symbol, price)
	pb.updateUnrealizedPnLLocked(symbol)
	pb.checkProtectiveOrders(symbol, price, time.Now())
}

// SetSymbolPrice pushes a ticker price into the broker cache without triggering
// PnL recalculation or protective order checks. Use this before PlaceOrder for
// new symbols that don't have open positions yet.
func (pb *PaperBroker) SetSymbolPrice(symbol string, price float64) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.setPriceLocked(symbol, price)
}

func (pb *PaperBroker) setPriceLocked(symbol string, price float64) {
	pb.prices[symbol] = price
}

func (pb *PaperBroker) updateUnrealizedPnLLocked(symbol string) {
	var totalUnrealized float64
	for _, pos := range pb.openPositions {
		if price, ok := pb.prices[pos.Symbol]; ok {
			pb.updatePositionPnL(pos, price)
		}
		totalUnrealized += pos.UnrealizedPnL
	}
	pb.unrealizedPnL = totalUnrealized
}

// ProcessCandle checks pending orders against candle OHLCV for fills.
func (pb *PaperBroker) ProcessCandle(candle domain.Candle) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	pb.prices[candle.Symbol] = candle.Close

	// Check limit orders
	for id, order := range pb.openOrders {
		if order.Symbol != candle.Symbol {
			continue
		}
		if order.OrderType == domain.OrderTypeLimit {
			if pb.wouldLimitFill(order, candle) {
				pb.fillLimitOrder(order, candle)
				delete(pb.openOrders, id)
			}
		}
	}

	// Check protective orders with conservative SL-first assumption
	pb.checkProtectiveOrdersCandle(candle.Symbol, candle, time.Now())
}

// GetClosedPositions returns closed positions.
func (pb *PaperBroker) GetClosedPositions() []domain.Position {
	pb.mu.RLock()
	defer pb.mu.RUnlock()
	return pb.closedPositions
}

// GetPosition returns a specific open position.
func (pb *PaperBroker) GetPosition(symbol string) (*domain.Position, bool) {
	pb.mu.RLock()
	defer pb.mu.RUnlock()
	pos, ok := pb.openPositions[symbol]
	return pos, ok
}

// --- Internal methods ---

// createProtectiveOrdersForPosition creates SL and TP orders for a position (lock held).
// Validates that SL/TP prices are on the correct side of the entry price.
// If SL is invalid, the position is emergency-closed (fail closed).
// Returns true if the position survived (SL/TP created successfully), false if emergency-closed.
func (pb *PaperBroker) createProtectiveOrdersForPosition(pos *domain.Position, slPrice, tpPrice float64) bool {
	if slPrice <= 0 && tpPrice <= 0 {
		return true
	}

	// Validate SL is on the correct side
	if slPrice > 0 {
		if pos.Side == domain.SideLong && slPrice >= pos.EntryPrice {
			pb.log.Error("invalid LONG SL >= entry — emergency closing position", map[string]any{
				"symbol": pos.Symbol, "sl": slPrice, "entry": pos.EntryPrice,
			})
			pb.closePositionInternalWithOrder(pos, pos.EntryPrice, time.Now(), nil)
			return false
		}
		if pos.Side == domain.SideShort && slPrice <= pos.EntryPrice {
			pb.log.Error("invalid SHORT SL <= entry — emergency closing position", map[string]any{
				"symbol": pos.Symbol, "sl": slPrice, "entry": pos.EntryPrice,
			})
			pb.closePositionInternalWithOrder(pos, pos.EntryPrice, time.Now(), nil)
			return false
		}
	}

	// Validate TP is on the correct side (skip TP if invalid, but don't close position)
	if tpPrice > 0 {
		if pos.Side == domain.SideLong && tpPrice <= pos.EntryPrice {
			pb.log.Error("invalid LONG TP <= entry — skipping TP", map[string]any{
				"symbol": pos.Symbol, "tp": tpPrice, "entry": pos.EntryPrice,
			})
			tpPrice = 0
		}
		if pos.Side == domain.SideShort && tpPrice >= pos.EntryPrice {
			pb.log.Error("invalid SHORT TP >= entry — skipping TP", map[string]any{
				"symbol": pos.Symbol, "tp": tpPrice, "entry": pos.EntryPrice,
			})
			tpPrice = 0
		}
	}

	slSide := domain.OrderSideSell
	tpSide := domain.OrderSideSell
	if pos.Side == domain.SideShort {
		slSide = domain.OrderSideBuy
		tpSide = domain.OrderSideBuy
	}

	if slPrice > 0 {
		slOrder := &domain.Order{
			ID:            pb.nextOrderID,
			BrokerOrderID: fmt.Sprintf("paper-%d", pb.nextOrderID),
			Symbol:        pos.Symbol,
			Side:          slSide,
			OrderType:     domain.OrderTypeStopMarket,
			Qty:           pos.Size,
			StopPrice:     &slPrice,
			Status:        domain.OrderStatusPending,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		pb.nextOrderID++
		pb.openOrders[slOrder.BrokerOrderID] = slOrder
		pos.SLOrderID = &slOrder.ID
	}

	if tpPrice > 0 {
		tpOrder := &domain.Order{
			ID:            pb.nextOrderID,
			BrokerOrderID: fmt.Sprintf("paper-%d", pb.nextOrderID),
			Symbol:        pos.Symbol,
			Side:          tpSide,
			OrderType:     domain.OrderTypeTakeProfitMarket,
			Qty:           pos.Size,
			StopPrice:     &tpPrice,
			Status:        domain.OrderStatusPending,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		pb.nextOrderID++
		pb.openOrders[tpOrder.BrokerOrderID] = tpOrder
		pos.TPOrderID = &tpOrder.ID
	}
	return true
}

// validateStopLoss checks that the SL price is on the correct side of the entry price.
// Must be called before any broker state is mutated.
func (pb *PaperBroker) validateStopLoss(side domain.OrderSide, entryPrice, slPrice float64) error {
	if side == domain.OrderSideBuy {
		if slPrice >= entryPrice {
			return fmt.Errorf("LONG StopLoss %.2f must be below entry %.2f", slPrice, entryPrice)
		}
	} else {
		if slPrice <= entryPrice {
			return fmt.Errorf("SHORT StopLoss %.2f must be above entry %.2f", slPrice, entryPrice)
		}
	}
	return nil
}

func (pb *PaperBroker) cancelSiblingOrders(pos *domain.Position) {
	for id, order := range pb.openOrders {
		if order.Symbol == pos.Symbol {
			if order.ID == derefInt64(pos.SLOrderID) || order.ID == derefInt64(pos.TPOrderID) {
				order.Status = domain.OrderStatusCancelled
				order.UpdatedAt = time.Now()
				pb.updateOrderInDB(order)
				delete(pb.openOrders, id)
			}
		}
	}
}

func (pb *PaperBroker) saveProtectiveOrdersForPosition(pos *domain.Position) {
	if pb.ordRepo == nil {
		return
	}
	for _, o := range pb.openOrders {
		if o.Symbol != pos.Symbol {
			continue
		}
		if o.OrderType != domain.OrderTypeStopMarket && o.OrderType != domain.OrderTypeTakeProfitMarket {
			continue
		}
		if (pos.SLOrderID != nil && o.ID == *pos.SLOrderID) || (pos.TPOrderID != nil && o.ID == *pos.TPOrderID) {
			pb.saveOrder(o)
		}
	}
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func validatePositionStopLoss(pos domain.Position) error {
	if pos.Side == domain.SideLong && pos.StopLoss >= pos.EntryPrice {
		return fmt.Errorf("persisted LONG position %s has stop loss %.8f >= entry %.8f", pos.Symbol, pos.StopLoss, pos.EntryPrice)
	}
	if pos.Side == domain.SideShort && pos.StopLoss <= pos.EntryPrice {
		return fmt.Errorf("persisted SHORT position %s has stop loss %.8f <= entry %.8f", pos.Symbol, pos.StopLoss, pos.EntryPrice)
	}
	return nil
}

func (pb *PaperBroker) hasOpenStopOrderLocked(symbol string) bool {
	for _, order := range pb.openOrders {
		if order.Symbol == symbol && order.OrderType == domain.OrderTypeStopMarket && order.Status == domain.OrderStatusPending {
			return true
		}
	}
	return false
}

func (pb *PaperBroker) sideFromOrderSide(os domain.OrderSide) domain.Side {
	if os == domain.OrderSideBuy {
		return domain.SideLong
	}
	return domain.SideShort
}

func (pb *PaperBroker) closePositionInternalWithOrder(pos *domain.Position, exitPrice float64, now time.Time, closingOrder *domain.Order) {
	// Cancel sibling protective orders first
	pb.cancelSiblingOrders(pos)

	takerFeeBps := pb.cfg.FeeTakerBps
	slippageBps := pb.cfg.SlippageBps

	// Apply slippage to exit
	var slippageAmt float64
	if pos.Side == domain.SideLong {
		slippageAmt = exitPrice * slippageBps / 10000
		exitPrice -= slippageAmt
	} else {
		slippageAmt = exitPrice * slippageBps / 10000
		exitPrice += slippageAmt
	}

	// Calculate PnL
	var pnl float64
	if pos.Side == domain.SideLong {
		pnl = (exitPrice - pos.EntryPrice) * pos.Size
	} else {
		pnl = (pos.EntryPrice - exitPrice) * pos.Size
	}

	notional := exitPrice * pos.Size
	fee := notional * takerFeeBps / 10000
	pnl -= fee

	pos.RealizedPnL = pnl
	pos.UnrealizedPnL = 0
	pos.Status = domain.PositionStatusClosed
	pos.ClosedAt = &now

	// Update account
	// Wallet balance model: margin was never subtracted from balance on open,
	// so only add realized PnL (which already includes exit fee) on close.
	pb.balance += pnl
	pb.usedMargin -= pos.Margin
	pb.realizedPnL += pnl
	pb.totalFees += fee
	pb.totalSlippage += slippageAmt * pos.Size

	if pnl < 0 {
		pb.dailyLoss += math.Abs(pnl)
	}

	delete(pb.openPositions, pos.Symbol)
	pb.closedPositions = append(pb.closedPositions, *pos)

	// Save closing execution
	closeSide := domain.OrderSideSell
	if pos.Side == domain.SideShort {
		closeSide = domain.OrderSideBuy
	}
	var closeOrderID int64
	if closingOrder != nil {
		closeOrderID = closingOrder.ID
	} else {
		// Synthetic MARKET close order for manual/emergency closes.
		// Never use order_id=0 — traceability invariant.
		synthOrder := &domain.Order{
			ID:            pb.nextOrderID,
			BrokerOrderID: fmt.Sprintf("paper-%d", pb.nextOrderID),
			Symbol:        pos.Symbol,
			Side:          closeSide,
			OrderType:     domain.OrderTypeMarket,
			Qty:           pos.Size,
			Status:        domain.OrderStatusFilled,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		pb.nextOrderID++
		pb.saveOrder(synthOrder)
		closeOrderID = synthOrder.ID
	}
	pb.saveExecution(&domain.Execution{
		ID:         pb.nextExecID,
		OrderID:    closeOrderID,
		Symbol:     pos.Symbol,
		Side:       closeSide,
		Qty:        pos.Size,
		Price:      exitPrice,
		Fee:        fee,
		Slippage:   slippageAmt,
		ExecutedAt: now,
	})
	pb.nextExecID++

	// DB persistence: update if already persisted, otherwise insert closed.
	pb.upsertPosition(pos)
	pb.saveAccountSnapshot()
	pb.log.Info("position closed", map[string]any{
		"symbol":   pos.Symbol,
		"side":     pos.Side,
		"entry":    pos.EntryPrice,
		"exit":     exitPrice,
		"pnl":      pnl,
		"fee":      fee,
		"slippage": slippageAmt,
	})

	// Alert on position close
	pb.sendAlert(alert.AlertEvent{
		Type:      "position_closed",
		Severity:  severityForPnL(pnl),
		Message:   fmt.Sprintf("Position closed: %s %s PnL=%.2f", pos.Symbol, pos.Side, pnl),
		Symbol:    pos.Symbol,
		PnL:       pnl,
		Timestamp: now,
	})
}

func (pb *PaperBroker) reducePosition(pos *domain.Position, side domain.OrderSide, qty, fillPrice, fee, slippageAmt float64) {
	if qty >= pos.Size {
		// Full close
		pb.closePositionInternalWithOrder(pos, fillPrice, time.Now(), nil)
		return
	}

	// Partial close
	var pnl float64
	if pos.Side == domain.SideLong {
		pnl = (fillPrice - pos.EntryPrice) * qty
	} else {
		pnl = (pos.EntryPrice - fillPrice) * qty
	}
	pnl -= fee

	marginFreed := pos.Margin * (qty / pos.Size)
	pos.Size -= qty
	pos.Margin -= marginFreed
	pos.RealizedPnL += pnl

	pb.balance += pnl
	pb.usedMargin -= marginFreed
	pb.realizedPnL += pnl
	pb.totalFees += fee
	pb.totalSlippage += slippageAmt * qty

	if pnl < 0 {
		pb.dailyLoss += math.Abs(pnl)
	}
}

func (pb *PaperBroker) updatePositionPnL(pos *domain.Position, price float64) {
	if pos.Side == domain.SideLong {
		pos.UnrealizedPnL = (price - pos.EntryPrice) * pos.Size
	} else {
		pos.UnrealizedPnL = (pos.EntryPrice - price) * pos.Size
	}
}

func (pb *PaperBroker) checkProtectiveOrders(symbol string, price float64, now time.Time) {
	for id, order := range pb.openOrders {
		if order.Symbol != symbol {
			continue
		}
		if order.OrderType == domain.OrderTypeStopMarket {
			if pb.shouldTriggerSL(order, price) {
				pb.triggerProtectiveOrder(order, price, now)
				delete(pb.openOrders, id)
			}
		}
		if order.OrderType == domain.OrderTypeTakeProfitMarket {
			if pb.shouldTriggerTP(order, price) {
				pb.triggerProtectiveOrder(order, price, now)
				delete(pb.openOrders, id)
			}
		}
	}
}

func (pb *PaperBroker) checkProtectiveOrdersCandle(symbol string, candle domain.Candle, now time.Time) {
	// Conservative SL-first assumption: if both SL and TP could trigger in same candle,
	// assume SL triggers first.
	slTriggered := false

	for id, order := range pb.openOrders {
		if order.Symbol != symbol {
			continue
		}
		if order.OrderType == domain.OrderTypeStopMarket {
			if order.StopPrice != nil && candleCrossesPrice(candle, *order.StopPrice) {
				pb.triggerProtectiveOrder(order, *order.StopPrice, now)
				delete(pb.openOrders, id)
				slTriggered = true
			}
		}
	}

	// Only check TP if SL was NOT triggered (conservative assumption)
	if !slTriggered {
		for id, order := range pb.openOrders {
			if order.Symbol != symbol {
				continue
			}
			if order.OrderType == domain.OrderTypeTakeProfitMarket {
				if order.StopPrice != nil && candleCrossesPrice(candle, *order.StopPrice) {
					pb.triggerProtectiveOrder(order, *order.StopPrice, now)
					delete(pb.openOrders, id)
				}
			}
		}
	}
}

func (pb *PaperBroker) shouldTriggerSL(order *domain.Order, price float64) bool {
	if order.StopPrice == nil {
		return false
	}
	pos, ok := pb.openPositions[order.Symbol]
	if !ok {
		return false
	}
	if pos.Side == domain.SideLong {
		return price <= *order.StopPrice
	}
	return price >= *order.StopPrice
}

func (pb *PaperBroker) shouldTriggerTP(order *domain.Order, price float64) bool {
	if order.StopPrice == nil {
		return false
	}
	pos, ok := pb.openPositions[order.Symbol]
	if !ok {
		return false
	}
	if pos.Side == domain.SideLong {
		return price >= *order.StopPrice
	}
	return price <= *order.StopPrice
}

func (pb *PaperBroker) triggerProtectiveOrder(order *domain.Order, triggerPrice float64, now time.Time) {
	pos, ok := pb.openPositions[order.Symbol]
	if !ok {
		return
	}
	pb.closePositionInternalWithOrder(pos, triggerPrice, now, order)
	order.Status = domain.OrderStatusFilled
	order.UpdatedAt = now
	pb.updateOrderInDB(order)
}

func (pb *PaperBroker) wouldLimitFill(order *domain.Order, candle domain.Candle) bool {
	if order.Price == nil {
		return false
	}
	if order.Side == domain.OrderSideBuy {
		return candle.Low <= *order.Price
	}
	return candle.High >= *order.Price
}

func (pb *PaperBroker) fillLimitOrder(order *domain.Order, candle domain.Candle) {
	if order.Price == nil {
		return
	}
	fillPrice := *order.Price

	// Validate SL side BEFORE mutating any broker state (H3 fix).
	if order.IntendedSL > 0 {
		if err := pb.validateStopLoss(order.Side, fillPrice, order.IntendedSL); err != nil {
			order.Status = domain.OrderStatusRejected
			order.UpdatedAt = time.Now()
			pb.updateOrderInDB(order)
			pb.log.Error("limit fill rejected: invalid StopLoss price", map[string]any{"symbol": order.Symbol, "error": err.Error()})
			return
		}
	}

	makerFeeBps := pb.cfg.FeeMakerBps
	notional := fillPrice * order.Qty
	fee := notional * makerFeeBps / 10000
	margin := notional / pb.cfg.DefaultLeverage

	if pb.balance-pb.usedMargin < margin+fee {
		order.Status = domain.OrderStatusRejected
		order.UpdatedAt = time.Now()
		pb.updateOrderInDB(order)
		pb.log.Error("limit order rejected: insufficient margin", map[string]any{
			"symbol": order.Symbol,
		})
		return
	}

	pos := &domain.Position{
		ID:         pb.nextPositionID,
		Symbol:     order.Symbol,
		Side:       pb.sideFromOrderSide(order.Side),
		EntryPrice: fillPrice,
		Size:       order.Qty,
		Leverage:   pb.cfg.DefaultLeverage,
		Margin:     margin,
		Status:     domain.PositionStatusOpen,
		Source:     "paper",
		OpenedAt:   time.Now(),
	}
	pb.nextPositionID++
	pb.openPositions[order.Symbol] = pos
	pb.usedMargin += margin
	pb.balance -= fee
	pb.totalFees += fee

	// Atomic protective orders for limit fills: create first (in-memory), then persist.
	if order.IntendedSL > 0 || order.IntendedTP > 0 {
		if !pb.createProtectiveOrdersForPosition(pos, order.IntendedSL, order.IntendedTP) {
			// Position was emergency-closed due to invalid SL.
			order.Status = domain.OrderStatusRejected
			order.UpdatedAt = time.Now()
			pb.updateOrderInDB(order)
			pb.log.Error("limit fill rejected: invalid StopLoss price", map[string]any{"symbol": order.Symbol})
			return
		}
	} else {
		// No SL intended — emergency close (safety invariant)
		pb.log.Error("limit fill with no SL — emergency closing", map[string]any{"symbol": order.Symbol})
		pb.closePositionInternalWithOrder(pos, fillPrice, time.Now(), nil)
		order.Status = domain.OrderStatusRejected
		order.UpdatedAt = time.Now()
		pb.updateOrderInDB(order)
		return
	}

	// Set SL/TP on position after protective orders are confirmed.
	pos.StopLoss = order.IntendedSL
	pos.TakeProfit = order.IntendedTP

	order.Status = domain.OrderStatusFilled
	order.UpdatedAt = time.Now()

	// DB persistence order: main order → protective orders → position → execution
	pb.updateOrderInDB(order)
	pb.saveProtectiveOrdersForPosition(pos)
	pb.savePosition(pos)
	pb.saveExecution(&domain.Execution{
		ID:         pb.nextExecID,
		OrderID:    order.ID,
		Symbol:     order.Symbol,
		Side:       order.Side,
		Qty:        order.Qty,
		Price:      fillPrice,
		Fee:        fee,
		Slippage:   0,
		ExecutedAt: time.Now(),
	})
	pb.nextExecID++
	// Snapshot account state after entry.
	pb.saveAccountSnapshotNow()

	pb.log.Info("limit order filled", map[string]any{
		"symbol":     order.Symbol,
		"side":       order.Side,
		"qty":        order.Qty,
		"fill_price": fillPrice,
		"fee":        fee,
	})
}

func candleCrossesPrice(candle domain.Candle, price float64) bool {
	return candle.Low <= price && candle.High >= price
}

// ResetDailyLoss resets the daily loss counter.
func (pb *PaperBroker) ResetDailyLoss() {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	pb.dailyLoss = 0
}

// SetProtectiveOrders sets SL and TP orders for a position.
// Returns error if SL cannot be created (safety invariant).
// Idempotent: cancels existing SL/TP orders before creating new ones.
func (pb *PaperBroker) SetProtectiveOrders(ctx context.Context, symbol string, slPrice, tpPrice float64) error {
	if slPrice <= 0 {
		return fmt.Errorf("SL price must be > 0 (no position without SL)")
	}

	// Cancel existing protective orders and snapshot position data under lock
	// to prevent TOCTOU race where the position is closed/modified between
	// cancel and re-link (C2).
	pb.mu.Lock()
	if pos, ok := pb.openPositions[symbol]; ok {
		pb.cancelSiblingOrders(pos)
	}
	pos, ok := pb.openPositions[symbol]
	var posCopy domain.Position
	if ok {
		posCopy = *pos
	}
	pb.mu.Unlock()

	if !ok {
		return fmt.Errorf("no open position for %s", symbol)
	}

	// Validate SL is on correct side using copied data
	if posCopy.Side == domain.SideLong && slPrice >= posCopy.EntryPrice {
		return fmt.Errorf("LONG SL must be below entry price")
	}
	if posCopy.Side == domain.SideShort && slPrice <= posCopy.EntryPrice {
		return fmt.Errorf("SHORT SL must be above entry price")
	}

	// Validate TP is on correct side (if provided)
	if tpPrice > 0 {
		if posCopy.Side == domain.SideLong && tpPrice <= posCopy.EntryPrice {
			return fmt.Errorf("LONG TP must be above entry price")
		}
		if posCopy.Side == domain.SideShort && tpPrice >= posCopy.EntryPrice {
			return fmt.Errorf("SHORT TP must be below entry price")
		}
	}

	// Place SL order
	slReq := OrderRequest{
		Symbol:    symbol,
		Side:      domain.OrderSideSell, // SL for long = sell, for short = buy
		OrderType: domain.OrderTypeStopMarket,
		Qty:       posCopy.Size,
		StopPrice: &slPrice,
	}
	if posCopy.Side == domain.SideShort {
		slReq.Side = domain.OrderSideBuy
	}

	slOrder, err := pb.PlaceOrder(ctx, slReq)
	if err != nil {
		return fmt.Errorf("create SL order: %w", err)
	}

	// Place TP order if provided
	if tpPrice > 0 {
		tpReq := OrderRequest{
			Symbol:    symbol,
			Side:      domain.OrderSideSell,
			OrderType: domain.OrderTypeTakeProfitMarket,
			Qty:       posCopy.Size,
			StopPrice: &tpPrice,
		}
		if posCopy.Side == domain.SideShort {
			tpReq.Side = domain.OrderSideBuy
		}

		tpOrder, err := pb.PlaceOrder(ctx, tpReq)
		if err != nil {
			// TP failed, but SL exists — log warning but don't emergency close
			pb.log.Warn("TP order creation failed, SL still active", map[string]any{
				"symbol": symbol,
				"error":  err.Error(),
			})
			_ = tpOrder
		} else {
			// Link orders to position only if it still exists with the same ID.
			pb.mu.Lock()
			if p, ok := pb.openPositions[symbol]; ok && p.ID == posCopy.ID {
				p.SLOrderID = &slOrder.ID
				p.TPOrderID = &tpOrder.ID
				p.StopLoss = slPrice
				p.TakeProfit = tpPrice
				pb.updatePositionInDB(p)
			}
			pb.mu.Unlock()
		}
	} else {
		pb.mu.Lock()
		if p, ok := pb.openPositions[symbol]; ok && p.ID == posCopy.ID {
			p.SLOrderID = &slOrder.ID
			p.StopLoss = slPrice
			pb.updatePositionInDB(p)
		}
		pb.mu.Unlock()
	}

	return nil
}

// --- DB persistence helpers ---

func (pb *PaperBroker) savePosition(pos *domain.Position) {
	if pb.posRepo == nil {
		return
	}
	ctx := context.Background()
	id, err := pb.posRepo.Insert(ctx, *pos)
	if err != nil {
		pb.log.Error("failed to save position to DB", map[string]any{"symbol": pos.Symbol, "error": err.Error()})
		return
	}
	pos.ID = id
}

func (pb *PaperBroker) saveOrder(order *domain.Order) {
	if pb.ordRepo == nil {
		return
	}
	ctx := context.Background()
	id, err := pb.ordRepo.Insert(ctx, *order)
	if err != nil {
		pb.log.Error("failed to save order to DB", map[string]any{"symbol": order.Symbol, "error": err.Error()})
		return
	}
	order.ID = id
}

func (pb *PaperBroker) updatePositionInDB(pos *domain.Position) {
	if pb.posRepo == nil {
		return
	}
	ctx := context.Background()
	if err := pb.posRepo.Update(ctx, *pos); err != nil {
		pb.log.Error("failed to update position in DB", map[string]any{"symbol": pos.Symbol, "error": err.Error()})
	}
}

// upsertPosition persists a position via update, falling back to insert
// if the position was never persisted (e.g. emergency-close before initial save).
func (pb *PaperBroker) upsertPosition(pos *domain.Position) {
	if pb.posRepo == nil {
		return
	}
	ctx := context.Background()
	if err := pb.posRepo.Update(ctx, *pos); err != nil {
		if _, insErr := pb.posRepo.Insert(ctx, *pos); insErr != nil {
			pb.log.Error("failed to upsert position in DB", map[string]any{"symbol": pos.Symbol, "error": insErr.Error()})
		}
	}
}

func (pb *PaperBroker) updateOrderInDB(order *domain.Order) {
	if pb.ordRepo == nil {
		return
	}
	ctx := context.Background()
	if err := pb.ordRepo.Update(ctx, *order); err != nil {
		pb.log.Error("failed to update order in DB", map[string]any{"symbol": order.Symbol, "error": err.Error()})
	}
}

func (pb *PaperBroker) saveExecution(exec *domain.Execution) {
	if pb.execRepo == nil {
		return
	}
	ctx := context.Background()
	if _, err := pb.execRepo.Insert(ctx, *exec); err != nil {
		pb.log.Error("failed to save execution to DB", map[string]any{"symbol": exec.Symbol, "error": err.Error()})
	}
}

func (pb *PaperBroker) saveAccountSnapshot() {
	pb.saveAccountSnapshotNow()
}

func (pb *PaperBroker) saveAccountSnapshotNow() {
	if pb.snapRepo == nil {
		return
	}
	pb.lastSnapshotAt = time.Now()

	// Must access fields directly since caller holds pb.mu.Lock().
	// GetAccountState would deadlock on pb.mu.RLock().
	equity := pb.balance + pb.unrealizedPnL
	state := domain.AccountState{
		Balance:          pb.balance,
		AvailableBalance: pb.balance - pb.usedMargin,
		UsedMargin:       pb.usedMargin,
		Equity:           equity,
		RealizedPnL:      pb.realizedPnL,
		UnrealizedPnL:    pb.unrealizedPnL,
		TotalFees:        pb.totalFees,
		TotalSlippage:    pb.totalSlippage,
		DailyLoss:        pb.dailyLoss,
		RecordedAt:       time.Now(),
	}
	ctx := context.Background()
	if _, err := pb.snapRepo.Insert(ctx, state); err != nil {
		pb.log.Error("failed to save account snapshot", map[string]any{"error": err.Error()})
	}
}

func (pb *PaperBroker) sendAlert(event alert.AlertEvent) {
	if pb.alert == nil {
		return
	}
	if err := pb.alert.Send(context.Background(), event); err != nil {
		pb.log.Error("failed to send alert", map[string]any{"type": event.Type, "error": err.Error()})
	}
}

func severityForPnL(pnl float64) string {
	if pnl >= 0 {
		return "info"
	}
	if pnl > -10 {
		return "warning"
	}
	return "danger"
}
