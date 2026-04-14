package analyzer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRepoSnapshotExpiresAfterTTL(t *testing.T) {
	cache := NewCacheStore(t.TempDir(), 30*time.Minute, 24*time.Hour)
	now := time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC)

	snapshot := RepoSnapshot{
		Repository: "acme/service-a",
		FetchedAt:  now.Add(-31 * time.Minute),
	}

	if err := cache.PutRepo(snapshot); err != nil {
		t.Fatalf("PutRepo returned error: %v", err)
	}
	if _, ok, _ := cache.GetRepo("acme/service-a", now); ok {
		t.Fatal("expected repo snapshot to be stale")
	}
}

func TestRepoSnapshotWithZeroFetchedAtExpires(t *testing.T) {
	cache := NewCacheStore(t.TempDir(), 30*time.Minute, 24*time.Hour)
	now := time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC)

	if err := cache.PutRepo(RepoSnapshot{
		Repository: "acme/service-a",
	}); err != nil {
		t.Fatalf("PutRepo returned error: %v", err)
	}

	if _, ok, _ := cache.GetRepo("acme/service-a", now); ok {
		t.Fatal("expected zero-fetched repo snapshot to be stale")
	}
}

func TestRepoCorruptedSnapshotReturnsMiss(t *testing.T) {
	cache := NewCacheStore(t.TempDir(), 30*time.Minute, 24*time.Hour)
	path := cache.repoPath("acme/service-a")

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if _, ok, err := cache.GetRepo("acme/service-a", time.Now()); err != nil || ok {
		t.Fatalf("expected corrupted repo cache to be treated as miss, got ok=%t err=%v", ok, err)
	}
}

func TestMembershipSnapshotReusedWithinTTL(t *testing.T) {
	cache := NewCacheStore(t.TempDir(), 30*time.Minute, 24*time.Hour)
	now := time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC)

	if err := cache.PutMembership(MembershipSnapshot{
		Organization: "acme",
		Login:        "octocat",
		Active:       true,
		FetchedAt:    now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("PutMembership returned error: %v", err)
	}

	got, ok, err := cache.GetMembership("acme", "octocat", now)
	if err != nil || !ok || !got.Active {
		t.Fatalf("expected cached active membership, got ok=%t err=%v snapshot=%+v", ok, err, got)
	}
}

func TestMembershipSnapshotExpiresAfterTTL(t *testing.T) {
	cache := NewCacheStore(t.TempDir(), 30*time.Minute, 90*time.Minute)
	now := time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC)

	if err := cache.PutMembership(MembershipSnapshot{
		Organization: "acme",
		Login:        "octocat",
		Active:       true,
		FetchedAt:    now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("PutMembership returned error: %v", err)
	}

	if _, ok, _ := cache.GetMembership("acme", "octocat", now); ok {
		t.Fatal("expected membership snapshot to be stale")
	}
}
