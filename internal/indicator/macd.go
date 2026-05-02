package indicator

import (
	"fmt"

	"github.com/virhan/botsurv/internal/domain"
)

// MACDResult contains macd line/signal/histogram.
type MACDResult struct {
	Line               float64
	Signal             float64
	Histogram          float64
	HistogramDirection string
}

// ComputeMACD computes MACD(12,26,9) style values for the latest candle.
func ComputeMACD(candles []domain.Candle, fastPeriod, slowPeriod, signalPeriod int) (MACDResult, error) {
	if fastPeriod <= 0 || slowPeriod <= 0 || signalPeriod <= 0 {
		return MACDResult{}, fmt.Errorf("macd periods must be > 0")
	}
	if fastPeriod >= slowPeriod {
		return MACDResult{}, fmt.Errorf("macd fast period must be < slow period")
	}
	if len(candles) < slowPeriod+signalPeriod {
		return MACDResult{}, fmt.Errorf("insufficient candles for macd: have %d need %d", len(candles), slowPeriod+signalPeriod)
	}
	if err := validateCandles(candles); err != nil {
		return MACDResult{}, err
	}

	closes := make([]float64, len(candles))
	for i := range candles {
		closes[i] = candles[i].Close
	}
	fast := emaSeries(closes, fastPeriod)
	slow := emaSeries(closes, slowPeriod)

	macdSeries := make([]float64, len(closes))
	for i := range closes {
		macdSeries[i] = fast[i] - slow[i]
	}
	signalSeries := emaSeries(macdSeries, signalPeriod)

	line := macdSeries[len(macdSeries)-1]
	signal := signalSeries[len(signalSeries)-1]
	hist := line - signal
	prevHist := macdSeries[len(macdSeries)-2] - signalSeries[len(signalSeries)-2]
	dir := "flat"
	if hist > prevHist {
		dir = "up"
	} else if hist < prevHist {
		dir = "down"
	}

	return MACDResult{
		Line:               line,
		Signal:             signal,
		Histogram:          hist,
		HistogramDirection: dir,
	}, nil
}

func emaSeries(values []float64, period int) []float64 {
	out := make([]float64, len(values))
	if len(values) == 0 {
		return out
	}
	if period <= 1 {
		copy(out, values)
		return out
	}

	sum := 0.0
	warm := period
	if warm > len(values) {
		warm = len(values)
	}
	for i := 0; i < warm; i++ {
		sum += values[i]
	}
	ema := sum / float64(warm)
	for i := 0; i < warm; i++ {
		out[i] = ema
	}
	m := 2.0 / float64(period+1)
	for i := warm; i < len(values); i++ {
		ema = (values[i]-ema)*m + ema
		out[i] = ema
	}
	return out
}
