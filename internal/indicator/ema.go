package indicator

import (
	"fmt"

	"github.com/virhan/botsurv/internal/domain"
)

// EMAResult is the result of an EMA calculation.
type EMAResult struct {
	Period int
	Value  float64
}

// ComputeEMA computes the last exponential moving average value for period.
func ComputeEMA(candles []domain.Candle, period int) (EMAResult, error) {
	if period <= 0 {
		return EMAResult{}, fmt.Errorf("ema period must be > 0")
	}
	if len(candles) < period {
		return EMAResult{}, fmt.Errorf("insufficient candles for ema%d: have %d need %d", period, len(candles), period)
	}
	if err := validateCandles(candles); err != nil {
		return EMAResult{}, err
	}

	var sum float64
	for i := 0; i < period; i++ {
		sum += candles[i].Close
	}
	ema := sum / float64(period)
	mult := 2.0 / float64(period+1)
	for i := period; i < len(candles); i++ {
		ema = (candles[i].Close-ema)*mult + ema
	}
	return EMAResult{Period: period, Value: ema}, nil
}
