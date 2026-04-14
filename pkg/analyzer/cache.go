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
		return RepoSnapshot{}, false, err
	}

	if !snapshot.FetchedAt.IsZero() && c.repoTTL > 0 && now.Sub(snapshot.FetchedAt) > c.repoTTL {
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
		return MembershipSnapshot{}, false, err
	}

	if !snapshot.FetchedAt.IsZero() && c.membershipTTL > 0 && now.Sub(snapshot.FetchedAt) > c.membershipTTL {
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

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write cache snapshot: %w", err)
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
