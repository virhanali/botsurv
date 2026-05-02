package strategy

import (
	"regexp"
	"testing"

	"github.com/virhan/botsurv/internal/domain"
)

func TestNewCandidateID_UUIDFormat(t *testing.T) {
	id, err := NewCandidateID()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	re := regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
	if !re.MatchString(id) {
		t.Fatalf("expected UUIDv4 format, got %s", id)
	}
}

func TestBuildRejectedCandidate(t *testing.T) {
	r := BuildRejectedCandidate(StrategyTrendPullback, domain.SideLong, "ORDIUSDT", "15m", "RSI too high")
	if !r.NearMiss {
		t.Fatal("expected near_miss true")
	}
	if r.WouldHaveBeen == "" || r.FailedOn == "" {
		t.Fatalf("unexpected rejection: %+v", r)
	}
}
