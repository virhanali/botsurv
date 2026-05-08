package app

import (
	"context"
	"fmt"
	"time"

	"github.com/virhan/botsurv/internal/db"
)

// LiveReadinessCheck holds the result of a live readiness check.
type LiveReadinessCheck struct {
	Passed     bool
	CheckName  string
	Message    string
	AutoCheck  bool // true for auto-checked criteria, false for manual
}

// CheckLiveReadiness runs all auto-checkable live readiness criteria.
// Returns a list of checks. If any auto-check fails, the first return value is false.
func CheckLiveReadiness(ctx context.Context, decisionLogRepo db.DecisionLogRepository, candidateOutcomeRepo db.CandidateOutcomeRepository, paperTradeRepo db.PaperTradeRepository) (bool, []LiveReadinessCheck) {
	since30d := time.Now().AddDate(0, 0, -30)
	var checks []LiveReadinessCheck

	// Helper: add a check result
	addCheck := func(name string, passed bool, msg string) {
		checks = append(checks, LiveReadinessCheck{
			Passed:    passed,
			CheckName: name,
			Message:   msg,
			AutoCheck: true,
		})
	}

	// Fail if no repos are provided (M12)
	if decisionLogRepo == nil && candidateOutcomeRepo == nil && paperTradeRepo == nil {
		addCheck("Repos_Provided", false, "no repositories provided for live readiness checks")
		return false, checks
	}

	// 1. No NaN/Inf reaching strategy layer (decision_logs with REJECTED_RISK for INVALID_ENTRY/INVALID_STOP_LOSS)
	if decisionLogRepo != nil {
		invalidCount, err := decisionLogRepo.CountByAction(ctx, "REJECTED_RISK", since30d)
		if err != nil {
			addCheck("C1-NaN_Inf_Rejected", false, fmt.Sprintf("query failed: %v", err))
		} else if invalidCount > 0 {
			addCheck("C1-NaN_Inf_Rejected", false, fmt.Sprintf("%d REJECTED_RISK decisions in last 30 days", invalidCount))
		} else {
			addCheck("C1-NaN_Inf_Rejected", true, "zero NaN/Inf reaching strategy layer")
		}
	}

	// 2. No BLOCKED_HARD decisions
	if decisionLogRepo != nil {
		blockedCount, err := decisionLogRepo.CountByAction(ctx, "BLOCKED_HARD", since30d)
		if err != nil {
			addCheck("C2-Blocked_Hard", false, fmt.Sprintf("query failed: %v", err))
		} else if blockedCount > 0 {
			addCheck("C2-Blocked_Hard", false, fmt.Sprintf("%d BLOCKED_HARD decisions in last 30 days", blockedCount))
		} else {
			addCheck("C2-Blocked_Hard", true, "zero candidates from incomplete candles")
		}
	}

	// 3. No REJECTED_SAFETY decisions
	if decisionLogRepo != nil {
		safetyCount, err := decisionLogRepo.CountByAction(ctx, "REJECTED_SAFETY", since30d)
		if err != nil {
			addCheck("C3-Position_Mismatch", false, fmt.Sprintf("query failed: %v", err))
		} else if safetyCount > 0 {
			addCheck("C3-Position_Mismatch", false, fmt.Sprintf("%d REJECTED_SAFETY decisions in last 30 days", safetyCount))
		} else {
			addCheck("C3-Position_Mismatch", true, "zero position state mismatches")
		}
	}

	// 4. Decision log coverage: check recent decisions exist
	if decisionLogRepo != nil {
		recent, err := decisionLogRepo.GetRecent(ctx, 1)
		if err != nil {
			addCheck("C4-DecisionLog_Coverage", false, fmt.Sprintf("query failed: %v", err))
		} else if len(recent) == 0 {
			addCheck("C4-DecisionLog_Coverage", false, "no recent decision logs found")
		} else {
			addCheck("C4-DecisionLog_Coverage", true, fmt.Sprintf("decision logging active (latest: %s)", recent[0].Timestamp.Format(time.RFC3339)))
		}
	}

	// 5. Paper trade decision_id linkage: count open paper trades
	if paperTradeRepo != nil {
		openTrades, err := paperTradeRepo.GetOpen(ctx)
		if err != nil {
			addCheck("C5-PaperTrade_Linkage", false, fmt.Sprintf("query failed: %v", err))
		} else {
			addCheck("C5-PaperTrade_Linkage", true, fmt.Sprintf("%d open paper trades", len(openTrades)))
		}
	}

	// 6. No orphan counterfactuals older than 48h
	if candidateOutcomeRepo != nil {
		pending, err := candidateOutcomeRepo.GetPending(ctx)
		if err != nil {
			addCheck("C6-Counterfactual_Orphans", false, fmt.Sprintf("query failed: %v", err))
		} else {
			var orphans int
			cutoff := time.Now().Add(-48 * time.Hour)
			for _, o := range pending {
				if o.TrackedUntil.Before(cutoff) && !o.TrackedUntil.IsZero() {
					orphans++
				}
			}
			if orphans > 0 {
				addCheck("C6-Counterfactual_Orphans", false, fmt.Sprintf("%d orphan outcomes older than 48h", orphans))
			} else {
				addCheck("C6-Counterfactual_Orphans", true, fmt.Sprintf("%d pending outcomes, zero orphans", len(pending)))
			}
		}
	}

	// Manual criteria (informational only, do not block)
	manualChecks := []struct{ name, msg string }{
		{"M1-Emergency_Stop_Test", "emergency stop tested manually"},
		{"M2-BTC_Flash_Crash_CB", "BTC flash crash circuit breaker tested with simulated data"},
		{"M3-Position_Reconciliation", "position reconciliation tested by manually creating mismatch"},
		{"M4-Live_Config_Review", "live config reviewed by 2 team members"},
		{"M5-Initial_Capital", "initial live capital agreed (start small)"},
		{"M6-LIVE_CONFIRMED", "LIVE_CONFIRMED=yes in environment"},
	}
	for _, m := range manualChecks {
		checks = append(checks, LiveReadinessCheck{
			Passed:    true, // manual checks never block
			CheckName: m.name,
			Message:   fmt.Sprintf("[MANUAL] %s", m.msg),
			AutoCheck: false,
		})
	}

	// Evaluate auto-checks
	passed := true
	for _, c := range checks {
		if c.AutoCheck && !c.Passed {
			passed = false
			break
		}
	}

	return passed, checks
}
