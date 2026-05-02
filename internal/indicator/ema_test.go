package indicator

import (
	"math"
	"testing"

	"github.com/virhan/botsurv/internal/domain"
)

func TestComputeEMA_KnownOutput(t *testing.T) {
	candles := closeSeries([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	res, err := ComputeEMA(candles, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(res.Value-9.0) > 1e-9 {
		t.Fatalf("expected ema=9, got %.10f", res.Value)
	}
}

func TestComputeEMA_EdgeCases(t *testing.T) {
	flat := closeSeries([]float64{5, 5, 5, 5, 5})
	res, err := ComputeEMA(flat, 3)
	if err != nil {
		t.Fatalf("unexpected flat error: %v", err)
	}
	if res.Value != 5 {
		t.Fatalf("expected flat ema=5, got %.8f", res.Value)
	}
	_, err = ComputeEMA(flat, 6)
	if err == nil {
		t.Fatal("expected insufficient candles error")
	}
}

func closeSeries(closes []float64) []domain.Candle {
	candles := make([]domain.Candle, len(closes))
	for i, c := range closes {
		candles[i] = domain.Candle{
			Symbol:   "X",
			OpenTime: int64(i + 1),
			Open:     c,
			High:     c,
			Low:      c,
			Close:    c,
			Volume:   1,
		}
	}
	return candles
}
