package analyzer

import (
	"strings"
	"testing"
)

func TestRenderHeaderIncludesTargetAndResumeState(t *testing.T) {
	state := ProgressState{
		Mode:           "org",
		Target:         "acme",
		ResumeEnabled:  true,
		Workers:        6,
		RateLimitFloor: 200,
	}

	header := RenderHeader(state, 80, true)
	if header == "" {
		t.Fatal("expected non-empty header")
	}
	if !containsAll(header, "inactivity", "acme", "resume", "workers=6", "floor=200") {
		t.Fatalf("unexpected header: %q", header)
	}
}

func TestRenderProgressShowsLiveCounters(t *testing.T) {
	state := ProgressState{
		TotalRepos:       100,
		CompletedRepos:   40,
		CachedRepos:      15,
		RevalidatedRepos: 5,
		FailedRepos:      2,
		ActiveWorkers:    4,
		Phase:            "scan",
	}

	line := RenderProgressLine(state)
	if !containsAll(line, "40/100", "cached=15", "revalidated=5", "failed=2", "workers=4", "phase=scan") {
		t.Fatalf("unexpected progress line: %q", line)
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
