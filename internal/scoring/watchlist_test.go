package scoring

import (
	"testing"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
)

func defaultWatchlistCfg() app.WatchlistContextConfig {
	return app.WatchlistContextConfig{
		Enabled: true,
		Fibonacci: app.FibonacciConfig{
			Enabled:          true,
			LookbackCandles:  80,
			ZoneTolerancePct: 1.0,
			ScoringWeight:    2.0,
		},
	}
}

func TestWatchlistContext_UnknownConfig_NeutralDelta(t *testing.T) {
	cfg := app.WatchlistContextConfig{Enabled: false}
	fibCtx := indicator.FibonacciContext{Valid: false}

	wc := BuildWatchlistContext(fibCtx, "BTCUSDT", domain.SideLong, 0, 0, cfg)

	if wc.ConfluenceScoreDelta != 0 {
		t.Errorf("expected delta 0 for disabled config, got %.2f", wc.ConfluenceScoreDelta)
	}
	if wc.ChartQualityLabel != "unknown" {
		t.Errorf("expected chart_quality=unknown, got %s", wc.ChartQualityLabel)
	}
	if wc.VolumeLiquidityLabel != "unknown" {
		t.Errorf("expected volume=unknown, got %s", wc.VolumeLiquidityLabel)
	}
}

func TestWatchlistContext_NarrativeActiveSector_GivesSmallPositiveDelta(t *testing.T) {
	cfg := defaultWatchlistCfg()
	cfg.Narrative = app.NarrativeConfig{
		ActiveSectors: []string{"AI"},
		SymbolSectors: map[string]string{"BTCUSDT": "AI"},
	}

	fibCtx := indicator.FibonacciContext{
		Valid:        true,
		SwingHigh:    200,
		SwingLow:     100,
		ZoneLabel:    "in_0_618_zone",
		CurrentPrice: 161.8,
	}

	wc := BuildWatchlistContext(fibCtx, "BTCUSDT", domain.SideLong, 1.5, 1.0, cfg)

	if wc.NarrativeSector != "AI" {
		t.Errorf("expected narrative_sector=AI, got %s", wc.NarrativeSector)
	}
	if !wc.NarrativeActive {
		t.Error("expected narrative_active=true")
	}

	// Delta should be positive but capped by scoring_weight (2.0)
	if wc.ConfluenceScoreDelta <= 0 {
		t.Errorf("expected positive delta with active narrative and strong confluence, got %.2f", wc.ConfluenceScoreDelta)
	}
	if wc.ConfluenceScoreDelta > 5 {
		t.Errorf("expected delta capped at 5, got %.2f", wc.ConfluenceScoreDelta)
	}
}

func TestWatchlistContext_MarketcapClass_FromConfig(t *testing.T) {
	cfg := defaultWatchlistCfg()
	cfg.Marketcap = app.MarketcapConfig{
		SymbolClasses: map[string]string{"BTCUSDT": "large_liquid"},
	}

	wc := BuildWatchlistContext(indicator.FibonacciContext{}, "BTCUSDT", domain.SideLong, 0, 0, cfg)

	if wc.MarketcapLiquidityClass != "large_liquid" {
		t.Errorf("expected marketcap=large_liquid, got %s", wc.MarketcapLiquidityClass)
	}
}

func TestWatchlistContext_VolumeLabels(t *testing.T) {
	cfg := defaultWatchlistCfg()
	tests := []struct {
		ratio float64
		want  string
	}{
		{1.5, "strong"},
		{1.1, "normal"},
		{0.5, "weak"},
		{0, "unknown"},
	}
	for _, tt := range tests {
		wc := BuildWatchlistContext(indicator.FibonacciContext{}, "TEST", domain.SideLong, tt.ratio, 0, cfg)
		if wc.VolumeLiquidityLabel != tt.want {
			t.Errorf("volumeRatio=%.1f expected %s, got %s", tt.ratio, tt.want, wc.VolumeLiquidityLabel)
		}
	}
}

func TestWatchlistContext_DeltaCapped(t *testing.T) {
	cfg := defaultWatchlistCfg()
	cfg.Fibonacci.ScoringWeight = 1.0

	// Max confluence: healthy_range + in_0_618_zone + strong volume + active narrative
	cfg.Narrative = app.NarrativeConfig{
		ActiveSectors: []string{"DEFI"},
		SymbolSectors: map[string]string{"TEST": "DEFI"},
	}
	fibCtx := indicator.FibonacciContext{
		Valid:        true,
		SwingHigh:    200,
		SwingLow:     100,
		ZoneLabel:    "in_0_618_zone",
		CurrentPrice: 161.8,
	}

	wc := BuildWatchlistContext(fibCtx, "TEST", domain.SideLong, 2.0, 2.5, cfg)

	// Expected: fib_zone(2.0) + healthy_range(0.5) + strong_vol(0.5) + narrative(1.0) = 4.0
	// But scoring_weight caps at 1.0
	if wc.ConfluenceScoreDelta > 1.0 {
		t.Errorf("expected delta capped by scoring_weight=1.0, got %.2f", wc.ConfluenceScoreDelta)
	}
}

func TestWatchlistContext_NegativeDeltaForChopAndExtended(t *testing.T) {
	cfg := defaultWatchlistCfg()

	// "extended" chart quality (above_pullback_zone) should give negative delta
	fibCtx := indicator.FibonacciContext{
		Valid:        true,
		SwingHigh:    200,
		SwingLow:     100,
		ZoneLabel:    "above_pullback_zone",
		CurrentPrice: 195,
	}

	wc := BuildWatchlistContext(fibCtx, "TEST", domain.SideLong, 0.5, 0.5, cfg)

	if wc.ConfluenceScoreDelta > 0 {
		t.Errorf("expected negative or zero delta for extended zone, got %.2f", wc.ConfluenceScoreDelta)
	}
}

func TestWatchlistContext_IncludesReasonCodes(t *testing.T) {
	cfg := defaultWatchlistCfg()
	cfg.Narrative = app.NarrativeConfig{
		ActiveSectors: []string{"AI"},
		SymbolSectors: map[string]string{"BTCUSDT": "AI"},
	}
	cfg.Marketcap = app.MarketcapConfig{
		SymbolClasses: map[string]string{"BTCUSDT": "large_liquid"},
	}

	fibCtx := indicator.FibonacciContext{
		Valid:        true,
		SwingHigh:    200,
		SwingLow:     100,
		ZoneLabel:    "in_0_618_zone",
		CurrentPrice: 161.8,
	}

	wc := BuildWatchlistContext(fibCtx, "BTCUSDT", domain.SideLong, 1.5, 2.0, cfg)

	if len(wc.ReasonCodes) == 0 {
		t.Error("expected non-empty reason codes")
	}

	found := false
	for _, r := range wc.ReasonCodes {
		if r == "sector:AI" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected sector:AI in reason codes, got %v", wc.ReasonCodes)
	}
}

func TestWatchlistContext_FibNotValid_StillReturnsContext(t *testing.T) {
	cfg := defaultWatchlistCfg()

	wc := BuildWatchlistContext(indicator.FibonacciContext{Valid: false}, "TEST", domain.SideLong, 0, 0, cfg)

	// Should not crash, should return context with unknown/neutral values
	if wc.ChartQualityLabel != "unknown" {
		t.Errorf("expected chart_quality=unknown when fib invalid, got %s", wc.ChartQualityLabel)
	}
}
