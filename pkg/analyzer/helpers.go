package analyzer

import (
	"context"
	"time"
)

// getLastCommitDate retrieves the date of the last commit for a repository (unexported version for internal use)
func getLastCommitDate(ctx context.Context, client GitHubClient, repoFullName string) (time.Time, error) {
	return client.GetLastCommitDate(ctx, repoFullName)
}

// getContributorsStatus checks how many contributors are still active in the organization (unexported version for internal use)
func getContributorsStatus(ctx context.Context, client GitHubClient, repoFullName, orgName string) (active, inactive int, err error) {
	return getContributorsStatusImpl(ctx, client, repoFullName, orgName, nil, time.Time{}, false)
}

// isRepositoryArchived is defined in archive.go
