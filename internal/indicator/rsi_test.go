package indicator

import "testing"

func TestComputeRSI_KnownOutput_AllGains(t *testing.T) {
	candles := closeSeries([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	res, err := ComputeRSI(candles, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Value != 100 {
		t.Fatalf("expected rsi=100, got %.8f", res.Value)
	}
}

func TestComputeRSI_EdgeCases(t *testing.T) {
	flat := closeSeries([]float64{10, 10, 10, 10, 10, 10})
	res, err := ComputeRSI(flat, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Value != 50 {
		t.Fatalf("expected flat rsi=50, got %.8f", res.Value)
	}

	_, err = ComputeRSI(flat, 6)
	if err == nil {
		t.Fatal("expected insufficient candles error")
	}
}
