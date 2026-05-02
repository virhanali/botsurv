package metrics

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/virhan/botsurv/internal/db"
	"github.com/virhan/botsurv/internal/domain"
)

// PaperMetrics holds computed metrics for paper trading performance.
type PaperMetrics struct {
	Period        string  `json:"period"`
	TotalTrades   int     `json:"total_trades"`
	WinRate       float64 `json:"win_rate"`
	ProfitFactor  float64 `json:"profit_factor"`
	Expectancy    float64 `json:"expectancy_per_trade"`
	AvgWinR       float64 `json:"avg_win_r"`
	AvgLossR      float64 `json:"avg_loss_r"`
	MaxDrawdownPct float64 `json:"max_drawdown_pct"`
	MaxConsecutiveLosses int     `json:"max_consecutive_losses"`
	TradeFreqPerDay      float64 `json:"trade_frequency_per_day"`
	ByStrategy           map[string]SubMetrics `json:"by_strategy,omitempty"`
	BySide               map[string]SubMetrics `json:"by_side"`

	// Counterfactual analysis
	RejectedButWouldHaveWon int     `json:"rejected_but_would_have_won"`
	RejectedCorrectly       int     `json:"rejected_correctly"`
	RejectionAccuracyPct    float64 `json:"rejection_accuracy_pct"`
}

// SubMetrics holds per-strategy or per-side metrics.
type SubMetrics struct {
	TotalTrades   int     `json:"total_trades"`
	WinRate       float64 `json:"win_rate"`
	ProfitFactor  float64 `json:"profit_factor"`
	Expectancy    float64 `json:"expectancy_per_trade"`
	AvgWinR       float64 `json:"avg_win_r"`
	AvgLossR      float64 `json:"avg_loss_r"`
}

// ComputePaperMetrics calculates paper trading metrics from trade data.
func ComputePaperMetrics(ctx context.Context, paperTradeRepo db.PaperTradeRepository, outcomeRepo db.CandidateOutcomeRepository, period string) (*PaperMetrics, error) {
	since := periodStart(period)
	now := time.Now()

	metrics := &PaperMetrics{Period: period}

	// Fetch closed paper trades
	var trades []domain.PaperTrade
	if paperTradeRepo != nil {
		var err error
		trades, err = paperTradeRepo.GetAll(ctx, since)
		if err != nil {
			return nil, fmt.Errorf("get paper trades: %w", err)
		}
	}

	// Fetch completed candidate outcomes for counterfactual analysis
	var outcomes []domain.CandidateOutcome
	if outcomeRepo != nil {
		var err error
		outcomes, err = outcomeRepo.GetCompleted(ctx, since)
		if err != nil {
			return nil, fmt.Errorf("get outcomes: %w", err)
		}
	}

	metrics.TotalTrades = len(trades)

	if len(trades) == 0 {
		return metrics, nil
	}

	// Basic metrics
	var wins, losses int
	var grossWins, grossLosses float64
	var totalR, totalWinR, totalLossR float64
	var winRCount, lossRCount int
	var maxConsecLosses, currentConsecLosses int
	var equity []float64
	cumEquity := 0.0
	var peak float64

	for _, t := range trades {
		if t.PnLNet == nil {
			continue
		}
		pnl := *t.PnLNet
		cumEquity += pnl

		if t.RMultiple != nil {
			totalR += *t.RMultiple
			if pnl > 0 {
				totalWinR += *t.RMultiple
				winRCount++
			} else {
				totalLossR += *t.RMultiple
				lossRCount++
			}
		}

		if pnl > 0 {
			wins++
			grossWins += pnl
			currentConsecLosses = 0
		} else {
			losses++
			grossLosses += math.Abs(pnl)
			currentConsecLosses++
			if currentConsecLosses > maxConsecLosses {
				maxConsecLosses = currentConsecLosses
			}
		}

		equity = append(equity, cumEquity)
		if cumEquity > peak {
			peak = cumEquity
		}
	}

	if wins+losses > 0 {
		metrics.WinRate = float64(wins) / float64(wins+losses) * 100
	}

	if grossLosses > 0 {
		metrics.ProfitFactor = grossWins / grossLosses
	} else if grossWins > 0 {
		metrics.ProfitFactor = math.Inf(1)
	}

	if len(trades) > 0 {
		metrics.Expectancy = cumEquity / float64(len(trades))
	}

	if winRCount > 0 {
		metrics.AvgWinR = totalWinR / float64(winRCount)
	}
	if lossRCount > 0 {
		metrics.AvgLossR = totalLossR / float64(lossRCount)
	}

	// Max drawdown
	var currentDD float64
	for _, eq := range equity {
		if eq > peak {
			peak = eq
		}
		dd := peak - eq
		if dd > currentDD {
			currentDD = dd
		}
	}
	if cumEquity > 0 && currentDD > 0 {
		metrics.MaxDrawdownPct = (currentDD / cumEquity * 100)
	}
	metrics.MaxConsecutiveLosses = maxConsecLosses

	// Trade frequency per day
	daysBetween := now.Sub(since).Hours() / 24
	if daysBetween <= 0 {
		daysBetween = 1
	}
	metrics.TradeFreqPerDay = float64(len(trades)) / daysBetween

	// By side aggregation
	metrics.BySide = make(map[string]SubMetrics)
	bySide := make(map[string][]float64)
	for _, t := range trades {
		side := string(t.Side)
		if t.PnLNet != nil {
			bySide[side] = append(bySide[side], *t.PnLNet)
		}
	}
	for side, pnls := range bySide {
		metrics.BySide[side] = computeSubMetrics(pnls)
	}

	// Counterfactual analysis
	for _, o := range outcomes {
		if o.WouldHaveOutcome == "sl_hit" {
			metrics.RejectedCorrectly++
		} else if o.WouldHaveOutcome == "tp1_hit" || o.WouldHaveOutcome == "tp2_hit" {
			metrics.RejectedButWouldHaveWon++
		}
	}
	totalRejected := metrics.RejectedButWouldHaveWon + metrics.RejectedCorrectly
	if totalRejected > 0 {
		metrics.RejectionAccuracyPct = float64(metrics.RejectedCorrectly) / float64(totalRejected) * 100
	}

	return metrics, nil
}

func computeSubMetrics(pnls []float64) SubMetrics {
	m := SubMetrics{TotalTrades: len(pnls)}
	if len(pnls) == 0 {
		return m
	}
	var wins, losses int
	var grossWins, grossLosses float64
	var totalWinR, totalLossR float64
	var winRCount, lossRCount int

	for _, p := range pnls {
		if p > 0 {
			wins++
			grossWins += p
		} else {
			losses++
			grossLosses += math.Abs(p)
		}
	}
	m.WinRate = float64(wins) / float64(len(pnls)) * 100
	if grossLosses > 0 {
		m.ProfitFactor = grossWins / grossLosses
	} else if grossWins > 0 {
		m.ProfitFactor = math.Inf(1)
	}
	m.Expectancy = (grossWins - grossLosses) / float64(len(pnls))
	if winRCount > 0 {
		m.AvgWinR = totalWinR / float64(winRCount)
	}
	if lossRCount > 0 {
		m.AvgLossR = totalLossR / float64(lossRCount)
	}
	return m
}

func periodStart(period string) time.Time {
	now := time.Now()
	switch period {
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "week":
		return now.AddDate(0, 0, -7)
	default:
		return time.Time{}
	}
}

// GetWins returns number of winning trades (deduced from win rate and total).
func (m *PaperMetrics) GetWins() int {
	if m.WinRate > 0 && m.TotalTrades > 0 {
		return int(float64(m.TotalTrades) * m.WinRate / 100.0)
	}
	return 0
}

// GetLosses returns number of losing trades.
func (m *PaperMetrics) GetLosses() int { return m.TotalTrades - m.GetWins() }
