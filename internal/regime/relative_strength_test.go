package regime

import (
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

func TestComputeRelativeStrength_Classifications(t *testing.T) {
	tests := []struct {
		name      string
		targetEnd float64
		wantClass string
	}{
		{name: "strong_outperform", targetEnd: 115, wantClass: "strong_outperform"},
		{name: "outperform", targetEnd: 104, wantClass: "outperform"},
		{name: "neutral", targetEnd: 101, wantClass: "neutral"},
		{name: "underperform", targetEnd: 95, wantClass: "underperform"},
		{name: "strong_underperform", targetEnd: 85, wantClass: "strong_underperform"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := linearCandles(25, 100, tc.targetEnd)
			btc := linearCandles(25, 100, 100)
			rs, err := ComputeRelativeStrength(target, btc, DefaultRelativeStrengthConfig())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rs.Classification != tc.wantClass {
				t.Fatalf("classification mismatch: want=%s got=%s (rs4h=%.4f rs24h=%.4f smoothed=%.4f)", tc.wantClass, rs.Classification, rs.RS4H, rs.RS24H, rs.RSSmoothed)
			}
		})
	}
}

func TestComputeRelativeStrength_ErrorsOnInsufficientAlignedData(t *testing.T) {
	target := linearCandles(20, 100, 110)
	btc := linearCandles(20, 100, 100)
	_, err := ComputeRelativeStrength(target, btc, DefaultRelativeStrengthConfig())
	if err == nil {
		t.Fatal("expected insufficient aligned candles error")
	}
}

func linearCandles(n int, start, end float64) []domain.Candle {
	out := make([]domain.Candle, n)
	base := time.Now().UTC().Add(-time.Duration(n) * time.Hour)
	step := 0.0
	if n > 1 {
		step = (end - start) / float64(n-1)
	}
	for i := 0; i < n; i++ {
		closeV := start + float64(i)*step
		ts := base.Add(time.Duration(i) * time.Hour)
		out[i] = domain.Candle{
			OpenTime:  ts.UnixMilli(),
			Open:      closeV,
			High:      closeV,
			Low:       closeV,
			Close:     closeV,
			Volume:    1,
			Confirmed: true,
		}
	}
	return out
}
