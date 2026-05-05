package llm

import (
	"encoding/json"
	"fmt"

	"github.com/virhan/botsurv/internal/domain"
)

// BuildVetoContext creates a compact structured JSON for LLM veto request.
// Uses encoding/json to produce valid JSON for quoted/special string inputs.
func BuildVetoContext(req VetoRequestInput) string {
	m := map[string]interface{}{
		"schema_version": "2.1",
		"request_id":     req.RequestID,
		"candidate_id":   req.CandidateID,
		"symbol":         req.Symbol,
		"side":           req.Side,
		"strategy":       req.Strategy,
		"entry_price":    req.EntryPrice,
		"stop_loss":      req.StopLoss,
		"rr":             req.RR,
		"score":          req.Score,
		"setup_quality":  req.SetupQuality,
		"regime":         req.Regime,
		"btc_trend":      req.BTCTrend,
		"btcd_trend":     req.BTCDTrend,
		"rs_1h":          req.RS1h,
		"rs_4h":          req.RS4h,
		"volume_ratio":   req.VolumeRatio,
		"atr_pct":        req.ATRPct,
	}
	if req.TakeProfit > 0 {
		m["take_profit"] = req.TakeProfit
	}
	if len(req.Warnings) > 0 {
		m["warnings"] = req.Warnings
	}
	if len(req.AmbiguityFlags) > 0 {
		m["ambiguity_flags"] = req.AmbiguityFlags
	}
	data, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// VetoRequestInput holds the data to build a veto context.
type VetoRequestInput struct {
	RequestID      string
	CandidateID    string
	Symbol         string
	Side           string
	Strategy       string
	EntryPrice     float64
	StopLoss       float64
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

// validSizeMultipliers is the set of allowed size_multiplier values.
var validSizeMultipliers = map[float64]bool{1.0: true, 0.75: true, 0.5: true, 0.25: true, 0.0: true}

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

	d := resp.ApplyToDecision()

	// Confidence >= 0.75 required
	if d.Confidence < 0.75 {
		return domain.LLMDecision{
			Decision:         "BLOCK",
			Confidence:       d.Confidence,
			SizeMultiplier:   0,
			ReasonCodes:      []string{"LOW_CONFIDENCE"},
			ValidationStatus: "low_confidence",
			RawResponse:      rawJSON,
		}, fmt.Errorf("parse veto response: confidence %.2f below minimum 0.75", d.Confidence)
	}

	// Size multiplier must be exactly one of the allowed values
	if d.Decision != "BLOCK" && !validSizeMultipliers[d.SizeMultiplier] {
		return domain.LLMDecision{
			Decision:         "BLOCK",
			Confidence:       0,
			SizeMultiplier:   0,
			ReasonCodes:      []string{"INVALID_SIZE_MULTIPLIER"},
			ValidationStatus: "invalid_multiplier",
			RawResponse:      rawJSON,
		}, fmt.Errorf("parse veto response: invalid size_multiplier %.2f", d.SizeMultiplier)
	}

	// Store raw response on success
	d.RawResponse = rawJSON
	d.ValidationStatus = "valid"
	return d, nil
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
