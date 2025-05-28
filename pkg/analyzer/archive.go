package analyzer

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// isRepositoryArchived is an internal function that checks if a repository is archived
func isRepositoryArchived(repoFullName string) (bool, error) {
	// Delegate to the exported version
	return IsRepositoryArchived(repoFullName)
}

// IsRepositoryArchived checks if a repository is archived in GitHub
func IsRepositoryArchived(repoFullName string) (bool, error) {
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s", repoFullName),
		"--jq", ".archived")

	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		return false, fmt.Errorf("failed to check if repository is archived: %w", err)
	}

	result := strings.TrimSpace(out.String())
	return result == "true", nil
}
