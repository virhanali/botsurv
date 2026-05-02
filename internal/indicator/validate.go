package indicator

import (
	"fmt"
	"math"

	"github.com/virhan/botsurv/internal/domain"
)

func validateCandles(candles []domain.Candle) error {
	if len(candles) == 0 {
		return fmt.Errorf("empty candle input")
	}
	prev := candles[0].OpenTime
	for i, c := range candles {
		if invalid(c.Open) || invalid(c.High) || invalid(c.Low) || invalid(c.Close) || invalid(c.Volume) {
			return fmt.Errorf("invalid candle values at index=%d", i)
		}
		if i > 0 && c.OpenTime <= prev {
			return fmt.Errorf("non-monotonic candle timestamps at index=%d", i)
		}
		prev = c.OpenTime
	}
	return nil
}

func invalid(v float64) bool {
	return math.IsNaN(v) || math.IsInf(v, 0) || v <= 0
}
