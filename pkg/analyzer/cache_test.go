package analyzer

import (
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
