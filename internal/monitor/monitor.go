package monitor

import (
	"context"
	"time"

	"github.com/virhan/botsurv/internal/alert"
	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/broker"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/logger"
)

// MarketDataProvider provides price updates.
type MarketDataProvider interface {
	GetLatestPrice(ctx context.Context, symbol string) (float64, error)
}

// Monitor continuously monitors positions and enforces kill switch.
type Monitor struct {
	broker       *broker.PaperBroker
	md           MarketDataProvider
	cfg          app.PortfolioRiskConfig
	log          *logger.Logger
	dailyResetAt time.Time
	alertSvc     alert.Service
}

// NewMonitor creates a new position Monitor.
func NewMonitor(broker *broker.PaperBroker, md MarketDataProvider, cfg app.PortfolioRiskConfig, log *logger.Logger) *Monitor {
	return &Monitor{
		broker: broker,
		md:     md,
		cfg:    cfg,
		log:    log,
	}
}

// SetAlertService sets the alert service for sending notifications.
func (m *Monitor) SetAlertService(svc alert.Service) { m.alertSvc = svc }

// Update processes a price tick: updates PnL, checks kill switch.
func (m *Monitor) Update(ctx context.Context, symbol string, price float64) {
	m.broker.UpdatePrice(symbol, price)
	m.checkKillSwitch(ctx)
}

// ProcessCandle processes a candle for order fills and protective order checks.
func (m *Monitor) ProcessCandle(candle domain.Candle) {
	m.broker.ProcessCandle(candle)
}

// checkKillSwitch checks if daily loss has been breached (uses realized + unrealized).
func (m *Monitor) checkKillSwitch(ctx context.Context) {
	state, err := m.broker.GetAccountState(ctx)
	if err != nil {
		return
	}

	maxDailyLossPct := m.cfg.MaxDailyLossPct
	if maxDailyLossPct <= 0 {
		return
	}

	// Daily loss = realized PnL losses accumulated + current unrealized loss
	totalDailyLoss := state.DailyLoss
	if state.UnrealizedPnL < 0 {
		totalDailyLoss += -state.UnrealizedPnL
	}

	maxDailyLoss := state.Equity * maxDailyLossPct / 100
	if totalDailyLoss >= maxDailyLoss {
		m.log.Error("KILL SWITCH TRIGGERED — Daily loss limit breached", map[string]any{
			"daily_loss":      totalDailyLoss,
			"max_daily_loss":  maxDailyLoss,
			"realized_loss":   state.DailyLoss,
			"unrealized_loss": -state.UnrealizedPnL,
			"equity":          state.Equity,
		})

		// Halt future entries
		m.broker.SetHalted("daily_loss_kill_switch")

		// Emergency close all
		if err := m.broker.EmergencyCloseAll(ctx); err != nil {
			m.log.Error("emergency close failed", map[string]any{"error": err.Error()})
		}

		// Alert on kill switch
		if m.alertSvc != nil {
			_ = m.alertSvc.Send(ctx, alert.AlertEvent{
				Type:      "kill_switch",
				Severity:  "danger",
				Message:   "Daily loss kill switch triggered. All positions closed, new entries halted.",
				Timestamp: time.Now(),
			})
		}
	}
}

// ResetDaily resets the daily loss counter.
func (m *Monitor) ResetDaily() {
	m.broker.ResetDailyLoss()
	m.dailyResetAt = time.Now()
	m.log.Info("daily loss counter reset", nil)
}

// IsHalted returns whether the broker is halted.
func (m *Monitor) IsHalted() bool {
	return m.broker.IsHalted()
}

// Status returns a summary of current positions and account.
func (m *Monitor) Status(ctx context.Context) MonitorStatus {
	state, _ := m.broker.GetAccountState(ctx)
	positions, _ := m.broker.GetOpenPositions(ctx)
	orders, _ := m.broker.GetOpenOrders(ctx)

	return MonitorStatus{
		AccountState:  state,
		OpenPositions: positions,
		OpenOrders:    orders,
		DailyResetAt:  m.dailyResetAt,
	}
}

// MonitorStatus holds the current monitoring state.
type MonitorStatus struct {
	AccountState  domain.AccountState
	OpenPositions []domain.Position
	OpenOrders    []domain.Order
	DailyResetAt  time.Time
}
