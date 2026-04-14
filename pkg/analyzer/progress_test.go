package analyzer

import (
	"strings"
	"testing"
	"time"
)

func TestRenderHeaderAdaptsToWidthAndColorState(t *testing.T) {
	state := ProgressState{
		Mode:           "org",
		Target:         "acme",
		ResumeEnabled:  true,
		Workers:        6,
		RateLimitFloor: 200,
	}

	wide := RenderHeader(state, 80, true)
	if !containsAll(wide, "\x1b[36minactivity\x1b[0m", "acme", "\n", "resume=true", "workers=6", "floor=200") {
		t.Fatalf("unexpected wide header: %q", wide)
	}
	if strings.Count(wide, "\n") != 1 {
		t.Fatalf("expected banner layout to use one newline, got %q", wide)
	}

	narrow := RenderHeader(state, 60, false)
	if strings.Contains(narrow, "\x1b[") {
		t.Fatalf("did not expect color codes in narrow header: %q", narrow)
	}
	if strings.Contains(narrow, "\n") {
		t.Fatalf("expected compact layout without newline, got %q", narrow)
	}
	if !containsAll(narrow, "inactivity", "acme", "resume=true", "workers=6", "floor=200") {
		t.Fatalf("unexpected narrow header: %q", narrow)
	}
}

func TestRenderProgressShowsLiveCounters(t *testing.T) {
	state := ProgressState{
		TotalRepos:           100,
		CompletedRepos:       40,
		CachedRepos:          15,
		RevalidatedRepos:     5,
		FailedRepos:          2,
		ActiveWorkers:        1,
		WorkerRecommendation: 4,
		RateLimitRemaining:   137,
		RateLimitResetAt:     time.Date(2026, 4, 14, 11, 0, 0, 0, time.UTC),
		Phase:                "scan",
	}

	line := RenderProgressLine(state)
	if !containsAll(line, "40/100", "cached=15", "revalidated=5", "failed=2", "active=1", "worker_target=4", "rate_remaining=137", "rate_reset=2026-04-14T11:00:00Z", "phase=scan") {
		t.Fatalf("unexpected progress line: %q", line)
	}
}

func TestRenderProgressShowsFallbackRateLimit(t *testing.T) {
	state := ProgressState{
		CompletedRepos:       3,
		TotalRepos:           10,
		ActiveWorkers:        1,
		WorkerRecommendation: 2,
		RateLimitRemaining:   25,
		Phase:                "scan",
	}

	line := RenderProgressLine(state)
	if !containsAll(line, "3/10", "rate_remaining=25", "phase=scan") {
		t.Fatalf("unexpected progress line: %q", line)
	}
	if strings.Contains(line, "rate_reset=") {
		t.Fatalf("did not expect reset timestamp in fallback progress line: %q", line)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
