package indicator

import (
	"math"
	"testing"
)

func TestComputeMACD_KnownOutput_Flat(t *testing.T) {
	candles := closeSeries(make([]float64, 60))
	for i := range candles {
		candles[i].Open = 100
		candles[i].High = 100
		candles[i].Low = 100
		candles[i].Close = 100
	}

	res, err := ComputeMACD(candles, 12, 26, 9)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(res.Line) > 1e-9 || math.Abs(res.Signal) > 1e-9 || math.Abs(res.Histogram) > 1e-9 {
		t.Fatalf("expected zeros for flat series, got line=%.8f signal=%.8f hist=%.8f", res.Line, res.Signal, res.Histogram)
	}
}

func TestComputeMACD_EdgeCases(t *testing.T) {
	ascending := closeSeries([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36})
	res, err := ComputeMACD(ascending, 12, 26, 9)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.IsNaN(res.Histogram) || math.IsInf(res.Histogram, 0) {
		t.Fatal("expected finite histogram")
	}

	_, err = ComputeMACD(ascending, 12, 26, 20)
	if err == nil {
		t.Fatal("expected insufficient candles error")
	}
}
