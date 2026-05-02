package indicator

import (
	"testing"
)

func TestDetectSwings_KnownOutput(t *testing.T) {
	candles := closeSeries([]float64{1, 2, 3, 2, 1, 2, 3, 2, 1, 2, 3})
	for i := range candles {
		candles[i].High = candles[i].Close
		candles[i].Low = candles[i].Close
	}
	swings, err := DetectSwings(candles, 1, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(swings) == 0 {
		t.Fatal("expected swings")
	}
}

func TestDetectSwings_EdgeCases(t *testing.T) {
	candles := closeSeries([]float64{5, 5, 5, 5, 5, 5, 5})
	swings, err := DetectSwings(candles, 2, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(swings) != 0 {
		t.Fatalf("expected no swings on flat series, got %d", len(swings))
	}
	_, err = DetectSwings(candles, 4, 5)
	if err == nil {
		t.Fatal("expected insufficient candles error")
	}
}

func TestBuildSupportResistance(t *testing.T) {
	swings := []SwingPoint{
		{Price: 95, Type: "low"},
		{Price: 96, Type: "low"},
		{Price: 105, Type: "high"},
		{Price: 106, Type: "high"},
	}
	s, r := BuildSupportResistance(swings, 100, 2, 1)
	if len(s) == 0 || len(r) == 0 {
		t.Fatal("expected both support and resistance levels")
	}
}
