package app

import (
	"testing"
)

func TestMarketRegimeConfig_RangingDefaults(t *testing.T) {
	cfg := MarketRegimeConfig{}
	wd := cfg.WithDefaults()
	if wd.RangingATRMultiplier != 2.5 {
		t.Errorf("expected default RangingATRMultiplier 2.5, got %f", wd.RangingATRMultiplier)
	}
	if wd.RangingLookbackCandles != 16 {
		t.Errorf("expected default RangingLookbackCandles 16, got %d", wd.RangingLookbackCandles)
	}
}

func TestMarketRegimeConfig_RangingValidation(t *testing.T) {
	cfg := MarketRegimeConfig{
		RangingATRMultiplier:      -1,
		RangingLookbackCandles:    -5,
		BTCNearLevelATRBuffer:     1.0,
		BTCDRisingFastPct:         0.8,
		Btc5mStaleness:             Btc5mStalenessConfig{WarnCycles: 3, CriticalCycles: 10},
		RelativeStrength:           RelativeStrengthConfig{SmoothedEMAPeriod: 8},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for negative ranging params")
	}
}