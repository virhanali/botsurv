package regime

import (
	"fmt"
	"time"

	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
)

// Config controls market regime filter thresholds.
type Config struct {
	BTCDumpShortThresholdPct   float64
	BTCDumpMediumThresholdPct  float64
	BTCNearLevelATRBuffer      float64
	BTCDRisingFastThresholdPct float64
	RelativeStrength           RelativeStrengthConfig
}

// MarketRegimeSnapshot is descriptive BTC/BTCD context for downstream modules.
type MarketRegimeSnapshot struct {
	Timestamp            time.Time                `json:"timestamp"`
	BTCFiltersTriggered  []string                 `json:"btc_filters_triggered"`
	BTCFiltersDetail     map[string]string        `json:"btc_filters_detail"`
	BTCDAvailable        bool                     `json:"btcd_available"`
	BTCDFiltersTriggered []string                 `json:"btcd_filters_triggered"`
	BTCDFiltersDetail    map[string]string        `json:"btcd_filters_detail"`
	RelativeStrength     RelativeStrengthSnapshot `json:"relative_strength"`
	Warnings             []string                 `json:"warnings"`
}

// SnapshotInput contains inputs needed to build a regime snapshot.
type SnapshotInput struct {
	Now time.Time

	BTC5mCandles  []domain.Candle
	BTC15mCandles []domain.Candle
	BTC1hCandles  []domain.Candle
	BTC1hSnapshot indicator.IndicatorSnapshot
	BTC4hSnapshot indicator.IndicatorSnapshot

	Target1hCandles []domain.Candle

	BTCD1hCandles  []domain.Candle
	BTCD1hSnapshot *indicator.IndicatorSnapshot
	BTCD4hSnapshot *indicator.IndicatorSnapshot

	Config Config
}

// DefaultConfig returns default market regime thresholds.
func DefaultConfig() Config {
	return Config{
		BTCDumpShortThresholdPct:   -2.5,
		BTCDumpMediumThresholdPct:  -3.0,
		BTCNearLevelATRBuffer:      1.0,
		BTCDRisingFastThresholdPct: 0.8,
		RelativeStrength:           DefaultRelativeStrengthConfig(),
	}
}

// BuildSnapshot computes the full market regime snapshot.
func BuildSnapshot(in SnapshotInput) (MarketRegimeSnapshot, error) {
	cfg := withDefaults(in.Config)
	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if len(in.BTC5mCandles) < 2 {
		return MarketRegimeSnapshot{}, fmt.Errorf("btc 5m candles unavailable")
	}
	if len(in.BTC15mCandles) < 2 {
		return MarketRegimeSnapshot{}, fmt.Errorf("btc 15m candles unavailable")
	}
	if len(in.BTC1hCandles) < 25 {
		return MarketRegimeSnapshot{}, fmt.Errorf("btc 1h candles unavailable for relative strength")
	}
	if len(in.Target1hCandles) < 25 {
		return MarketRegimeSnapshot{}, fmt.Errorf("target 1h candles unavailable for relative strength")
	}
	if in.BTC1hSnapshot.Symbol == "" || in.BTC4hSnapshot.Symbol == "" {
		return MarketRegimeSnapshot{}, fmt.Errorf("btc indicator snapshots unavailable")
	}

	rs, err := ComputeRelativeStrength(in.Target1hCandles, in.BTC1hCandles, cfg.RelativeStrength)
	if err != nil {
		return MarketRegimeSnapshot{}, fmt.Errorf("relative strength: %w", err)
	}

	out := MarketRegimeSnapshot{
		Timestamp:            now,
		BTCFiltersTriggered:  make([]string, 0),
		BTCFiltersDetail:     make(map[string]string),
		BTCDFiltersTriggered: make([]string, 0),
		BTCDFiltersDetail:    make(map[string]string),
		RelativeStrength:     rs,
	}

	btcFilters := map[string]FilterOutcome{
		"BTCDumpedRecentlyShort":  BTCDumpedRecentlyShort(in.BTC5mCandles, cfg.BTCDumpShortThresholdPct),
		"BTCDumpedRecentlyMedium": BTCDumpedRecentlyMedium(in.BTC15mCandles, cfg.BTCDumpMediumThresholdPct),
		"BTCStronglyBearishHTF":   BTCStronglyBearishHTF(in.BTC1hSnapshot, in.BTC4hSnapshot),
		"BTCStronglyBullishHTF":   BTCStronglyBullishHTF(in.BTC1hSnapshot, in.BTC4hSnapshot),
		"BTCNearMajorResistance":  BTCNearMajorResistance(in.BTC4hSnapshot, cfg.BTCNearLevelATRBuffer),
		"BTCNearMajorSupport":     BTCNearMajorSupport(in.BTC4hSnapshot, cfg.BTCNearLevelATRBuffer),
	}
	for name, res := range btcFilters {
		out.BTCFiltersDetail[name] = res.Detail
		if res.Triggered {
			out.BTCFiltersTriggered = append(out.BTCFiltersTriggered, name)
		}
	}

	btcdAvailable := len(in.BTCD1hCandles) >= 2 && in.BTCD1hSnapshot != nil && in.BTCD4hSnapshot != nil
	out.BTCDAvailable = btcdAvailable

	btcdFilters := map[string]FilterOutcome{
		"BTCDominanceRisingFast": BTCDominanceRisingFast(in.BTCD1hCandles, cfg.BTCDRisingFastThresholdPct),
		"BTCDominanceFalling":    BTCDominanceFalling(in.BTCD1hSnapshot, in.BTCD4hSnapshot),
	}
	for name, res := range btcdFilters {
		out.BTCDFiltersDetail[name] = res.Detail
		if res.Severity == "n/a" {
			out.Warnings = append(out.Warnings, name+": "+res.Detail)
		}
		if res.Triggered {
			out.BTCDFiltersTriggered = append(out.BTCDFiltersTriggered, name)
		}
	}
	if !btcdAvailable {
		out.Warnings = append(out.Warnings, "btcd data unavailable")
	}

	return out, nil
}

func withDefaults(cfg Config) Config {
	d := DefaultConfig()
	if cfg.BTCDumpShortThresholdPct == 0 {
		cfg.BTCDumpShortThresholdPct = d.BTCDumpShortThresholdPct
	}
	if cfg.BTCDumpMediumThresholdPct == 0 {
		cfg.BTCDumpMediumThresholdPct = d.BTCDumpMediumThresholdPct
	}
	if cfg.BTCNearLevelATRBuffer <= 0 {
		cfg.BTCNearLevelATRBuffer = d.BTCNearLevelATRBuffer
	}
	if cfg.BTCDRisingFastThresholdPct == 0 {
		cfg.BTCDRisingFastThresholdPct = d.BTCDRisingFastThresholdPct
	}
	cfg.RelativeStrength = withRelativeStrengthDefaults(cfg.RelativeStrength)
	return cfg
}
