package config

import "time"

type Config struct {
	Organization             string
	SingleRepository         string
	RepoListFile             string
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
	RepoCacheTTL             time.Duration
	MembershipCacheTTL       time.Duration
}

func Default() Config {
	return Config{
		MaxCommitAgeInDays:       180,
		InactiveContribThreshold: 0.5,
		OutputFormat:             "console",
		Workers:                  6,
		RateLimitFloor:           200,
		CacheDir:                 ".inactivity/cache",
		CheckpointDir:            ".inactivity/checkpoints",
		RepoCacheTTL:             30 * time.Minute,
		MembershipCacheTTL:       24 * time.Hour,
	}
}
