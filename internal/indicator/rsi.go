package indicator

import (
	"fmt"

	"github.com/virhan/botsurv/internal/domain"
)

// RSIResult represents RSI output.
type RSIResult struct {
	Period int
	Value  float64
}

// ComputeRSI computes RSI using Wilder smoothing.
func ComputeRSI(candles []domain.Candle, period int) (RSIResult, error) {
	if period <= 0 {
		return RSIResult{}, fmt.Errorf("rsi period must be > 0")
	}
	if len(candles) < period+1 {
		return RSIResult{}, fmt.Errorf("insufficient candles for rsi%d: have %d need %d", period, len(candles), period+1)
	}
	if err := validateCandles(candles); err != nil {
		return RSIResult{}, err
	}

	start := len(candles) - period - 1
	avgGain := 0.0
	avgLoss := 0.0
	for i := start + 1; i <= start+period; i++ {
		delta := candles[i].Close - candles[i-1].Close
		if delta > 0 {
			avgGain += delta
		} else {
			avgLoss += -delta
		}
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)

	for i := start + period + 1; i < len(candles); i++ {
		delta := candles[i].Close - candles[i-1].Close
		gain := 0.0
		loss := 0.0
		if delta > 0 {
			gain = delta
		} else {
			loss = -delta
		}
		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
	}

	if avgLoss == 0 {
		if avgGain == 0 {
			return RSIResult{Period: period, Value: 50}, nil
		}
		return RSIResult{Period: period, Value: 100}, nil
	}
	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))
	return RSIResult{Period: period, Value: rsi}, nil
}
