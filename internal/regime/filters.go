package regime

import (
	"fmt"
	"math"

	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
)

// FilterOutcome is a single regime filter result.
type FilterOutcome struct {
	Triggered bool
	Severity  string
	Detail    string
}

// BTCDumpedRecentlyShort checks BTC 5m drawdown.
func BTCDumpedRecentlyShort(btc5mCandles []domain.Candle, thresholdPct float64) FilterOutcome {
	ret, ok := returnPct(btc5mCandles)
	if !ok {
		return FilterOutcome{Triggered: false, Severity: "danger", Detail: "btc 5m data unavailable"}
	}
	if ret <= thresholdPct {
		return FilterOutcome{Triggered: true, Severity: "danger", Detail: fmt.Sprintf("btc 5m return %.4f%% <= %.4f%%", ret, thresholdPct)}
	}
	return FilterOutcome{Triggered: false, Severity: "info", Detail: fmt.Sprintf("btc 5m return %.4f%%", ret)}
}

// BTCDumpedRecentlyMedium checks BTC 15m drawdown.
func BTCDumpedRecentlyMedium(btc15mCandles []domain.Candle, thresholdPct float64) FilterOutcome {
	ret, ok := returnPct(btc15mCandles)
	if !ok {
		return FilterOutcome{Triggered: false, Severity: "danger", Detail: "btc 15m data unavailable"}
	}
	if ret <= thresholdPct {
		return FilterOutcome{Triggered: true, Severity: "danger", Detail: fmt.Sprintf("btc 15m return %.4f%% <= %.4f%%", ret, thresholdPct)}
	}
	return FilterOutcome{Triggered: false, Severity: "info", Detail: fmt.Sprintf("btc 15m return %.4f%%", ret)}
}

// BTCStronglyBearishHTF requires bearish 1h and 4h alignment.
func BTCStronglyBearishHTF(btc1h, btc4h indicator.IndicatorSnapshot) FilterOutcome {
	bear1h := !btc1h.EMAAlignment.PriceAboveEMA50 && !btc1h.EMAAlignment.EMA20AboveEMA50
	bear4h := !btc4h.EMAAlignment.PriceAboveEMA50 && !btc4h.EMAAlignment.EMA20AboveEMA50
	if bear1h && bear4h {
		return FilterOutcome{Triggered: true, Severity: "danger", Detail: "btc bearish on 1h and 4h (price below ema50 on both)"}
	}
	return FilterOutcome{Triggered: false, Severity: "info", Detail: "btc not strongly bearish across 1h/4h"}
}

// BTCStronglyBullishHTF requires bullish 1h and 4h alignment.
func BTCStronglyBullishHTF(btc1h, btc4h indicator.IndicatorSnapshot) FilterOutcome {
	bull1h := btc1h.EMAAlignment.PriceAboveEMA50 && btc1h.EMAAlignment.EMA20AboveEMA50
	bull4h := btc4h.EMAAlignment.PriceAboveEMA50 && btc4h.EMAAlignment.EMA20AboveEMA50
	if bull1h && bull4h {
		return FilterOutcome{Triggered: true, Severity: "info", Detail: "btc bullish on 1h and 4h (price above ema50 on both)"}
	}
	return FilterOutcome{Triggered: false, Severity: "info", Detail: "btc not strongly bullish across 1h/4h"}
}

// BTCNearMajorResistance checks if BTC is near resistance by ATR buffer.
func BTCNearMajorResistance(btc4h indicator.IndicatorSnapshot, atrBuffer float64) FilterOutcome {
	if len(btc4h.ResistanceLevels) == 0 {
		return FilterOutcome{Triggered: false, Severity: "info", Detail: "no 4h resistance levels available"}
	}
	if atrBuffer <= 0 {
		atrBuffer = 1.0
	}
	price := latestPriceFromSnapshot(btc4h)
	buf := btc4h.ATR14 * atrBuffer
	for _, lvl := range btc4h.ResistanceLevels {
		if lvl >= price && (lvl-price) <= buf {
			return FilterOutcome{Triggered: true, Severity: "warning", Detail: fmt.Sprintf("btc near 4h resistance %.4f (distance %.4f <= buffer %.4f)", lvl, lvl-price, buf)}
		}
	}
	return FilterOutcome{Triggered: false, Severity: "info", Detail: "btc not near major 4h resistance"}
}

// BTCNearMajorSupport checks if BTC is near support by ATR buffer.
func BTCNearMajorSupport(btc4h indicator.IndicatorSnapshot, atrBuffer float64) FilterOutcome {
	if len(btc4h.SupportLevels) == 0 {
		return FilterOutcome{Triggered: false, Severity: "info", Detail: "no 4h support levels available"}
	}
	if atrBuffer <= 0 {
		atrBuffer = 1.0
	}
	price := latestPriceFromSnapshot(btc4h)
	buf := btc4h.ATR14 * atrBuffer
	for _, lvl := range btc4h.SupportLevels {
		if lvl <= price && (price-lvl) <= buf {
			return FilterOutcome{Triggered: true, Severity: "warning", Detail: fmt.Sprintf("btc near 4h support %.4f (distance %.4f <= buffer %.4f)", lvl, price-lvl, buf)}
		}
	}
	return FilterOutcome{Triggered: false, Severity: "info", Detail: "btc not near major 4h support"}
}

// BTCDominanceRisingFast checks dominance rise over window candles.
func BTCDominanceRisingFast(btcd1hCandles []domain.Candle, thresholdPct float64) FilterOutcome {
	if len(btcd1hCandles) < 2 {
		return FilterOutcome{Triggered: false, Severity: "n/a", Detail: "btcd data unavailable"}
	}
	ret, ok := returnPct(btcd1hCandles)
	if !ok {
		return FilterOutcome{Triggered: false, Severity: "n/a", Detail: "btcd data unavailable"}
	}
	if ret >= thresholdPct {
		return FilterOutcome{Triggered: true, Severity: "warning", Detail: fmt.Sprintf("btcd rising fast %.4f%% >= %.4f%%", ret, thresholdPct)}
	}
	return FilterOutcome{Triggered: false, Severity: "info", Detail: fmt.Sprintf("btcd change %.4f%%", ret)}
}

// BTCDominanceFalling checks 1h+4h bearish trend on BTCD snapshot.
func BTCDominanceFalling(btcd1h, btcd4h *indicator.IndicatorSnapshot) FilterOutcome {
	if btcd1h == nil || btcd4h == nil {
		return FilterOutcome{Triggered: false, Severity: "n/a", Detail: "btcd data unavailable"}
	}
	fall1h := !btcd1h.EMAAlignment.PriceAboveEMA50 && !btcd1h.EMAAlignment.EMA20AboveEMA50
	fall4h := !btcd4h.EMAAlignment.PriceAboveEMA50 && !btcd4h.EMAAlignment.EMA20AboveEMA50
	if fall1h && fall4h {
		return FilterOutcome{Triggered: true, Severity: "info", Detail: "btcd trending down on 1h and 4h"}
	}
	return FilterOutcome{Triggered: false, Severity: "info", Detail: "btcd not trending down on both 1h/4h"}
}

func returnPct(candles []domain.Candle) (float64, bool) {
	if len(candles) < 2 {
		return 0, false
	}
	start := candles[0].Close
	end := candles[len(candles)-1].Close
	if start == 0 || math.IsNaN(start) || math.IsInf(start, 0) || math.IsNaN(end) || math.IsInf(end, 0) {
		return 0, false
	}
	return (end - start) / start * 100.0, true
}

func latestPriceFromSnapshot(s indicator.IndicatorSnapshot) float64 {
	return s.LastClosePrice
}
