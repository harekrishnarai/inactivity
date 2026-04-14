package analyzer

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
