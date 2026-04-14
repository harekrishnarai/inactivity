package analyzer

import (
	"errors"
)

var ErrRateLimitPause = errors.New("rate limit pause requested")

const rateLimitPollEveryRepos = 10

type RateLimiter struct {
	floor    int
	state    RateLimitState
	hasState bool
}

func NewRateLimiter(floor int) *RateLimiter {
	return &RateLimiter{floor: floor}
}

func (r *RateLimiter) Update(state RateLimitState) {
	r.state = state
	r.hasState = true
}

func (r *RateLimiter) HasState() bool {
	return r.hasState
}

func (r *RateLimiter) UseLastKnownOrFallback() {
	if r.hasState {
		return
	}
	r.Update(RateLimitState{Remaining: r.floor})
}

func (r *RateLimiter) ShouldPause() bool {
	if !r.hasState {
		return false
	}
	return r.state.Remaining <= r.floor
}

func (r *RateLimiter) RecommendedWorkers(current int) int {
	if !r.hasState {
		return current
	}
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

func (r *RateLimiter) ShouldPoll(repoIndex int) bool {
	return repoIndex == 0 || repoIndex%rateLimitPollEveryRepos == 0
}
