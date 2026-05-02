package regime

import (
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
)

func TestBTCDumpedRecentlyShort(t *testing.T) {
	pos := BTCDumpedRecentlyShort(twoCloseCandles(100, 97), -2.0)
	if !pos.Triggered {
		t.Fatal("expected short dump trigger")
	}

	neg := BTCDumpedRecentlyShort(twoCloseCandles(100, 99.5), -2.0)
	if neg.Triggered {
		t.Fatal("expected no short dump trigger")
	}

	edge := BTCDumpedRecentlyShort(twoCloseCandles(100, 98), -2.0)
	if !edge.Triggered {
		t.Fatal("expected short dump trigger at exact threshold")
	}
}

func TestBTCDumpedRecentlyMedium(t *testing.T) {
	pos := BTCDumpedRecentlyMedium(twoCloseCandles(100, 96.5), -3.0)
	if !pos.Triggered {
		t.Fatal("expected medium dump trigger")
	}

	neg := BTCDumpedRecentlyMedium(twoCloseCandles(100, 98.5), -3.0)
	if neg.Triggered {
		t.Fatal("expected no medium dump trigger")
	}

	edge := BTCDumpedRecentlyMedium(twoCloseCandles(100, 97), -3.0)
	if !edge.Triggered {
		t.Fatal("expected medium dump trigger at exact threshold")
	}
}

func TestBTCStronglyBearishHTF(t *testing.T) {
	bear := snapshotWithAlignment(100, false, false, nil, nil, 2)
	bull := snapshotWithAlignment(100, true, true, nil, nil, 2)

	pos := BTCStronglyBearishHTF(bear, bear)
	if !pos.Triggered {
		t.Fatal("expected bearish HTF trigger")
	}

	neg := BTCStronglyBearishHTF(bear, bull)
	if neg.Triggered {
		t.Fatal("expected no bearish HTF trigger")
	}

	edge := BTCStronglyBearishHTF(snapshotWithAlignment(100, false, true, nil, nil, 2), bear)
	if edge.Triggered {
		t.Fatal("expected no trigger when one timeframe lacks bearish alignment")
	}
}

func TestBTCStronglyBullishHTF(t *testing.T) {
	bull := snapshotWithAlignment(100, true, true, nil, nil, 2)
	bear := snapshotWithAlignment(100, false, false, nil, nil, 2)

	pos := BTCStronglyBullishHTF(bull, bull)
	if !pos.Triggered {
		t.Fatal("expected bullish HTF trigger")
	}

	neg := BTCStronglyBullishHTF(bull, bear)
	if neg.Triggered {
		t.Fatal("expected no bullish HTF trigger")
	}

	edge := BTCStronglyBullishHTF(snapshotWithAlignment(100, true, false, nil, nil, 2), bull)
	if edge.Triggered {
		t.Fatal("expected no trigger when one timeframe lacks bullish alignment")
	}
}

func TestBTCNearMajorResistance(t *testing.T) {
	snap := snapshotWithAlignment(100, true, true, nil, []float64{101.5}, 2)
	pos := BTCNearMajorResistance(snap, 1.0)
	if !pos.Triggered {
		t.Fatal("expected near resistance trigger")
	}

	snapFar := snapshotWithAlignment(100, true, true, nil, []float64{110}, 2)
	neg := BTCNearMajorResistance(snapFar, 1.0)
	if neg.Triggered {
		t.Fatal("expected no near resistance trigger")
	}

	snapEdge := snapshotWithAlignment(100, true, true, nil, []float64{102}, 2)
	edge := BTCNearMajorResistance(snapEdge, 1.0)
	if !edge.Triggered {
		t.Fatal("expected near resistance trigger at exact ATR buffer")
	}
}

func TestBTCNearMajorSupport(t *testing.T) {
	snap := snapshotWithAlignment(100, true, true, []float64{98.5}, nil, 2)
	pos := BTCNearMajorSupport(snap, 1.0)
	if !pos.Triggered {
		t.Fatal("expected near support trigger")
	}

	snapFar := snapshotWithAlignment(100, true, true, []float64{90}, nil, 2)
	neg := BTCNearMajorSupport(snapFar, 1.0)
	if neg.Triggered {
		t.Fatal("expected no near support trigger")
	}

	snapEdge := snapshotWithAlignment(100, true, true, []float64{98}, nil, 2)
	edge := BTCNearMajorSupport(snapEdge, 1.0)
	if !edge.Triggered {
		t.Fatal("expected near support trigger at exact ATR buffer")
	}
}

func TestBTCDominanceRisingFast(t *testing.T) {
	pos := BTCDominanceRisingFast(twoCloseCandles(50, 50.5), 0.8)
	if !pos.Triggered {
		t.Fatal("expected btcd dominance rising fast trigger")
	}

	neg := BTCDominanceRisingFast(twoCloseCandles(50, 50.2), 0.8)
	if neg.Triggered {
		t.Fatal("expected no btcd dominance rising fast trigger")
	}

	edgeCandles := twoCloseCandles(100, 100.8)
	edgeThreshold := (edgeCandles[1].Close - edgeCandles[0].Close) / edgeCandles[0].Close * 100.0
	edge := BTCDominanceRisingFast(edgeCandles, edgeThreshold)
	if !edge.Triggered {
		t.Fatal("expected trigger at exact btcd threshold")
	}

	na := BTCDominanceRisingFast(nil, 0.8)
	if na.Severity != "n/a" || na.Triggered {
		t.Fatalf("expected n/a path, got %+v", na)
	}
}

func TestBTCDominanceFalling(t *testing.T) {
	fall := snapshotWithAlignment(100, false, false, nil, nil, 2)
	notFall := snapshotWithAlignment(100, true, true, nil, nil, 2)

	pos := BTCDominanceFalling(&fall, &fall)
	if !pos.Triggered {
		t.Fatal("expected btcd dominance falling trigger")
	}

	neg := BTCDominanceFalling(&fall, &notFall)
	if neg.Triggered {
		t.Fatal("expected no btcd dominance falling trigger")
	}

	edge := BTCDominanceFalling(nil, nil)
	if edge.Severity != "n/a" || edge.Triggered {
		t.Fatalf("expected n/a path, got %+v", edge)
	}
}

func twoCloseCandles(start, end float64) []domain.Candle {
	now := time.Now().UTC().Add(-time.Hour)
	return []domain.Candle{
		{OpenTime: now.UnixMilli(), Open: start, High: start, Low: start, Close: start, Volume: 1, Confirmed: true},
		{OpenTime: now.Add(time.Hour).UnixMilli(), Open: end, High: end, Low: end, Close: end, Volume: 1, Confirmed: true},
	}
}

func snapshotWithAlignment(price float64, priceAbove50, ema20Above50 bool, support, resistance []float64, atr float64) indicator.IndicatorSnapshot {
	return indicator.IndicatorSnapshot{
		LastClosePrice:   price,
		ATR14:            atr,
		SupportLevels:    support,
		ResistanceLevels: resistance,
		EMAAlignment: indicator.EMAlignment{
			PriceAboveEMA50: priceAbove50,
			EMA20AboveEMA50: ema20Above50,
		},
	}
}
