package config

import (
	"testing"
	"time"
)

func TestDefaultLargeOrgSettings(t *testing.T) {
	cfg := Default()

	if cfg.Organization != "" || cfg.SingleRepository != "" || cfg.RepoListFile != "" || cfg.MaxCommitAgeInDays != 180 || cfg.InactiveContribThreshold != 0.5 || cfg.OutputFormat != "console" || cfg.OutputFile != "" || cfg.Silent || cfg.Resume || cfg.Refresh {
		t.Fatalf("unexpected zero-value defaults: %+v", cfg)
	}
	if cfg.Workers != 6 {
		t.Fatalf("expected default workers 6, got %d", cfg.Workers)
	}
	if cfg.RateLimitFloor != 200 {
		t.Fatalf("expected rate limit floor 200, got %d", cfg.RateLimitFloor)
	}
	if cfg.CacheDir != ".inactivity/cache" {
		t.Fatalf("expected cache dir .inactivity/cache, got %q", cfg.CacheDir)
	}
	if cfg.CheckpointDir != ".inactivity/checkpoints" {
		t.Fatalf("expected checkpoint dir .inactivity/checkpoints, got %q", cfg.CheckpointDir)
	}
	if cfg.RepoCacheTTL != 30*time.Minute {
		t.Fatalf("expected repo cache TTL 30m, got %s", cfg.RepoCacheTTL)
	}
	if cfg.MembershipCacheTTL != 24*time.Hour {
		t.Fatalf("expected membership cache TTL 24h, got %s", cfg.MembershipCacheTTL)
	}
}
