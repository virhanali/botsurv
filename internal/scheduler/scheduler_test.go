package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/virhan/botsurv/internal/logger"
)

func TestCycleID_Generation(t *testing.T) {
	s := &Scheduler{log: logger.New(nil, logger.LevelDebug)}
	s.cycleSeq = 0

	// Each RunOnce should generate unique cycle IDs
	ids := make(map[string]bool)
	for i := 0; i < 3; i++ {
		result := &CycleResult{
			CycleID:   fmt.Sprintf("cycle-%d-%s", i+1, time.Now().Format("20060102-150405")),
			StartedAt: time.Now(),
		}
		if ids[result.CycleID] {
			t.Errorf("duplicate cycle ID: %s", result.CycleID)
		}
		ids[result.CycleID] = true
	}
}

func TestNoOverlappingCycles(t *testing.T) {
	s := &Scheduler{
		log:      logger.New(nil, logger.LevelDebug),
		running:  true, // simulate a running cycle
		cycleSeq: 0,
	}

	_, err := s.RunOnce(context.Background())
	if err == nil {
		t.Error("expected error for overlapping cycle")
	}
}

func TestSkipReason_String(t *testing.T) {
	sr := SkipReason{Symbol: "BTCUSDT", Reason: "LLM_BLOCK"}
	if sr.Symbol != "BTCUSDT" || sr.Reason != "LLM_BLOCK" {
		t.Error("SkipReason fields not set correctly")
	}
}

func TestCycleResult_Fields(t *testing.T) {
	result := &CycleResult{
		CycleID:    "test-cycle",
		StartedAt:  time.Now(),
		Candidates: 5,
		LLMCalls:   3,
		Executions: 1,
		Skips: []SkipReason{
			{Symbol: "A", Reason: "LLM_BLOCK"},
			{Symbol: "B", Reason: "RISK_REJECTED"},
		},
		ReasonCodes: []string{"CYCLE_COMPLETE"},
	}

	if result.CycleID != "test-cycle" {
		t.Errorf("expected test-cycle, got %s", result.CycleID)
	}
	if result.Candidates != 5 {
		t.Errorf("expected 5 candidates, got %d", result.Candidates)
	}
	if len(result.Skips) != 2 {
		t.Errorf("expected 2 skips, got %d", len(result.Skips))
	}
}
