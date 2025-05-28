package config

// Config holds the configuration for the inactivity analyzer
type Config struct {
	// Organization to analyze
	Organization string // GitHub organization name

	// SingleRepository is the name of a single repository to analyze (org/repo format)
	SingleRepository string // Single repository name to analyze

	// RepoListFile is the path to a file containing repository URLs to analyze
	RepoListFile string // Path to a file with repository URLs

	// MaxCommitAgeInDays is the maximum age of last commit in days
	MaxCommitAgeInDays int // Maximum age of last commit in days

	// InactiveContribThreshold is the threshold percentage of inactive contributors (0.0-1.0)
	InactiveContribThreshold float64 // Threshold of inactive contributors (0.0-1.0)

	// OutputFormat is the format of the output (console or json)
	OutputFormat string // Output format (console, json, csv)

	// OutputFile is the path to the output file (optional)
	OutputFile string // Output file path (optional)

	// Silent is whether to suppress non-essential output
	Silent bool // Whether to suppress non-essential output
}
