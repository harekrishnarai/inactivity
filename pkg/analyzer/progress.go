package analyzer

import "fmt"

func RenderHeader(state ProgressState, width int, colorEnabled bool) string {
	_ = width
	_ = colorEnabled

	line1 := fmt.Sprintf("inactivity | mode=%s | target=%s", state.Mode, state.Target)
	line2 := fmt.Sprintf("resume=%t workers=%d floor=%d", state.ResumeEnabled, state.Workers, state.RateLimitFloor)
	return line1 + "\n" + line2
}

func RenderProgressLine(state ProgressState) string {
	return fmt.Sprintf(
		"progress=%d/%d cached=%d revalidated=%d failed=%d workers=%d phase=%s",
		state.CompletedRepos,
		state.TotalRepos,
		state.CachedRepos,
		state.RevalidatedRepos,
		state.FailedRepos,
		state.ActiveWorkers,
		state.Phase,
	)
}
