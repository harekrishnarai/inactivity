package analyzer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Checkpoint struct {
	RunID      string                  `json:"runId"`
	Target     string                  `json:"target"`
	StartedAt  time.Time               `json:"startedAt"`
	UpdatedAt  time.Time               `json:"updatedAt"`
	Discovered []string                `json:"discovered,omitempty"`
	NextPage   int                     `json:"nextPage,omitempty"`
	Pending    []string                `json:"pending"`
	InProgress []string                `json:"inProgress"`
	Completed  map[string]RepoSnapshot `json:"completed"`
	Failed     map[string]string       `json:"failed"`
	Progress   ProgressState           `json:"progress"`
}

type CheckpointStore struct {
	root string
}

func NewCheckpointStore(root string) *CheckpointStore {
	return &CheckpointStore{root: root}
}

func (s *CheckpointStore) Save(checkpoint Checkpoint) error {
	if checkpoint.RunID == "" {
		checkpoint.RunID = fmt.Sprintf("run-%d", time.Now().UTC().UnixNano())
	}

	now := time.Now().UTC()
	if checkpoint.StartedAt.IsZero() {
		checkpoint.StartedAt = now
	}
	checkpoint.UpdatedAt = now
	if checkpoint.Completed == nil {
		checkpoint.Completed = map[string]RepoSnapshot{}
	}
	if checkpoint.Failed == nil {
		checkpoint.Failed = map[string]string{}
	}

	data, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}

	path := s.checkpointPath(checkpoint.Target, checkpoint.RunID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create checkpoint directory: %w", err)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create checkpoint file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()

	if _, err := tempFile.Write(data); err != nil {
		return fmt.Errorf("write checkpoint file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close checkpoint file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace checkpoint file: %w", err)
	}

	return nil
}

// LoadLatestTarget scans the checkpoint root directory and returns the Target field
// from the most recently modified checkpoint file across all organizations.
// Use this to auto-detect which org to resume when none is specified.
func (s *CheckpointStore) LoadLatestTarget() (string, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return "", err
	}

	type candidate struct {
		path    string
		modTime time.Time
	}

	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(s.root, entry.Name())
		files, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) == ".tmp" {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			candidates = append(candidates, candidate{
				path:    filepath.Join(dirPath, f.Name()),
				modTime: info.ModTime(),
			})
		}
	}

	if len(candidates) == 0 {
		return "", os.ErrNotExist
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})

	for _, c := range candidates {
		data, err := os.ReadFile(c.path)
		if err != nil {
			continue
		}
		var checkpoint Checkpoint
		if err := json.Unmarshal(data, &checkpoint); err != nil {
			continue
		}
		if checkpoint.Target != "" {
			return checkpoint.Target, nil
		}
	}

	return "", os.ErrNotExist
}

func (s *CheckpointStore) LoadLatest(target string) (Checkpoint, error) {
	dir := s.targetDir(target)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Checkpoint{}, err
	}

	type checkpointFile struct {
		path    string
		modTime time.Time
	}

	var candidates []checkpointFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) == ".tmp" {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return Checkpoint{}, fmt.Errorf("stat checkpoint file %q: %w", entry.Name(), err)
		}
		candidates = append(candidates, checkpointFile{
			path:    filepath.Join(dir, entry.Name()),
			modTime: info.ModTime(),
		})
	}

	if len(candidates) == 0 {
		return Checkpoint{}, os.ErrNotExist
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modTime.Equal(candidates[j].modTime) {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].modTime.After(candidates[j].modTime)
	})

	data, err := os.ReadFile(candidates[0].path)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("read checkpoint file: %w", err)
	}

	var checkpoint Checkpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("unmarshal checkpoint: %w", err)
	}
	if checkpoint.Completed == nil {
		checkpoint.Completed = map[string]RepoSnapshot{}
	}
	if checkpoint.Failed == nil {
		checkpoint.Failed = map[string]string{}
	}

	return checkpoint, nil
}

func (s *CheckpointStore) checkpointPath(target, runID string) string {
	return filepath.Join(s.targetDir(target), cacheName(runID)+".json")
}

func (s *CheckpointStore) targetDir(target string) string {
	return filepath.Join(s.root, cacheName(target))
}

func resumePendingRepos(allRepos []string, checkpoint Checkpoint) []string {
	completed := completedSnapshotsByRepo(checkpoint.Completed)
	pending := make([]string, 0, len(allRepos))
	for _, repo := range allRepos {
		if _, ok := completed[repo]; ok {
			continue
		}
		pending = append(pending, repo)
	}

	return pending
}

func completedRepositories(checkpoint Checkpoint, allRepos []string) []Repository {
	if len(checkpoint.Completed) == 0 {
		return nil
	}

	completed := completedSnapshotsByRepo(checkpoint.Completed)
	results := make([]Repository, 0, len(checkpoint.Completed))
	for _, repo := range allRepos {
		snapshot, ok := completed[repo]
		if !ok {
			continue
		}
		results = append(results, repositoryFromSnapshot(snapshot))
	}

	return results
}

func checkpointFailures(checkpoint Checkpoint, allRepos []string) map[string]string {
	if len(checkpoint.Failed) == 0 {
		return map[string]string{}
	}

	failures := failuresByRepo(checkpoint.Failed)
	failed := make(map[string]string, len(checkpoint.Failed))
	for _, repo := range allRepos {
		if msg, ok := failures[repo]; ok {
			failed[repo] = msg
		}
	}

	return failed
}

func completedSnapshotsByRepo(completed map[string]RepoSnapshot) map[string]RepoSnapshot {
	if len(completed) == 0 {
		return map[string]RepoSnapshot{}
	}

	normalized := make(map[string]RepoSnapshot, len(completed))
	for key, snapshot := range completed {
		repo := key
		if snapshot.Repository != "" {
			repo = snapshot.Repository
		}
		normalized[checkpointRepoName(repo)] = snapshot
	}

	return normalized
}

func failuresByRepo(failed map[string]string) map[string]string {
	if len(failed) == 0 {
		return map[string]string{}
	}

	normalized := make(map[string]string, len(failed))
	for repo, msg := range failed {
		normalized[checkpointRepoName(repo)] = msg
	}

	return normalized
}

func checkpointRepoName(repo string) string {
	if _, name, ok := strings.Cut(repo, "/"); ok {
		return name
	}
	return repo
}
