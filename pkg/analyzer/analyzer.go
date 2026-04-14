package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatih/color"
	"github.com/harekrishnarai/inactivity/pkg/config"
	"github.com/mattn/go-isatty"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/term"
)

var (
	orgRepositoriesLoader = listOrganizationRepos
	interruptWatcher      = watchInterrupts
	gitHubClientFactory   = NewGitHubClient
)

// Repository represents a GitHub repository with its inactivity status
type Repository struct {
	Name                 string    `json:"name"`
	LastCommitDate       time.Time `json:"lastCommitDate"`
	DaysSinceLastCommit  int       `json:"daysSinceLastCommit"`
	TotalContributors    int       `json:"totalContributors"`
	InactiveContributors int       `json:"inactiveContributors"`
	InactivePercentage   float64   `json:"inactivePercentage"`
	Archived             bool      `json:"archived"`
	Flagged              bool      `json:"flagged"`
}

// scanResult holds the outcome of processing a single repository.
type scanResult struct {
	repoFullName string
	repo         *Repository
	snapshot     *RepoSnapshot
	fromCache    bool
	failed       bool
	failedMsg    string
	interrupted  bool
}

// rateLimitDisplayInfo carries rate-limit state from the dispatch loop to the collector.
type rateLimitDisplayInfo struct {
	remaining     int
	resetAt       time.Time
	activeWorkers int
	recommended   int
}

// ValidateGitHubCLI checks if GitHub CLI is installed and authenticated
func ValidateGitHubCLI() error {
	// Check if gh is installed
	cmd := exec.Command("gh", "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("GitHub CLI (gh) is not installed or not in PATH: %w", err)
	}

	// Check if gh is authenticated
	cmd = exec.Command("gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("GitHub CLI is not authenticated: %w", err)
	}

	return nil
}

// GetUserOrganizations returns a list of organizations the authenticated user has access to
func GetUserOrganizations() ([]string, error) {
	cmd := exec.Command("gh", "api", "user/memberships/orgs", "--jq", ".[].organization.login")
	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to get organizations: %w", err)
	}

	orgs := strings.Split(strings.TrimSpace(out.String()), "\n")
	// Filter out empty strings
	var result []string
	for _, org := range orgs {
		if org != "" {
			result = append(result, org)
		}
	}

	return result, nil
}

// DisplayBanner prints a beautiful banner for the tool unless silent mode is enabled
// showOrgBanner controls whether to show organization-related information
func DisplayBanner(silent bool, showOrgBanner bool) {
	if silent {
		return
	}
	// Try to use colors if available, but don't fail if they're not supported
	cyan := color.New(color.FgCyan).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()
	red := color.New(color.FgRed).SprintFunc()
	green := color.New(color.FgGreen).SprintFunc()
	purple := color.New(color.FgMagenta).SprintFunc()
	blue := color.New(color.FgBlue).SprintFunc()
	white := color.New(color.FgHiWhite, color.Bold).SprintFunc()

	// Print a creative new banner
	fmt.Println()
	fmt.Println(blue("╭─────────────────────────────────────────────────────────────╮"))
	fmt.Println(blue("│") + "                                                             " + blue("│"))
	fmt.Println(blue("│") + "   " + purple("⚡") + white(" GITHUB REPOSITORY PULSE CHECK ") + purple("⚡") + "                      " + blue("│"))
	fmt.Println(blue("│") + "                                                             " + blue("│"))
	fmt.Println(blue("│") + "   " + cyan("[ ") + green("Activity") + cyan(" | ") + yellow("Contributors") + cyan(" | ") + red("Health") + cyan(" ]") + "                            " + blue("│"))
	fmt.Println(blue("│") + "                                                             " + blue("│"))
	fmt.Println(blue("│") + "   " + yellow("📊") + " " + white("Uncovering repository health since 2025") + "             " + blue("│"))
	fmt.Println(blue("│") + "   " + green("🔍") + " " + cyan("Identifying inactive repositories in your organization") + " " + blue("│"))
	fmt.Println(blue("│") + "                                                             " + blue("│"))
	fmt.Println(blue("╰─────────────────────────────────────────────────────────────╯"))
	fmt.Println()

	fmt.Println(yellow("✦ Repository Inactivity Analyzer ✦"))

	// Only show organization-related information if showOrgBanner is true
	if showOrgBanner {
		fmt.Println(cyan("Find and track inactive repositories in your GitHub organizations"))
		fmt.Println()
	} else {
		fmt.Println(cyan("Analyzing a single repository for inactivity metrics"))
		fmt.Println()
	}
}

// processOneRepo analyzes a single repository and returns a scanResult.
// It is safe to call concurrently from multiple goroutines.
func processOneRepo(ctx context.Context, client GitHubClient, cfg config.Config, cache *CacheStore, repoFullName string, now time.Time) scanResult {
	res := scanResult{repoFullName: repoFullName}

	if !cfg.Refresh {
		snapshot, ok, err := cache.GetRepo(repoFullName, now)
		if err == nil && ok {
			res.fromCache = true
			r := repositoryFromSnapshot(snapshot)
			res.repo = &r
			s := snapshot
			res.snapshot = &s
			return res
		}
	}

	r := Repository{Name: repoFullName}

	isArchived, err := isRepositoryArchived(ctx, client, repoFullName)
	if err != nil {
		if interrupted(ctx, err) {
			res.interrupted = true
			return res
		}
		res.failed = true
		res.failedMsg = err.Error()
		return res
	}
	r.Archived = isArchived

	lastCommitDate, err := getLastCommitDate(ctx, client, repoFullName)
	if err != nil {
		if interrupted(ctx, err) {
			res.interrupted = true
			return res
		}
		res.failed = true
		res.failedMsg = err.Error()
		return res
	}
	r.LastCommitDate = lastCommitDate
	r.DaysSinceLastCommit = int(now.Sub(lastCommitDate).Hours() / 24)

	activeContribs, inactiveContribs, err := getContributorsStatusWithCache(ctx, client, repoFullName, cfg.Organization, cache, now, cfg.Refresh)
	if err != nil {
		if interrupted(ctx, err) {
			res.interrupted = true
			return res
		}
		res.failed = true
		res.failedMsg = err.Error()
		return res
	}

	r.TotalContributors = activeContribs + inactiveContribs
	r.InactiveContributors = inactiveContribs
	if r.TotalContributors > 0 {
		r.InactivePercentage = float64(inactiveContribs) / float64(r.TotalContributors)
	}

	if r.Archived {
		r.Flagged = true
	} else {
		isOld := r.DaysSinceLastCommit > cfg.MaxCommitAgeInDays
		if isOld {
			if r.TotalContributors > 0 {
				if r.InactivePercentage >= cfg.InactiveContribThreshold {
					r.Flagged = true
				}
			} else {
				r.Flagged = true
			}
		}
	}

	snap := repoSnapshotFromRepository(r, now)
	res.repo = &r
	res.snapshot = &snap
	return res
}

// remainingPending returns repo names from pendingRepos that are not yet completed or failed.
func remainingPending(org string, pendingRepos []string, checkpoint Checkpoint) []string {
	completed := completedSnapshotsByRepo(checkpoint.Completed)
	remaining := make([]string, 0, len(pendingRepos))
	for _, name := range pendingRepos {
		if _, done := completed[name]; done {
			continue
		}
		full := fmt.Sprintf("%s/%s", org, name)
		if _, failed := checkpoint.Failed[full]; failed {
			continue
		}
		remaining = append(remaining, name)
	}
	return remaining
}

// AnalyzeRepositories analyzes all repositories in the given organization
func AnalyzeRepositories(cfg config.Config) ([]Repository, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopSignals := interruptWatcher(cancel)
	defer stopSignals()
	client, err := gitHubClientFactory(ctx)
	if err != nil {
		return nil, err
	}
	progressState := ProgressState{
		Mode:           "org",
		Target:         cfg.Organization,
		ResumeEnabled:  cfg.Resume,
		Workers:        cfg.Workers,
		RateLimitFloor: cfg.RateLimitFloor,
		Phase:          "enumerate",
	}
	var results []Repository
	now := time.Now()
	cache := NewCacheStore(cfg.CacheDir, cfg.RepoCacheTTL, cfg.MembershipCacheTTL)
	checkpointStore := NewCheckpointStore(cfg.CheckpointDir)
	startTime := time.Now()
	rateLimiter := NewRateLimiter(cfg.RateLimitFloor)
	currentCheckpoint := Checkpoint{
		RunID:     fmt.Sprintf("run-%d", now.UTC().UnixNano()),
		Target:    cfg.Organization,
		StartedAt: now.UTC(),
		UpdatedAt: now.UTC(),
		Completed: map[string]RepoSnapshot{},
		Failed:    map[string]string{},
		Progress:  progressState,
	}
	resumeScanState := false

	if cfg.Resume {
		if checkpoint, err := checkpointStore.LoadLatest(cfg.Organization); err == nil {
			currentCheckpoint = checkpoint
			if currentCheckpoint.RunID == "" {
				currentCheckpoint.RunID = fmt.Sprintf("run-%d", now.UTC().UnixNano())
			}
			if currentCheckpoint.StartedAt.IsZero() {
				currentCheckpoint.StartedAt = now.UTC()
			}
			currentCheckpoint.Target = cfg.Organization
			currentCheckpoint.InProgress = nil
			if currentCheckpoint.Completed == nil {
				currentCheckpoint.Completed = map[string]RepoSnapshot{}
			}
			if currentCheckpoint.Failed == nil {
				currentCheckpoint.Failed = map[string]string{}
			}
			progressState = currentCheckpoint.Progress
			progressState.Mode = "org"
			progressState.Target = cfg.Organization
			progressState.ResumeEnabled = cfg.Resume
			progressState.Workers = cfg.Workers
			progressState.RateLimitFloor = cfg.RateLimitFloor
			currentCheckpoint.Progress = progressState
			resumeScanState = progressState.Phase == "scan"
		}
	}

	saveCheckpoint := func(onShutdown bool) error {
		currentCheckpoint.Progress = progressState
		currentCheckpoint.UpdatedAt = time.Now().UTC()
		if err := checkpointStore.Save(currentCheckpoint); err != nil {
			if onShutdown {
				return fmt.Errorf("save checkpoint on shutdown: %w", err)
			}
			if !cfg.Silent {
				fmt.Printf("⚠️ Warning: Failed to save checkpoint for %s: %v\n", cfg.Organization, err)
			}
		}
		return nil
	}

	var allRepos []string
	if resumeScanState && len(currentCheckpoint.Discovered) > 0 {
		allRepos = append([]string(nil), currentCheckpoint.Discovered...)
	} else {
		var onEnumerateProgress func(int)
		var stopSpinner func()

		if !cfg.Silent {
			var discovered atomic.Int64
			spinnerDone := make(chan struct{})

			onEnumerateProgress = func(n int) { discovered.Store(int64(n)) }

			frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
			go func() {
				ticker := time.NewTicker(80 * time.Millisecond)
				defer ticker.Stop()
				for i := 0; ; i++ {
					select {
					case <-spinnerDone:
						return
					case <-ticker.C:
						fmt.Printf("\r%s Enumerating repositories... %d found",
							frames[i%len(frames)], discovered.Load())
					}
				}
			}()

			stopSpinner = func() {
				close(spinnerDone)
				fmt.Println() // move past the spinner line
			}
		}

		allRepos, err = loadOrganizationReposWithCheckpoint(ctx, client, cfg.Organization, checkpointStore, &currentCheckpoint, &progressState, onEnumerateProgress)
		if stopSpinner != nil {
			stopSpinner()
		}
		if err != nil {
			return nil, err
		}
	}
	progressState.TotalRepos = len(allRepos)
	progressState.Phase = "scan"
	currentCheckpoint.NextPage = 0

	if !cfg.Silent {
		fmt.Printf("📂 Found %d repositories in %s\n", len(allRepos), cfg.Organization)
	}

	pendingRepos := append([]string(nil), allRepos...)
	if resumeScanState {
		pendingRepos = resumePendingRepos(allRepos, currentCheckpoint)
		results = append(results, completedRepositories(currentCheckpoint, allRepos)...)
		progressState.CompletedRepos = len(results)
		progressState.FailedRepos = len(checkpointFailures(currentCheckpoint, allRepos))
	}
	currentCheckpoint.Pending = append([]string(nil), pendingRepos...)

	if !cfg.Silent {
		width, colorEnabled := terminalHeaderSettings()
		fmt.Println(RenderHeader(progressState, width, colorEnabled))
		fmt.Println()
	}

	// Create progress bar
	var bar *progressbar.ProgressBar
	if !cfg.Silent {
		bar = progressbar.NewOptions(len(allRepos),
			progressbar.OptionEnableColorCodes(false),
			progressbar.OptionSetDescription("⚡ Analyzing repositories"),
			progressbar.OptionSetTheme(progressbar.Theme{
				Saucer:        "█",
				SaucerHead:    "█",
				SaucerPadding: "░",
				BarStart:      "|",
				BarEnd:        "|",
			}),
			progressbar.OptionShowCount(),
			progressbar.OptionSetWidth(50),
			progressbar.OptionThrottle(100*time.Millisecond),
			progressbar.OptionShowIts(),
			progressbar.OptionSetItsString("repos"),
			progressbar.OptionClearOnFinish(),
			progressbar.OptionSetPredictTime(true),
			progressbar.OptionFullWidth(),
			progressbar.OptionOnCompletion(func() {
				fmt.Printf("\n%s\n", color.New(color.FgGreen).Sprint("✅ Analysis complete!"))
			}),
		)
	}
	if !cfg.Silent && bar != nil && len(results) > 0 {
		_ = bar.Add(len(results))
	}
	if err := saveCheckpoint(false); err != nil {
		return nil, err
	}

	advanceProgress := func(i int) {
		if cfg.Silent || bar == nil {
			return
		}

		elapsed := time.Since(startTime)
		timePerRepo := time.Duration(0)
		if i+1 > 0 {
			timePerRepo = elapsed / time.Duration(i+1)
		}
		// Use pendingRepos length for accurate ETA (avoids overcounting already-completed repos on resume)
		remaining := timePerRepo * time.Duration(len(pendingRepos)-i-1)

		bar.Describe(fmt.Sprintf("%s [%s elapsed, %s remaining]",
			RenderProgressLine(progressState), formatDuration(elapsed), formatDuration(remaining)))
		_ = bar.Add(1)
	}

	// Concurrent scan using a worker pool.
	numWorkers := cfg.Workers
	if numWorkers < 1 {
		numWorkers = 1
	}

	progressState.ActiveWorkers = numWorkers
	progressState.WorkerRecommendation = numWorkers

	// sem limits the number of in-flight workers.
	sem := make(chan struct{}, numWorkers)
	// resultCh carries completed work back to the collector.
	// Buffer size numWorkers+2 ensures workers never block on send while a slot is available.
	resultCh := make(chan scanResult, numWorkers+2)
	var wg sync.WaitGroup

	// rateLimitCh passes rate-limit display updates from the dispatch loop to the collector.
	// Non-blocking sends mean the dispatch loop never stalls on this.
	rateLimitCh := make(chan rateLimitDisplayInfo, 1)

	// Collector goroutine: sole writer to shared state (results, progressState, currentCheckpoint).
	collectorDone := make(chan struct{})
	collected := 0
	go func() {
		defer close(collectorDone)
		for result := range resultCh {
			// Apply any pending rate-limit display update (non-blocking drain).
			select {
			case info := <-rateLimitCh:
				progressState.RateLimitRemaining = info.remaining
				progressState.RateLimitResetAt = info.resetAt
				progressState.ActiveWorkers = info.activeWorkers
				progressState.WorkerRecommendation = info.recommended
			default:
			}

			if result.interrupted {
				cancel()
				continue
			}

			repoFull := result.repoFullName
			if result.fromCache {
				progressState.CachedRepos++
				results = append(results, *result.repo)
				currentCheckpoint.Completed[repoFull] = *result.snapshot
				delete(currentCheckpoint.Failed, repoFull)
			} else if result.failed {
				if !cfg.Silent {
					fmt.Printf("⚠️ Warning: Failed to analyze %s: %s\n", repoFull, result.failedMsg)
				}
				progressState.FailedRepos++
				currentCheckpoint.Failed[repoFull] = result.failedMsg
			} else if result.repo != nil {
				results = append(results, *result.repo)
				currentCheckpoint.Completed[repoFull] = *result.snapshot
				delete(currentCheckpoint.Failed, repoFull)
				if cacheErr := cache.PutRepo(*result.snapshot); cacheErr != nil && !cfg.Silent {
					fmt.Printf("⚠️ Warning: Failed to store repo cache for %s: %v\n", repoFull, cacheErr)
				}
			}
			currentCheckpoint.InProgress = nil
			progressState.CompletedRepos = len(results)
			if saveErr := saveCheckpoint(false); saveErr != nil && !cfg.Silent {
				fmt.Printf("⚠️ Warning: %v\n", saveErr)
			}
			collected++
			advanceProgress(collected - 1)
		}
	}()

	// Dispatch loop: sends jobs to workers, handles rate limiting.
	pauseErr := error(nil)
	for i, repoName := range pendingRepos {
		if ctx.Err() != nil {
			break
		}

		if rateLimiter.ShouldPoll(i) {
			if state, err := client.GetRateLimitState(ctx); err == nil {
				rateLimiter.Update(state)
			} else {
				rateLimiter.UseLastKnownOrFallback()
			}
		}

		if rateLimiter.HasState() {
			rec := rateLimiter.RecommendedWorkers(numWorkers)
			active := numWorkers
			if rec == 0 {
				active = 0
			}
			// Communicate rate-limit info to the collector (non-blocking; stale display is acceptable).
			select {
			case rateLimitCh <- rateLimitDisplayInfo{
				remaining:     rateLimiter.state.Remaining,
				resetAt:       rateLimiter.state.ResetAt,
				activeWorkers: active,
				recommended:   rec,
			}:
			default:
			}
			if rateLimiter.ShouldPause() {
				pauseErr = ErrRateLimitPause
				break
			}
		}

		repoFull := fmt.Sprintf("%s/%s", cfg.Organization, repoName)

		sem <- struct{}{} // acquire a worker slot (blocks when all workers are busy)
		if ctx.Err() != nil {
			<-sem
			break
		}

		wg.Add(1)
		go func(repoFull string) {
			defer wg.Done()
			defer func() { <-sem }()
			result := processOneRepo(ctx, client, cfg, cache, repoFull, now)
			select {
			case resultCh <- result:
			case <-ctx.Done():
			}
		}(repoFull)
	}

	// Wait for all in-flight workers to complete, then signal the collector to stop.
	wg.Wait()
	close(resultCh)
	<-collectorDone

	// After all goroutines have finished, shared state is safe to access without locks.

	if ctx.Err() != nil {
		currentCheckpoint.Pending = remainingPending(cfg.Organization, pendingRepos, currentCheckpoint)
		currentCheckpoint.InProgress = nil
		currentCheckpoint.Progress = progressState
		if err := saveCheckpoint(true); err != nil {
			return nil, err
		}
		return results, ErrGracefulStop
	}

	if pauseErr != nil {
		// All goroutines have finished; apply the final rate-limit state to progressState.
		if rateLimiter.HasState() {
			rec := rateLimiter.RecommendedWorkers(numWorkers)
			progressState.WorkerRecommendation = rec
			progressState.ActiveWorkers = numWorkers
			if rec == 0 {
				progressState.ActiveWorkers = 0
			}
			progressState.RateLimitRemaining = rateLimiter.state.Remaining
			progressState.RateLimitResetAt = rateLimiter.state.ResetAt
		}
		currentCheckpoint.Pending = remainingPending(cfg.Organization, pendingRepos, currentCheckpoint)
		currentCheckpoint.InProgress = nil
		currentCheckpoint.Progress = progressState
		if err := checkpointStore.Save(currentCheckpoint); err != nil {
			return nil, fmt.Errorf("save checkpoint before rate limit pause: %w", err)
		}
		return results, pauseErr
	}

	return results, nil
}

func loadOrganizationReposWithCheckpoint(ctx context.Context, client GitHubClient, org string, checkpointStore *CheckpointStore, checkpoint *Checkpoint, progressState *ProgressState, onProgress func(discovered int)) ([]string, error) {
	allRepos := append([]string(nil), checkpoint.Discovered...)
	startPage := checkpoint.NextPage
	if startPage < 1 {
		startPage = 1
	}

	for page := startPage; ; page++ {
		repoNames, err := client.ListOrganizationRepos(ctx, org, page, 100)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				if saveErr := checkpointStore.Save(*checkpoint); saveErr != nil {
					return nil, fmt.Errorf("save checkpoint on shutdown: %w", saveErr)
				}
				return nil, ErrGracefulStop
			}
			return nil, fmt.Errorf("failed to list repositories on page %d: %w", page, err)
		}
		if len(repoNames) == 0 {
			return allRepos, nil
		}

		allRepos = append(allRepos, repoNames...)
		checkpoint.Discovered = append([]string(nil), allRepos...)
		checkpoint.NextPage = page + 1
		progressState.TotalRepos = len(allRepos)
		progressState.Phase = "enumerate"
		checkpoint.Progress = *progressState
		if onProgress != nil {
			onProgress(len(allRepos))
		}
		if err := checkpointStore.Save(*checkpoint); err != nil {
			return nil, fmt.Errorf("save checkpoint during repository enumeration: %w", err)
		}
	}
}

func terminalHeaderSettings() (int, bool) {
	width := 80
	colorEnabled := false

	if termWidth, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && termWidth > 0 {
		width = termWidth
	}

	if isatty.IsTerminal(os.Stdout.Fd()) && !color.NoColor {
		colorEnabled = true
	}

	return width, colorEnabled
}

// formatDuration returns a human-readable string for the given duration
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)

	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}

	m := d / time.Minute
	d -= m * time.Minute

	if m < 60 {
		return fmt.Sprintf("%dm %ds", m, int(d.Seconds()))
	}

	h := m / 60
	m -= h * 60

	return fmt.Sprintf("%dh %dm", h, m)
}

func interrupted(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)
}

// GetLastCommitDate retrieves the date of the last commit for a repository
func GetLastCommitDate(repoFullName string) (time.Time, error) {
	ctx := context.Background()
	client, err := gitHubClientFactory(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return client.GetLastCommitDate(ctx, repoFullName)
}

// GetContributorsStatus checks how many contributors are still active in the organization
func GetContributorsStatus(repoFullName, orgName string) (active, inactive int, err error) {
	ctx := context.Background()
	client, err := gitHubClientFactory(ctx)
	if err != nil {
		return 0, 0, err
	}
	return getContributorsStatusImpl(ctx, client, repoFullName, orgName, nil, time.Time{}, false)
}

func getContributorsStatusWithCache(ctx context.Context, client GitHubClient, repoFullName, orgName string, cache *CacheStore, now time.Time, refresh bool) (active, inactive int, err error) {
	return getContributorsStatusImpl(ctx, client, repoFullName, orgName, cache, now, refresh)
}

func getContributorsStatusImpl(ctx context.Context, client GitHubClient, repoFullName, orgName string, cache *CacheStore, now time.Time, refresh bool) (active, inactive int, err error) {
	contributors, err := client.ListContributors(ctx, repoFullName)
	if err != nil {
		return 0, 0, err
	}

	for _, contributor := range contributors {
		if cache != nil && !refresh {
			snapshot, ok, err := cache.GetMembership(orgName, contributor, now)
			if err != nil {
				return 0, 0, err
			}
			if ok {
				if snapshot.Active {
					active++
				} else {
					inactive++
				}
				continue
			}
		}

		isActive, err := client.IsOrgMember(ctx, orgName, contributor)
		if err != nil {
			inactive++
			continue
		}
		if isActive {
			active++
		} else {
			inactive++
		}

		if cache != nil {
			_ = cache.PutMembership(MembershipSnapshot{
				Organization: orgName,
				Login:        contributor,
				Active:       isActive,
				FetchedAt:    now,
			})
		}
	}

	return active, inactive, nil
}

func repositoryFromSnapshot(snapshot RepoSnapshot) Repository {
	return Repository{
		Name:                 snapshot.Repository,
		LastCommitDate:       snapshot.LastCommitDate,
		DaysSinceLastCommit:  snapshot.DaysSinceLastCommit,
		TotalContributors:    snapshot.TotalContributors,
		InactiveContributors: snapshot.InactiveContributors,
		InactivePercentage:   snapshot.InactivePercentage,
		Archived:             snapshot.Archived,
		Flagged:              snapshot.Flagged,
	}
}

func repoSnapshotFromRepository(repo Repository, fetchedAt time.Time) RepoSnapshot {
	return RepoSnapshot{
		Repository:           repo.Name,
		Archived:             repo.Archived,
		LastCommitDate:       repo.LastCommitDate,
		DaysSinceLastCommit:  repo.DaysSinceLastCommit,
		TotalContributors:    repo.TotalContributors,
		InactiveContributors: repo.InactiveContributors,
		InactivePercentage:   repo.InactivePercentage,
		Flagged:              repo.Flagged,
		FetchedAt:            fetchedAt,
	}
}

// OutputResults outputs the analysis results in the specified format
func OutputResults(repos []Repository, cfg config.Config) error {
	// Count flagged repositories
	flaggedCount := 0
	for _, repo := range repos {
		if repo.Flagged {
			flaggedCount++
		}
	}

	if cfg.OutputFormat == "json" {
		// Output as JSON
		data, err := json.MarshalIndent(repos, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}

		if cfg.OutputFile != "" {
			if err := os.WriteFile(cfg.OutputFile, data, 0644); err != nil {
				return fmt.Errorf("failed to write output file: %w", err)
			}
			fmt.Printf("💾 Results saved to %s\n", cfg.OutputFile)
		} else {
			fmt.Println(string(data))
		}
	} else if cfg.OutputFormat == "csv" {
		// Output as CSV
		var csvBuffer bytes.Buffer

		// Write CSV header
		csvBuffer.WriteString("Repository Name,Last Commit Date,Days Since Last Commit,Total Contributors,Inactive Contributors,Inactive Percentage,Archived,Flagged\n")

		// Write repository data
		for _, repo := range repos {
			csvBuffer.WriteString(fmt.Sprintf("%s,%s,%d,%d,%d,%.2f,%t,%t\n",
				repo.Name,
				repo.LastCommitDate.Format("2006-01-02"),
				repo.DaysSinceLastCommit,
				repo.TotalContributors,
				repo.InactiveContributors,
				repo.InactivePercentage*100,
				repo.Archived,
				repo.Flagged))
		}

		if cfg.OutputFile != "" {
			if err := os.WriteFile(cfg.OutputFile, csvBuffer.Bytes(), 0644); err != nil {
				return fmt.Errorf("failed to write CSV file: %w", err)
			}
			fmt.Printf("💾 Results saved to %s\n", cfg.OutputFile)
		} else {
			fmt.Println(csvBuffer.String())
		}

		// Print summary to console
		fmt.Printf("\n📊 Analysis Results for %s\n", cfg.Organization)
		fmt.Printf("Total repositories analyzed: %d\n", len(repos))
		fmt.Printf("🚩 Flagged repositories: %d\n", flaggedCount)
	} else {
		// Output to console in human-readable format
		fmt.Printf("\n📊 Analysis Results for %s\n", cfg.Organization)
		fmt.Printf("Total repositories analyzed: %d\n", len(repos))
		fmt.Printf("🚩 Flagged repositories: %d\n\n", flaggedCount)

		if flaggedCount > 0 {
			fmt.Println("🚩 Flagged Repositories:")
			fmt.Println("---------------------")
			for _, repo := range repos {
				if repo.Flagged {
					fmt.Printf("- %s\n", repo.Name)
					fmt.Printf("  Last commit: %s (%d days ago)\n",
						repo.LastCommitDate.Format("2006-01-02"), repo.DaysSinceLastCommit)
					fmt.Printf("  Contributors: %d total, %d inactive (%.1f%%)\n",
						repo.TotalContributors, repo.InactiveContributors,
						repo.InactivePercentage*100)
					if repo.Archived {
						fmt.Printf("  📦 Repository Status: Archived\n\n")
					} else {
						fmt.Printf("  📦 Repository Status: Not Archived\n\n")
					}
				}
			}
		}

		if cfg.OutputFile != "" {
			// Create a text report
			var reportBuf bytes.Buffer
			reportBuf.WriteString(fmt.Sprintf("Analysis Results for %s\n", cfg.Organization))
			reportBuf.WriteString(fmt.Sprintf("Date: %s\n", time.Now().Format("2006-01-02")))
			reportBuf.WriteString(fmt.Sprintf("Total repositories analyzed: %d\n", len(repos)))
			reportBuf.WriteString(fmt.Sprintf("Flagged repositories: %d\n\n", flaggedCount))

			if flaggedCount > 0 {
				reportBuf.WriteString("🚩 Flagged Repositories:\n")
				reportBuf.WriteString("---------------------\n")
				for _, repo := range repos {
					if repo.Flagged {
						reportBuf.WriteString(fmt.Sprintf("- %s\n", repo.Name))
						reportBuf.WriteString(fmt.Sprintf("  Last commit: %s (%d days ago)\n",
							repo.LastCommitDate.Format("2006-01-02"), repo.DaysSinceLastCommit))
						reportBuf.WriteString(fmt.Sprintf("  Contributors: %d total, %d inactive (%.1f%%)\n",
							repo.TotalContributors, repo.InactiveContributors,
							repo.InactivePercentage*100))
						if repo.Archived {
							reportBuf.WriteString("  Repository Status: Archived\n\n")
						} else {
							reportBuf.WriteString("  Repository Status: Not Archived\n\n")
						}
					}
				}
			}

			if err := os.WriteFile(cfg.OutputFile, reportBuf.Bytes(), 0644); err != nil {
				return fmt.Errorf("failed to write output file: %w", err)
			}
			fmt.Printf("💾 Results saved to %s\n", cfg.OutputFile)
		}
	}

	return nil
}

// OutputSingleRepositoryResult outputs the analysis results for a single repository
func OutputSingleRepositoryResult(repo Repository, cfg config.Config) error {
	if cfg.OutputFormat == "json" {
		// Output as JSON
		data, err := json.MarshalIndent(repo, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}

		if cfg.OutputFile != "" {
			if err := os.WriteFile(cfg.OutputFile, data, 0644); err != nil {
				return fmt.Errorf("failed to write output file: %w", err)
			}
			fmt.Printf("💾 Results saved to %s\n", cfg.OutputFile)
		} else {
			fmt.Println(string(data))
		}
	} else if cfg.OutputFormat == "csv" {
		// Output as CSV
		var csvBuffer bytes.Buffer

		// Write CSV header
		csvBuffer.WriteString("Repository Name,Last Commit Date,Days Since Last Commit,Total Contributors,Inactive Contributors,Inactive Percentage,Archived,Flagged\n")

		// Write repository data
		csvBuffer.WriteString(fmt.Sprintf("%s,%s,%d,%d,%d,%.2f,%t,%t\n",
			repo.Name,
			repo.LastCommitDate.Format("2006-01-02"),
			repo.DaysSinceLastCommit,
			repo.TotalContributors,
			repo.InactiveContributors,
			repo.InactivePercentage*100,
			repo.Archived,
			repo.Flagged))

		if cfg.OutputFile != "" {
			if err := os.WriteFile(cfg.OutputFile, csvBuffer.Bytes(), 0644); err != nil {
				return fmt.Errorf("failed to write CSV file: %w", err)
			}
			fmt.Printf("💾 Results saved to %s\n", cfg.OutputFile)
		} else {
			fmt.Println(csvBuffer.String())
		}
	} else {
		// Output to console in human-readable format
		fmt.Printf("\n📊 Analysis Results for %s\n", repo.Name)
		fmt.Printf("Last commit: %s (%d days ago)\n",
			repo.LastCommitDate.Format("2006-01-02"), repo.DaysSinceLastCommit)
		fmt.Printf("Contributors: %d total, %d inactive (%.1f%%)\n",
			repo.TotalContributors, repo.InactiveContributors,
			repo.InactivePercentage*100)

		if repo.Archived {
			fmt.Println("📦 Repository Status: Archived")
		} else {
			fmt.Println("📦 Repository Status: Active (Not Archived)")
		}

		if repo.Flagged {
			fmt.Println("🚩 Status: Flagged as inactive")
		} else {
			fmt.Println("✅ Status: Active")
		}

		if cfg.OutputFile != "" {
			// Create a text report
			var reportBuf bytes.Buffer
			reportBuf.WriteString(fmt.Sprintf("Analysis Results for %s\n", repo.Name))
			reportBuf.WriteString(fmt.Sprintf("Date: %s\n", time.Now().Format("2006-01-02")))
			reportBuf.WriteString(fmt.Sprintf("Last commit: %s (%d days ago)\n",
				repo.LastCommitDate.Format("2006-01-02"), repo.DaysSinceLastCommit))
			reportBuf.WriteString(fmt.Sprintf("Contributors: %d total, %d inactive (%.1f%%)\n",
				repo.TotalContributors, repo.InactiveContributors,
				repo.InactivePercentage*100))

			if repo.Archived {
				reportBuf.WriteString("Repository Status: Archived\n")
			} else {
				reportBuf.WriteString("Repository Status: Not Archived\n")
			}

			if repo.Flagged {
				reportBuf.WriteString("Status: Flagged as inactive\n")
			} else {
				reportBuf.WriteString("Status: Active\n")
			}

			if err := os.WriteFile(cfg.OutputFile, reportBuf.Bytes(), 0644); err != nil {
				return fmt.Errorf("failed to write output file: %w", err)
			}
			fmt.Printf("💾 Results saved to %s\n", cfg.OutputFile)
		}
	}

	return nil
}

// isRepositoryArchived is defined in archive.go

// GetRepositoryDetails retrieves various details for a repository
func GetRepositoryDetails(repoFullName string) (time.Time, bool, error) {
	ctx := context.Background()
	client, err := gitHubClientFactory(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	metadata, err := client.GetRepoMetadata(ctx, repoFullName)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("failed to get repository details: %w", err)
	}
	return metadata.UpdatedAt, metadata.Archived, nil
}
