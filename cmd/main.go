package cmd

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/harekrishnarai/inactivity/pkg/analyzer"
	"github.com/harekrishnarai/inactivity/pkg/config"
)

const Version = "v0.3.0"

// Main is the entry point for the application
// It's exported so it can be called from the root package
func Main() {
	// Check if any command line arguments are provided
	if len(os.Args) < 2 {
		displayUsage()
		os.Exit(1)
	}

	cfg := config.Default()
	commonFlags := flag.NewFlagSet("common", flag.ExitOnError)
	commonFlags.IntVar(&cfg.MaxCommitAgeInDays, "days", cfg.MaxCommitAgeInDays, "Maximum age of last commit in days")
	commonFlags.Float64Var(&cfg.InactiveContribThreshold, "threshold", cfg.InactiveContribThreshold, "Threshold of inactive contributors (0.0-1.0)")
	commonFlags.StringVar(&cfg.OutputFormat, "format", cfg.OutputFormat, "Output format: console, json, or csv")
	commonFlags.StringVar(&cfg.OutputFile, "output", "", "Output file path (optional)")
	commonFlags.BoolVar(&cfg.Silent, "silent", false, "Suppress banner and progress output")

	switch os.Args[1] {
	case "org":
		cfg, err := parseOrgArgs(os.Args[2:])
		if err != nil {
			log.Fatalf("❌ Failed to parse org command flags: %v", err)
		}
		// Run the organization analysis
		analyzeOrganization(cfg)

	case "repo":
		// New functionality: analyze a single repository
		repoCmd := flag.NewFlagSet("repo", flag.ExitOnError)

		// Parse repo command flags
		if len(os.Args) < 3 {
			fmt.Println("❌ Error: Repository name required")
			fmt.Println("Usage: inactivity repo <org/repo-name> [options]")
			os.Exit(1)
		}

		// Set the repository name
		cfg.SingleRepository = os.Args[2]
		// Parse remaining flags
		if len(os.Args) > 3 {
			// Copy common flags to repo command
			commonFlags.VisitAll(func(f *flag.Flag) {
				if rg := repoCmd.Lookup(f.Name); rg == nil {
					repoCmd.Var(f.Value, f.Name, f.Usage)
				}
			})

			// Parse repo command with common flags
			if err := repoCmd.Parse(os.Args[3:]); err != nil {
				log.Fatalf("❌ Error parsing command flags: %v", err)
			} // Check for format as a positional argument
			if repoCmd.NArg() >= 1 {
				if repoCmd.Arg(0) == "json" || repoCmd.Arg(0) == "csv" || repoCmd.Arg(0) == "console" {
					cfg.OutputFormat = repoCmd.Arg(0)
				}
			}

			// Check for output as a separate positional argument
			for i := 0; i < repoCmd.NArg(); i++ {
				if repoCmd.Arg(i) == "-output" && i+1 < repoCmd.NArg() {
					cfg.OutputFile = repoCmd.Arg(i + 1)
					break
				}
			}
		}

		// Run the single repository analysis
		analyzeSingleRepository(cfg)

	case "file":
		// New functionality: analyze repositories from a file
		fileCmd := flag.NewFlagSet("file", flag.ExitOnError)

		// Parse file command flags
		if len(os.Args) < 3 {
			fmt.Println("❌ Error: File path required")
			fmt.Println("Usage: inactivity file <file-path> [options]")
			os.Exit(1)
		}

		// Set the repository list file path
		cfg.RepoListFile = os.Args[2]
		// Parse remaining flags
		if len(os.Args) > 3 {
			// Copy common flags to file command
			commonFlags.VisitAll(func(f *flag.Flag) {
				if fg := fileCmd.Lookup(f.Name); fg == nil {
					fileCmd.Var(f.Value, f.Name, f.Usage)
				}
			})

			// Parse file command with common flags
			if err := fileCmd.Parse(os.Args[3:]); err != nil {
				log.Fatalf("❌ Error parsing command flags: %v", err)
			} // Check for format as a positional argument
			if fileCmd.NArg() >= 1 {
				if fileCmd.Arg(0) == "json" || fileCmd.Arg(0) == "csv" || fileCmd.Arg(0) == "console" {
					cfg.OutputFormat = fileCmd.Arg(0)
				}
			}

			// Check for output as a separate positional argument
			for i := 0; i < fileCmd.NArg(); i++ {
				if fileCmd.Arg(i) == "-output" && i+1 < fileCmd.NArg() {
					cfg.OutputFile = fileCmd.Arg(i + 1)
					break
				}
			}
		}

		// Default to CSV format for file-based analysis
		if cfg.OutputFormat == "console" {
			cfg.OutputFormat = "csv"
		}

		// Run the file-based repository analysis
		analyzeRepositoriesFromFile(cfg)

	case "help":
		displayUsage()

	default:
		fmt.Printf("❌ Unknown command: %s\n", os.Args[1])
		displayUsage()
		os.Exit(1)
	}
}

func parseOrgArgs(args []string) (config.Config, error) {
	cfg := config.Default()

	orgCmd := flag.NewFlagSet("org", flag.ContinueOnError)
	orgCmd.SetOutput(io.Discard)

	orgCmd.StringVar(&cfg.Organization, "org", "", "GitHub organization to analyze")
	orgCmd.IntVar(&cfg.MaxCommitAgeInDays, "days", cfg.MaxCommitAgeInDays, "Maximum age of last commit in days")
	orgCmd.Float64Var(&cfg.InactiveContribThreshold, "threshold", cfg.InactiveContribThreshold, "Threshold of inactive contributors (0.0-1.0)")
	orgCmd.StringVar(&cfg.OutputFormat, "format", cfg.OutputFormat, "Output format: console, json, or csv")
	orgCmd.StringVar(&cfg.OutputFile, "output", "", "Output file path (optional)")
	orgCmd.BoolVar(&cfg.Silent, "silent", false, "Suppress banner and progress output")
	orgCmd.BoolVar(&cfg.Resume, "resume", false, "Resume from the latest checkpoint for this org scan")
	orgCmd.BoolVar(&cfg.Refresh, "refresh", false, "Ignore cached repo and membership data")
	orgCmd.IntVar(&cfg.Workers, "workers", cfg.Workers, "Maximum concurrent repository workers")
	orgCmd.IntVar(&cfg.RateLimitFloor, "rate-limit-floor", cfg.RateLimitFloor, "Pause the scan when the remaining GitHub API budget reaches this floor")
	orgCmd.StringVar(&cfg.CacheDir, "cache-dir", cfg.CacheDir, "Directory used for local cache files")
	orgCmd.StringVar(&cfg.CheckpointDir, "checkpoint-dir", cfg.CheckpointDir, "Directory used for resumable checkpoint files")

	if err := orgCmd.Parse(args); err != nil {
		return config.Config{}, err
	}

	if orgCmd.NArg() > 0 {
		if arg := orgCmd.Arg(0); arg == "json" || arg == "csv" || arg == "console" {
			cfg.OutputFormat = arg
		} else if cfg.Organization == "" {
			// Treat the first positional arg as the org name when not a known format specifier.
			// This supports: inactivity org meltwater --resume
			cfg.Organization = arg
		}

		for i := 0; i < orgCmd.NArg(); i++ {
			if orgCmd.Arg(i) == "-output" && i+1 < orgCmd.NArg() {
				cfg.OutputFile = orgCmd.Arg(i + 1)
				break
			}
		}
	}

	return cfg, nil
}

func handleAnalyzeOrganizationError(err error) bool {
	if errors.Is(err, analyzer.ErrRateLimitPause) {
		fmt.Println("Scan paused at the configured rate-limit floor. Re-run with --resume to continue.")
		return true
	}

	if errors.Is(err, analyzer.ErrGracefulStop) {
		fmt.Println("Scan stopped gracefully. Re-run with --resume to continue.")
		return true
	}

	return false
}

// displayUsage shows the usage information for the tool
func displayUsage() {
	// Create color functions
	cyan := color.New(color.FgCyan, color.Bold).SprintFunc()
	yellow := color.New(color.FgYellow, color.Bold).SprintFunc()
	green := color.New(color.FgGreen, color.Bold).SprintFunc()

	fmt.Printf("\n%s\n\n", cyan(fmt.Sprintf("Repository Inactivity Analyzer %s", Version)))
	fmt.Printf("%s\n", yellow("Usage:"))
	fmt.Printf("  %s\n", green("inactivity org [options]"))
	fmt.Printf("  %s\n", green("inactivity org [format] [options]  # Alternative syntax"))
	fmt.Printf("  %s\n", green("inactivity repo <org/repo-name> [options]"))
	fmt.Printf("  %s\n", green("inactivity file <file-path> [options]"))
	fmt.Printf("  %s\n\n", green("inactivity help"))

	fmt.Printf("%s\n", yellow("Commands:"))
	fmt.Printf("  %s\t%s\n", green("org"), "Analyze all repositories in an organization")
	fmt.Printf("  %s\t%s\n", green("repo"), "Analyze a single repository")
	fmt.Printf("  %s\t%s\n", green("file"), "Analyze repositories from a file")
	fmt.Printf("  %s\t%s\n\n", green("help"), "Show this help message")

	fmt.Printf("%s\n", yellow("Output Formats:"))
	fmt.Printf("  %s\t%s\n", green("console"), "Display results in human-readable format (default)")
	fmt.Printf("  %s\t%s\n", green("json"), "Output results in JSON format")
	fmt.Printf("  %s\t%s\n\n", green("csv"), "Output results in CSV format")

	fmt.Printf("%s\n", yellow("Options:"))
	fmt.Printf("  %s\t%s\n", green("-days int"), "Maximum age of last commit in days (default: 180)")
	fmt.Printf("  %s\t%s\n", green("-threshold float"), "Threshold of inactive contributors (0.0-1.0) (default: 0.5)")
	fmt.Printf("  %s\t%s\n", green("-format string"), "Output format: console, json, or csv (default: console)")
	fmt.Printf("  %s\t%s\n", green("-output string"), "Output file path (optional)")
	fmt.Printf("  %s\t%s\n", green("-silent"), "Suppress banner and progress output")
	fmt.Printf("  %s\t%s\n\n", green("-org string"), "GitHub organization to analyze (for 'org' command)")
	fmt.Printf("  %s\t%s\n", green("-resume"), "Resume from the latest checkpoint for this org scan")
	fmt.Printf("  %s\t%s\n", green("-refresh"), "Ignore cached repo and membership data")
	fmt.Printf("  %s\t%s\n", green("-workers int"), "Maximum concurrent repository workers (default: 6)")
	fmt.Printf("  %s\t%s\n", green("-rate-limit-floor int"), "Pause the scan when the remaining GitHub API budget reaches this floor (default: 200)")
	fmt.Printf("  %s\t%s\n", green("-cache-dir string"), "Directory used for local cache files (default: .inactivity/cache)")
	fmt.Printf("  %s\t%s\n\n", green("-checkpoint-dir string"), "Directory used for resumable checkpoint files (default: .inactivity/checkpoints)")

	fmt.Printf("%s\n", yellow("Examples:"))
	fmt.Printf("  %s\n", green("inactivity org -org mycompany"))
	fmt.Printf("  %s\n", green("inactivity org -org mycompany -resume"))
	fmt.Printf("  %s\n", green("inactivity org -org mycompany -workers 8 -rate-limit-floor 300"))
	fmt.Printf("  %s\n", green("inactivity org -org mycompany -refresh"))
	fmt.Printf("  %s\n", green("inactivity repo mycompany/myrepo -days 90"))
	fmt.Printf("  %s\n", green("inactivity file repos.txt -format csv -output results.csv"))
	fmt.Printf("  %s\n", green("inactivity org -org mycompany -format json -output results.json"))
	fmt.Printf("  %s\n", green("inactivity repo mycompany/myrepo -format csv -output repo-result.csv"))
	fmt.Printf("  %s\n\n", green("inactivity org csv -output results.csv  # Alternative format syntax"))
}

// analyzeOrganization analyzes all repositories in an organization
func analyzeOrganization(cfg config.Config) {
	// Validate GitHub CLI installation
	if err := analyzer.ValidateGitHubCLI(); err != nil {
		log.Fatalf("❌ GitHub CLI validation failed: %v", err)
	}

	// When resuming without an explicit org, try to auto-detect from the latest checkpoint.
	if cfg.Resume && cfg.Organization == "" {
		cs := analyzer.NewCheckpointStore(cfg.CheckpointDir)
		if target, err := cs.LoadLatestTarget(); err == nil && target != "" {
			cfg.Organization = target
			if !cfg.Silent {
				fmt.Printf("🔄 Resuming latest scan for: %s\n", cfg.Organization)
			}
		}
	}

	// Get available organizations
	orgs, err := analyzer.GetUserOrganizations()
	if err != nil {
		log.Fatalf("❌ Failed to get organizations: %v", err)
	}

	// If organization is not provided, let the user select from available ones
	if cfg.Organization == "" {
		if !cfg.Silent {
			// Create color functions
			cyan := color.New(color.FgCyan, color.Bold).SprintFunc()
			yellow := color.New(color.FgYellow, color.Bold).SprintFunc()
			green := color.New(color.FgGreen, color.Bold).SprintFunc()
			magenta := color.New(color.FgMagenta, color.Bold).SprintFunc()

			fmt.Printf("%s\n", cyan("📋 Available organizations:"))
			for i, org := range orgs {
				fmt.Printf("%s %s\n", yellow(fmt.Sprintf("➡️ %d.", i+1)), green(org))
			}
			fmt.Printf("\n%s ", magenta("👉 Select an organization (enter the number):"))
			var choice int
			fmt.Scanln(&choice)
			if choice < 1 || choice > len(orgs) {
				log.Fatal("❌ Invalid selection")
			}
			cfg.Organization = orgs[choice-1]
		} else {
			// In silent mode, must provide organization as parameter
			log.Fatal("❌ Organization must be provided in silent mode")
		}
	}

	// Analyze repositories
	repos, err := analyzer.AnalyzeRepositories(cfg)
	if err != nil {
		if handleAnalyzeOrganizationError(err) {
			return
		}
		log.Fatalf("❌ Analysis failed: %v", err)
	}

	// Output results
	if err := analyzer.OutputResults(repos, cfg); err != nil {
		log.Fatalf("❌ Failed to output results: %v", err)
	}
}

// analyzeSingleRepository analyzes a single repository
func analyzeSingleRepository(cfg config.Config) { // Display banner unless silent mode is enabled
	if !cfg.Silent {
		// Use custom banner code here instead of calling DisplayBanner
		cyan := color.New(color.FgCyan).SprintFunc()
		yellow := color.New(color.FgYellow).SprintFunc()
		red := color.New(color.FgRed).SprintFunc()
		green := color.New(color.FgGreen).SprintFunc()
		purple := color.New(color.FgMagenta).SprintFunc()
		blue := color.New(color.FgBlue).SprintFunc()
		white := color.New(color.FgHiWhite, color.Bold).SprintFunc()
		brightGreen := color.New(color.FgHiGreen).SprintFunc()

		// Print a creative ASCII art banner
		fmt.Println()
		fmt.Println(blue("╔══════════════════════════════════════════════════════════╗"))
		fmt.Println(blue("║") + "                                                          " + blue("║"))
		fmt.Println(blue("║") + "  " + red("   ██████╗ ███████╗██████╗  ██████╗     ██████╗ ██╗   ") + blue("║"))
		fmt.Println(blue("║") + "  " + brightGreen("   ██╔══██╗██╔════╝██╔══██╗██╔═══██╗    ██╔══██╗██║   ") + blue("║"))
		fmt.Println(blue("║") + "  " + yellow("   ██████╔╝█████╗  ██████╔╝██║   ██║    ██████╔╝██║   ") + blue("║"))
		fmt.Println(blue("║") + "  " + purple("   ██╔══██╗██╔══╝  ██╔═══╝ ██║   ██║    ██╔═══╝ ██║   ") + blue("║"))
		fmt.Println(blue("║") + "  " + cyan("   ██║  ██║███████╗██║     ╚██████╔╝    ██║     ███████╗") + blue("║"))
		fmt.Println(blue("║") + "  " + white("   ╚═╝  ╚═╝╚══════╝╚═╝      ╚═════╝     ╚═╝     ╚══════╝") + blue("║"))
		fmt.Println(blue("║") + "                                                          " + blue("║"))
		fmt.Println(blue("║") + "  " + purple("⚡") + white(" PULSE MONITOR") + cyan(" ⋮ ") + yellow("REPOSITORY ANALYZER") + purple(" ⚡") + "                  " + blue("║"))
		fmt.Println(blue("║") + "  " + green("  Single Repository Health & Activity Scanner") + "               " + blue("║"))
		fmt.Println(blue("║") + "                                                          " + blue("║"))
		fmt.Println(blue("╚══════════════════════════════════════════════════════════╝"))
		fmt.Println()

		fmt.Println(yellow("✦ Repository Inactivity Analyzer ✦"))
		fmt.Println(cyan("⟹ Analyzing a single repository for inactivity metrics"))
		fmt.Println()
	}

	// Validate GitHub CLI installation
	if err := analyzer.ValidateGitHubCLI(); err != nil {
		log.Fatalf("❌ GitHub CLI validation failed: %v", err)
	}

	// Validate repository name format
	if cfg.SingleRepository == "" {
		log.Fatal("❌ Repository name is required")
	}

	// Extract org/repo from URL if a full GitHub URL is provided
	repoFullName := cfg.SingleRepository
	if strings.HasPrefix(repoFullName, "http") {
		// Handle URLs like https://github.com/org/repo or http://github.com/org/repo
		urlParts := strings.Split(repoFullName, "github.com/")
		if len(urlParts) != 2 {
			log.Fatalf("❌ Invalid GitHub URL format: %s", repoFullName)
		}

		// Get the org/repo part
		repoFullName = strings.TrimPrefix(urlParts[1], "/")

		// Remove any trailing slash or .git extension
		repoFullName = strings.TrimSuffix(repoFullName, "/")
		repoFullName = strings.TrimSuffix(repoFullName, ".git")
	}

	if !cfg.Silent {
		fmt.Printf("🔍 Analyzing repository: %s\n", repoFullName)
	}

	// Get repository parts (org/repo)
	parts := strings.Split(repoFullName, "/")
	if len(parts) != 2 {
		log.Fatalf("❌ Invalid repository name format. Expected 'org/repo', got: %s", repoFullName)
	}

	// Analyze single repository directly without calling GetUserOrganizations
	now := time.Now()

	// Create repository object
	repo := analyzer.Repository{
		Name: repoFullName,
	}

	// Validate repository exists and is accessible
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s", repoFullName),
		"--silent")

	if err := cmd.Run(); err != nil {
		log.Fatalf("❌ Repository %s not found or not accessible: %v", repoFullName, err)
	}
	// Get organization name from full repository name
	orgName := parts[0]

	// Check if repository is archived
	isArchived, err := analyzer.IsRepositoryArchived(repoFullName)
	if err != nil {
		log.Printf("⚠️ Warning: Failed to check if repository is archived: %v", err)
	} else {
		repo.Archived = isArchived
	}

	// Get last commit date
	lastCommitDate, err := analyzer.GetLastCommitDate(repoFullName)
	if err != nil {
		log.Fatalf("❌ Failed to get last commit date: %v", err)
	}
	repo.LastCommitDate = lastCommitDate
	repo.DaysSinceLastCommit = int(now.Sub(lastCommitDate).Hours() / 24)

	// Get contributors and check if they are still in the organization
	activeContribs, inactiveContribs, err := analyzer.GetContributorsStatus(repoFullName, orgName)
	if err != nil {
		log.Fatalf("❌ Failed to analyze contributors: %v", err)
	}

	repo.TotalContributors = activeContribs + inactiveContribs
	repo.InactiveContributors = inactiveContribs

	if repo.TotalContributors > 0 {
		repo.InactivePercentage = float64(inactiveContribs) / float64(repo.TotalContributors)
	}

	// Flag repository based on criteria
	isOld := repo.DaysSinceLastCommit > cfg.MaxCommitAgeInDays

	if isOld {
		if repo.TotalContributors > 0 {
			// If there are contributors, flag if the inactive percentage meets the threshold
			if repo.InactivePercentage >= cfg.InactiveContribThreshold {
				repo.Flagged = true
			}
		} else {
			// If there are no contributors, flag it simply for being old
			repo.Flagged = true
		}
	}

	// Output results for single repository
	if err := analyzer.OutputSingleRepositoryResult(repo, cfg); err != nil {
		log.Fatalf("❌ Failed to output results: %v", err)
	}
}

// analyzeRepositoriesFromFile analyzes repositories listed in a file
func analyzeRepositoriesFromFile(cfg config.Config) {
	// Display banner unless silent mode is enabled
	if !cfg.Silent {
		// Custom banner for file-based analysis
		cyan := color.New(color.FgCyan).SprintFunc()
		yellow := color.New(color.FgYellow).SprintFunc()
		red := color.New(color.FgRed).SprintFunc()
		green := color.New(color.FgGreen).SprintFunc()
		purple := color.New(color.FgMagenta).SprintFunc()
		blue := color.New(color.FgBlue).SprintFunc()
		white := color.New(color.FgHiWhite, color.Bold).SprintFunc()

		// Print a creative file analysis banner
		fmt.Println()
		fmt.Println(blue("┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓"))
		fmt.Println(blue("┃") + "                                                          " + blue("┃"))
		fmt.Println(blue("┃") + "   " + purple("📋") + white(" BATCH REPOSITORY ANALYZER ") + purple("📋") + "                       " + blue("┃"))
		fmt.Println(blue("┃") + "                                                          " + blue("┃"))
		fmt.Println(blue("┃") + "   " + yellow("🔍") + " " + green("Processing multiple repositories from file") + "           " + blue("┃"))
		fmt.Println(blue("┃") + "   " + red("📊") + " " + cyan("Analyzing contributor activity and commit freshness") + "    " + blue("┃"))
		fmt.Println(blue("┃") + "   " + green("📦") + " " + yellow("Identifying stale and abandoned repositories") + "         " + blue("┃"))
		fmt.Println(blue("┃") + "                                                          " + blue("┃"))
		fmt.Println(blue("┃") + "   " + white("REPO·PULSE") + " " + purple("※") + " " + white("VERSION 2025") + "                             " + blue("┃"))
		fmt.Println(blue("┃") + "                                                          " + blue("┃"))
		fmt.Println(blue("┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛"))
		fmt.Println()

		fmt.Println(yellow("✦ Repository Inactivity Analyzer - Batch Mode ✦"))
		fmt.Println(cyan("⟹ Processing repositories from file"))
		fmt.Println()
	}

	// Validate GitHub CLI installation
	if err := analyzer.ValidateGitHubCLI(); err != nil {
		log.Fatalf("❌ GitHub CLI validation failed: %v", err)
	}

	// Open the file containing repository names
	file, err := os.Open(cfg.RepoListFile)
	if err != nil {
		log.Fatalf("❌ Failed to open repository list file: %v", err)
	}
	defer file.Close()

	var repos []analyzer.Repository
	now := time.Now()

	// Count total number of repositories for progress reporting
	var totalRepos int
	preScanner := bufio.NewScanner(file)
	for preScanner.Scan() {
		line := strings.TrimSpace(preScanner.Text())
		if line != "" {
			totalRepos++
		}
	}

	// Reset file position for main scan
	if _, err := file.Seek(0, 0); err != nil {
		log.Fatalf("❌ Failed to reset file position: %v", err)
	}

	// Read the file line by line
	scanner := bufio.NewScanner(file)
	repoCount := 0

	if !cfg.Silent {
		fmt.Printf("\n🔍 Starting analysis of %d repositories from %s\n\n", totalRepos, cfg.RepoListFile)
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue // Skip empty lines
		}

		repoCount++

		// Extract org/repo from URL if a full GitHub URL is provided
		repoFullName := line
		if strings.HasPrefix(repoFullName, "http") {
			// Handle URLs like https://github.com/org/repo or http://github.com/org/repo
			urlParts := strings.Split(repoFullName, "github.com/")
			if len(urlParts) != 2 {
				if !cfg.Silent {
					log.Printf("❌ Invalid GitHub URL format: %s (skipping)", repoFullName)
				}
				continue
			}

			// Get the org/repo part
			repoFullName = strings.TrimPrefix(urlParts[1], "/")

			// Remove any trailing slash or .git extension
			repoFullName = strings.TrimSuffix(repoFullName, "/")
			repoFullName = strings.TrimSuffix(repoFullName, ".git")
		}

		if !cfg.Silent {
			fmt.Printf("📊 [%d/%d] Analyzing repository: %s\n", repoCount, totalRepos, repoFullName)
		}

		// Get repository parts (org/repo)
		parts := strings.Split(repoFullName, "/")
		if len(parts) != 2 {
			if !cfg.Silent {
				log.Printf("❌ Invalid repository name format. Expected 'org/repo', got: %s (skipping)", repoFullName)
			}
			continue
		}

		// Create repository object
		repo := analyzer.Repository{
			Name: repoFullName,
		}

		// Validate repository exists and is accessible
		cmd := exec.Command("gh", "api",
			fmt.Sprintf("repos/%s", repoFullName),
			"--silent")

		if err := cmd.Run(); err != nil {
			if !cfg.Silent {
				log.Printf("❌ Repository %s not found or not accessible (skipping)", repoFullName)
			}
			continue
		}

		// Get organization name from full repository name
		orgName := parts[0]

		// Get last commit date
		if !cfg.Silent {
			fmt.Printf("   ↳ Getting last commit date...")
		}
		lastCommitDate, err := analyzer.GetLastCommitDate(repoFullName)
		if err != nil {
			if !cfg.Silent {
				log.Printf("\r❌ Failed to get last commit date for %s: %v (skipping)\n", repoFullName, err)
			}
			continue
		}
		repo.LastCommitDate = lastCommitDate
		repo.DaysSinceLastCommit = int(now.Sub(lastCommitDate).Hours() / 24)
		if !cfg.Silent {
			fmt.Printf("\r   ↳ Last commit: %s (%d days ago)  \n",
				lastCommitDate.Format("2006-01-02"), repo.DaysSinceLastCommit)
		}

		// Get contributors and check if they are still in the organization
		if !cfg.Silent {
			fmt.Printf("   ↳ Analyzing contributors...")
		}
		activeContribs, inactiveContribs, err := analyzer.GetContributorsStatus(repoFullName, orgName)
		if err != nil {
			if !cfg.Silent {
				log.Printf("\r❌ Failed to analyze contributors for %s: %v (skipping)\n", repoFullName, err)
			}
			continue
		}

		repo.TotalContributors = activeContribs + inactiveContribs
		repo.InactiveContributors = inactiveContribs

		if repo.TotalContributors > 0 {
			repo.InactivePercentage = float64(inactiveContribs) / float64(repo.TotalContributors)
		}

		// Flag repository based on criteria
		isOld := repo.DaysSinceLastCommit > cfg.MaxCommitAgeInDays

		if isOld {
			if repo.TotalContributors > 0 {
				// If there are contributors, flag if the inactive percentage meets the threshold
				if repo.InactivePercentage >= cfg.InactiveContribThreshold {
					repo.Flagged = true
				}
			} else {
				// If there are no contributors, flag it simply for being old
				repo.Flagged = true
			}
		}

		if !cfg.Silent {
			fmt.Printf("\r   ↳ Contributors: %d total, %d inactive (%.1f%%)  \n",
				repo.TotalContributors, repo.InactiveContributors,
				repo.InactivePercentage*100)

			if repo.Flagged {
				fmt.Printf("   ↳ Status: %s\n", color.RedString("🚩 Flagged as inactive"))
			} else {
				fmt.Printf("   ↳ Status: %s\n", color.GreenString("✅ Active"))
			}
			fmt.Println()
		}

		repos = append(repos, repo)
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("❌ Error reading repository list file: %v", err)
	}

	if !cfg.Silent {
		fmt.Printf("✅ Analysis completed for %d repositories\n\n", len(repos))
	}

	// Output results for file-based analysis
	if err := analyzer.OutputResults(repos, cfg); err != nil {
		log.Fatalf("❌ Failed to output results: %v", err)
	}
}
