package analyzer

import (
	"context"
	"fmt"
)

// isRepositoryArchived is an internal function that checks if a repository is archived
func isRepositoryArchived(ctx context.Context, client GitHubClient, repoFullName string) (bool, error) {
	metadata, err := client.GetRepoMetadata(ctx, repoFullName)
	if err != nil {
		return false, fmt.Errorf("failed to check if repository is archived: %w", err)
	}
	return metadata.Archived, nil
}

// IsRepositoryArchived checks if a repository is archived in GitHub
func IsRepositoryArchived(repoFullName string) (bool, error) {
	client, err := gitHubClientFactory(context.Background())
	if err != nil {
		return false, err
	}
	return isRepositoryArchived(context.Background(), client, repoFullName)
}
