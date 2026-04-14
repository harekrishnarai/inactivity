package config

import "testing"

func TestDefaultLargeOrgSettings(t *testing.T) {
	cfg := Default()

	if cfg.Workers != 6 {
		t.Fatalf("expected default workers 6, got %d", cfg.Workers)
	}
	if cfg.RateLimitFloor != 200 {
		t.Fatalf("expected rate limit floor 200, got %d", cfg.RateLimitFloor)
	}
	if cfg.RepoCacheTTL <= 0 || cfg.MembershipCacheTTL <= 0 {
		t.Fatalf("expected positive cache TTLs, got repo=%s membership=%s", cfg.RepoCacheTTL, cfg.MembershipCacheTTL)
	}
}
