package indicator

import "testing"

func TestComputeVolumeMA_KnownOutput(t *testing.T) {
	candles := closeSeries([]float64{1, 2, 3})
	candles[0].Volume = 10
	candles[1].Volume = 20
	candles[2].Volume = 30
	res, err := ComputeVolumeMA(candles, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.MA != 20 {
		t.Fatalf("expected ma=20 got %.8f", res.MA)
	}
	if res.Ratio != 1.5 {
		t.Fatalf("expected ratio=1.5 got %.8f", res.Ratio)
	}
}

func TestComputeVolumeMA_EdgeCases(t *testing.T) {
	candles := closeSeries([]float64{1, 2, 3, 4, 5})
	for i := range candles {
		candles[i].Volume = 10
	}
	res, err := ComputeVolumeMA(candles, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.MA != 10 || res.Ratio != 1 {
		t.Fatalf("expected ma=10 ratio=1 got ma=%.8f ratio=%.8f", res.MA, res.Ratio)
	}
	_, err = ComputeVolumeMA(candles, 6)
	if err == nil {
		t.Fatal("expected insufficient candles error")
	}
}
