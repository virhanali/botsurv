package scoring

import (
	"math"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
)

// WatchlistContext holds confluence context for LLM veto decisions.
// This is optional context only — it does not affect deterministic scoring.
type WatchlistContext struct {
	ChartQualityLabel       string                     `json:"chart_quality_label"`
	VolumeLiquidityLabel    string                     `json:"volume_liquidity_label"`
	NarrativeSector         string                     `json:"narrative_sector"`
	NarrativeActive         bool                       `json:"narrative_active"`
	MarketcapLiquidityClass string                     `json:"marketcap_liquidity_class"`
	Fibonacci               indicator.FibonacciContext `json:"fibonacci"`
	ConfluenceScoreDelta    float64                    `json:"confluence_score_delta"`
	ReasonCodes             []string                   `json:"reason_codes"`
}

// BuildWatchlistContext assembles a WatchlistContext from available data.
// The result is purely informational for LLM context — it never modifies
// deterministic scores, entry, SL, or TP.
func BuildWatchlistContext(
	fibCtx indicator.FibonacciContext,
	symbol string,
	side domain.Side,
	volumeRatio float64,
	atrPct float64,
	cfg app.WatchlistContextConfig,
) WatchlistContext {
	reasonCodes := make([]string, 0)

	chartQuality := computeChartQuality(fibCtx, atrPct)
	if chartQuality != "unknown" {
		reasonCodes = append(reasonCodes, "chart_quality:"+chartQuality)
	}

	volumeLabel := computeVolumeLiquidity(volumeRatio)
	if volumeLabel != "unknown" {
		reasonCodes = append(reasonCodes, "volume:"+volumeLabel)
	}

	narrativeSector := "unknown"
	narrativeActive := false
	if cfg.Narrative.SymbolSectors != nil {
		if s, ok := cfg.Narrative.SymbolSectors[symbol]; ok && s != "" {
			narrativeSector = s
			reasonCodes = append(reasonCodes, "sector:"+s)
		}
	}
	if narrativeSector != "unknown" && cfg.Narrative.ActiveSectors != nil {
		for _, active := range cfg.Narrative.ActiveSectors {
			if active == narrativeSector {
				narrativeActive = true
				reasonCodes = append(reasonCodes, "narrative_active:"+narrativeSector)
				break
			}
		}
	}

	marketcapClass := "unknown"
	if cfg.Marketcap.SymbolClasses != nil {
		if c, ok := cfg.Marketcap.SymbolClasses[symbol]; ok && c != "" {
			marketcapClass = c
			reasonCodes = append(reasonCodes, "marketcap:"+c)
		}
	}

	delta := computeConfluenceDelta(chartQuality, volumeLabel, narrativeActive, fibCtx, cfg)
	reasonCodes = append(reasonCodes, deltaReasonCodes(delta)...)

	return WatchlistContext{
		ChartQualityLabel:       chartQuality,
		VolumeLiquidityLabel:    volumeLabel,
		NarrativeSector:         narrativeSector,
		NarrativeActive:         narrativeActive,
		MarketcapLiquidityClass: marketcapClass,
		Fibonacci:               fibCtx,
		ConfluenceScoreDelta:    clamp(delta, -5, 5),
		ReasonCodes:             reasonCodes,
	}
}

func computeChartQuality(fibCtx indicator.FibonacciContext, atrPct float64) string {
	if !fibCtx.Valid {
		return "unknown"
	}
	if fibCtx.SwingHigh <= 0 || fibCtx.SwingLow <= 0 {
		return "unknown"
	}

	rangePct := (fibCtx.SwingHigh - fibCtx.SwingLow) / fibCtx.SwingLow * 100

	if atrPct <= 0 || rangePct <= 0 {
		return "unknown"
	}

	rangeToATR := rangePct / atrPct

	// chop: very tight range relative to ATR
	if rangeToATR < 2.0 {
		return "chop"
	}

	// extended: price is near the swing extreme, beyond 0.5 zone
	if fibCtx.ZoneLabel == "above_pullback_zone" {
		return "extended"
	}

	// healthy: price is in a pullback zone with reasonable range
	if rangeToATR >= 2.0 && rangeToATR <= 10.0 {
		return "healthy_range"
	}

	return "unknown"
}

func computeVolumeLiquidity(volumeRatio float64) string {
	if math.IsNaN(volumeRatio) || math.IsInf(volumeRatio, 0) || volumeRatio <= 0 {
		return "unknown"
	}
	switch {
	case volumeRatio >= 1.3:
		return "strong"
	case volumeRatio >= 1.0:
		return "normal"
	default:
		return "weak"
	}
}

func computeConfluenceDelta(
	chartQuality, volumeLabel string,
	narrativeActive bool,
	fibCtx indicator.FibonacciContext,
	cfg app.WatchlistContextConfig,
) float64 {
	if !cfg.Enabled {
		return 0
	}

	delta := 0.0

	// Fibonacci zone contribution
	if fibCtx.Valid && cfg.Fibonacci.Enabled {
		switch fibCtx.ZoneLabel {
		case "in_0_618_zone":
			delta += 2.0
		case "in_0_5_zone", "in_0_786_zone":
			delta += 1.0
		}
	}

	// Chart quality contribution
	switch chartQuality {
	case "healthy_range":
		delta += 0.5
	case "chop":
		delta -= 0.5
	case "extended":
		delta -= 1.0
	}

	// Volume contribution
	switch volumeLabel {
	case "strong":
		delta += 0.5
	case "weak":
		delta -= 0.5
	}

	// Narrative contribution
	if narrativeActive {
		delta += 1.0
	}

	// Apply scoring weight cap
	maxWeight := cfg.Fibonacci.ScoringWeight
	if maxWeight <= 0 {
		maxWeight = 2.0
	}
	if delta > maxWeight {
		delta = maxWeight
	}
	if delta < -maxWeight {
		delta = -maxWeight
	}

	return clamp(delta, -5, 5)
}

func deltaReasonCodes(delta float64) []string {
	if delta > 0 {
		return []string{"confluence_positive"}
	}
	if delta < 0 {
		return []string{"confluence_negative"}
	}
	return []string{"confluence_neutral"}
}
