package strategy

import (
	"testing"

	"github.com/virhan/botsurv/internal/domain"
)

func makeCandles(basePrice float64, count int) []domain.Candle {
	candles := make([]domain.Candle, count)
	for i := range candles {
		p := basePrice + float64(i)*10
		candles[i] = domain.Candle{
			Open:   p,
			High:   p + 20,
			Low:    p - 10,
			Close:  p + 5,
			Volume: 1000 + float64(i)*100,
		}
	}
	return candles
}

func TestATR_BasicCalculation(t *testing.T) {
	candles := []domain.Candle{
		{High: 110, Low: 90, Close: 100},
		{High: 120, Low: 95, Close: 105},
		{High: 115, Low: 100, Close: 110},
		{High: 125, Low: 105, Close: 115},
	}
	atr := ATR(candles, 3)
	if atr <= 0 {
		t.Errorf("expected positive ATR, got %.2f", atr)
	}
	// Last 3 TRs:
	// TR2: max(15, |115-105|=10, |100-105|=5) = 15
	// TR3: max(20, |125-110|=15, |105-110|=5) = 20
	// But wait, need to recalculate. ATR uses last `period` TRs starting from index `len-period`.
	// len=4, period=3, start=1. So TRs at indices 1,2,3:
	// i=1: TR of candles[1] vs candles[0].Close=100: max(25, |120-100|=20, |95-100|=5) = 25
	// i=2: TR of candles[2] vs candles[1].Close=105: max(15, |115-105|=10, |100-105|=5) = 15
	// i=3: TR of candles[3] vs candles[2].Close=110: max(20, |125-110|=15, |105-110|=5) = 20
	// ATR(3) = (25+15+20)/3 = 20
	expected := 20.0
	if diff := atr - expected; diff > 0.01 || diff < -0.01 {
		t.Errorf("ATR: got %.2f, want %.2f", atr, expected)
	}
}

func TestATR_InsufficientData(t *testing.T) {
	candles := []domain.Candle{{High: 110, Low: 90, Close: 100}}
	if ATR(candles, 14) != 0 {
		t.Error("expected 0 for insufficient data")
	}
}

func TestEMA_BasicCalculation(t *testing.T) {
	// Use flat-ish prices so EMA converges
	candles := make([]domain.Candle, 250)
	for i := range candles {
		p := 100.0 + float64(i%10)*0.5 // oscillates 100-104.5
		candles[i] = domain.Candle{Close: p}
	}
	ema := EMA(candles, 200)
	if ema <= 0 {
		t.Errorf("expected positive EMA, got %.2f", ema)
	}
	// EMA should be near the average of recent prices (~102)
	if ema > 106 || ema < 99 {
		t.Errorf("EMA %.2f seems unreasonable for oscillating prices", ema)
	}
}

func TestEMA_InsufficientData(t *testing.T) {
	candles := makeCandles(100, 10)
	if EMA(candles, 200) != 0 {
		t.Error("expected 0 for insufficient data")
	}
}

func TestSMAVolume_Basic(t *testing.T) {
	candles := []domain.Candle{
		{Volume: 100}, {Volume: 200}, {Volume: 300}, {Volume: 400}, {Volume: 500},
	}
	sma := SMAVolume(candles, 3)
	// Last 3: 300, 400, 500 = avg 400
	if sma != 400 {
		t.Errorf("expected 400, got %.2f", sma)
	}
}

func TestRangeHighLow_Basic(t *testing.T) {
	candles := []domain.Candle{
		{High: 110, Low: 90},
		{High: 120, Low: 95},
		{High: 115, Low: 100},
		{High: 125, Low: 105},
		{High: 130, Low: 110},
	}
	high, low := RangeHighLow(candles, 3)
	// Last 3: High=max(115,125,130)=130, Low=min(100,105,110)=100
	if high != 130 {
		t.Errorf("expected high 130, got %.2f", high)
	}
	if low != 100 {
		t.Errorf("expected low 100, got %.2f", low)
	}
}

func TestBodyRatio_Basic(t *testing.T) {
	candle := domain.Candle{Open: 100, Close: 110, High: 115, Low: 95}
	// body = |110-100| = 10, range = 115-95 = 20
	ratio := BodyRatio(candle)
	if ratio != 0.5 {
		t.Errorf("expected 0.5, got %.2f", ratio)
	}
}

func TestBodyRatio_ZeroRange(t *testing.T) {
	candle := domain.Candle{Open: 100, Close: 100, High: 100, Low: 100}
	if BodyRatio(candle) != 0 {
		t.Error("expected 0 for zero range")
	}
}

func TestBreakoutExtension_Basic(t *testing.T) {
	candle := domain.Candle{Close: 120}
	level := 100.0
	atr := 10.0
	ext := BreakoutExtension(candle, level, atr)
	// |120-100|/10 = 2
	if ext != 2.0 {
		t.Errorf("expected 2.0, got %.2f", ext)
	}
}

func TestDetectRegime_TrendUp(t *testing.T) {
	candles := makeCandles(100, 5)
	candles[len(candles)-1].Close = 120 // 20% above EMA
	ema := 100.0
	cfg := RegimeConfig{
		TrendUpMinDistanceFromEMAPct:   1.0,
		TrendDownMaxDistanceFromEMAPct: -1.0,
		RangeMaxDistanceFromEMAPct:     0.5,
	}
	regime := DetectRegime(candles, ema, cfg)
	if regime != RegimeTrendUp {
		t.Errorf("expected trend_up, got %s", regime)
	}
}

func TestDetectRegime_TrendDown(t *testing.T) {
	candles := makeCandles(100, 5)
	candles[len(candles)-1].Close = 80 // 20% below EMA
	ema := 100.0
	cfg := RegimeConfig{
		TrendUpMinDistanceFromEMAPct:   1.0,
		TrendDownMaxDistanceFromEMAPct: -1.0,
		RangeMaxDistanceFromEMAPct:     0.5,
	}
	regime := DetectRegime(candles, ema, cfg)
	if regime != RegimeTrendDown {
		t.Errorf("expected trend_down, got %s", regime)
	}
}

func TestDetectRegime_Range(t *testing.T) {
	candles := makeCandles(100, 5)
	candles[len(candles)-1].Close = 100.2 // 0.2% from EMA
	ema := 100.0
	cfg := RegimeConfig{
		TrendUpMinDistanceFromEMAPct:   1.0,
		TrendDownMaxDistanceFromEMAPct: -1.0,
		RangeMaxDistanceFromEMAPct:     0.5,
	}
	regime := DetectRegime(candles, ema, cfg)
	if regime != RegimeRange {
		t.Errorf("expected range, got %s", regime)
	}
}

func TestDetectSetup_BreakoutLong(t *testing.T) {
	cfg := SetupConfig{
		RangeCandles:               20,
		VolumeSMAPeriod:            20,
		MinVolumeRatio:             1.3,
		MaxBreakoutExtensionATR:    2.5,
		MaxDistanceFromBreakoutATR: 1.0,
		MinRR:                      2.0,
		ExpectedMoveCostMultiplier: 3.0,
		MinBodyRatio:               0.5,
		AtrPeriod:                  14,
	}

	// Create 21 candles: 20 range candles with consistent range + 1 breakout
	candles := make([]domain.Candle, 21)
	for i := 0; i < 20; i++ {
		candles[i] = domain.Candle{
			Open:   100,
			High:   110,
			Low:    90,
			Close:  102,
			Volume: 1000,
		}
	}
	// Breakout candle: close above range high (110), good volume
	candles[20] = domain.Candle{
		Open: 108, High: 118, Low: 106, Close: 116,
		Volume: 2000, // 2x average
	}

	atr := 5.0
	ema200 := 100.0
	regime := RegimeTrendUp

	result := DetectSetup(candles, atr, ema200, regime, cfg)
	if result.SetupType != SetupBreakout {
		t.Errorf("expected breakout, got %s (reasons: %v)", result.SetupType, result.ReasonCodes)
	}
	if result.Side != domain.SideLong {
		t.Errorf("expected LONG, got %s", result.Side)
	}
	if result.RR < 2.0 {
		t.Errorf("expected RR >= 2.0, got %.2f", result.RR)
	}
	if result.SetupScore <= 0 {
		t.Errorf("expected positive setup score, got %.2f", result.SetupScore)
	}
}

func TestDetectSetup_BreakoutShort(t *testing.T) {
	cfg := SetupConfig{
		RangeCandles:               5,
		VolumeSMAPeriod:            5,
		MinVolumeRatio:             1.3,
		MaxBreakoutExtensionATR:    2.5,
		MaxDistanceFromBreakoutATR: 1.0,
		MinRR:                      2.0,
		ExpectedMoveCostMultiplier: 3.0,
		MinBodyRatio:               0.5,
		AtrPeriod:                  14,
	}

	candles := make([]domain.Candle, 6)
	for i := 0; i < 5; i++ {
		candles[i] = domain.Candle{
			Open:   100 + float64(i)*0.5,
			High:   105 + float64(i)*0.5,
			Low:    95 + float64(i)*0.5,
			Close:  102 + float64(i)*0.5,
			Volume: 1000,
		}
	}
	// Breakout below range low
	candles[5] = domain.Candle{
		Open: 96, High: 98, Low: 88, Close: 90,
		Volume: 2500,
	}

	atr := 5.0
	ema200 := 110.0 // price below EMA = trend_down
	regime := RegimeTrendDown

	result := DetectSetup(candles, atr, ema200, regime, cfg)
	if result.SetupType != SetupBreakout {
		t.Errorf("expected breakout, got %s (reasons: %v)", result.SetupType, result.ReasonCodes)
	}
	if result.Side != domain.SideShort {
		t.Errorf("expected SHORT, got %s", result.Side)
	}
}

func TestDetectSetup_VolumeTooLow(t *testing.T) {
	cfg := SetupConfig{
		RangeCandles:    5,
		VolumeSMAPeriod: 5,
		MinVolumeRatio:  1.3,
		MinBodyRatio:    0.5,
		MinRR:           2.0,
	}

	candles := make([]domain.Candle, 6)
	for i := 0; i < 5; i++ {
		candles[i] = domain.Candle{
			High: 105, Low: 95, Close: 102, Volume: 1000,
		}
	}
	candles[5] = domain.Candle{
		High: 115, Low: 108, Close: 114, Volume: 500, // low volume
	}

	result := DetectSetup(candles, 5.0, 100.0, RegimeTrendUp, cfg)
	if result.SetupType != SetupNone {
		t.Errorf("expected no setup, got %s", result.SetupType)
	}
	found := false
	for _, r := range result.ReasonCodes {
		if r == "volume_too_low" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected volume_too_low reason, got %v", result.ReasonCodes)
	}
}

func TestDetectSetup_RegimeMismatch(t *testing.T) {
	cfg := SetupConfig{
		RangeCandles:    5,
		VolumeSMAPeriod: 5,
		MinVolumeRatio:  1.3,
		MinBodyRatio:    0.5,
		MinRR:           2.0,
	}

	candles := make([]domain.Candle, 6)
	for i := 0; i < 5; i++ {
		candles[i] = domain.Candle{
			High: 105, Low: 95, Close: 102, Volume: 1000,
		}
	}
	// Long breakout but regime is trend_down
	candles[5] = domain.Candle{
		High: 115, Low: 108, Close: 114, Volume: 2000,
	}

	result := DetectSetup(candles, 5.0, 120.0, RegimeTrendDown, cfg)
	if result.SetupType != SetupNone {
		t.Errorf("expected no setup (regime mismatch), got %s", result.SetupType)
	}
}

func TestDetectSetup_InsufficientData(t *testing.T) {
	cfg := SetupConfig{RangeCandles: 20}
	candles := makeCandles(100, 5)
	result := DetectSetup(candles, 5.0, 100.0, RegimeRange, cfg)
	if result.SetupType != SetupNone {
		t.Errorf("expected no setup, got %s", result.SetupType)
	}
}
