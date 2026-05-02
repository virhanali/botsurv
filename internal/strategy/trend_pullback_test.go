package strategy

import (
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
)

func TestTrendPullback_Long_Positive(t *testing.T) {
	in := baseTrendLongInput()
	cand, rej := generateTrendPullbackForSide(in, domain.SideLong)
	if rej != nil {
		t.Fatalf("unexpected rejection: %+v", *rej)
	}
	if cand == nil {
		t.Fatal("expected candidate")
	}
	if cand.Strategy != StrategyTrendPullback || cand.Side != domain.SideLong {
		t.Fatalf("unexpected candidate: %+v", cand)
	}
	if cand.RiskRewardRatio < 1.4 {
		t.Fatalf("expected rr >= 1.4, got %.3f", cand.RiskRewardRatio)
	}
	if len(cand.TakeProfits) != 2 {
		t.Fatalf("expected two take profits, got %d", len(cand.TakeProfits))
	}
	if cand.TakeProfits[0].SizePct+cand.TakeProfits[1].SizePct != 100 {
		t.Fatalf("tp sizing must sum to 100, got %.2f", cand.TakeProfits[0].SizePct+cand.TakeProfits[1].SizePct)
	}
}

func TestTrendPullback_Long_ConditionFailures(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*TrendPullbackInput)
	}{
		{name: "htf trend", mutate: func(in *TrendPullbackInput) {
			in.Snapshot1h.EMAAlignment = indicator.EMAlignment{}
			in.Snapshot1h.RecentSwings = nil
		}},
		{name: "4h opposite", mutate: func(in *TrendPullbackInput) {
			in.Snapshot4h.EMAAlignment.PriceAboveEMA50 = false
			in.Snapshot4h.EMAAlignment.EMA20AboveEMA50 = false
		}},
		{name: "pullback", mutate: func(in *TrendPullbackInput) {
			in.Snapshot15m.EMA20 = 130
			in.Snapshot15m.EMA50 = 128
			in.Snapshot15m.SupportLevels = []float64{90}
		}},
		{name: "reaction", mutate: func(in *TrendPullbackInput) {
			c := &in.Candles15m[len(in.Candles15m)-1]
			c.Open, c.Close, c.High, c.Low = 113.5, 113.45, 113.55, 113.4
		}},
		{name: "momentum", mutate: func(in *TrendPullbackInput) { in.Snapshot15m.RSI14 = 75 }},
		{name: "volume", mutate: func(in *TrendPullbackInput) { in.Snapshot15m.VolumeCurrent = 50; in.Snapshot15m.VolumeMA20 = 120 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseTrendLongInput()
			tc.mutate(&in)
			cand, rej := generateTrendPullbackForSide(in, domain.SideLong)
			if cand != nil {
				t.Fatal("expected no candidate")
			}
			if rej == nil || !rej.NearMiss {
				t.Fatalf("expected near miss rejection, got %+v", rej)
			}
		})
	}
}

func TestTrendPullback_Short_PositiveAndFailures(t *testing.T) {
	in := baseTrendShortInput()
	cand, rej := generateTrendPullbackForSide(in, domain.SideShort)
	if rej != nil {
		t.Fatalf("unexpected rejection: %+v", *rej)
	}
	if cand == nil {
		t.Fatal("expected short candidate")
	}

	cases := []func(*TrendPullbackInput){
		func(in *TrendPullbackInput) {
			in.Snapshot1h.EMAAlignment.PriceAboveEMA50 = true
			in.Snapshot1h.EMAAlignment.EMA20AboveEMA50 = true
			in.Snapshot1h.RecentSwings = nil
		},
		func(in *TrendPullbackInput) {
			in.Snapshot4h.EMAAlignment.PriceAboveEMA50 = true
			in.Snapshot4h.EMAAlignment.EMA20AboveEMA50 = true
		},
		func(in *TrendPullbackInput) {
			in.Snapshot15m.EMA20 = 80
			in.Snapshot15m.EMA50 = 82
			in.Snapshot15m.ResistanceLevels = []float64{150}
		},
		func(in *TrendPullbackInput) {
			c := &in.Candles15m[len(in.Candles15m)-1]
			c.Open, c.Close, c.High, c.Low = 108.1, 108.15, 108.2, 108.05
		},
		func(in *TrendPullbackInput) { in.Snapshot15m.RSI14 = 25 },
		func(in *TrendPullbackInput) { in.Snapshot15m.VolumeCurrent = 20; in.Snapshot15m.VolumeMA20 = 100 },
	}

	for i, mutate := range cases {
		bad := baseTrendShortInput()
		mutate(&bad)
		cand, rej := generateTrendPullbackForSide(bad, domain.SideShort)
		if cand != nil {
			t.Fatalf("case %d expected no candidate", i)
		}
		if rej == nil {
			t.Fatalf("case %d expected rejection", i)
		}
	}
}

func baseTrendLongInput() TrendPullbackInput {
	candles := make([]domain.Candle, 80)
	base := time.Now().UTC().Add(-80 * 15 * time.Minute)
	price := 100.0
	for i := 0; i < 80; i++ {
		if i > 65 {
			price -= 0.05
		} else {
			price += 0.20
		}
		op := price - 0.05
		cl := price
		hi := price + 0.10
		lo := price - 0.10
		vol := 100.0
		if i == 79 {
			op = price - 0.05
			cl = price + 0.10
			hi = price + 0.12
			lo = price - 0.40
			vol = 140
		}
		candles[i] = domain.Candle{OpenTime: base.Add(time.Duration(i) * 15 * time.Minute).UnixMilli(), Open: op, High: hi, Low: lo, Close: cl, Volume: vol, Confirmed: true}
	}
	return TrendPullbackInput{
		Now:        time.Now().UTC(),
		Symbol:     "ORDIUSDT",
		Timeframe:  "15m",
		TickSize:   0.01,
		Candles15m: candles,
		Snapshot15m: indicator.IndicatorSnapshot{
			EMA20:            candles[len(candles)-1].Close - 0.2,
			EMA50:            candles[len(candles)-1].Close - 0.35,
			RSI14:            55,
			ATR14:            0.8,
			VolumeCurrent:    140,
			VolumeMA20:       120,
			VolumeRatio:      1.16,
			MACD:             indicator.MACDSnapshot{HistogramDirection: "up"},
			SupportLevels:    []float64{candles[len(candles)-1].Close - 0.35},
			ResistanceLevels: []float64{candles[len(candles)-1].Close + 5, candles[len(candles)-1].Close + 8},
			RecentSwings:     []indicator.SwingPoint{{Price: 108, Type: "low"}, {Price: 111, Type: "high"}, {Price: 109, Type: "low"}, {Price: 112, Type: "high"}, {Price: 110, Type: "low"}, {Price: 113, Type: "high"}},
		},
		Snapshot1h:           indicator.IndicatorSnapshot{EMAAlignment: indicator.EMAlignment{PriceAboveEMA50: true, EMA20AboveEMA50: true}, RecentSwings: []indicator.SwingPoint{{Price: 100, Type: "low"}, {Price: 110, Type: "high"}, {Price: 103, Type: "low"}, {Price: 113, Type: "high"}, {Price: 106, Type: "low"}, {Price: 116, Type: "high"}}},
		Snapshot4h:           indicator.IndicatorSnapshot{EMAAlignment: indicator.EMAlignment{PriceAboveEMA50: true, EMA20AboveEMA50: true}},
		IndicatorSnapshotRef: "ind-ref",
		RegimeSnapshotRef:    "reg-ref",
	}
}

func baseTrendShortInput() TrendPullbackInput {
	in := baseTrendLongInput()
	in.Symbol = "ARBUSDT"
	for i := range in.Candles15m {
		c := &in.Candles15m[i]
		base := 220.0 - (c.Close - 100.0)
		c.Open = base + 0.05
		c.Close = base
		c.High = base + 0.12
		c.Low = base - 0.12
		if i == len(in.Candles15m)-1 {
			c.Open = base + 0.10
			c.Close = base - 0.08
			c.High = base + 0.40
			c.Low = base - 0.15
		}
	}
	last := in.Candles15m[len(in.Candles15m)-1].Close
	in.Snapshot15m.EMA20 = last + 0.2
	in.Snapshot15m.EMA50 = last + 0.35
	in.Snapshot15m.RSI14 = 45
	in.Snapshot15m.MACD.HistogramDirection = "down"
	in.Snapshot15m.SupportLevels = []float64{last - 2, last - 4}
	in.Snapshot15m.ResistanceLevels = []float64{last + 0.25}
	in.Snapshot15m.RecentSwings = []indicator.SwingPoint{{Price: 120, Type: "high"}, {Price: 116, Type: "low"}, {Price: 118, Type: "high"}, {Price: 114, Type: "low"}, {Price: 116, Type: "high"}, {Price: 112, Type: "low"}}
	in.Snapshot1h = indicator.IndicatorSnapshot{EMAAlignment: indicator.EMAlignment{PriceAboveEMA50: false, EMA20AboveEMA50: false}, RecentSwings: []indicator.SwingPoint{{Price: 120, Type: "high"}, {Price: 114, Type: "low"}, {Price: 118, Type: "high"}, {Price: 112, Type: "low"}, {Price: 116, Type: "high"}, {Price: 110, Type: "low"}}}
	in.Snapshot4h = indicator.IndicatorSnapshot{EMAAlignment: indicator.EMAlignment{PriceAboveEMA50: false, EMA20AboveEMA50: false}}
	return in
}
