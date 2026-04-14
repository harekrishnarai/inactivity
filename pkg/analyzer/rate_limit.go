package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

var ErrRateLimitPause = errors.New("rate limit pause requested")

type RateLimiter struct {
	floor int
	state RateLimitState
}

func NewRateLimiter(floor int) *RateLimiter {
	return &RateLimiter{floor: floor}
}

func (r *RateLimiter) Update(state RateLimitState) {
	r.state = state
}

func (r *RateLimiter) ShouldPause() bool {
	return r.state.Remaining <= r.floor
}

func (r *RateLimiter) RecommendedWorkers(current int) int {
	if r.state.Remaining <= r.floor {
		return 0
	}
	if r.state.Remaining <= r.floor+50 && current > 2 {
		return 2
	}
	if r.state.Remaining <= r.floor+100 && current > 4 {
		return 4
	}
	return current
}

func loadRateLimitState(ctx context.Context) (RateLimitState, error) {
	cmd := exec.CommandContext(ctx, "gh", "api", "rate_limit")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return RateLimitState{}, fmt.Errorf("get GitHub rate limit: %w", err)
	}

	var response struct {
		Rate struct {
			Limit     int   `json:"limit"`
			Remaining int   `json:"remaining"`
			Used      int   `json:"used"`
			Reset     int64 `json:"reset"`
		} `json:"rate"`
	}

	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		return RateLimitState{}, fmt.Errorf("parse GitHub rate limit: %w", err)
	}

	return RateLimitState{
		Limit:     response.Rate.Limit,
		Remaining: response.Rate.Remaining,
		Used:      response.Rate.Used,
		ResetAt:   time.Unix(response.Rate.Reset, 0).UTC(),
	}, nil
}
