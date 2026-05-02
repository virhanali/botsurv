package indicator

import (
	"fmt"

	"github.com/virhan/botsurv/internal/domain"
)

// VolumeMAResult contains volume moving average and ratio.
type VolumeMAResult struct {
	Period        int
	CurrentVolume float64
	MA            float64
	Ratio         float64
}

// ComputeVolumeMA computes simple moving average of volume and latest ratio.
func ComputeVolumeMA(candles []domain.Candle, period int) (VolumeMAResult, error) {
	if period <= 0 {
		return VolumeMAResult{}, fmt.Errorf("volume ma period must be > 0")
	}
	if len(candles) < period {
		return VolumeMAResult{}, fmt.Errorf("insufficient candles for volume ma%d: have %d need %d", period, len(candles), period)
	}
	if err := validateCandles(candles); err != nil {
		return VolumeMAResult{}, err
	}

	start := len(candles) - period
	sum := 0.0
	for i := start; i < len(candles); i++ {
		sum += candles[i].Volume
	}
	ma := sum / float64(period)
	current := candles[len(candles)-1].Volume
	ratio := 0.0
	if ma > 0 {
		ratio = current / ma
	}
	return VolumeMAResult{Period: period, CurrentVolume: current, MA: ma, Ratio: ratio}, nil
}
