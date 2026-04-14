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
		allRepos, err = loadOrganizationReposWithCheckpoint(ctx, client, cfg.Organization, checkpointStore, &currentCheckpoint, &progressState)
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
		// Create a colorful progress bar like popular scanner tools
		bar = progressbar.NewOptions(len(allRepos),
			progressbar.OptionEnableColorCodes(false), // Set to false if using custom color functions for description
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
		remaining := timePerRepo * time.Duration(len(allRepos)-i-1)

		bar.Describe(fmt.Sprintf("%s [%s elapsed, %s remaining]",
			RenderProgressLine(progressState), formatDuration(elapsed), formatDuration(remaining)))
		_ = bar.Add(1)
	}

	// Analyze each repository
	for i, repoName := range pendingRepos {
		select {
		case <-ctx.Done():
			if err := saveCheckpoint(true); err != nil {
				return nil, err
			}
			return results, ErrGracefulStop
		default:
		}

		progressState.CompletedRepos = len(results)
		if rateLimiter.ShouldPoll(i) {
			if state, err := client.GetRateLimitState(ctx); err == nil {
				rateLimiter.Update(state)
			} else {
				rateLimiter.UseLastKnownOrFallback()
			}
		}
		if rateLimiter.HasState() {
			progressState.WorkerRecommendation = rateLimiter.RecommendedWorkers(cfg.Workers)
			progressState.ActiveWorkers = 1
			if progressState.WorkerRecommendation == 0 {
				progressState.ActiveWorkers = 0
			}
			progressState.RateLimitRemaining = rateLimiter.state.Remaining
			progressState.RateLimitResetAt = rateLimiter.state.ResetAt
			currentCheckpoint.Progress = progressState
			if rateLimiter.ShouldPause() {
				currentCheckpoint.InProgress = nil
				currentCheckpoint.Pending = append([]string(nil), pendingRepos[i:]...)
				currentCheckpoint.Progress = progressState
				if err := checkpointStore.Save(currentCheckpoint); err != nil {
					return nil, fmt.Errorf("save checkpoint before rate limit pause: %w", err)
				}
				return results, ErrRateLimitPause
			}
		} else {
			progressState.ActiveWorkers = 1
			progressState.WorkerRecommendation = cfg.Workers
			progressState.RateLimitRemaining = 0
			progressState.RateLimitResetAt = time.Time{}
		}

		repoFullName := fmt.Sprintf("%s/%s", cfg.Organization, repoName)
		currentCheckpoint.InProgress = []string{repoFullName}
		currentCheckpoint.Pending = append([]string(nil), pendingRepos[i+1:]...)
		if err := saveCheckpoint(false); err != nil {
			return nil, err
		}

		if !cfg.Refresh {
			snapshot, ok, err := cache.GetRepo(repoFullName, now)
			if err != nil {
				return nil, fmt.Errorf("get repo cache: %w", err)
			}
			if ok {
				progressState.CachedRepos++
				repo := repositoryFromSnapshot(snapshot)
				results = append(results, repo)
				currentCheckpoint.Completed[repoFullName] = snapshot
				delete(currentCheckpoint.Failed, repoFullName)
				currentCheckpoint.InProgress = nil
				progressState.CompletedRepos = len(results)
				if err := saveCheckpoint(false); err != nil {
					return nil, err
				}
				advanceProgress(i)
				continue
			}
		}

		r := Repository{Name: repoFullName}
		// Check if repository is archived
		isArchived, err := isRepositoryArchived(ctx, client, repoFullName)
		if err != nil {
			if interrupted(ctx, err) {
				if err := saveCheckpoint(true); err != nil {
					return nil, err
				}
				return results, ErrGracefulStop
			}
			if !cfg.Silent {
				fmt.Printf("⚠️ Warning: Failed to check if repository is archived for %s: %v\n", repoFullName, err)
			}
			progressState.FailedRepos++
			currentCheckpoint.Failed[repoFullName] = err.Error()
			currentCheckpoint.InProgress = nil
			if err := saveCheckpoint(false); err != nil {
				return nil, err
			}
			advanceProgress(i)
			continue
		}
		r.Archived = isArchived

		// Get last commit date
		lastCommitDate, err := getLastCommitDate(ctx, client, repoFullName)
		if err != nil {
			if interrupted(ctx, err) {
				if err := saveCheckpoint(true); err != nil {
					return nil, err
				}
				return results, ErrGracefulStop
			}
			if !cfg.Silent {
				fmt.Printf("⚠️ Warning: Failed to get last commit date for %s: %v\n", repoFullName, err)
			}
			progressState.FailedRepos++
			currentCheckpoint.Failed[repoFullName] = err.Error()
			currentCheckpoint.InProgress = nil
			if err := saveCheckpoint(false); err != nil {
				return nil, err
			}
			advanceProgress(i)
			continue
		}
		r.LastCommitDate = lastCommitDate
		r.DaysSinceLastCommit = int(now.Sub(lastCommitDate).Hours() / 24)

		// Get contributors and check if they are still in the organization
		activeContribs, inactiveContribs, err := getContributorsStatusWithCache(ctx, client, repoFullName, cfg.Organization, cache, now, cfg.Refresh)
		if err != nil {
			if interrupted(ctx, err) {
				if err := saveCheckpoint(true); err != nil {
					return nil, err
				}
				return results, ErrGracefulStop
			}
			if !cfg.Silent {
				fmt.Printf("⚠️ Warning: Failed to analyze contributors for %s: %v\n", repoFullName, err)
			}
			progressState.FailedRepos++
			currentCheckpoint.Failed[repoFullName] = err.Error()
			currentCheckpoint.InProgress = nil
			if err := saveCheckpoint(false); err != nil {
				return nil, err
			}
			advanceProgress(i)
			continue
		}

		r.TotalContributors = activeContribs + inactiveContribs
		r.InactiveContributors = inactiveContribs

		if r.TotalContributors > 0 {
			r.InactivePercentage = float64(inactiveContribs) / float64(r.TotalContributors)
		}

		// Flag repository based on criteria
		// 1. Repositories are flagged if they are archived
		// 2. Repositories are flagged if they meet the age and inactive contributor criteria

		// Always flag archived repositories
		if r.Archived {
			r.Flagged = true
		} else {
			// For non-archived repos, check age and contributor criteria
			isOld := r.DaysSinceLastCommit > cfg.MaxCommitAgeInDays

			if isOld {
				if r.TotalContributors > 0 {
					// If there are contributors, flag if the inactive percentage meets the threshold
					if r.InactivePercentage >= cfg.InactiveContribThreshold {
						r.Flagged = true
					}
				} else {
					// If there are no contributors, flag it simply for being old
					r.Flagged = true
				}
			}
		}

		results = append(results, r)
		currentCheckpoint.Completed[repoFullName] = repoSnapshotFromRepository(r, now)
		delete(currentCheckpoint.Failed, repoFullName)
		currentCheckpoint.InProgress = nil
		progressState.CompletedRepos = len(results)
		if err := cache.PutRepo(repoSnapshotFromRepository(r, now)); err != nil && !cfg.Silent {
			fmt.Printf("⚠️ Warning: Failed to store repo cache for %s: %v\n", repoFullName, err)
		}
		if err := saveCheckpoint(false); err != nil {
			return nil, err
		}
		advanceProgress(i)
	}

	return results, nil
}

func loadOrganizationReposWithCheckpoint(ctx context.Context, client GitHubClient, org string, checkpointStore *CheckpointStore, checkpoint *Checkpoint, progressState *ProgressState) ([]string, error) {
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
