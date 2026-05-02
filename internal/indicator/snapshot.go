package indicator

import (
	"fmt"
	"math"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

// SnapshotConfig controls indicator snapshot calculations.
type SnapshotConfig struct {
	EMA20Period              int
	EMA50Period              int
	EMA200Period             int
	RSIPeriod                int
	MACDFastPeriod           int
	MACDSlowPeriod           int
	MACDSignalPeriod         int
	ATRPeriod                int
	VolumeMAPeriod           int
	SwingLookback            int
	RecentSwingCount         int
	SupportResistanceATRTol  float64
}

// EMAlignment indicates EMA/price positioning.
type EMAlignment struct {
	EMA20AboveEMA50 bool `json:"ema20_above_ema50"`
	EMA50AboveEMA200 bool `json:"ema50_above_ema200"`
	PriceAboveEMA20 bool `json:"price_above_ema20"`
	PriceAboveEMA50 bool `json:"price_above_ema50"`
	PriceAboveEMA200 bool `json:"price_above_ema200"`
}

// MACDSnapshot represents MACD values.
type MACDSnapshot struct {
	Line               float64 `json:"line"`
	Signal             float64 `json:"signal"`
	Histogram          float64 `json:"histogram"`
	HistogramDirection string  `json:"histogram_direction"`
}

// IndicatorSnapshot is the full typed snapshot for downstream modules.
type IndicatorSnapshot struct {
	Symbol           string       `json:"symbol"`
	Timeframe        string       `json:"timeframe"`
	LastCloseTime    time.Time    `json:"last_close_time"`
	LastClosePrice   float64      `json:"last_close_price"`
	EMA20            float64      `json:"ema20"`
	EMA50            float64      `json:"ema50"`
	EMA200           float64      `json:"ema200"`
	EMAAlignment     EMAlignment  `json:"ema_alignment"`
	RSI14            float64      `json:"rsi14"`
	RSIState         string       `json:"rsi_state"`
	MACD             MACDSnapshot `json:"macd"`
	ATR14            float64      `json:"atr14"`
	ATRPct           float64      `json:"atr_pct"`
	VolumeCurrent    float64      `json:"volume_current"`
	VolumeMA20       float64      `json:"volume_ma20"`
	VolumeRatio      float64      `json:"volume_ratio"`
	RecentSwings     []SwingPoint `json:"recent_swings"`
	SupportLevels    []float64    `json:"support_levels"`
	ResistanceLevels []float64    `json:"resistance_levels"`
}

// DefaultSnapshotConfig returns default indicator snapshot configuration.
func DefaultSnapshotConfig() SnapshotConfig {
	return SnapshotConfig{
		EMA20Period:             20,
		EMA50Period:             50,
		EMA200Period:            200,
		RSIPeriod:               14,
		MACDFastPeriod:          12,
		MACDSlowPeriod:          26,
		MACDSignalPeriod:        9,
		ATRPeriod:               14,
		VolumeMAPeriod:          20,
		SwingLookback:           3,
		RecentSwingCount:        5,
		SupportResistanceATRTol: 1.0,
	}
}

// BuildSnapshot computes a full indicator snapshot from validated candles.
func BuildSnapshot(symbol, timeframe string, candles []domain.Candle, cfg SnapshotConfig) (IndicatorSnapshot, error) {
	if err := validateCandles(candles); err != nil {
		return IndicatorSnapshot{}, err
	}
	cfg = withDefaults(cfg)

	ema20, err := ComputeEMA(candles, cfg.EMA20Period)
	if err != nil {
		return IndicatorSnapshot{}, err
	}
	ema50, err := ComputeEMA(candles, cfg.EMA50Period)
	if err != nil {
		return IndicatorSnapshot{}, err
	}
	ema200, err := ComputeEMA(candles, cfg.EMA200Period)
	if err != nil {
		return IndicatorSnapshot{}, err
	}
	rsi14, err := ComputeRSI(candles, cfg.RSIPeriod)
	if err != nil {
		return IndicatorSnapshot{}, err
	}
	macd, err := ComputeMACD(candles, cfg.MACDFastPeriod, cfg.MACDSlowPeriod, cfg.MACDSignalPeriod)
	if err != nil {
		return IndicatorSnapshot{}, err
	}
	atr14, err := ComputeATR(candles, cfg.ATRPeriod)
	if err != nil {
		return IndicatorSnapshot{}, err
	}
	vol, err := ComputeVolumeMA(candles, cfg.VolumeMAPeriod)
	if err != nil {
		return IndicatorSnapshot{}, err
	}
	swings, err := DetectSwings(candles, cfg.SwingLookback, cfg.RecentSwingCount)
	if err != nil {
		return IndicatorSnapshot{}, err
	}

	last := candles[len(candles)-1]
	price := last.Close
	support, resistance := BuildSupportResistance(swings, price, atr14.Value, cfg.SupportResistanceATRTol)
	interval, err := inferTimeframeDuration(candles)
	if err != nil {
		return IndicatorSnapshot{}, err
	}

	snap := IndicatorSnapshot{
		Symbol:        symbol,
		Timeframe:     timeframe,
		LastCloseTime: time.UnixMilli(last.OpenTime).UTC().Add(interval),
		LastClosePrice: price,
		EMA20:         ema20.Value,
		EMA50:         ema50.Value,
		EMA200:        ema200.Value,
		EMAAlignment: EMAlignment{
			EMA20AboveEMA50:  ema20.Value > ema50.Value,
			EMA50AboveEMA200: ema50.Value > ema200.Value,
			PriceAboveEMA20:  price > ema20.Value,
			PriceAboveEMA50:  price > ema50.Value,
			PriceAboveEMA200: price > ema200.Value,
		},
		RSI14: rsi14.Value,
		RSIState: classifyRSI(rsi14.Value),
		MACD: MACDSnapshot{
			Line:               macd.Line,
			Signal:             macd.Signal,
			Histogram:          macd.Histogram,
			HistogramDirection: macd.HistogramDirection,
		},
		ATR14:            atr14.Value,
		ATRPct:           pct(atr14.Value, price),
		VolumeCurrent:    vol.CurrentVolume,
		VolumeMA20:       vol.MA,
		VolumeRatio:      vol.Ratio,
		RecentSwings:     swings,
		SupportLevels:    support,
		ResistanceLevels: resistance,
	}

	if err := validateSnapshotFinite(snap); err != nil {
		return IndicatorSnapshot{}, err
	}
	return snap, nil
}

func withDefaults(cfg SnapshotConfig) SnapshotConfig {
	d := DefaultSnapshotConfig()
	if cfg.EMA20Period <= 0 {
		cfg.EMA20Period = d.EMA20Period
	}
	if cfg.EMA50Period <= 0 {
		cfg.EMA50Period = d.EMA50Period
	}
	if cfg.EMA200Period <= 0 {
		cfg.EMA200Period = d.EMA200Period
	}
	if cfg.RSIPeriod <= 0 {
		cfg.RSIPeriod = d.RSIPeriod
	}
	if cfg.MACDFastPeriod <= 0 {
		cfg.MACDFastPeriod = d.MACDFastPeriod
	}
	if cfg.MACDSlowPeriod <= 0 {
		cfg.MACDSlowPeriod = d.MACDSlowPeriod
	}
	if cfg.MACDSignalPeriod <= 0 {
		cfg.MACDSignalPeriod = d.MACDSignalPeriod
	}
	if cfg.ATRPeriod <= 0 {
		cfg.ATRPeriod = d.ATRPeriod
	}
	if cfg.VolumeMAPeriod <= 0 {
		cfg.VolumeMAPeriod = d.VolumeMAPeriod
	}
	if cfg.SwingLookback <= 0 {
		cfg.SwingLookback = d.SwingLookback
	}
	if cfg.RecentSwingCount <= 0 {
		cfg.RecentSwingCount = d.RecentSwingCount
	}
	if cfg.SupportResistanceATRTol <= 0 {
		cfg.SupportResistanceATRTol = d.SupportResistanceATRTol
	}
	return cfg
}

func classifyRSI(v float64) string {
	switch {
	case v < 30:
		return "oversold"
	case v < 45:
		return "neutral_bearish"
	case v > 70:
		return "overbought"
	case v > 55:
		return "neutral_bullish"
	default:
		return "neutral"
	}
}

func pct(x, y float64) float64 {
	if y == 0 {
		return 0
	}
	return x / y * 100
}

func inferTimeframeDuration(candles []domain.Candle) (time.Duration, error) {
	if len(candles) < 2 {
		return 0, fmt.Errorf("insufficient candles to infer timeframe")
	}
	d := candles[len(candles)-1].OpenTime - candles[len(candles)-2].OpenTime
	if d <= 0 {
		return 0, fmt.Errorf("invalid candle spacing")
	}
	return time.Duration(d) * time.Millisecond, nil
}

func validateSnapshotFinite(s IndicatorSnapshot) error {
	values := []float64{
		s.LastClosePrice,
		s.EMA20, s.EMA50, s.EMA200, s.RSI14,
		s.MACD.Line, s.MACD.Signal, s.MACD.Histogram,
		s.ATR14, s.ATRPct, s.VolumeCurrent, s.VolumeMA20, s.VolumeRatio,
	}
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("snapshot contains NaN/Inf")
		}
	}
	return nil
}
