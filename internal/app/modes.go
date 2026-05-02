package app

import (
	"fmt"
	"os"
	"strings"
)

// BotMode represents the operational mode of the bot.
type BotMode string

const (
	ModeShadow BotMode = "shadow"
	ModePaper  BotMode = "paper"
	ModeLive   BotMode = "live"
)

// ValidModes returns all valid bot modes.
func ValidModes() []BotMode {
	return []BotMode{ModeShadow, ModePaper, ModeLive}
}

// IsValid returns true if the mode string is a known mode.
func (m BotMode) IsValid() bool {
	for _, v := range ValidModes() {
		if m == v {
			return true
		}
	}
	return false
}

// ResolveMode resolves the effective mode from environment and config.
//
// Priority:
// 1. BOTSURV_MODE env var (if set, must be valid)
// 2. Config file app.mode
// 3. Default: shadow
//
// LIVE mode requires BOTSURV_MODE=LIVE AND LIVE_CONFIRMED=yes in env.
// If BOTSURV_MODE is LIVE without LIVE_CONFIRMED=yes, startup is refused.
func ResolveMode(configMode string) (BotMode, error) {
	envMode := os.Getenv("BOTSURV_MODE")
	if envMode != "" {
		mode := BotMode(strings.ToLower(envMode))
		if !mode.IsValid() {
			return "", fmt.Errorf("BOTSURV_MODE=%q is invalid; must be one of: shadow, paper, live", envMode)
		}
		if mode == ModeLive {
			if strings.ToLower(os.Getenv("LIVE_CONFIRMED")) != "yes" {
				return "", fmt.Errorf("BOTSURV_MODE=live requires LIVE_CONFIRMED=yes in environment")
			}
		}
		return mode, nil
	}

	// Fall back to config file value
	if configMode != "" {
		mode := BotMode(strings.ToLower(configMode))
		if !mode.IsValid() {
			return "", fmt.Errorf("app.mode=%q is invalid; must be one of: shadow, paper, live", configMode)
		}
		if mode == ModeLive {
			return "", fmt.Errorf("live mode requires BOTSURV_MODE=live and LIVE_CONFIRMED=yes in environment (not config file alone)")
		}
		return mode, nil
	}

	// Default
	return ModeShadow, nil
}

// IsLive returns true if mode is live.
func (m BotMode) IsLive() bool { return m == ModeLive }

// IsPaper returns true if mode is paper.
func (m BotMode) IsPaper() bool { return m == ModePaper }

// IsShadow returns true if mode is shadow.
func (m BotMode) IsShadow() bool { return m == ModeShadow }

// String returns the string representation.
func (m BotMode) String() string { return string(m) }
