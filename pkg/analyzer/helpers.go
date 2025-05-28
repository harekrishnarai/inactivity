package analyzer

import (
	"time"
)

// getLastCommitDate retrieves the date of the last commit for a repository (unexported version for internal use)
func getLastCommitDate(repoFullName string) (time.Time, error) {
	// Delegate to the exported version
	return GetLastCommitDate(repoFullName)
}

// getContributorsStatus checks how many contributors are still active in the organization (unexported version for internal use)
func getContributorsStatus(repoFullName, orgName string) (active, inactive int, err error) {
	// Delegate to the exported version
	return GetContributorsStatus(repoFullName, orgName)
}

// isRepositoryArchived is defined in archive.go
