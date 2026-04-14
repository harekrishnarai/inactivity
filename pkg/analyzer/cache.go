package analyzer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func NewCacheStore(root string, repoTTL, membershipTTL time.Duration) *CacheStore {
	return &CacheStore{
		root:          root,
		repoTTL:       repoTTL,
		membershipTTL: membershipTTL,
	}
}

func (c *CacheStore) PutRepo(snapshot RepoSnapshot) error {
	return writeSnapshot(c.repoPath(snapshot.Repository), snapshot)
}

func (c *CacheStore) GetRepo(name string, now time.Time) (RepoSnapshot, bool, error) {
	var snapshot RepoSnapshot

	if err := readSnapshot(c.repoPath(name), &snapshot); err != nil {
		if os.IsNotExist(err) {
			return RepoSnapshot{}, false, nil
		}
		return RepoSnapshot{}, false, nil
	}

	if snapshotExpired(snapshot.FetchedAt, c.repoTTL, now) {
		return RepoSnapshot{}, false, nil
	}

	return snapshot, true, nil
}

func (c *CacheStore) PutMembership(snapshot MembershipSnapshot) error {
	return writeSnapshot(c.membershipPath(snapshot.Organization, snapshot.Login), snapshot)
}

func (c *CacheStore) GetMembership(org, login string, now time.Time) (MembershipSnapshot, bool, error) {
	var snapshot MembershipSnapshot

	if err := readSnapshot(c.membershipPath(org, login), &snapshot); err != nil {
		if os.IsNotExist(err) {
			return MembershipSnapshot{}, false, nil
		}
		return MembershipSnapshot{}, false, nil
	}

	if snapshotExpired(snapshot.FetchedAt, c.membershipTTL, now) {
		return MembershipSnapshot{}, false, nil
	}

	return snapshot, true, nil
}

func (c *CacheStore) repoPath(name string) string {
	return filepath.Join(c.root, "repos", cacheName(name)+".json")
}

func (c *CacheStore) membershipPath(org, login string) string {
	return filepath.Join(c.root, "memberships", cacheName(org+"__"+login)+".json")
}

func cacheName(value string) string {
	replacer := strings.NewReplacer(
		"/", "__",
		"\\", "__",
		":", "_",
		" ", "_",
	)
	return replacer.Replace(value)
}

func writeSnapshot(path string, snapshot any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal cache snapshot: %w", err)
	}

	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create cache snapshot: %w", err)
	}
	tempPath := file.Name()
	defer func() {
		_ = file.Close()
		_ = os.Remove(tempPath)
	}()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write cache snapshot: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close cache snapshot: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace cache snapshot: %w", err)
	}

	return nil
}

func readSnapshot(path string, snapshot any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, snapshot); err != nil {
		return fmt.Errorf("unmarshal cache snapshot: %w", err)
	}

	return nil
}

func snapshotExpired(fetchedAt time.Time, ttl time.Duration, now time.Time) bool {
	if ttl <= 0 {
		return false
	}
	if fetchedAt.IsZero() {
		return true
	}
	return now.Sub(fetchedAt) > ttl
}
