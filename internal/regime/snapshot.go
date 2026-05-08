package regime

import (
	"fmt"
	"time"

	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
)

// Btc5mStalenessConfig controls BTC 5m staleness detection thresholds.
type Btc5mStalenessConfig struct {
	WarnCycles     int
	CriticalCycles int
}

// Config controls market regime filter thresholds.
type Config struct {
	BTCDumpShortThresholdPct   float64
	BTCDumpMediumThresholdPct  float64
	BTCNearLevelATRBuffer      float64
	BTCDRisingFastThresholdPct float64
	RelativeStrength           RelativeStrengthConfig
	Btc5mStaleness              Btc5mStalenessConfig
	RangingATRMultiplier       float64
	RangingLookbackCandles     int
	ReduceNewPositionsWhenRanging bool
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
	IsRanging            bool                     `json:"is_ranging"`
	RangingBandWidth    float64                  `json:"ranging_band_width"`
	BTCDataStale        bool                     `json:"btc_data_stale"`
	Warnings             []string                 `json:"warnings"`
	Btc5mStaleCounter    int                      `json:"btc_5m_stale_counter"`
}

// SnapshotInput contains inputs needed to build a regime snapshot.
type SnapshotInput struct {
	Now time.Time

	BTC5mCandles       []domain.Candle
	BTC15mCandles      []domain.Candle
	BTC1hCandles        []domain.Candle
	BTC1hSnapshot       indicator.IndicatorSnapshot
	BTC4hSnapshot       indicator.IndicatorSnapshot
	BTC5mStaleCounter   int

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
		RangingATRMultiplier:       2.5,
		RangingLookbackCandles:     16,
		ReduceNewPositionsWhenRanging: true,
	}
}

// BuildSnapshot computes the full market regime snapshot.
func BuildSnapshot(in SnapshotInput) (MarketRegimeSnapshot, error) {
	cfg := withDefaults(in.Config)
	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
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

	rangingResult := ComputeBTCRanging(in.BTC15mCandles, cfg.RangingATRMultiplier, cfg.RangingLookbackCandles)
	out.IsRanging = rangingResult.IsRanging
	out.RangingBandWidth = rangingResult.BandWidth
	out.BTCFiltersDetail["BTCRanging"] = rangingResult.Detail
	if rangingResult.IsRanging {
		out.BTCFiltersTriggered = append(out.BTCFiltersTriggered, "BTCRanging")
		out.Warnings = append(out.Warnings, "MARKET_RANGING")
	}
	if in.BTC5mStaleCounter > 0 {
		out.BTCDataStale = true
		if !containsWarning(out.Warnings, "BTC_DATA_STALE") {
			out.Warnings = append(out.Warnings, "BTC_DATA_STALE")
		}
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
	cfg.Btc5mStaleness = withBtc5mStalenessDefaults(cfg.Btc5mStaleness)
	if cfg.RangingATRMultiplier <= 0 {
		cfg.RangingATRMultiplier = 2.5
	}
	if cfg.RangingLookbackCandles <= 0 {
		cfg.RangingLookbackCandles = 16
	}
	return cfg
}

func containsWarning(warnings []string, prefix string) bool {
	for _, w := range warnings {
		if len(w) >= len(prefix) && w[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func withBtc5mStalenessDefaults(c Btc5mStalenessConfig) Btc5mStalenessConfig {
	out := c
	if out.WarnCycles <= 0 {
		out.WarnCycles = 3
	}
	if out.CriticalCycles <= 0 {
		out.CriticalCycles = 10
	}
	return out
}

// StalenessTracker tracks BTC 5m data staleness across cycles.
type StalenessTracker struct {
	counter int
	cfg     Btc5mStalenessConfig
}

// NewStalenessTracker creates a new BTC 5m staleness tracker.
func NewStalenessTracker(cfg Btc5mStalenessConfig) *StalenessTracker {
	warnCycles := cfg.WarnCycles
	if warnCycles <= 0 {
		warnCycles = 3
	}
	criticalCycles := cfg.CriticalCycles
	if criticalCycles <= 0 {
		criticalCycles = 10
	}
	return &StalenessTracker{
		cfg: Btc5mStalenessConfig{
			WarnCycles:     warnCycles,
			CriticalCycles: criticalCycles,
		},
	}
}

// RecordFailure increments the stale counter when BTC 5m data fails.
func (t *StalenessTracker) RecordFailure() int {
	t.counter++
	return t.counter
}

// RecordSuccess resets the stale counter when BTC 5m data succeeds.
func (t *StalenessTracker) RecordSuccess() {
	t.counter = 0
}

// Counter returns the current stale counter value.
func (t *StalenessTracker) Counter() int {
	return t.counter
}

// ShouldWarn returns true if the counter has reached the warning threshold.
func (t *StalenessTracker) ShouldWarn() bool {
	return t.counter >= t.cfg.WarnCycles
}

// ShouldCritical returns true if the counter has reached the critical threshold.
func (t *StalenessTracker) ShouldCritical() bool {
	return t.counter >= t.cfg.CriticalCycles
}

// InjectWarning adds a staleness warning to the snapshot if thresholds are met.
func (t *StalenessTracker) InjectWarning(snap *MarketRegimeSnapshot) {
	snap.Btc5mStaleCounter = t.counter
	if t.ShouldCritical() {
		snap.Warnings = append(snap.Warnings, fmt.Sprintf("BTC 5m data CRITICAL: %d consecutive stale cycles", t.counter))
	} else if t.ShouldWarn() {
		snap.Warnings = append(snap.Warnings, fmt.Sprintf("BTC 5m data stale: %d consecutive failures", t.counter))
	}
}
