package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidationError describes why an LLM response was invalid.
type ValidationError struct {
	Reason string
	Detail string
}

func (e *ValidationError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s: %s", e.Reason, e.Detail)
	}
	return e.Reason
}

// ValidateReviewResponse validates an LLM review response against the strict schema.
// Returns nil if valid, or an error describing the first validation failure.
func ValidateReviewResponse(rawJSON string, mode LMReviewMode, originalCandidate *ReviewCandidate) (*ReviewResponse, error) {
	// 1. Parse JSON.
	var resp ReviewResponse
	if err := json.Unmarshal([]byte(rawJSON), &resp); err != nil {
		return nil, &ValidationError{
			Reason: "JSON_PARSE_FAILED",
			Detail: err.Error(),
		}
	}

	// 2. Check required fields.
	if resp.SchemaVersion == "" {
		return nil, &ValidationError{Reason: "MISSING_FIELD", Detail: "schema_version"}
	}
	if resp.Review.Action == "" {
		return nil, &ValidationError{Reason: "MISSING_FIELD", Detail: "review.action"}
	}

	// 3. Check action is in allowed set for this mode.
	allowed := AllowedActionsForMode(mode)
	if !isActionAllowed(ReviewAction(resp.Review.Action), allowed) {
		return nil, &ValidationError{
			Reason: "FORBIDDEN_ACTION",
			Detail: fmt.Sprintf("action %q not allowed in mode %s (allowed: %v)", resp.Review.Action, mode, allowed),
		}
	}

	// 4. Check size_multiplier is in (0, 1.0].
	sm := resp.RecommendedAdjustment.SizeMultiplier
	if sm <= 0 || sm > 1.0 {
		return nil, &ValidationError{
			Reason: "INVALID_SIZE_MULTIPLIER",
			Detail: fmt.Sprintf("size_multiplier %.4f not in range (0, 1.0]", sm),
		}
	}

	// 5. Check no forbidden field changes.
	if originalCandidate != nil {
		if err := detectForbiddenChanges(resp, originalCandidate); err != nil {
			return nil, err
		}
	}

	// 6. Check reason_summary length.
	if len(resp.Review.ReasonSummary) > 200 {
		resp.Review.ReasonSummary = resp.Review.ReasonSummary[:200]
	}

	// 7. Validate confidence range.
	if resp.Review.Confidence < 0 || resp.Review.Confidence > 100 {
		return nil, &ValidationError{
			Reason: "INVALID_CONFIDENCE",
			Detail: fmt.Sprintf("confidence %.1f not in range [0, 100]", resp.Review.Confidence),
		}
	}

	return &resp, nil
}

func isActionAllowed(action ReviewAction, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(string(action), a) {
			return true
		}
	}
	return false
}

func detectForbiddenChanges(resp ReviewResponse, original *ReviewCandidate) error {
	// The LLM MUST NOT change these fields. We check the response for any
	// indication that it tried to override them.
	// Since the response schema does NOT include candidate fields, the LLM
	// cannot change them structurally. This check is defense-in-depth.
	// If any recommended_adjustment field suggests changing entry/side/SL/TP,
	// we reject it.

	// The recommended_adjustment only has size_multiplier, entry_mode, do_not_chase.
	// entry_mode must not change the entry_type to something invalid.
	if resp.RecommendedAdjustment.EntryMode != "" {
		validModes := map[string]bool{"market": true, "limit_retest": true, "": true}
		if !validModes[strings.ToLower(resp.RecommendedAdjustment.EntryMode)] {
			return &ValidationError{
				Reason: "FORBIDDEN_CHANGE",
				Detail: fmt.Sprintf("entry_mode %q is not allowed", resp.RecommendedAdjustment.EntryMode),
			}
		}
	}

	return nil
}

// TruncateReasonSummary truncates a reason summary to 200 chars.
func TruncateReasonSummary(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
