package indicator

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

// SwingPoint represents a local high/low point.
type SwingPoint struct {
	Price     float64
	Timestamp time.Time
	Type      string // high | low
}

// SupportResistanceResult contains swing-derived S/R levels.
type SupportResistanceResult struct {
	RecentSwings []SwingPoint
	Support      []float64
	Resistance   []float64
}

// DetectSwings finds recent local highs/lows with lookback window.
func DetectSwings(candles []domain.Candle, lookback, recentCount int) ([]SwingPoint, error) {
	if lookback <= 0 {
		return nil, fmt.Errorf("swing lookback must be > 0")
	}
	if recentCount <= 0 {
		return nil, fmt.Errorf("swing recent count must be > 0")
	}
	if len(candles) < lookback*2+1 {
		return nil, fmt.Errorf("insufficient candles for swing detection: have %d need %d", len(candles), lookback*2+1)
	}
	if err := validateCandles(candles); err != nil {
		return nil, err
	}

	points := make([]SwingPoint, 0)
	for i := lookback; i < len(candles)-lookback; i++ {
		isHigh := true
		isLow := true
		for j := i - lookback; j <= i+lookback; j++ {
			if j == i {
				continue
			}
			if candles[j].High >= candles[i].High {
				isHigh = false
			}
			if candles[j].Low <= candles[i].Low {
				isLow = false
			}
			if !isHigh && !isLow {
				break
			}
		}
		ts := time.UnixMilli(candles[i].OpenTime).UTC()
		if isHigh {
			points = append(points, SwingPoint{Price: candles[i].High, Timestamp: ts, Type: "high"})
		}
		if isLow {
			points = append(points, SwingPoint{Price: candles[i].Low, Timestamp: ts, Type: "low"})
		}
	}
	if len(points) > recentCount {
		points = points[len(points)-recentCount:]
	}
	return points, nil
}

// BuildSupportResistance clusters swing points using ATR-based tolerance.
func BuildSupportResistance(swings []SwingPoint, lastPrice, atr, toleranceATR float64) (support []float64, resistance []float64) {
	if len(swings) == 0 || lastPrice <= 0 || atr <= 0 {
		return nil, nil
	}
	if toleranceATR <= 0 {
		toleranceATR = 1.0
	}
	tol := atr * toleranceATR
	if tol <= 0 {
		return nil, nil
	}

	below := make([]float64, 0)
	above := make([]float64, 0)
	for _, s := range swings {
		if s.Price <= 0 {
			continue
		}
		if s.Price <= lastPrice {
			below = append(below, s.Price)
		} else {
			above = append(above, s.Price)
		}
	}

	support = clusterLevels(below, tol)
	resistance = clusterLevels(above, tol)

	sort.Slice(support, func(i, j int) bool {
		return math.Abs(lastPrice-support[i]) < math.Abs(lastPrice-support[j])
	})
	sort.Slice(resistance, func(i, j int) bool {
		return math.Abs(lastPrice-resistance[i]) < math.Abs(lastPrice-resistance[j])
	})

	if len(support) > 3 {
		support = support[:3]
	}
	if len(resistance) > 3 {
		resistance = resistance[:3]
	}
	return support, resistance
}

func clusterLevels(levels []float64, tolerance float64) []float64 {
	if len(levels) == 0 {
		return nil
	}
	sorted := make([]float64, len(levels))
	copy(sorted, levels)
	sort.Float64s(sorted)

	type cluster struct {
		sum   float64
		count int
		last  float64
	}
	clusters := make([]cluster, 0)
	for _, level := range sorted {
		if len(clusters) == 0 {
			clusters = append(clusters, cluster{sum: level, count: 1, last: level})
			continue
		}
		curr := &clusters[len(clusters)-1]
		if math.Abs(level-curr.last) <= tolerance {
			curr.sum += level
			curr.count++
			curr.last = level
		} else {
			clusters = append(clusters, cluster{sum: level, count: 1, last: level})
		}
	}

	out := make([]float64, 0, len(clusters))
	for _, c := range clusters {
		out = append(out, c.sum/float64(c.count))
	}
	return out
}
