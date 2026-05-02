package app

import (
	"os"
	"testing"
)

func TestResolveMode_DefaultShadow(t *testing.T) {
	os.Unsetenv("BOTSURV_MODE")
	os.Unsetenv("LIVE_CONFIRMED")

	mode, err := ResolveMode("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != ModeShadow {
		t.Errorf("expected shadow, got %s", mode)
	}
}

func TestResolveMode_FromEnv(t *testing.T) {
	t.Setenv("BOTSURV_MODE", "paper")
	os.Unsetenv("LIVE_CONFIRMED")

	mode, err := ResolveMode("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != ModePaper {
		t.Errorf("expected paper, got %s", mode)
	}
}

func TestResolveMode_EnvUpperCase(t *testing.T) {
	t.Setenv("BOTSURV_MODE", "PAPER")
	os.Unsetenv("LIVE_CONFIRMED")

	mode, err := ResolveMode("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != ModePaper {
		t.Errorf("expected paper, got %s", mode)
	}
}

func TestResolveMode_ShadowFromEnv(t *testing.T) {
	t.Setenv("BOTSURV_MODE", "shadow")
	os.Unsetenv("LIVE_CONFIRMED")

	mode, err := ResolveMode("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != ModeShadow {
		t.Errorf("expected shadow, got %s", mode)
	}
}

func TestResolveMode_LiveRequiresConfirmation(t *testing.T) {
	t.Setenv("BOTSURV_MODE", "live")
	os.Unsetenv("LIVE_CONFIRMED")

	_, err := ResolveMode("")
	if err == nil {
		t.Fatal("expected error for live mode without LIVE_CONFIRMED=yes")
	}
}

func TestResolveMode_LiveWithConfirmation(t *testing.T) {
	t.Setenv("BOTSURV_MODE", "live")
	t.Setenv("LIVE_CONFIRMED", "yes")

	mode, err := ResolveMode("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != ModeLive {
		t.Errorf("expected live, got %s", mode)
	}
}

func TestResolveMode_LiveConfirmationCaseInsensitive(t *testing.T) {
	t.Setenv("BOTSURV_MODE", "live")
	t.Setenv("LIVE_CONFIRMED", "YES")

	mode, err := ResolveMode("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != ModeLive {
		t.Errorf("expected live, got %s", mode)
	}
}

func TestResolveMode_LiveFromConfigRejected(t *testing.T) {
	os.Unsetenv("BOTSURV_MODE")
	os.Unsetenv("LIVE_CONFIRMED")

	// Config file alone cannot enable live mode
	_, err := ResolveMode("live")
	if err == nil {
		t.Fatal("expected error for live mode via config only")
	}
}

func TestResolveMode_InvalidEnv(t *testing.T) {
	t.Setenv("BOTSURV_MODE", "production")

	_, err := ResolveMode("")
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestResolveMode_FromConfig(t *testing.T) {
	os.Unsetenv("BOTSURV_MODE")
	os.Unsetenv("LIVE_CONFIRMED")

	mode, err := ResolveMode("paper")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != ModePaper {
		t.Errorf("expected paper, got %s", mode)
	}
}

func TestResolveMode_EnvOverridesConfig(t *testing.T) {
	t.Setenv("BOTSURV_MODE", "shadow")
	os.Unsetenv("LIVE_CONFIRMED")

	// Env should override config
	mode, err := ResolveMode("paper")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != ModeShadow {
		t.Errorf("expected shadow (env override), got %s", mode)
	}
}

func TestBotMode_IsMethods(t *testing.T) {
	if !ModeShadow.IsShadow() {
		t.Error("shadow should be shadow")
	}
	if ModeShadow.IsPaper() || ModeShadow.IsLive() {
		t.Error("shadow should not be paper or live")
	}
	if !ModePaper.IsPaper() {
		t.Error("paper should be paper")
	}
	if ModePaper.IsShadow() || ModePaper.IsLive() {
		t.Error("paper should not be shadow or live")
	}
	if !ModeLive.IsLive() {
		t.Error("live should be live")
	}
	if ModeLive.IsShadow() || ModeLive.IsPaper() {
		t.Error("live should not be shadow or paper")
	}
}

func TestBotMode_String(t *testing.T) {
	if ModeShadow.String() != "shadow" {
		t.Errorf("expected 'shadow', got '%s'", ModeShadow.String())
	}
	if ModePaper.String() != "paper" {
		t.Errorf("expected 'paper', got '%s'", ModePaper.String())
	}
	if ModeLive.String() != "live" {
		t.Errorf("expected 'live', got '%s'", ModeLive.String())
	}
}
