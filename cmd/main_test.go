package cmd

import "testing"

func TestOrgFlagsPopulateLargeOrgOptions(t *testing.T) {
	cfg, err := parseOrgArgs([]string{
		"-org", "acme",
		"-resume",
		"-workers", "10",
		"-rate-limit-floor", "300",
	})
	if err != nil {
		t.Fatalf("parseOrgArgs returned error: %v", err)
	}
	if cfg.Organization != "acme" || !cfg.Resume || cfg.Workers != 10 || cfg.RateLimitFloor != 300 {
		t.Fatalf("unexpected parsed config: %+v", cfg)
	}
}
