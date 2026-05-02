package indicator

import (
	"fmt"
	"math"

	"github.com/virhan/botsurv/internal/domain"
)

// ATRResult is ATR output.
type ATRResult struct {
	Period int
	Value  float64
}

// ComputeATR computes average true range over period.
func ComputeATR(candles []domain.Candle, period int) (ATRResult, error) {
	if period <= 0 {
		return ATRResult{}, fmt.Errorf("atr period must be > 0")
	}
	if len(candles) < period+1 {
		return ATRResult{}, fmt.Errorf("insufficient candles for atr%d: have %d need %d", period, len(candles), period+1)
	}
	if err := validateCandles(candles); err != nil {
		return ATRResult{}, err
	}

	start := len(candles) - period
	sumTR := 0.0
	for i := start; i < len(candles); i++ {
		prevClose := candles[i-1].Close
		tr := trueRange(candles[i], prevClose)
		sumTR += tr
	}
	return ATRResult{Period: period, Value: sumTR / float64(period)}, nil
}

func trueRange(c domain.Candle, prevClose float64) float64 {
	tr := c.High - c.Low
	if v := math.Abs(c.High - prevClose); v > tr {
		tr = v
	}
	if v := math.Abs(c.Low - prevClose); v > tr {
		tr = v
	}
	return tr
}
