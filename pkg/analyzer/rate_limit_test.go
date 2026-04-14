package analyzer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/harekrishnarai/inactivity/pkg/config"
)

func TestRateLimiterRequestsPauseAtConfiguredFloor(t *testing.T) {
	rl := NewRateLimiter(200)
	rl.Update(RateLimitState{
		Remaining: 180,
		ResetAt:   time.Date(2026, 4, 14, 11, 0, 0, 0, time.UTC),
	})

	if !rl.ShouldPause() {
		t.Fatal("expected pause when remaining budget is below floor")
	}
}

func TestRateLimiterRequestsPauseAtConfiguredFloorBoundary(t *testing.T) {
	rl := NewRateLimiter(200)
	rl.Update(RateLimitState{
		Remaining: 200,
		ResetAt:   time.Date(2026, 4, 14, 11, 0, 0, 0, time.UTC),
	})

	if !rl.ShouldPause() {
		t.Fatal("expected pause when remaining budget matches the floor")
	}
}

func TestRecommendedWorkersDropsNearFloor(t *testing.T) {
	rl := NewRateLimiter(200)
	rl.Update(RateLimitState{Remaining: 230})

	if got := rl.RecommendedWorkers(8); got >= 8 {
		t.Fatalf("expected reduced workers near floor, got %d", got)
	}
}

func TestRateLimiterShouldPollCadence(t *testing.T) {
	rl := NewRateLimiter(200)

	cases := []struct {
		index int
		want  bool
	}{
		{index: 0, want: true},
		{index: 1, want: false},
		{index: 9, want: false},
		{index: 10, want: true},
		{index: 19, want: false},
		{index: 20, want: true},
	}

	for _, tc := range cases {
		if got := rl.ShouldPoll(tc.index); got != tc.want {
			t.Fatalf("ShouldPoll(%d) = %t, want %t", tc.index, got, tc.want)
		}
	}
}

func TestRateLimiterRetainsLastKnownStateWhenRefreshFails(t *testing.T) {
	rl := NewRateLimiter(200)
	rl.Update(RateLimitState{Remaining: 190})
	rl.UseLastKnownOrFallback()

	if !rl.ShouldPause() {
		t.Fatal("expected pause to continue when refresh fails after a low remaining budget")
	}
}

func TestRateLimiterFallsBackConservativelyWithoutKnownState(t *testing.T) {
	rl := NewRateLimiter(200)
	rl.UseLastKnownOrFallback()

	if !rl.ShouldPause() {
		t.Fatal("expected conservative pause when rate limit state is unavailable")
	}
}

func TestAnalyzeRepositoriesPersistsCheckpointOnRateLimitPause(t *testing.T) {
	originalWatch := interruptWatcher
	originalFactory := gitHubClientFactory
	defer func() {
		interruptWatcher = originalWatch
		gitHubClientFactory = originalFactory
	}()
	interruptWatcher = func(context.CancelFunc) func() { return func() {} }
	gitHubClientFactory = func(context.Context) (GitHubClient, error) {
		return fakeGitHubClient{
			reposByPage: map[int][]string{
				1: {"service-a"},
			},
			rateLimitState: RateLimitState{
				Limit:     5000,
				Remaining: 200,
				ResetAt:   time.Date(2026, 4, 14, 11, 0, 0, 0, time.UTC),
			},
		}, nil
	}

	cfg := config.Default()
	cfg.Organization = "acme"
	cfg.Resume = true
	cfg.Silent = true
	cfg.CacheDir = filepath.Join(t.TempDir(), "cache")
	cfg.CheckpointDir = filepath.Join(t.TempDir(), "checkpoints")

	results, err := AnalyzeRepositories(cfg)
	if !errors.Is(err, ErrRateLimitPause) {
		t.Fatalf("expected rate-limit pause, got %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no completed results before pause, got %d", len(results))
	}

	checkpoint, loadErr := NewCheckpointStore(cfg.CheckpointDir).LoadLatest(cfg.Organization)
	if loadErr != nil {
		t.Fatalf("LoadLatest returned error: %v", loadErr)
	}
	if len(checkpoint.Pending) != 1 || checkpoint.Pending[0] != "service-a" {
		t.Fatalf("unexpected pending repos after pause: %+v", checkpoint.Pending)
	}
	if len(checkpoint.InProgress) != 0 {
		t.Fatalf("expected no in-progress repo after pause: %+v", checkpoint.InProgress)
	}
	if checkpoint.Progress.Phase != "scan" {
		t.Fatalf("unexpected checkpoint phase: %+v", checkpoint.Progress)
	}
	if checkpoint.Progress.ActiveWorkers != 0 {
		t.Fatalf("expected checkpoint to record zero active workers while paused, got %+v", checkpoint.Progress)
	}
	if checkpoint.Progress.WorkerRecommendation != 0 {
		t.Fatalf("expected checkpoint to record zero recommended workers while paused, got %+v", checkpoint.Progress)
	}
}

func TestAnalyzeRepositoriesDoesNotReturnPauseWhenCheckpointSaveFails(t *testing.T) {
	originalWatch := interruptWatcher
	originalFactory := gitHubClientFactory
	defer func() {
		interruptWatcher = originalWatch
		gitHubClientFactory = originalFactory
	}()
	interruptWatcher = func(context.CancelFunc) func() { return func() {} }
	gitHubClientFactory = func(context.Context) (GitHubClient, error) {
		return fakeGitHubClient{
			reposByPage: map[int][]string{
				1: {"service-a"},
			},
			rateLimitState: RateLimitState{
				Limit:     5000,
				Remaining: 200,
				ResetAt:   time.Date(2026, 4, 14, 11, 0, 0, 0, time.UTC),
			},
		}, nil
	}

	root := filepath.Join(t.TempDir(), "checkpoint-root")
	if err := os.WriteFile(root, []byte("block"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	cfg := config.Default()
	cfg.Organization = "acme"
	cfg.Resume = false
	cfg.Silent = true
	cfg.CacheDir = filepath.Join(t.TempDir(), "cache")
	cfg.CheckpointDir = root
	cfg.RateLimitFloor = 200

	_, err := AnalyzeRepositories(cfg)
	if err == nil {
		t.Fatal("expected checkpoint save failure")
	}
	if errors.Is(err, ErrRateLimitPause) {
		t.Fatalf("expected checkpoint save failure, got pause: %v", err)
	}
}
