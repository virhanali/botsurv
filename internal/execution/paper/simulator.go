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

	// In-memory account cache
	startingEquity float64
	currentEquity  float64
	totalTrades    int
	wins           int
	losses         int
	realizedPnL    float64
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

	// Rehydrate open positions count from DB
	if s.paperTradeRepo != nil {
		open, err := s.paperTradeRepo.GetOpen(ctx)
		if err != nil {
			s.log.Warn("failed to load open paper trades", map[string]any{"error": err.Error()})
		}
		s.log.Info("paper simulator initialized", map[string]any{
			"starting_equity": s.startingEquity,
			"current_equity":  s.currentEquity,
			"open_positions":  len(open),
			"total_trades":    s.totalTrades,
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
	s.mu.Lock()
	defer s.mu.Unlock()

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
		price = price * (1 + slippageBps/10000)
	} else {
		price = price * (1 - slippageBps/10000)
	}

	// Calculate fees (taker)
	fee := plan.Qty * price * (s.cfg.FeeTakerBps / 10000)
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
	if plan.EntryPrice > 0 && plan.EntryType == "market" {
		slippagePct := math.Abs(price-plan.EntryPrice) / plan.EntryPrice
		if slippagePct > 0.005 { // 0.5% threshold
			riskDist := math.Abs(plan.EntryPrice - plan.StopLoss)
			tpDist := 0.0
			if len(plan.TakeProfits) > 0 {
				tpDist = math.Abs(plan.TakeProfits[0].Price - plan.EntryPrice)
			}
			if plan.Side == domain.SideLong {
				sl = price - riskDist
				tp = price + tpDist
			} else {
				sl = price + riskDist
				tp = price - tpDist
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

	if s.paperTradeRepo != nil {
		if err := s.paperTradeRepo.Insert(ctx, trade); err != nil {
			return nil, fmt.Errorf("insert paper trade: %w", err)
		}
	}

	// Deduct fees from equity
	s.currentEquity -= fee
	s.totalTrades++

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
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	// Calculate PnL
	var pnlGross float64
	if trade.Side == domain.SideLong {
		pnlGross = (exitPrice - trade.EntryPrice) * trade.Qty
	} else {
		pnlGross = (trade.EntryPrice - exitPrice) * trade.Qty
	}

	// Exit fee (taker)
	exitFee := trade.Qty * exitPrice * (s.cfg.FeeTakerBps / 10000)
	if exitFee < 0 {
		exitFee = 0
	}

	pnlNet := pnlGross - exitFee

	// Calculate R-multiple
	var rMultiple float64
	if trade.EntryPrice > 0 && trade.StopLoss > 0 {
		riskPerUnit := math.Abs(trade.EntryPrice - trade.StopLoss)
		if riskPerUnit > 0 {
			resultPerUnit := math.Abs(exitPrice - trade.EntryPrice)
			if reason == "sl" {
				rMultiple = -1.0
			} else {
				rMultiple = resultPerUnit / riskPerUnit
			}
		}
	}

	totalFees := trade.FeesPaid + exitFee

	trade.ClosedAt = &now
	trade.ExitPrice = &exitPrice
	trade.FeesPaid = totalFees
	trade.PnLGross = &pnlGross
	trade.PnLNet = &pnlNet
	trade.RMultiple = &rMultiple
	trade.ExitReason = reason

	if s.paperTradeRepo != nil {
		if err := s.paperTradeRepo.Update(ctx, trade); err != nil {
			s.log.Error("failed to update paper trade on close", map[string]any{
				"paper_trade_id": trade.PaperTradeID,
				"error":          err.Error(),
			})
			return
		}
	}

	// Update account
	s.currentEquity += pnlNet
	s.realizedPnL += pnlNet
	if pnlNet > 0 {
		s.wins++
	} else {
		s.losses++
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
	})
}

func (s *Simulator) persistAccountState(ctx context.Context) {
	if s.accountRepo == nil {
		return
	}
	err := s.accountRepo.Update(ctx, domain.PaperAccountState{
		ID:             1,
		StartingEquity: s.startingEquity,
		CurrentEquity:  s.currentEquity,
		TotalTrades:    s.totalTrades,
		Wins:           s.wins,
		Losses:         s.losses,
		RealizedPnL:    s.realizedPnL,
	})
	if err != nil {
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

// GetRealizedPnL returns the total realized paper PnL.
func (s *Simulator) GetRealizedPnL() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.realizedPnL
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
