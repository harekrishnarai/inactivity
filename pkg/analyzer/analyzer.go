package analyzer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/harekrishnarai/inactivity/pkg/config"
	"github.com/schollz/progressbar/v3"
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
	// Use pagination to get all repositories in the organization
	// We'll start with a higher limit and implement pagination logic
	var allRepos []struct {
		Name string `json:"name"`
	}

	page := 1
	perPage := 100 // GitHub API typically uses 100 as maximum per page

	for {
		if !cfg.Silent {
			fmt.Printf("📄 Fetching page %d of repositories...\n", page)
		}

		cmd := exec.Command("gh", "api",
			fmt.Sprintf("orgs/%s/repos?per_page=%d&page=%d", cfg.Organization, perPage, page),
			"--jq", ".[].name")

		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("failed to list repositories on page %d: %w", page, err)
		}

		// Get repo names from the output
		repoNames := strings.Split(strings.TrimSpace(out.String()), "\n")

		// If we got fewer items than perPage or empty response, we've reached the end
		if len(repoNames) == 0 || (len(repoNames) == 1 && repoNames[0] == "") {
			break
		}

		// Add repos to our collection
		for _, name := range repoNames {
			if name != "" { // Skip empty lines
				allRepos = append(allRepos, struct {
					Name string `json:"name"`
				}{Name: name})
			}
		}

		// Check if we got fewer items than the maximum per page, which means we're done
		if len(repoNames) < perPage {
			break
		}

		page++
	}

	if !cfg.Silent {
		fmt.Printf("📂 Found %d repositories in %s\n", len(allRepos), cfg.Organization)
	}

	var results []Repository
	now := time.Now()
	startTime := time.Now()

	// Define color functions for progress bar if not in silent mode
	var cyan func(...interface{}) string
	if !cfg.Silent {
		cyan = color.New(color.FgCyan).SprintFunc()
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

	// Analyze each repository
	for i, repo := range allRepos {
		repoFullName := fmt.Sprintf("%s/%s", cfg.Organization, repo.Name)
		r := Repository{
			Name: repoFullName,
		}
		// Check if repository is archived
		isArchived, err := isRepositoryArchived(repoFullName)
		if err != nil {
			if !cfg.Silent {
				fmt.Printf("⚠️ Warning: Failed to check if repository is archived for %s: %v\n", repoFullName, err)
			}
			continue
		}
		r.Archived = isArchived

		// Get last commit date
		lastCommitDate, err := getLastCommitDate(repoFullName)
		if err != nil {
			if !cfg.Silent {
				fmt.Printf("⚠️ Warning: Failed to get last commit date for %s: %v\n", repoFullName, err)
			}
			continue
		}
		r.LastCommitDate = lastCommitDate
		r.DaysSinceLastCommit = int(now.Sub(lastCommitDate).Hours() / 24)

		// Get contributors and check if they are still in the organization
		activeContribs, inactiveContribs, err := getContributorsStatus(repoFullName, cfg.Organization)
		if err != nil {
			if !cfg.Silent {
				fmt.Printf("⚠️ Warning: Failed to analyze contributors for %s: %v\n", repoFullName, err)
			}
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

		// Update progress bar with elapsed time information
		if !cfg.Silent && bar != nil {
			elapsed := time.Since(startTime)
			timePerRepo := time.Duration(0)
			if i+1 > 0 {
				timePerRepo = elapsed / time.Duration(i+1)
			}
			remaining := timePerRepo * time.Duration(len(allRepos)-i-1)

			percentDone := float64(i+1) / float64(len(allRepos)) * 100
			// Apply color to the progress bar description string
			bar.Describe(fmt.Sprintf("%s [%.1f%%] [%s elapsed, %s remaining]",
				cyan("⚡ Analyzing repositories"), percentDone, formatDuration(elapsed), formatDuration(remaining)))
			_ = bar.Add(1) // Use _ = to ignore error return value
		}
	}

	return results, nil
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

// GetLastCommitDate retrieves the date of the last commit for a repository
func GetLastCommitDate(repoFullName string) (time.Time, error) {
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/commits", repoFullName),
		"--jq", ".[0].commit.committer.date",
		"--method", "GET",
		"--paginate",
		"--cache", "1h")

	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get commits: %w", err)
	}

	dateStr := strings.TrimSpace(out.String())
	if dateStr == "" {
		return time.Time{}, fmt.Errorf("no commits found")
	}

	// Fix: Split the result and take only the first date if there are multiple
	// This happens because --paginate might return multiple results
	dates := strings.Split(dateStr, "\n")
	firstDate := dates[0]

	// Parse the ISO 8601 date format
	return time.Parse(time.RFC3339, firstDate)
}

// GetContributorsStatus checks how many contributors are still active in the organization
func GetContributorsStatus(repoFullName, orgName string) (active, inactive int, err error) {
	// Get all contributors
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/contributors", repoFullName),
		"--jq", ".[].login")

	var out bytes.Buffer
	cmd.Stdout = &out

	err = cmd.Run()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get contributors: %w", err)
	}

	contributors := strings.Split(strings.TrimSpace(out.String()), "\n")

	// Filter out empty strings
	var validContributors []string
	for _, c := range contributors {
		if c != "" {
			validContributors = append(validContributors, c)
		}
	}

	if len(validContributors) == 0 {
		return 0, 0, nil
	}

	// Check if each contributor is still in the organization
	for _, contributor := range validContributors {
		cmd := exec.Command("gh", "api",
			fmt.Sprintf("orgs/%s/members/%s", orgName, contributor),
			"--silent")

		if err := cmd.Run(); err != nil {
			// User is not in the organization anymore
			inactive++
		} else {
			active++
		}
	}

	return active, inactive, nil
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
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s", repoFullName),
		"--jq", "{archived: .archived, updated_at: .updated_at}")

	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		return time.Time{}, false, fmt.Errorf("failed to get repository details: %w", err)
	}

	var result struct {
		Archived  bool   `json:"archived"`
		UpdatedAt string `json:"updated_at"`
	}

	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		return time.Time{}, false, fmt.Errorf("failed to parse repository details: %w", err)
	}

	updatedAt, err := time.Parse(time.RFC3339, result.UpdatedAt)
	if err != nil {
		return time.Time{}, result.Archived, fmt.Errorf("failed to parse updated_at time: %w", err)
	}

	return updatedAt, result.Archived, nil
}
