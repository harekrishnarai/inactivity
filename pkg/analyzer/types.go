package analyzer

import "time"

type ProgressState struct {
	Mode             string
	Target           string
	ResumeEnabled    bool
	Workers          int
	RateLimitFloor   int
	TotalRepos       int
	CompletedRepos   int
	CachedRepos      int
	RevalidatedRepos int
	FailedRepos      int
	ActiveWorkers    int
	Phase            string
}

type RepoSnapshot struct {
	Repository           string    `json:"repository"`
	Archived             bool      `json:"archived"`
	LastCommitDate       time.Time `json:"lastCommitDate"`
	DaysSinceLastCommit  int       `json:"daysSinceLastCommit"`
	TotalContributors    int       `json:"totalContributors"`
	InactiveContributors int       `json:"inactiveContributors"`
	InactivePercentage   float64   `json:"inactivePercentage"`
	Flagged              bool      `json:"flagged"`
	FetchedAt            time.Time `json:"fetchedAt"`
}

type MembershipSnapshot struct {
	Organization string    `json:"organization"`
	Login        string    `json:"login"`
	Active       bool      `json:"active"`
	FetchedAt    time.Time `json:"fetchedAt"`
}

type CacheStore struct {
	root          string
	repoTTL       time.Duration
	membershipTTL time.Duration
}
