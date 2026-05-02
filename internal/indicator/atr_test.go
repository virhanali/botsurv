package indicator

import (
	"math"
	"testing"

	"github.com/virhan/botsurv/internal/domain"
)

func TestComputeATR_KnownOutput(t *testing.T) {
	candles := []domain.Candle{
		{OpenTime: 1, Open: 10, High: 11, Low: 9, Close: 10, Volume: 1},
		{OpenTime: 2, Open: 10, High: 12, Low: 10, Close: 11, Volume: 1},
		{OpenTime: 3, Open: 11, High: 13, Low: 11, Close: 12, Volume: 1},
		{OpenTime: 4, Open: 12, High: 14, Low: 12, Close: 13, Volume: 1},
	}
	res, err := ComputeATR(candles, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(res.Value-2.0) > 1e-9 {
		t.Fatalf("expected atr=2.0 got %.8f", res.Value)
	}
}

func TestComputeATR_EdgeCases(t *testing.T) {
	flat := closeSeries([]float64{10, 10, 10, 10, 10, 10})
	res, err := ComputeATR(flat, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Value != 0 {
		t.Fatalf("expected flat atr=0 got %.8f", res.Value)
	}

	_, err = ComputeATR(flat, 6)
	if err == nil {
		t.Fatal("expected insufficient candles error")
	}
}
