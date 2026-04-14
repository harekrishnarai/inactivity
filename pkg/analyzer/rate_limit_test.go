package analyzer

import (
	"testing"
	"time"
)

func TestRateLimiterRequestsPauseAtConfiguredFloor(t *testing.T) {
	rl := NewRateLimiter(200)
	rl.Update(RateLimitState{
		Remaining: 180,
		ResetAt:    time.Date(2026, 4, 14, 11, 0, 0, 0, time.UTC),
	})

	if !rl.ShouldPause() {
		t.Fatal("expected pause when remaining budget is below floor")
	}
}

func TestRecommendedWorkersDropsNearFloor(t *testing.T) {
	rl := NewRateLimiter(200)
	rl.Update(RateLimitState{Remaining: 230})

	if got := rl.RecommendedWorkers(8); got >= 8 {
		t.Fatalf("expected reduced workers near floor, got %d", got)
	}
}
