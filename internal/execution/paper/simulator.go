package paper

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/db"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
	"github.com/virhan/botsurv/internal/risk"
	"github.com/virhan/botsurv/internal/shadow"
)

// MarketDataProvider provides real-time price data for the simulator.
type MarketDataProvider interface {
	GetLatestPrice(ctx context.Context, symbol string) (float64, error)
}

// Simulator simulates order execution and position management in paper mode.
type Simulator struct {
	md              MarketDataProvider
	paperTradeRepo  db.PaperTradeRepository
	accountRepo     db.PaperAccountStateRepository
	cfg             app.PaperConfig
	log             *logger.Logger
	mu              sync.RWMutex
	checkMu         sync.Mutex // serializes CheckOpenPositions to prevent double-close race (C1)

	// In-memory account cache
	startingEquity     float64
	currentEquity      float64
	totalTrades        int
	wins               int
	losses             int
	realizedPnL        float64
	consecutiveLosses int

	// initializedAt tracks when Initialize() was called. Trades opened before
	// this time are rehydrated positions from a previous session and should not
	// contribute to wins/losses/totalTrades on close.
	initializedAt time.Time
}

// NewSimulator creates a new paper mode simulator.
func NewSimulator(md MarketDataProvider, paperTradeRepo db.PaperTradeRepository, accountRepo db.PaperAccountStateRepository, cfg app.PaperConfig, log *logger.Logger) *Simulator {
	return &Simulator{
		md:             md,
		paperTradeRepo: paperTradeRepo,
		accountRepo:    accountRepo,
		cfg:            cfg,
		log:            log,
	}
}

// Initialize loads or creates the paper account state.
func (s *Simulator) Initialize(ctx context.Context) error {
	s.initializedAt = time.Now()

	if s.accountRepo == nil {
		s.startingEquity = s.cfg.StartingBalanceUSD
		s.currentEquity = s.cfg.StartingBalanceUSD
		return nil
	}
	state, err := s.accountRepo.Get(ctx)
	if err != nil {
		return fmt.Errorf("load paper account: %w", err)
	}
	if state.StartingEquity <= 0 {
		state.StartingEquity = s.cfg.StartingBalanceUSD
		state.CurrentEquity = s.cfg.StartingBalanceUSD
	}
	s.startingEquity = state.StartingEquity
	s.currentEquity = state.CurrentEquity
	s.totalTrades = state.TotalTrades
	s.wins = state.Wins
	s.losses = state.Losses
	s.realizedPnL = state.RealizedPnL
	s.consecutiveLosses = state.ConsecutiveLosses

	// Ensure row exists in DB (first-ever init or migration didn't create it)
	s.persistAccountState(ctx)

	// Rehydrate open positions count from DB
	if s.paperTradeRepo != nil {
		open, err := s.paperTradeRepo.GetOpen(ctx)
		if err != nil {
			s.log.Warn("failed to load open paper trades", map[string]any{"error": err.Error()})
		}
		s.log.Info("paper simulator initialized", map[string]any{
			"starting_equity":  s.startingEquity,
			"current_equity":   s.currentEquity,
			"open_positions":    len(open),
			"total_trades":     s.totalTrades,
			"wins":             s.wins,
			"losses":           s.losses,
			"consecutive_losses": s.consecutiveLosses,
		})
	}
	return nil
}

// AccountState returns the current paper account state.
func (s *Simulator) AccountState() domain.AccountState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return domain.AccountState{
		Balance:       s.currentEquity,
		Equity:        s.currentEquity,
		RealizedPnL:   s.realizedPnL,
		UnrealizedPnL: 0,
	}
}

// ConsecutiveLosses returns the current consecutive loss count.
func (s *Simulator) ConsecutiveLosses() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.consecutiveLosses
}

// SetConsecutiveLosses sets the consecutive loss count (used by scheduler to sync state).
func (s *Simulator) SetConsecutiveLosses(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consecutiveLosses = n
}

// Wins returns the current win count.
func (s *Simulator) Wins() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.wins
}

// Losses returns the current loss count.
func (s *Simulator) Losses() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.losses
}

// TotalTrades returns the current total trade count.
func (s *Simulator) TotalTrades() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalTrades
}

// OpenPositionCount returns the number of currently open paper positions.
func (s *Simulator) OpenPositionCount(ctx context.Context) int {
	if s.paperTradeRepo == nil {
		return 0
	}
	open, err := s.paperTradeRepo.GetOpen(ctx)
	if err != nil {
		return 0
	}
	return len(open)
}

// SimulateFill simulates opening a position from an order plan.
func (s *Simulator) SimulateFill(ctx context.Context, decisionID string, plan risk.OrderPlan) (*domain.PaperTrade, error) {
	price, err := s.md.GetLatestPrice(ctx, plan.Symbol)
	if err != nil {
		return nil, fmt.Errorf("get latest price: %w", err)
	}
	if price <= 0 {
		return nil, fmt.Errorf("invalid price for %s: %.4f", plan.Symbol, price)
	}

	// Apply slippage
	slippageBps := s.cfg.SlippageBps
	if slippageBps <= 0 {
		slippageBps = 5
	}
	if plan.Side == domain.SideLong {
		price = roundToPrecision(price*(1+slippageBps/10000), 8)
	} else {
		price = roundToPrecision(price*(1-slippageBps/10000), 8)
	}

	// Calculate fees (taker)
	fee := roundToPrecision(plan.Qty*price*(s.cfg.FeeTakerBps/10000), 8)
	if fee < 0 {
		fee = 0
	}

	// Find TP from plan
	var tp float64
	if len(plan.TakeProfits) > 0 {
		tp = plan.TakeProfits[0].Price
	}
	// Fallback: use the proposed trade TP
	if tp <= 0 && plan.StopLoss > 0 {
		// If we only have SL, use entry + risk as TP
		risk := math.Abs(plan.EntryPrice - plan.StopLoss)
		if plan.Side == domain.SideLong {
			tp = plan.EntryPrice + risk*2
		} else {
			tp = plan.EntryPrice - risk*2
		}
	}

	// Recalculate TP/SL if actual fill differs significantly from planned entry
	sl := plan.StopLoss
	if plan.EntryPrice > 0 && plan.StopLoss > 0 && tp > 0 {
		slippagePct := math.Abs(price-plan.EntryPrice) / plan.EntryPrice
		if slippagePct > 0.005 { // 0.5% threshold
			riskDist := math.Abs(plan.EntryPrice - plan.StopLoss)
			tpDist := math.Abs(tp - plan.EntryPrice)
			if plan.Side == domain.SideLong {
				sl = roundToPrecision(price-riskDist, 8)
				tp = roundToPrecision(price+tpDist, 8)
			} else {
				sl = roundToPrecision(price+riskDist, 8)
				tp = roundToPrecision(price-tpDist, 8)
			}
		}
	}

	trade := domain.PaperTrade{
		PaperTradeID: shadow.NewUUID(),
		DecisionID:   decisionID,
		OpenedAt:     time.Now(),
		Symbol:       plan.Symbol,
		Side:         plan.Side,
		Qty:          plan.Qty,
		Leverage:     plan.Leverage,
		EntryPrice:   price,
		StopLoss:     sl,
		TakeProfit:   tp,
		FeesPaid:     fee,
	}

	// Update in-memory state under lock
	s.mu.Lock()
	s.currentEquity = roundToPrecision(s.currentEquity-fee, 8)
	s.totalTrades++
	s.mu.Unlock()

	// DB writes outside lock
	if s.paperTradeRepo != nil {
		if err := s.paperTradeRepo.Insert(ctx, trade); err != nil {
			return nil, fmt.Errorf("insert paper trade: %w", err)
		}
	}
	s.persistAccountState(ctx)

	s.log.Info("paper position opened", map[string]any{
		"paper_trade_id": trade.PaperTradeID,
		"symbol":         trade.Symbol,
		"side":           trade.Side,
		"qty":            trade.Qty,
		"entry_price":    trade.EntryPrice,
		"sl":             trade.StopLoss,
		"tp":             trade.TakeProfit,
		"fee":            fee,
	})

	return &trade, nil
}

// CheckOpenPositions checks all open paper positions against current prices.
// Closes positions if SL or TP is hit.
func (s *Simulator) CheckOpenPositions(ctx context.Context) error {
	if s.paperTradeRepo == nil {
		return nil
	}

	// Serialize position checks to prevent double-close race between the 5s
	// background ticker and the scheduler cycle (C1).
	s.checkMu.Lock()
	defer s.checkMu.Unlock()

	openTrades, err := s.paperTradeRepo.GetOpen(ctx)
	if err != nil {
		return fmt.Errorf("get open paper trades: %w", err)
	}

	for _, trade := range openTrades {
		price, err := s.md.GetLatestPrice(ctx, trade.Symbol)
		if err != nil || price <= 0 {
			continue
		}

		var shouldClose bool
		var exitReason string
		var exitPrice float64

		if trade.Side == domain.SideLong {
			if price <= trade.StopLoss {
				shouldClose = true
				exitReason = "sl"
				// Apply slippage on SL
				slippage := s.cfg.SlippageBps / 10000
				exitPrice = trade.StopLoss * (1 - slippage)
			} else if price >= trade.TakeProfit {
				shouldClose = true
				exitReason = "tp1"
				// Apply slippage on TP
				slippage := s.cfg.SlippageBps / 10000
				exitPrice = trade.TakeProfit * (1 - slippage)
			}
		} else {
			if price >= trade.StopLoss {
				shouldClose = true
				exitReason = "sl"
				slippage := s.cfg.SlippageBps / 10000
				exitPrice = trade.StopLoss * (1 + slippage)
			} else if price <= trade.TakeProfit {
				shouldClose = true
				exitReason = "tp1"
				slippage := s.cfg.SlippageBps / 10000
				exitPrice = trade.TakeProfit * (1 + slippage)
			}
		}

		if shouldClose {
			s.closeTrade(ctx, trade, exitPrice, exitReason)
		}
	}
	return nil
}

func (s *Simulator) closeTrade(ctx context.Context, trade domain.PaperTrade, exitPrice float64, reason string) {
	now := time.Now()

	// Calculate PnL (pure arithmetic, no state mutation)
	var pnlGross float64
	if trade.Side == domain.SideLong {
		pnlGross = roundToPrecision((exitPrice-trade.EntryPrice)*trade.Qty, 8)
	} else {
		pnlGross = roundToPrecision((trade.EntryPrice-exitPrice)*trade.Qty, 8)
	}

	// Exit fee (taker)
	exitFee := roundToPrecision(trade.Qty*exitPrice*(s.cfg.FeeTakerBps/10000), 8)
	if exitFee < 0 {
		exitFee = 0
	}

	pnlNet := roundToPrecision(pnlGross-exitFee, 8)

	// Calculate R-multiple
	var rMultiple float64
	if trade.EntryPrice > 0 && trade.StopLoss > 0 {
		riskPerUnit := math.Abs(trade.EntryPrice - trade.StopLoss)
		if riskPerUnit > 0 {
			resultPerUnit := math.Abs(exitPrice - trade.EntryPrice)
			if reason == "sl" {
				rMultiple = -1.0
			} else {
				rMultiple = roundToPrecision(resultPerUnit/riskPerUnit, 8)
			}
		}
	}

	totalFees := roundToPrecision(trade.FeesPaid+exitFee, 8)

	trade.ClosedAt = &now
	trade.ExitPrice = &exitPrice
	trade.FeesPaid = totalFees
	trade.PnLGross = &pnlGross
	trade.PnLNet = &pnlNet
	trade.RMultiple = &rMultiple
	trade.ExitReason = reason

	// Determine if this trade was opened before simulator init (rehydrated).
	// Rehydrated trades should still affect equity/PnL (real financial outcome)
	// but must NOT increment wins/losses/totalTrades/consecutiveLosses since
	// those were already counted in a previous session.
	isRehydrated := !s.initializedAt.IsZero() && trade.OpenedAt.Before(s.initializedAt)

	// Update in-memory state under lock
	s.mu.Lock()
	s.currentEquity = roundToPrecision(s.currentEquity+pnlNet, 8)
	s.realizedPnL = roundToPrecision(s.realizedPnL+pnlNet, 8)
	if !isRehydrated {
		if pnlNet > 0 {
			s.wins++
			s.consecutiveLosses = 0
		} else {
			s.losses++
			s.consecutiveLosses++
		}
	}
	s.mu.Unlock()

	// DB writes outside lock
	if s.paperTradeRepo != nil {
		if err := s.paperTradeRepo.Update(ctx, trade); err != nil {
			s.log.Error("failed to update paper trade on close", map[string]any{
				"paper_trade_id": trade.PaperTradeID,
				"error":          err.Error(),
			})
			return
		}
	}

	s.persistAccountState(ctx)

	s.log.Info("paper position closed", map[string]any{
		"paper_trade_id": trade.PaperTradeID,
		"symbol":         trade.Symbol,
		"exit_reason":    reason,
		"exit_price":     exitPrice,
		"pnl_gross":      pnlGross,
		"pnl_net":        pnlNet,
		"r_multiple":     rMultiple,
		"rehydrated":     isRehydrated,
	})
}

func (s *Simulator) persistAccountState(ctx context.Context) {
	if s.accountRepo == nil {
		return
	}
	s.mu.RLock()
	state := domain.PaperAccountState{
		ID:                1,
		StartingEquity:    s.startingEquity,
		CurrentEquity:      s.currentEquity,
		TotalTrades:        s.totalTrades,
		Wins:               s.wins,
		Losses:             s.losses,
		RealizedPnL:        s.realizedPnL,
		ConsecutiveLosses:  s.consecutiveLosses,
	}
	s.mu.RUnlock()

	if err := s.accountRepo.Update(ctx, state); err != nil {
		s.log.Error("failed to persist paper account state", map[string]any{"error": err.Error()})
	}
}

// GetOpenPositions returns all currently open paper trades.
func (s *Simulator) GetOpenPositions(ctx context.Context) ([]domain.Position, error) {
	if s.paperTradeRepo == nil {
		return nil, nil
	}
	trades, err := s.paperTradeRepo.GetOpen(ctx)
	if err != nil {
		return nil, err
	}
	var positions []domain.Position
	for _, t := range trades {
		positions = append(positions, domain.Position{
			Symbol:     t.Symbol,
			Side:       t.Side,
			EntryPrice: t.EntryPrice,
			Size:       t.Qty,
			Leverage:   t.Leverage,
			StopLoss:   t.StopLoss,
			TakeProfit: t.TakeProfit,
			Status:     domain.PositionStatusOpen,
			Source:     "paper",
			OpenedAt:   t.OpenedAt,
		})
	}
	return positions, nil
}

// GetRecentClosedTrades returns paper trades closed since the given time.
func (s *Simulator) GetRecentClosedTrades(ctx context.Context, since time.Time) ([]domain.PaperTrade, error) {
	if s.paperTradeRepo == nil {
		return nil, nil
	}
	all, err := s.paperTradeRepo.GetAll(ctx, since)
	if err != nil {
		return nil, err
	}
	var closed []domain.PaperTrade
	for _, t := range all {
		if t.ClosedAt != nil && t.ExitReason != "" {
			closed = append(closed, t)
		}
	}
	return closed, nil
}

// GetRealizedPnL returns the total realized paper PnL.
func (s *Simulator) GetRealizedPnL() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.realizedPnL
}

// CancelOpenPositions cancels all currently open paper trades.
// This should be called during a simulator reset to avoid rehydrating stale
// positions from a previous session whose PnL would distort the reset equity.
func (s *Simulator) CancelOpenPositions(ctx context.Context) error {
	if s.paperTradeRepo == nil {
		return nil
	}
	open, err := s.paperTradeRepo.GetOpen(ctx)
	if err != nil {
		return fmt.Errorf("get open trades for cancellation: %w", err)
	}
	now := time.Now()
	cancelFee := 0.0
	for _, trade := range open {
		trade.ClosedAt = &now
		trade.ExitReason = "cancelled"
		trade.PnLGross = &cancelFee
		trade.PnLNet = &cancelFee
		rZero := 0.0
		trade.RMultiple = &rZero
		if err := s.paperTradeRepo.Update(ctx, trade); err != nil {
			s.log.Error("failed to cancel open paper trade", map[string]any{
				"paper_trade_id": trade.PaperTradeID,
				"error":           err.Error(),
			})
			continue
		}
		s.log.Info("cancelled stale paper trade on reset", map[string]any{
			"paper_trade_id": trade.PaperTradeID,
			"symbol":         trade.Symbol,
		})
	}
	return nil
}

// Run starts the background position checker.
func (s *Simulator) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.log.Info("paper simulator checker started", map[string]any{"interval": interval.String()})

	for {
		select {
		case <-ctx.Done():
			s.log.Info("paper simulator checker stopped", nil)
			return
		case <-ticker.C:
			if err := s.CheckOpenPositions(ctx); err != nil {
				s.log.Error("paper position check failed", map[string]any{"error": err.Error()})
			}
		}
	}
}

// roundToPrecision rounds a float64 to the given number of decimal places.
// Used for all financial calculations to prevent float drift on penny coins (C3).
func roundToPrecision(val float64, prec int) float64 {
	if prec <= 0 {
		return math.Round(val)
	}
	p := math.Pow10(prec)
	return math.Round(val*p) / p
}
