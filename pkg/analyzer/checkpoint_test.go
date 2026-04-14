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

func TestCheckpointRoundTripPreservesCompletedRepos(t *testing.T) {
	store := NewCheckpointStore(t.TempDir())
	now := time.Date(2026, time.April, 14, 10, 0, 0, 0, time.UTC)
	original := Checkpoint{
		RunID:      "run-123",
		Target:     "acme",
		StartedAt:  now,
		UpdatedAt:  now,
		Pending:    []string{"service-b", "service-c"},
		InProgress: []string{"acme/service-b"},
		Completed: map[string]RepoSnapshot{
			"acme/service-a": {
				Repository:           "acme/service-a",
				Archived:             true,
				LastCommitDate:       now.Add(-24 * time.Hour),
				DaysSinceLastCommit:  1,
				TotalContributors:    3,
				InactiveContributors: 2,
				InactivePercentage:   2.0 / 3.0,
				Flagged:              true,
				FetchedAt:            now,
			},
		},
		Failed: map[string]string{
			"acme/service-c": "rate limited",
		},
		Progress: ProgressState{
			Mode:                 "org",
			Target:               "acme",
			ResumeEnabled:        true,
			Workers:              4,
			RateLimitFloor:       10,
			TotalRepos:           3,
			CompletedRepos:       1,
			FailedRepos:          1,
			ActiveWorkers:        1,
			WorkerRecommendation: 2,
			Phase:                "scan",
		},
	}

	if err := store.Save(original); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	loaded, err := store.LoadLatest("acme")
	if err != nil {
		t.Fatalf("LoadLatest returned error: %v", err)
	}
	if _, ok := loaded.Completed["acme/service-a"]; !ok {
		t.Fatalf("missing completed repo in loaded checkpoint: %+v", loaded)
	}
	if len(loaded.Pending) != 2 || loaded.Pending[0] != "service-b" || loaded.Pending[1] != "service-c" {
		t.Fatalf("unexpected pending repos: %+v", loaded.Pending)
	}
	if len(loaded.InProgress) != 1 || loaded.InProgress[0] != "acme/service-b" {
		t.Fatalf("unexpected in-progress repos: %+v", loaded.InProgress)
	}
	if msg := loaded.Failed["acme/service-c"]; msg != "rate limited" {
		t.Fatalf("unexpected failed repos: %+v", loaded.Failed)
	}
	if loaded.Progress != original.Progress {
		t.Fatalf("progress mismatch: got %+v want %+v", loaded.Progress, original.Progress)
	}
}

func TestLoadLatestIgnoresTempFiles(t *testing.T) {
	root := t.TempDir()
	store := NewCheckpointStore(root)

	original := Checkpoint{
		RunID:     "run-123",
		Target:    "acme",
		Completed: map[string]RepoSnapshot{"acme/service-a": {Repository: "acme/service-a"}},
	}
	if err := store.Save(original); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	tempPath := filepath.Join(root, cacheName("acme"), "run-999.json.123.tmp")
	if err := os.WriteFile(tempPath, []byte("not json"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(tempPath, future, future); err != nil {
		t.Fatalf("Chtimes returned error: %v", err)
	}

	loaded, err := store.LoadLatest("acme")
	if err != nil {
		t.Fatalf("LoadLatest returned error: %v", err)
	}
	if _, ok := loaded.Completed["acme/service-a"]; !ok {
		t.Fatalf("loaded checkpoint came from temp file: %+v", loaded)
	}
}

func TestResumeReconstructsStateFromOrgScanCheckpoint(t *testing.T) {
	checkpoint := Checkpoint{
		Pending:    []string{"service-b", "service-c"},
		InProgress: []string{"acme/service-b"},
		Completed: map[string]RepoSnapshot{
			"acme/service-a": {Repository: "acme/service-a", Flagged: true},
		},
		Failed: map[string]string{
			"acme/service-c": "contributors failed",
		},
	}
	allRepos := []string{"service-a", "service-b", "service-c", "service-d"}

	pending := resumePendingRepos(allRepos, checkpoint)
	if len(pending) != 3 || pending[0] != "service-b" || pending[1] != "service-c" || pending[2] != "service-d" {
		t.Fatalf("unexpected resumed pending repos: %+v", pending)
	}

	completed := completedRepositories(checkpoint, allRepos)
	if len(completed) != 1 || completed[0].Name != "acme/service-a" || !completed[0].Flagged {
		t.Fatalf("unexpected completed repos: %+v", completed)
	}

	failed := checkpointFailures(checkpoint, allRepos)
	if len(failed) != 1 || failed["service-c"] != "contributors failed" {
		t.Fatalf("unexpected checkpoint failures: %+v", failed)
	}
}

func TestLoadOrganizationReposWithCheckpointPersistsEnumerationStateOnGracefulStop(t *testing.T) {
	store := NewCheckpointStore(t.TempDir())
	progress := ProgressState{Mode: "org", Target: "acme", Phase: "enumerate"}
	checkpoint := Checkpoint{
		RunID:     "run-123",
		Target:    "acme",
		Completed: map[string]RepoSnapshot{},
		Failed:    map[string]string{},
		Progress:  progress,
	}

	ctx, cancel := context.WithCancel(context.Background())
	client := enumerationCheckpointClient{
		list: func(ctx context.Context, page int) ([]string, error) {
			switch page {
			case 1:
				cancel()
				return []string{"service-a"}, nil
			default:
				<-ctx.Done()
				return nil, ctx.Err()
			}
		},
	}

	_, err := loadOrganizationReposWithCheckpoint(ctx, client, "acme", store, &checkpoint, &progress)
	if !errors.Is(err, ErrGracefulStop) {
		t.Fatalf("loadOrganizationReposWithCheckpoint error = %v, want %v", err, ErrGracefulStop)
	}

	loaded, err := store.LoadLatest("acme")
	if err != nil {
		t.Fatalf("LoadLatest returned error: %v", err)
	}
	if len(loaded.Discovered) != 1 || loaded.Discovered[0] != "service-a" {
		t.Fatalf("unexpected discovered repos: %+v", loaded.Discovered)
	}
	if loaded.NextPage != 2 {
		t.Fatalf("NextPage = %d, want 2", loaded.NextPage)
	}
	if loaded.Progress.Phase != "enumerate" {
		t.Fatalf("unexpected phase: %+v", loaded.Progress)
	}
}

func TestLoadOrganizationReposWithCheckpointResumesFromSavedPage(t *testing.T) {
	store := NewCheckpointStore(t.TempDir())
	progress := ProgressState{Mode: "org", Target: "acme", Phase: "enumerate"}
	checkpoint := Checkpoint{
		RunID:      "run-123",
		Target:     "acme",
		Discovered: []string{"service-a"},
		NextPage:   2,
		Completed:  map[string]RepoSnapshot{},
		Failed:     map[string]string{},
		Progress:   progress,
	}

	client := enumerationCheckpointClient{
		list: func(_ context.Context, page int) ([]string, error) {
			switch page {
			case 2:
				return []string{"service-b"}, nil
			case 3:
				return nil, nil
			default:
				t.Fatalf("unexpected page: %d", page)
				return nil, nil
			}
		},
	}

	repos, err := loadOrganizationReposWithCheckpoint(context.Background(), client, "acme", store, &checkpoint, &progress)
	if err != nil {
		t.Fatalf("loadOrganizationReposWithCheckpoint returned error: %v", err)
	}
	if len(repos) != 2 || repos[0] != "service-a" || repos[1] != "service-b" {
		t.Fatalf("unexpected repos: %+v", repos)
	}
}

func TestAnalyzeRepositoriesResumeFromScanCheckpointSkipsEnumeration(t *testing.T) {
	originalWatch := interruptWatcher
	originalFactory := gitHubClientFactory
	defer func() {
		interruptWatcher = originalWatch
		gitHubClientFactory = originalFactory
	}()
	interruptWatcher = func(context.CancelFunc) func() { return func() {} }

	checkpointDir := filepath.Join(t.TempDir(), "checkpoints")
	store := NewCheckpointStore(checkpointDir)
	if err := store.Save(Checkpoint{
		RunID:      "run-123",
		Target:     "acme",
		Discovered: []string{"service-a"},
		NextPage:   0,
		Pending:    []string{"service-a"},
		Completed:  map[string]RepoSnapshot{},
		Failed:     map[string]string{},
		Progress: ProgressState{
			Mode:           "org",
			Target:         "acme",
			ResumeEnabled:  true,
			Workers:        4,
			RateLimitFloor: 200,
			TotalRepos:     1,
			Phase:          "scan",
		},
	}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	client := fakeGitHubClient{
		reposByPage: map[int][]string{
			1: {"unexpected"},
		},
		metadataByRepo: map[string]RepoMetadata{
			"acme/service-a": {UpdatedAt: time.Date(2026, time.April, 14, 10, 0, 0, 0, time.UTC)},
		},
		lastCommitByRepo: map[string]time.Time{
			"acme/service-a": time.Date(2026, time.April, 13, 10, 0, 0, 0, time.UTC),
		},
		contributorsByRepo: map[string][]string{
			"acme/service-a": {},
		},
		rateLimitState: RateLimitState{
			Limit:     5000,
			Remaining: 5000,
			ResetAt:   time.Date(2026, time.April, 14, 11, 0, 0, 0, time.UTC),
		},
	}
	gitHubClientFactory = func(context.Context) (GitHubClient, error) {
		return client, nil
	}

	cfg := config.Default()
	cfg.Organization = "acme"
	cfg.Resume = true
	cfg.Silent = true
	cfg.CacheDir = filepath.Join(t.TempDir(), "cache")
	cfg.CheckpointDir = checkpointDir

	results, err := AnalyzeRepositories(cfg)
	if err != nil {
		t.Fatalf("AnalyzeRepositories returned error: %v", err)
	}
	if len(results) != 1 || results[0].Name != "acme/service-a" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestAnalyzeRepositoriesReturnsGracefulStopWhenRepoOperationIsInterrupted(t *testing.T) {
	originalWatch := interruptWatcher
	originalFactory := gitHubClientFactory
	defer func() {
		interruptWatcher = originalWatch
		gitHubClientFactory = originalFactory
	}()
	interruptWatcher = func(context.CancelFunc) func() { return func() {} }

	checkpointDir := filepath.Join(t.TempDir(), "checkpoints")
	store := NewCheckpointStore(checkpointDir)
	if err := store.Save(Checkpoint{
		RunID:      "run-123",
		Target:     "acme",
		Discovered: []string{"service-a"},
		Pending:    []string{"service-a"},
		Completed:  map[string]RepoSnapshot{},
		Failed:     map[string]string{},
		Progress: ProgressState{
			Mode:           "org",
			Target:         "acme",
			ResumeEnabled:  true,
			Workers:        4,
			RateLimitFloor: 200,
			TotalRepos:     1,
			Phase:          "scan",
		},
	}); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	gitHubClientFactory = func(context.Context) (GitHubClient, error) {
		return fakeGitHubClient{
			metadataByRepo: map[string]RepoMetadata{
				"acme/service-a": {UpdatedAt: time.Date(2026, time.April, 14, 10, 0, 0, 0, time.UTC)},
			},
			lastCommitErr: context.Canceled,
			rateLimitState: RateLimitState{
				Limit:     5000,
				Remaining: 5000,
				ResetAt:   time.Date(2026, time.April, 14, 11, 0, 0, 0, time.UTC),
			},
		}, nil
	}

	cfg := config.Default()
	cfg.Organization = "acme"
	cfg.Resume = true
	cfg.Silent = true
	cfg.CacheDir = filepath.Join(t.TempDir(), "cache")
	cfg.CheckpointDir = checkpointDir

	_, err := AnalyzeRepositories(cfg)
	if !errors.Is(err, ErrGracefulStop) {
		t.Fatalf("AnalyzeRepositories error = %v, want %v", err, ErrGracefulStop)
	}
}

type enumerationCheckpointClient struct {
	list func(ctx context.Context, page int) ([]string, error)
}

func (c enumerationCheckpointClient) ListOrganizationRepos(ctx context.Context, _ string, page, _ int) ([]string, error) {
	return c.list(ctx, page)
}

func (enumerationCheckpointClient) GetRepoMetadata(context.Context, string) (RepoMetadata, error) {
	return RepoMetadata{}, nil
}

func (enumerationCheckpointClient) GetLastCommitDate(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}

func (enumerationCheckpointClient) ListContributors(context.Context, string) ([]string, error) {
	return nil, nil
}

func (enumerationCheckpointClient) IsOrgMember(context.Context, string, string) (bool, error) {
	return false, nil
}

func (enumerationCheckpointClient) GetRateLimitState(context.Context) (RateLimitState, error) {
	return RateLimitState{}, nil
}
