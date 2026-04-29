package alert

import (
	"context"
	"time"
)

// AlertEvent represents a single alert event.
type AlertEvent struct {
	Type      string // e.g., "trade_executed", "position_closed", "kill_switch"
	Severity  string // "info", "warning", "danger"
	Message   string
	Symbol    string
	PnL       float64
	Timestamp time.Time
}

// Service is the interface for sending alerts.
type Service interface {
	Send(ctx context.Context, event AlertEvent) error
}
