package strategy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/virhan/botsurv/internal/domain"
)

// StrategyName identifies the setup generator.
type StrategyName string

const (
	StrategyTrendPullback  StrategyName = "trend_pullback"
	StrategyBreakoutRetest StrategyName = "breakout_retest"
)

// CandidateEntryType is strategy-level entry type.
type CandidateEntryType string

const (
	CandidateEntryMarket      CandidateEntryType = "market"
	CandidateEntryLimitRetest CandidateEntryType = "limit_retest"
	CandidateEntryStop        CandidateEntryType = "stop"
)

// TakeProfitTarget defines one take-profit leg.
type TakeProfitTarget struct {
	Price   float64 `json:"price"`
	SizePct float64 `json:"size_pct"`
}

// TAReasoning stores compact explainability fields.
type TAReasoning struct {
	TrendContext string `json:"trend_context"`
	Trigger      string `json:"trigger"`
	Confirmation string `json:"confirmation"`
	Invalidation string `json:"invalidation"`
}

// SnapshotRefs references logged indicator/regime snapshots.
type SnapshotRefs struct {
	IndicatorSnapshotRef string `json:"indicator_snapshot_ref"`
	RegimeSnapshotRef    string `json:"regime_snapshot_ref"`
}

// TradeCandidate is the typed output of the phase-3 strategy engine.
type TradeCandidate struct {
	CandidateID       string             `json:"candidate_id"`
	GeneratedAt       time.Time          `json:"generated_at"`
	Symbol            string             `json:"symbol"`
	Timeframe         string             `json:"timeframe"`
	Strategy          StrategyName       `json:"strategy"`
	Side              domain.Side        `json:"side"`
	EntryType         CandidateEntryType `json:"entry_type"`
	EntryPrice        float64            `json:"entry_price"`
	StopLoss          float64            `json:"stop_loss"`
	TakeProfits       []TakeProfitTarget `json:"take_profits"`
	RiskRewardRatio   float64            `json:"risk_reward_ratio"`
	InvalidationLevel float64            `json:"invalidation_level"`
	TAReasoning       TAReasoning        `json:"ta_reasoning"`
	InputsSnapshot    SnapshotRefs       `json:"inputs_snapshot"`
}

// RejectedCandidate captures near-miss information.
type RejectedCandidate struct {
	WouldHaveBeen string `json:"would_have_been"`
	FailedOn      string `json:"failed_on"`
	NearMiss      bool   `json:"near_miss"`
}

// NewCandidateID generates a UUIDv4 string without external dependencies.
func NewCandidateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	), nil
}

// BuildRejectedCandidate standardizes near-miss output.
func BuildRejectedCandidate(strategy StrategyName, side domain.Side, symbol, timeframe, failed string) RejectedCandidate {
	return RejectedCandidate{
		WouldHaveBeen: fmt.Sprintf("%s %s %s %s", strategy, side, symbol, timeframe),
		FailedOn:      failed,
		NearMiss:      true,
	}
}
