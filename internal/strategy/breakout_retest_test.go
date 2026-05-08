package strategy

import (
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
)

func TestBreakoutRetest_Long_PositiveAndFailures(t *testing.T) {
	in := baseBreakoutLongInput()
	cand, rej := generateBreakoutRetestForSide(in, domain.SideLong)
	if rej != nil {
		t.Fatalf("unexpected rejection: %+v", *rej)
	}
	if cand == nil {
		t.Fatal("expected long breakout candidate")
	}
	if cand.RiskRewardRatio < 1.4 {
		t.Fatalf("expected rr >= 1.4, got %.3f", cand.RiskRewardRatio)
	}

	cases := []func(*BreakoutRetestInput){
		func(in *BreakoutRetestInput) { in.Snapshot15m.ResistanceLevels = []float64{1000} },
		func(in *BreakoutRetestInput) {
			for i := 38; i < len(in.Candles15m); i++ {
				in.Candles15m[i].Low = in.Candles15m[i].Low + 2.0
				in.Candles15m[i].High = in.Candles15m[i].High + 2.0
			}
		},
		func(in *BreakoutRetestInput) {
			c := &in.Candles15m[len(in.Candles15m)-1]
			c.Close = 99.8
			c.Open = 100.2
		},
		func(in *BreakoutRetestInput) {
			in.Snapshot1h.EMAAlignment.PriceAboveEMA50 = false
			in.Snapshot1h.EMAAlignment.EMA20AboveEMA50 = false
		},
	}
	for i, mutate := range cases {
		bad := baseBreakoutLongInput()
		mutate(&bad)
		c, r := generateBreakoutRetestForSide(bad, domain.SideLong)
		if c != nil {
			t.Fatalf("case %d expected no candidate", i)
		}
		if r == nil {
			t.Fatalf("case %d expected rejection", i)
		}
	}
}

func TestBreakoutRetest_Short_PositiveAndFailures(t *testing.T) {
	in := baseBreakoutShortInput()
	cand, rej := generateBreakoutRetestForSide(in, domain.SideShort)
	if rej != nil {
		t.Fatalf("unexpected rejection: %+v", *rej)
	}
	if cand == nil {
		t.Fatal("expected short breakout candidate")
	}

	cases := []func(*BreakoutRetestInput){
		func(in *BreakoutRetestInput) { in.Snapshot15m.SupportLevels = []float64{0.1} },
		func(in *BreakoutRetestInput) {
			for i := 38; i < len(in.Candles15m); i++ {
				in.Candles15m[i].High = in.Candles15m[i].High - 2.0
				in.Candles15m[i].Low = in.Candles15m[i].Low - 2.0
			}
		},
		func(in *BreakoutRetestInput) {
			c := &in.Candles15m[len(in.Candles15m)-1]
			c.Close = 118.2
			c.Open = 117.9
		},
		func(in *BreakoutRetestInput) {
			in.Snapshot1h.EMAAlignment.PriceAboveEMA50 = true
			in.Snapshot1h.EMAAlignment.EMA20AboveEMA50 = true
		},
	}
	for i, mutate := range cases {
		bad := baseBreakoutShortInput()
		mutate(&bad)
		c, r := generateBreakoutRetestForSide(bad, domain.SideShort)
		if c != nil {
			t.Fatalf("case %d expected no candidate", i)
		}
		if r == nil {
			t.Fatalf("case %d expected rejection", i)
		}
	}
}

func baseBreakoutLongInput() BreakoutRetestInput {
	candles := make([]domain.Candle, 45)
	base := time.Now().UTC().Add(-45 * 15 * time.Minute)
	price := 99.0
	for i := 0; i < 45; i++ {
		price += 0.02
		op := price - 0.04
		cl := price + 0.01
		hi := price + 0.08
		lo := price - 0.08
		vol := 100.0
		if i == 37 { // breakout candle
			op = 99.9
			cl = 101.3
			hi = 101.5
			lo = 99.8
			vol = 220
		}
		if i == 40 { // retest wick
			op = 100.9
			cl = 100.7
			hi = 101.0
			lo = 100.1
			vol = 110
		}
		if i == 44 { // confirmation
			op = 100.7
			cl = 101.1
			hi = 101.2
			lo = 100.6
			vol = 150
		}
		candles[i] = domain.Candle{OpenTime: base.Add(time.Duration(i) * 15 * time.Minute).UnixMilli(), Open: op, High: hi, Low: lo, Close: cl, Volume: vol, Confirmed: true}
	}
	return BreakoutRetestInput{
		Now:        time.Now().UTC(),
		Symbol:     "SOLUSDT",
		Timeframe:  "15m",
		TickSize:   0.01,
		MinRR:      1.4,
		Candles15m: candles,
		Snapshot15m: indicator.IndicatorSnapshot{
			ATR14:            0.8,
			ResistanceLevels: []float64{100.0, 102.5},
			SupportLevels:    []float64{98.0, 97.5},
		},
		Snapshot1h:           indicator.IndicatorSnapshot{EMAAlignment: indicator.EMAlignment{PriceAboveEMA50: true, EMA20AboveEMA50: true}},
		IndicatorSnapshotRef: "ind-ref",
		RegimeSnapshotRef:    "reg-ref",
	}
}

func baseBreakoutShortInput() BreakoutRetestInput {
	in := baseBreakoutLongInput()
	in.Symbol = "ADAUSDT"
	for i := range in.Candles15m {
		c := &in.Candles15m[i]
		v := 220.0 - (c.Close - 99.0)
		c.Open = v + 0.03
		c.Close = v - 0.01
		c.High = v + 0.08
		c.Low = v - 0.08
		if i == 37 {
			c.Open = 120.2
			c.Close = 118.7
			c.High = 120.4
			c.Low = 118.5
			c.Volume = 220
		}
		if i == 40 {
			c.Open = 119.2
			c.Close = 119.1
			c.High = 119.65
			c.Low = 118.8
		}
		if i == 44 {
			c.Open = 119.0
			c.Close = 118.6
			c.High = 119.1
			c.Low = 118.5
		}
	}
	in.Snapshot15m.SupportLevels = []float64{119.8, 118.0}
	in.Snapshot15m.ResistanceLevels = []float64{121.5, 122.8}
	in.Snapshot1h = indicator.IndicatorSnapshot{EMAAlignment: indicator.EMAlignment{PriceAboveEMA50: false, EMA20AboveEMA50: false}}
	return in
}
