package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/harekrishnarai/inactivity/pkg/analyzer"
)

func TestParseOrgArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want func(*testing.T, configSnapshot)
	}{
		{
			name: "defaults",
			args: nil,
			want: func(t *testing.T, got configSnapshot) {
				t.Helper()
				if got.Organization != "" || got.MaxCommitAgeInDays != 180 || got.InactiveContribThreshold != 0.5 || got.OutputFormat != "console" || got.OutputFile != "" || got.Silent || got.Resume || got.Refresh || got.Workers != 6 || got.RateLimitFloor != 200 || got.CacheDir != ".inactivity/cache" || got.CheckpointDir != ".inactivity/checkpoints" {
					t.Fatalf("unexpected default config: %+v", got)
				}
			},
		},
		{
			name: "org options",
			args: []string{
				"-org", "acme",
				"-resume",
				"-refresh",
				"-workers", "10",
				"-rate-limit-floor", "300",
				"-cache-dir", "/tmp/cache",
				"-checkpoint-dir", "/tmp/checkpoints",
			},
			want: func(t *testing.T, got configSnapshot) {
				t.Helper()
				if got.Organization != "acme" || !got.Resume || !got.Refresh || got.Workers != 10 || got.RateLimitFloor != 300 || got.CacheDir != "/tmp/cache" || got.CheckpointDir != "/tmp/checkpoints" {
					t.Fatalf("unexpected parsed config: %+v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseOrgArgs(tt.args)
			if err != nil {
				t.Fatalf("parseOrgArgs returned error: %v", err)
			}

			tt.want(t, configSnapshot{
				Organization:             cfg.Organization,
				MaxCommitAgeInDays:       cfg.MaxCommitAgeInDays,
				InactiveContribThreshold: cfg.InactiveContribThreshold,
				OutputFormat:             cfg.OutputFormat,
				OutputFile:               cfg.OutputFile,
				Silent:                   cfg.Silent,
				Resume:                   cfg.Resume,
				Refresh:                  cfg.Refresh,
				Workers:                  cfg.Workers,
				RateLimitFloor:           cfg.RateLimitFloor,
				CacheDir:                 cfg.CacheDir,
				CheckpointDir:            cfg.CheckpointDir,
			})
		})
	}
}

type configSnapshot struct {
	Organization             string
	MaxCommitAgeInDays       int
	InactiveContribThreshold float64
	OutputFormat             string
	OutputFile               string
	Silent                   bool
	Resume                   bool
	Refresh                  bool
	Workers                  int
	RateLimitFloor           int
	CacheDir                 string
	CheckpointDir            string
}

func TestHandleAnalyzeOrganizationError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantLine string
	}{
		{
			name:     "rate limit pause",
			err:      fmt.Errorf("wrapped: %w", analyzer.ErrRateLimitPause),
			wantLine: "Scan paused at the configured rate-limit floor. Re-run with --resume to continue.",
		},
		{
			name:     "graceful stop",
			err:      fmt.Errorf("wrapped: %w", analyzer.ErrGracefulStop),
			wantLine: "Scan stopped gracefully. Re-run with --resume to continue.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handled, output := captureHandleAnalyzeOrganizationError(t, tt.err)
			if !handled {
				t.Fatal("expected error to be handled")
			}
			if !strings.Contains(output, tt.wantLine) {
				t.Fatalf("unexpected stdout: %q", output)
			}
		})
	}
}

func captureHandleAnalyzeOrganizationError(t *testing.T, input error) (bool, string) {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = oldStdout
	})

	handled := handleAnalyzeOrganizationError(input)

	if err := w.Close(); err != nil {
		t.Fatalf("closing write end failed: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("reading stdout failed: %v", err)
	}

	return handled, buf.String()
}
