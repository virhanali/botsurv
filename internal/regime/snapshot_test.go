package regime

import (
	"strings"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
)

func TestBuildSnapshot_FullFixture(t *testing.T) {
	btc5m := twoCloseCandles(100, 97)
	btc15m := twoCloseCandles(100, 96.5)
	btc1h := linearCandles(30, 100, 100)
	target1h := linearCandles(30, 100, 103)

	btc1hSnap := indicator.IndicatorSnapshot{
		Symbol:         "BTCUSDT",
		LastClosePrice: 100,
		ATR14:          2,
		EMAAlignment: indicator.EMAlignment{
			PriceAboveEMA50: false,
			EMA20AboveEMA50: false,
		},
	}
	btc4hSnap := indicator.IndicatorSnapshot{
		Symbol:           "BTCUSDT",
		LastClosePrice:   100,
		ATR14:            2,
		ResistanceLevels: []float64{101.5},
		SupportLevels:    []float64{98.5},
		EMAAlignment: indicator.EMAlignment{
			PriceAboveEMA50: false,
			EMA20AboveEMA50: false,
		},
	}

	btcd1hShort := twoCloseCandles(50, 50.6)
	btcd1hSnap := &indicator.IndicatorSnapshot{
		Symbol: "BTCD",
		EMAAlignment: indicator.EMAlignment{
			PriceAboveEMA50: false,
			EMA20AboveEMA50: false,
		},
	}
	btcd4hSnap := &indicator.IndicatorSnapshot{
		Symbol: "BTCD",
		EMAAlignment: indicator.EMAlignment{
			PriceAboveEMA50: false,
			EMA20AboveEMA50: false,
		},
	}

	snap, err := BuildSnapshot(SnapshotInput{
		Now:             time.Now().UTC(),
		BTC5mCandles:    btc5m,
		BTC15mCandles:   btc15m,
		BTC1hCandles:    btc1h,
		BTC1hSnapshot:   btc1hSnap,
		BTC4hSnapshot:   btc4hSnap,
		Target1hCandles: target1h,
		BTCD1hCandles:   btcd1hShort,
		BTCD1hSnapshot:  btcd1hSnap,
		BTCD4hSnapshot:  btcd4hSnap,
		Config: Config{
			BTCDumpShortThresholdPct:   -2.5,
			BTCDumpMediumThresholdPct:  -3.0,
			BTCNearLevelATRBuffer:      1.0,
			BTCDRisingFastThresholdPct: 1.0,
			RelativeStrength:           DefaultRelativeStrengthConfig(),
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Timestamp.IsZero() {
		t.Fatal("expected timestamp")
	}
	if len(snap.BTCFiltersDetail) == 0 {
		t.Fatal("expected btc filters detail populated")
	}
	if len(snap.BTCFiltersTriggered) == 0 {
		t.Fatal("expected at least one btc trigger")
	}
	if !snap.BTCDAvailable {
		t.Fatal("expected btcd available")
	}
	if snap.RelativeStrength.Classification == "" {
		t.Fatal("expected relative strength classification")
	}
}

func TestBuildSnapshot_BTCDUnavailableDoesNotError(t *testing.T) {
	btc := linearCandles(30, 100, 100)
	target := linearCandles(30, 100, 101)
	btc1hSnap := indicator.IndicatorSnapshot{Symbol: "BTCUSDT", LastClosePrice: 100, ATR14: 2}
	btc4hSnap := indicator.IndicatorSnapshot{Symbol: "BTCUSDT", LastClosePrice: 100, ATR14: 2}

	snap, err := BuildSnapshot(SnapshotInput{
		Now:             time.Now().UTC(),
		BTC5mCandles:    twoCloseCandles(100, 99),
		BTC15mCandles:   twoCloseCandles(100, 99),
		BTC1hCandles:    btc,
		BTC1hSnapshot:   btc1hSnap,
		BTC4hSnapshot:   btc4hSnap,
		Target1hCandles: target,
		BTCD1hCandles:   nil,
		BTCD1hSnapshot:  nil,
		BTCD4hSnapshot:  nil,
		Config:          DefaultConfig(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.BTCDAvailable {
		t.Fatal("expected btcd unavailable")
	}
	if !strings.Contains(strings.Join(snap.Warnings, " | "), "btcd data unavailable") {
		t.Fatalf("expected btcd warning, got: %v", snap.Warnings)
	}
}

func TestBuildSnapshot_ErrorsWhenBTCUnavailable(t *testing.T) {
	_, err := BuildSnapshot(SnapshotInput{
		BTC5mCandles: []domain.Candle{},
	})
	if err == nil {
		t.Fatal("expected error when btc data unavailable")
	}
}
