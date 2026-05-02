package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/virhan/botsurv/internal/domain"
)

// BuildVetoContext creates a compact structured JSON for LLM veto request.
func BuildVetoContext(req VetoRequestInput) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`{"schema_version":"2.1","request_id":"%s","candidate_id":"%s","symbol":"%s","side":"%s","strategy":"%s",`, req.RequestID, req.CandidateID, req.Symbol, req.Side, req.Strategy))
	b.WriteString(fmt.Sprintf(`"entry_price":%.4f,"stop_loss":%.4f,`, req.EntryPrice, req.StopLoss))
	if req.TakeProfit > 0 {
		b.WriteString(fmt.Sprintf(`"take_profit":%.4f,`, req.TakeProfit))
	}
	b.WriteString(fmt.Sprintf(`"rr":%.2f,"score":%.1f,"setup_quality":%.1f,"regime":"%s",`, req.RR, req.Score, req.SetupQuality, req.Regime))
	b.WriteString(fmt.Sprintf(`"btc_trend":"%s","btcd_trend":"%s","rs_1h":%.1f,"rs_4h":%.1f,"volume_ratio":%.2f,"atr_pct":%.2f`, req.BTCTrend, req.BTCDTrend, req.RS1h, req.RS4h, req.VolumeRatio, req.ATRPct))

	if len(req.Warnings) > 0 {
		b.WriteString(fmt.Sprintf(`,"warnings":["%s"]`, strings.Join(req.Warnings, `","`)))
	}
	if len(req.AmbiguityFlags) > 0 {
		b.WriteString(fmt.Sprintf(`,"ambiguity_flags":["%s"]`, strings.Join(req.AmbiguityFlags, `","`)))
	}
	b.WriteString("}")
	return b.String()
}

// VetoRequestInput holds the data to build a veto context.
type VetoRequestInput struct {
	RequestID      string
	CandidateID    string
	Symbol         string
	Side           string
	Strategy       string
	EntryPrice     float64
	StopLoss      float64
	TakeProfit     float64
	RR             float64
	Score          float64
	SetupQuality   float64
	Regime         string
	BTCTrend       string
	BTCDTrend      string
	RS1h           float64
	RS4h           float64
	VolumeRatio    float64
	ATRPct         float64
	Warnings       []string
	AmbiguityFlags []string
}

// ParseVetoResponse parses raw LLM JSON and validates it.
// Returns a domain.LLMDecision suitable for the risk engine.
func ParseVetoResponse(rawJSON string) (domain.LLMDecision, error) {
	resp, err := parseVetoJSON(rawJSON)
	if err != nil {
		return domain.LLMDecision{
			Decision:         "BLOCK",
			Confidence:       0,
			SizeMultiplier:   0,
			ReasonCodes:      []string{"PARSE_ERROR"},
			ValidationStatus: "invalid_json",
			RawResponse:      rawJSON,
		}, fmt.Errorf("parse veto response: %w", err)
	}
	return resp.ApplyToDecision(), nil
}

func parseVetoJSON(raw string) (*ReviewResponse, error) {
	var resp ReviewResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, &ValidationError{Reason: "JSON_PARSE_FAILED", Detail: err.Error()}
	}
	if resp.SchemaVersion == "" {
		return nil, &ValidationError{Reason: "MISSING_FIELD", Detail: "schema_version"}
	}
	if resp.Review.Action == "" {
		return nil, &ValidationError{Reason: "MISSING_FIELD", Detail: "review.action"}
	}
	if !ValidReviewActions[resp.Review.Action] {
		return nil, &ValidationError{Reason: "INVALID_ACTION", Detail: string(resp.Review.Action)}
	}
	if resp.Review.Confidence < 0 || resp.Review.Confidence > 100 {
		return nil, &ValidationError{Reason: "INVALID_CONFIDENCE", Detail: fmt.Sprintf("%.1f", resp.Review.Confidence)}
	}
	sm := resp.RecommendedAdjustment.SizeMultiplier
	if sm < 0 || sm > 1.0 {
		return nil, &ValidationError{Reason: "INVALID_SIZE_MULTIPLIER", Detail: fmt.Sprintf("%.4f", sm)}
	}
	if resp.Review.Action == ActionReject && (resp.RejectionReason == nil || *resp.RejectionReason == "") {
		return nil, &ValidationError{Reason: "MISSING_REJECTION_REASON", Detail: "required when REJECT"}
	}
	return &resp, nil
}

// ApplyToDecision converts the older ReviewResponse to domain.LLMDecision.
func (r *ReviewResponse) ApplyToDecision() domain.LLMDecision {
	conf := r.Review.Confidence
	if conf > 1.0 {
		conf = conf / 100.0
	}
	d := domain.LLMDecision{
		Confidence:     conf,
		SizeMultiplier: 1.0,
		ReasonCodes:    []string{r.Review.ReasonSummary},
		RawResponse:    "",
	}
	sm := r.RecommendedAdjustment.SizeMultiplier
	if sm > 0 && sm <= 1.0 {
		d.SizeMultiplier = sm
	}
	switch r.Review.Action {
	case ActionApprove:
		d.Decision = "ALLOW_MARKET"
	case ActionApproveRetestOnly:
		d.Decision = "ALLOW_LIMIT_RETEST"
	case ActionReduceSize:
		d.Decision = "REDUCE_SIZE"
		if d.SizeMultiplier > 0.5 {
			d.SizeMultiplier = 0.5
		}
	case ActionReject:
		d.Decision = "BLOCK"
		if r.RejectionReason != nil {
			d.ReasonCodes = append(d.ReasonCodes, *r.RejectionReason)
		}
	}
	return d
}
