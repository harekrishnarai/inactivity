package analyzer

import (
	"fmt"
	"time"
)

func RenderHeader(state ProgressState, width int, colorEnabled bool) string {
	title := "inactivity"
	if colorEnabled {
		title = "\x1b[36m" + title + "\x1b[0m"
	}

	if width < 72 {
		return fmt.Sprintf("%s | mode=%s | target=%s | resume=%t workers=%d floor=%d",
			title, state.Mode, state.Target, state.ResumeEnabled, state.Workers, state.RateLimitFloor)
	}

	line1 := fmt.Sprintf("%s | mode=%s | target=%s", title, state.Mode, state.Target)
	line2 := fmt.Sprintf("resume=%t workers=%d floor=%d", state.ResumeEnabled, state.Workers, state.RateLimitFloor)
	return line1 + "\n" + line2
}

func RenderProgressLine(state ProgressState) string {
	line := fmt.Sprintf(
		"progress=%d/%d cached=%d revalidated=%d failed=%d active=%d worker_target=%d",
		state.CompletedRepos,
		state.TotalRepos,
		state.CachedRepos,
		state.RevalidatedRepos,
		state.FailedRepos,
		state.ActiveWorkers,
		state.WorkerRecommendation,
	)
	line += fmt.Sprintf(" rate_remaining=%d", state.RateLimitRemaining)
	if !state.RateLimitResetAt.IsZero() {
		line += fmt.Sprintf(" rate_reset=%s", state.RateLimitResetAt.UTC().Format(time.RFC3339))
	}
	return line + fmt.Sprintf(" phase=%s", state.Phase)
}
