package strategy

import (
	"math"

	"github.com/virhan/botsurv/internal/domain"
)

// ATR computes Average True Range over the given period.
// Requires at least period+1 candles.
func ATR(candles []domain.Candle, period int) float64 {
	if len(candles) < period+1 || period <= 0 {
		return 0
	}

	start := len(candles) - period
	var trSum float64
	for i := start; i < len(candles); i++ {
		tr := trueRange(candles[i], candles[i-1].Close)
		trSum += tr
	}
	return trSum / float64(period)
}

func trueRange(candle domain.Candle, prevClose float64) float64 {
	tr := candle.High - candle.Low
	if math.Abs(candle.High-prevClose) > tr {
		tr = math.Abs(candle.High - prevClose)
	}
	if math.Abs(candle.Low-prevClose) > tr {
		tr = math.Abs(candle.Low - prevClose)
	}
	return tr
}

// EMA computes Exponential Moving Average over the given period.
// Returns the last EMA value, or 0 if insufficient data.
func EMA(candles []domain.Candle, period int) float64 {
	if len(candles) < period || period <= 0 {
		return 0
	}

	// Start with SMA for the first 'period' values
	var sum float64
	for i := 0; i < period; i++ {
		sum += candles[i].Close
	}
	ema := sum / float64(period)

	multiplier := 2.0 / float64(period+1)
	for i := period; i < len(candles); i++ {
		ema = (candles[i].Close-ema)*multiplier + ema
	}
	return ema
}

// SMAVolume computes Simple Moving Average of volume.
func SMAVolume(candles []domain.Candle, period int) float64 {
	if len(candles) < period || period <= 0 {
		return 0
	}

	var sum float64
	start := len(candles) - period
	for i := start; i < len(candles); i++ {
		sum += candles[i].Volume
	}
	return sum / float64(period)
}

// RangeHighLow computes the highest high and lowest low over the last N candles.
func RangeHighLow(candles []domain.Candle, lookback int) (high, low float64) {
	if len(candles) < lookback || lookback <= 0 {
		return 0, 0
	}

	start := len(candles) - lookback
	high = math.Inf(-1)
	low = math.Inf(1)
	for i := start; i < len(candles); i++ {
		if candles[i].High > high {
			high = candles[i].High
		}
		if candles[i].Low < low {
			low = candles[i].Low
		}
	}
	return high, low
}

// BodyRatio computes the ratio of candle body to total range.
// Returns 0 if range is 0.
func BodyRatio(candle domain.Candle) float64 {
	rng := candle.High - candle.Low
	if rng <= 0 {
		return 0
	}
	return math.Abs(candle.Close-candle.Open) / rng
}

// BreakoutExtension computes how far the candle has extended beyond a level in ATR units.
func BreakoutExtension(candle domain.Candle, level float64, atr float64) float64 {
	if atr <= 0 {
		return 0
	}
	extension := math.Abs(candle.Close - level)
	return extension / atr
}

// Regime represents the 1H market regime.
type Regime string

const (
	RegimeTrendUp   Regime = "trend_up"
	RegimeTrendDown Regime = "trend_down"
	RegimeRange     Regime = "range"
	RegimeChop      Regime = "chop"
)

// DetectRegime determines the 1H regime based on EMA200 distance.
func DetectRegime(candles []domain.Candle, ema200 float64, cfg RegimeConfig) Regime {
	if ema200 <= 0 || len(candles) == 0 {
		return RegimeChop
	}

	price := candles[len(candles)-1].Close
	distancePct := (price - ema200) / ema200 * 100

	if distancePct >= cfg.TrendUpMinDistanceFromEMAPct {
		return RegimeTrendUp
	}
	if distancePct <= cfg.TrendDownMaxDistanceFromEMAPct {
		return RegimeTrendDown
	}
	if math.Abs(distancePct) <= cfg.RangeMaxDistanceFromEMAPct {
		return RegimeRange
	}
	return RegimeChop
}

// RegimeConfig holds regime detection thresholds.
type RegimeConfig struct {
	TrendUpMinDistanceFromEMAPct   float64
	TrendDownMaxDistanceFromEMAPct float64
	RangeMaxDistanceFromEMAPct     float64
}

// SetupType represents the type of setup detected.
type SetupType string

const (
	SetupBreakout SetupType = "breakout"
	SetupRetest   SetupType = "retest"
	SetupNone     SetupType = ""
)

// SetupResult holds the output of setup detection.
type SetupResult struct {
	SetupType     SetupType
	Side          domain.Side
	EntryType     domain.EntryType
	ProposedEntry float64
	StopLoss      float64
	TakeProfit    float64
	RR            float64
	SetupScore    float64
	ReasonCodes   []string
}

// DetectSetup checks for breakout/retest setups on 15m candles.
func DetectSetup(
	candles15m []domain.Candle,
	atr float64,
	ema200 float64,
	regime Regime,
	cfg SetupConfig,
) SetupResult {
	if len(candles15m) < cfg.RangeCandles+1 || atr <= 0 {
		return SetupResult{SetupType: SetupNone, ReasonCodes: []string{"insufficient_data"}}
	}

	// Range from last N candles (excluding current)
	rangeHigh, rangeLow := RangeHighLow(candles15m[:len(candles15m)-1], cfg.RangeCandles)

	// Current candle
	current := candles15m[len(candles15m)-1]

	// Volume check
	smaVol := SMAVolume(candles15m[:len(candles15m)-1], cfg.VolumeSMAPeriod)
	volumeRatio := 0.0
	if smaVol > 0 {
		volumeRatio = current.Volume / smaVol
	}

	// Body ratio
	bodyRatio := BodyRatio(current)

	// Check for breakout above range high (LONG)
	if current.Close > rangeHigh {
		return checkBreakoutLong(current, rangeHigh, atr, volumeRatio, bodyRatio, regime, cfg)
	}

	// Check for breakout below range low (SHORT)
	if current.Close < rangeLow {
		return checkBreakoutShort(current, rangeLow, atr, volumeRatio, bodyRatio, regime, cfg)
	}

	return SetupResult{SetupType: SetupNone, ReasonCodes: []string{"no_breakout"}}
}

// SetupConfig holds setup detection parameters.
type SetupConfig struct {
	RangeCandles               int
	VolumeSMAPeriod            int
	MinVolumeRatio             float64
	MaxBreakoutExtensionATR    float64
	MaxDistanceFromBreakoutATR float64
	MinRR                      float64
	ExpectedMoveCostMultiplier float64
	MinBodyRatio               float64
	AtrPeriod                  int
}

func checkBreakoutLong(
	candle domain.Candle,
	breakoutLevel, atr, volumeRatio, bodyRatio float64,
	regime Regime,
	cfg SetupConfig,
) SetupResult {
	result := SetupResult{
		Side:        domain.SideLong,
		SetupType:   SetupBreakout,
		ReasonCodes: []string{},
	}

	// Volume confirmation
	if volumeRatio < cfg.MinVolumeRatio {
		return SetupResult{SetupType: SetupNone, ReasonCodes: []string{"volume_too_low"}}
	}

	// Body ratio check
	if bodyRatio < cfg.MinBodyRatio {
		result.ReasonCodes = append(result.ReasonCodes, "weak_body")
	}

	// Extension check
	extension := BreakoutExtension(candle, breakoutLevel, atr)
	if extension > cfg.MaxBreakoutExtensionATR {
		// Too extended — propose LIMIT_RETEST
		result.EntryType = domain.EntryTypeLimitRetest
		result.ProposedEntry = breakoutLevel + atr*0.3 // retest near breakout level
		result.ReasonCodes = append(result.ReasonCodes, "extended_breakout")
	} else {
		result.EntryType = domain.EntryTypeMarket
		result.ProposedEntry = candle.Close
	}

	// Regime filter: prefer trend_up for longs
	if regime == RegimeTrendDown {
		return SetupResult{SetupType: SetupNone, ReasonCodes: []string{"regime_mismatch"}}
	}

	// SL below breakout level
	result.StopLoss = breakoutLevel - atr*0.5
	if result.StopLoss <= 0 {
		return SetupResult{SetupType: SetupNone, ReasonCodes: []string{"invalid_sl"}}
	}

	// TP: 2x the risk distance
	riskDist := result.ProposedEntry - result.StopLoss
	result.TakeProfit = result.ProposedEntry + riskDist*2

	// RR
	if riskDist > 0 {
		tpDist := result.TakeProfit - result.ProposedEntry
		result.RR = tpDist / riskDist
	}

	// Expected move (simplified: ATR * 2)
	expectedMove := atr * 2
	estCost := result.ProposedEntry * 0.001 // ~10bps total cost estimate

	// Final checks
	if result.RR < cfg.MinRR {
		return SetupResult{SetupType: SetupNone, ReasonCodes: []string{"rr_too_low"}}
	}
	if expectedMove <= cfg.ExpectedMoveCostMultiplier*estCost {
		return SetupResult{SetupType: SetupNone, ReasonCodes: []string{"expected_move_too_small"}}
	}

	// Setup score: based on volume ratio, body ratio, regime
	result.SetupScore = computeSetupScore(volumeRatio, bodyRatio, regime, extension, cfg)

	return result
}

func checkBreakoutShort(
	candle domain.Candle,
	breakoutLevel, atr, volumeRatio, bodyRatio float64,
	regime Regime,
	cfg SetupConfig,
) SetupResult {
	result := SetupResult{
		Side:        domain.SideShort,
		SetupType:   SetupBreakout,
		ReasonCodes: []string{},
	}

	if volumeRatio < cfg.MinVolumeRatio {
		return SetupResult{SetupType: SetupNone, ReasonCodes: []string{"volume_too_low"}}
	}

	if bodyRatio < cfg.MinBodyRatio {
		result.ReasonCodes = append(result.ReasonCodes, "weak_body")
	}

	extension := BreakoutExtension(candle, breakoutLevel, atr)
	if extension > cfg.MaxBreakoutExtensionATR {
		result.EntryType = domain.EntryTypeLimitRetest
		result.ProposedEntry = breakoutLevel - atr*0.3
		result.ReasonCodes = append(result.ReasonCodes, "extended_breakout")
	} else {
		result.EntryType = domain.EntryTypeMarket
		result.ProposedEntry = candle.Close
	}

	if regime == RegimeTrendUp {
		return SetupResult{SetupType: SetupNone, ReasonCodes: []string{"regime_mismatch"}}
	}

	result.StopLoss = breakoutLevel + atr*0.5

	riskDist := result.StopLoss - result.ProposedEntry
	result.TakeProfit = result.ProposedEntry - riskDist*2

	if riskDist > 0 {
		tpDist := result.ProposedEntry - result.TakeProfit
		result.RR = tpDist / riskDist
	}

	expectedMove := atr * 2
	estCost := result.ProposedEntry * 0.001

	if result.RR < cfg.MinRR {
		return SetupResult{SetupType: SetupNone, ReasonCodes: []string{"rr_too_low"}}
	}
	if expectedMove <= cfg.ExpectedMoveCostMultiplier*estCost {
		return SetupResult{SetupType: SetupNone, ReasonCodes: []string{"expected_move_too_small"}}
	}

	result.SetupScore = computeSetupScore(volumeRatio, bodyRatio, regime, extension, cfg)

	return result
}

func computeSetupScore(volumeRatio, bodyRatio float64, regime Regime, extension float64, cfg SetupConfig) float64 {
	score := 50.0

	// Volume bonus
	if volumeRatio >= 1.3 {
		score += 15
	} else if volumeRatio >= 1.0 {
		score += 5
	}

	// Body ratio bonus
	if bodyRatio >= 0.7 {
		score += 15
	} else if bodyRatio >= 0.5 {
		score += 5
	}

	// Regime bonus
	if regime == RegimeTrendUp || regime == RegimeTrendDown {
		score += 10
	}

	// Extension penalty
	if extension > cfg.MaxBreakoutExtensionATR {
		score -= 10
	}

	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return score
}

// QualityStopMultiplier computes a dynamic ATR multiplier for stop-loss width
// based on signal quality indicators: trade activity, volatility, volume.
// Base multiplier (e.g. 0.5) is scaled up when signal quality is poor.
// Returns a multiplier in range [base, 3.0].
func QualityStopMultiplier(baseMultiplier float64, tradeCount int, atrPct, volRatio float64) float64 {
	m := 1.0
	if tradeCount < 20 && tradeCount > 0 {
		m *= 1.5
	}
	if atrPct > 3.0 {
		m *= 1.5
	}
	if volRatio < 0.8 && volRatio > 0 {
		m *= 1.3
	}
	result := baseMultiplier * m
	if result < baseMultiplier {
		result = baseMultiplier
	}
	if result > 3.0 {
		result = 3.0
	}
	return result
}

// CountTradeActivity counts candles with non-zero volume in the lookback window.
func CountTradeActivity(candles []domain.Candle, lookback int) int {
	count := 0
	start := len(candles) - lookback
	if start < 0 {
		start = 0
	}
	for i := start; i < len(candles); i++ {
		if candles[i].Volume > 0 {
			count++
		}
	}
	return count
}
