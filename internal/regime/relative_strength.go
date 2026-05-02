package regime

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

// RelativeStrengthConfig controls relative-strength calculations.
type RelativeStrengthConfig struct {
	StrongOutperformPct   float64
	StrongUnderperformPct float64
	NeutralBandPct        float64
	SmoothedEMAPeriod     int
}

// RelativeStrengthSnapshot is target-vs-BTC relative strength.
type RelativeStrengthSnapshot struct {
	RS1H           float64 `json:"rs_1h"`
	RS4H           float64 `json:"rs_4h"`
	RS24H          float64 `json:"rs_24h"`
	RSSmoothed     float64 `json:"rs_smoothed"`
	Classification string  `json:"classification"`
}

// DefaultRelativeStrengthConfig returns phase-2 default thresholds.
func DefaultRelativeStrengthConfig() RelativeStrengthConfig {
	return RelativeStrengthConfig{
		StrongOutperformPct:   2.0,
		StrongUnderperformPct: -2.0,
		NeutralBandPct:        0.5,
		SmoothedEMAPeriod:     8,
	}
}

// ComputeRelativeStrength computes target-vs-BTC relative strength over 1h/4h/24h.
func ComputeRelativeStrength(targetCandles, btcCandles []domain.Candle, cfg RelativeStrengthConfig) (RelativeStrengthSnapshot, error) {
	cfg = withRelativeStrengthDefaults(cfg)
	pairs, err := alignCandles(targetCandles, btcCandles)
	if err != nil {
		return RelativeStrengthSnapshot{}, err
	}

	rs1h, err := deltaReturnPct(pairs, 1)
	if err != nil {
		return RelativeStrengthSnapshot{}, fmt.Errorf("rs_1h: %w", err)
	}
	rs4h, err := deltaReturnPct(pairs, 4)
	if err != nil {
		return RelativeStrengthSnapshot{}, fmt.Errorf("rs_4h: %w", err)
	}
	rs24h, err := deltaReturnPct(pairs, 24)
	if err != nil {
		return RelativeStrengthSnapshot{}, fmt.Errorf("rs_24h: %w", err)
	}

	series := make([]float64, 0, len(pairs)-1)
	for i := 1; i < len(pairs); i++ {
		tRet := pctChange(pairs[i-1].TargetClose, pairs[i].TargetClose)
		bRet := pctChange(pairs[i-1].BTCClose, pairs[i].BTCClose)
		series = append(series, tRet-bRet)
	}
	smoothed := emaLast(series, cfg.SmoothedEMAPeriod)

	s := RelativeStrengthSnapshot{
		RS1H:           rs1h,
		RS4H:           rs4h,
		RS24H:          rs24h,
		RSSmoothed:     smoothed,
		Classification: classifyRelativeStrength(rs4h, rs24h, smoothed, cfg),
	}
	if hasInvalidFloat([]float64{s.RS1H, s.RS4H, s.RS24H, s.RSSmoothed}) {
		return RelativeStrengthSnapshot{}, fmt.Errorf("relative strength contains NaN/Inf")
	}
	return s, nil
}

func withRelativeStrengthDefaults(cfg RelativeStrengthConfig) RelativeStrengthConfig {
	d := DefaultRelativeStrengthConfig()
	if cfg.StrongOutperformPct == 0 {
		cfg.StrongOutperformPct = d.StrongOutperformPct
	}
	if cfg.StrongUnderperformPct == 0 {
		cfg.StrongUnderperformPct = d.StrongUnderperformPct
	}
	if cfg.NeutralBandPct == 0 {
		cfg.NeutralBandPct = d.NeutralBandPct
	}
	if cfg.SmoothedEMAPeriod <= 0 {
		cfg.SmoothedEMAPeriod = d.SmoothedEMAPeriod
	}
	return cfg
}

func classifyRelativeStrength(rs4h, rs24h, smoothed float64, cfg RelativeStrengthConfig) string {
	switch {
	case rs4h > cfg.StrongOutperformPct && rs24h > 0:
		return "strong_outperform"
	case rs4h < cfg.StrongUnderperformPct && rs24h < 0:
		return "strong_underperform"
	case math.Abs(rs4h) <= cfg.NeutralBandPct:
		return "neutral"
	case rs4h > 0 && smoothed > 0:
		return "outperform"
	case rs4h < 0 && smoothed < 0:
		return "underperform"
	default:
		return "neutral"
	}
}

type candlePair struct {
	OpenTime    int64
	TargetClose float64
	BTCClose    float64
}

func alignCandles(targetCandles, btcCandles []domain.Candle) ([]candlePair, error) {
	if len(targetCandles) == 0 || len(btcCandles) == 0 {
		return nil, fmt.Errorf("target/btc candles required")
	}
	btcByTime := make(map[int64]float64, len(btcCandles))
	for _, c := range btcCandles {
		if c.OpenTime <= 0 || c.Close <= 0 || math.IsNaN(c.Close) || math.IsInf(c.Close, 0) {
			continue
		}
		btcByTime[timeBucket1h(c.OpenTime)] = c.Close
	}

	pairs := make([]candlePair, 0)
	for _, c := range targetCandles {
		if c.OpenTime <= 0 || c.Close <= 0 || math.IsNaN(c.Close) || math.IsInf(c.Close, 0) {
			continue
		}
		b, ok := btcByTime[timeBucket1h(c.OpenTime)]
		if !ok {
			continue
		}
		pairs = append(pairs, candlePair{OpenTime: c.OpenTime, TargetClose: c.Close, BTCClose: b})
	}

	sort.Slice(pairs, func(i, j int) bool { return pairs[i].OpenTime < pairs[j].OpenTime })
	if len(pairs) < 25 {
		return nil, fmt.Errorf("insufficient aligned candles: have %d need at least 25", len(pairs))
	}
	for i := 1; i < len(pairs); i++ {
		if pairs[i].OpenTime <= pairs[i-1].OpenTime {
			return nil, fmt.Errorf("non-monotonic aligned candles at index=%d", i)
		}
	}
	return pairs, nil
}

func timeBucket1h(openTimeMillis int64) int64 {
	return openTimeMillis / int64(time.Hour/time.Millisecond)
}

func deltaReturnPct(pairs []candlePair, hours int) (float64, error) {
	if hours <= 0 {
		return 0, fmt.Errorf("hours must be > 0")
	}
	if len(pairs) < hours+1 {
		return 0, fmt.Errorf("need %d aligned candles, have %d", hours+1, len(pairs))
	}
	start := pairs[len(pairs)-hours-1]
	end := pairs[len(pairs)-1]
	if start.TargetClose <= 0 || start.BTCClose <= 0 {
		return 0, fmt.Errorf("invalid start close")
	}
	tRet := pctChange(start.TargetClose, end.TargetClose)
	bRet := pctChange(start.BTCClose, end.BTCClose)
	return tRet - bRet, nil
}

func pctChange(start, end float64) float64 {
	if start == 0 {
		return 0
	}
	return (end - start) / start * 100.0
}

func emaLast(values []float64, period int) float64 {
	if len(values) == 0 {
		return 0
	}
	if period <= 1 {
		return values[len(values)-1]
	}
	warm := period
	if warm > len(values) {
		warm = len(values)
	}
	sum := 0.0
	for i := 0; i < warm; i++ {
		sum += values[i]
	}
	ema := sum / float64(warm)
	m := 2.0 / float64(period+1)
	for i := warm; i < len(values); i++ {
		ema = (values[i]-ema)*m + ema
	}
	return ema
}

func hasInvalidFloat(values []float64) bool {
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return true
		}
	}
	return false
}
