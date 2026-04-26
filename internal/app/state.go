package app

import "time"

// State represents the runtime mutable state of the bot.
type State struct {
	Mode         string     `json:"mode"`
	Running      bool       `json:"running"`
	LastCycleAt  *time.Time `json:"last_cycle_at"`
	DailyResetAt *time.Time `json:"daily_reset_at"`
	Halted       bool       `json:"halted"`
	HaltReason   string     `json:"halt_reason"`
}

// NewState creates a default state.
func NewState(mode string) *State {
	return &State{Mode: mode}
}
