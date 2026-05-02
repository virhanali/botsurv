package scoring

import (
	"math"

	"github.com/virhan/botsurv/internal/app"
	"github.com/virhan/botsurv/internal/domain"
	"github.com/virhan/botsurv/internal/indicator"
	"github.com/virhan/botsurv/internal/regime"
	"github.com/virhan/botsurv/internal/strategy"
)

// Action is the score-to-action output.
type Action string

const (
	ActionAllowMarket     Action = "ALLOW_MARKET"
	ActionAllowRetestOnly Action = "ALLOW_RETEST_ONLY"
	ActionReduceSize      Action = "REDUCE_SIZE"
	ActionReject          Action = "REJECT"
)

// ScoreComponents holds weighted component scores.
type ScoreComponents struct {
	TrendAlignment     float64 `json:"trend_alignment"`
	SetupQuality       float64 `json:"setup_quality"`
	MarketStructure    float64 `json:"market_structure"`
	MomentumConfluence float64 `json:"momentum_confluence"`
	VolumeConfirmation float64 `json:"volume_confirmation"`
	RelativeStrength   float64 `json:"relative_strength"`
	RiskReward         float64 `json:"risk_reward"`
}

// ScoreResult is a full scoring payload.
type ScoreResult struct {
	ScoreTotal     float64         `json:"score_total"`
	Components     ScoreComponents `json:"components"`
	ScoringVersion string          `json:"scoring_version"`
	Action         Action          `json:"action"`
	SizeMultiplier float64         `json:"size_multiplier"`
}

// Input captures all data needed to score a trade candidate.
type Input struct {
	Candidate      strategy.TradeCandidate
	Snapshot15m    indicator.IndicatorSnapshot
	Snapshot1h     indicator.IndicatorSnapshot
	Snapshot4h     indicator.IndicatorSnapshot
	RegimeSnapshot regime.MarketRegimeSnapshot
}

// Score computes component and total scores and resolves action mapping.
func Score(in Input, cfg app.ScoringConfig) ScoreResult {
	cfg = cfg.WithDefaults()
	comp := ScoreComponents{}
	comp.TrendAlignment = trendAlignment(in)
	comp.SetupQuality = setupQuality(in)
	comp.MarketStructure = marketStructure(in)
	comp.MomentumConfluence = momentumConfluence(in)
	comp.VolumeConfirmation = volumeConfirmation(in)
	comp.RelativeStrength = relativeStrength(in)
	comp.RiskReward = riskReward(in.Candidate.RiskRewardRatio)
	total := comp.TrendAlignment + comp.SetupQuality + comp.MarketStructure + comp.MomentumConfluence + comp.VolumeConfirmation + comp.RelativeStrength + comp.RiskReward
	total = clamp(total, 0, 100)
	action, size := resolveAction(total, cfg)
	return ScoreResult{
		ScoreTotal:     round2(total),
		Components:     roundComponents(comp),
		ScoringVersion: cfg.ScoringVersion,
		Action:         action,
		SizeMultiplier: size,
	}
}

func resolveAction(score float64, cfg app.ScoringConfig) (Action, float64) {
	thresholds := cfg.Balanced
	if cfg.Mode == "aggressive" {
		thresholds = cfg.Aggressive
	}
	s := round2(score)
	switch {
	case s >= thresholds.AllowMarket:
		return ActionAllowMarket, 1.0
	case s >= thresholds.AllowRetestOnly:
		return ActionAllowRetestOnly, 1.0
	case s >= thresholds.ReduceSize:
		return ActionReduceSize, 0.5
	default:
		return ActionReject, 0
	}
}

func trendAlignment(in Input) float64 {
	side := in.Candidate.Side
	score := 0.0
	if side == domain.SideLong {
		if in.Snapshot1h.EMAAlignment.PriceAboveEMA50 && in.Snapshot1h.EMAAlignment.EMA20AboveEMA50 {
			score += 15
		}
		if in.Snapshot4h.EMAAlignment.PriceAboveEMA50 && in.Snapshot4h.EMAAlignment.EMA20AboveEMA50 {
			score += 10
		}
	} else {
		if !in.Snapshot1h.EMAAlignment.PriceAboveEMA50 && !in.Snapshot1h.EMAAlignment.EMA20AboveEMA50 {
			score += 15
		}
		if !in.Snapshot4h.EMAAlignment.PriceAboveEMA50 && !in.Snapshot4h.EMAAlignment.EMA20AboveEMA50 {
			score += 10
		}
	}
	return clamp(score, 0, 25)
}

func setupQuality(in Input) float64 {
	score := 0.0
	if in.Candidate.EntryType == strategy.CandidateEntryMarket {
		score += 8
	} else {
		score += 6
	}
	if in.Snapshot15m.VolumeRatio >= 1.3 {
		score += 6
	} else if in.Snapshot15m.VolumeRatio >= 1.0 {
		score += 3
	}
	if in.Candidate.RiskRewardRatio >= 2.0 {
		score += 6
	} else if in.Candidate.RiskRewardRatio >= 1.4 {
		score += 3
	}
	return clamp(score, 0, 20)
}

func marketStructure(in Input) float64 {
	swings := in.Snapshot15m.RecentSwings
	if len(swings) == 0 {
		return 0
	}
	if in.Candidate.Side == domain.SideLong {
		if hasHHHL(swings) {
			return 15
		}
		return 8
	}
	if hasLHLH(swings) {
		return 15
	}
	return 8
}

func momentumConfluence(in Input) float64 {
	score := 0.0
	if in.Candidate.Side == domain.SideLong {
		if in.Snapshot15m.RSI14 >= 45 && in.Snapshot15m.RSI14 <= 65 {
			score += 5
		} else if in.Snapshot15m.RSI14 >= 38 && in.Snapshot15m.RSI14 <= 68 {
			score += 3
		}
		if in.Snapshot15m.MACD.HistogramDirection == "up" {
			score += 5
		}
	} else {
		if in.Snapshot15m.RSI14 >= 35 && in.Snapshot15m.RSI14 <= 55 {
			score += 5
		} else if in.Snapshot15m.RSI14 >= 32 && in.Snapshot15m.RSI14 <= 62 {
			score += 3
		}
		if in.Snapshot15m.MACD.HistogramDirection == "down" {
			score += 5
		}
	}
	return clamp(score, 0, 10)
}

func volumeConfirmation(in Input) float64 {
	v := in.Snapshot15m.VolumeRatio
	switch {
	case v >= 1.3:
		return 10
	case v >= 1.0:
		return 7
	case v >= 0.7:
		return 5
	default:
		return 0
	}
}

func relativeStrength(in Input) float64 {
	c := in.RegimeSnapshot.RelativeStrength.Classification
	if in.Candidate.Side == domain.SideLong {
		switch c {
		case "strong_outperform":
			return 10
		case "outperform":
			return 8
		case "neutral":
			return 5
		case "underperform":
			return 2
		case "strong_underperform":
			return 0
		default:
			return 5
		}
	}
	switch c {
	case "strong_underperform":
		return 10
	case "underperform":
		return 8
	case "neutral":
		return 5
	case "outperform":
		return 2
	case "strong_outperform":
		return 0
	default:
		return 5
	}
}

func riskReward(rr float64) float64 {
	if rr <= 1.4 {
		return 0
	}
	if rr >= 2.5 {
		return 10
	}
	if rr <= 2.0 {
		return ((rr - 1.4) / 0.6) * 7.0
	}
	return 7.0 + ((rr-2.0)/0.5)*3.0
}

func roundComponents(c ScoreComponents) ScoreComponents {
	return ScoreComponents{
		TrendAlignment:     round2(c.TrendAlignment),
		SetupQuality:       round2(c.SetupQuality),
		MarketStructure:    round2(c.MarketStructure),
		MomentumConfluence: round2(c.MomentumConfluence),
		VolumeConfirmation: round2(c.VolumeConfirmation),
		RelativeStrength:   round2(c.RelativeStrength),
		RiskReward:         round2(c.RiskReward),
	}
}

func clamp(v, minV, maxV float64) float64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func hasHHHL(swings []indicator.SwingPoint) bool {
	highs := make([]float64, 0)
	lows := make([]float64, 0)
	for _, s := range swings {
		if s.Type == "high" {
			highs = append(highs, s.Price)
		}
		if s.Type == "low" {
			lows = append(lows, s.Price)
		}
	}
	if len(highs) < 3 || len(lows) < 3 {
		return false
	}
	h := highs[len(highs)-3:]
	l := lows[len(lows)-3:]
	return h[0] < h[1] && h[1] < h[2] && l[0] < l[1] && l[1] < l[2]
}

func hasLHLH(swings []indicator.SwingPoint) bool {
	highs := make([]float64, 0)
	lows := make([]float64, 0)
	for _, s := range swings {
		if s.Type == "high" {
			highs = append(highs, s.Price)
		}
		if s.Type == "low" {
			lows = append(lows, s.Price)
		}
	}
	if len(highs) < 3 || len(lows) < 3 {
		return false
	}
	h := highs[len(highs)-3:]
	l := lows[len(lows)-3:]
	return h[0] > h[1] && h[1] > h[2] && l[0] > l[1] && l[1] > l[2]
}
