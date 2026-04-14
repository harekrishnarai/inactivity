package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

type RepoMetadata struct {
	Archived  bool
	UpdatedAt time.Time
}

type GitHubClient interface {
	ListOrganizationRepos(ctx context.Context, org string, page, perPage int) ([]string, error)
	GetRepoMetadata(ctx context.Context, repoFullName string) (RepoMetadata, error)
	GetLastCommitDate(ctx context.Context, repoFullName string) (time.Time, error)
	ListContributors(ctx context.Context, repoFullName string) ([]string, error)
	IsOrgMember(ctx context.Context, orgName, login string) (bool, error)
	GetRateLimitState(ctx context.Context) (RateLimitState, error)
}

type shellCommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

type shellGitHubClient struct {
	run shellCommandRunner
}

type tokenGitHubClient struct {
	baseURL    *url.URL
	httpClient *http.Client
	token      string
}

func NewGitHubClient(ctx context.Context) (GitHubClient, error) {
	return newGitHubClientWithToken(ctx, os.Getenv("GITHUB_TOKEN"))
}

func newGitHubClientWithToken(ctx context.Context, token string) (GitHubClient, error) {
	_ = ctx
	if strings.TrimSpace(token) != "" {
		return newTokenGitHubClient(token, "https://api.github.com", http.DefaultClient)
	}
	return newShellGitHubClient(defaultShellCommandRunner), nil
}

func newShellGitHubClient(run shellCommandRunner) *shellGitHubClient {
	return &shellGitHubClient{run: run}
}

func newTokenGitHubClient(token, baseURL string, httpClient *http.Client) (*tokenGitHubClient, error) {
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &tokenGitHubClient{
		baseURL:    parsedBaseURL,
		httpClient: httpClient,
		token:      token,
	}, nil
}

func defaultShellCommandRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(out.String()))
	}
	return out.Bytes(), nil
}

func (c *shellGitHubClient) ListOrganizationRepos(ctx context.Context, org string, page, perPage int) ([]string, error) {
	out, err := c.run(ctx, "gh", "api", fmt.Sprintf("orgs/%s/repos?per_page=%d&page=%d", org, perPage, page), "--jq", ".[].name")
	if err != nil {
		return nil, fmt.Errorf("list organization repositories: %w", err)
	}

	return splitLines(out), nil
}

func (c *shellGitHubClient) GetRepoMetadata(ctx context.Context, repoFullName string) (RepoMetadata, error) {
	out, err := c.run(ctx, "gh", "api", fmt.Sprintf("repos/%s", repoFullName))
	if err != nil {
		return RepoMetadata{}, fmt.Errorf("get repository metadata: %w", err)
	}

	var response struct {
		Archived  bool   `json:"archived"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal(out, &response); err != nil {
		return RepoMetadata{}, fmt.Errorf("parse repository metadata: %w", err)
	}
	return repoMetadataFromResponse(response.Archived, response.UpdatedAt)
}

func (c *shellGitHubClient) GetLastCommitDate(ctx context.Context, repoFullName string) (time.Time, error) {
	out, err := c.run(ctx, "gh", "api", fmt.Sprintf("repos/%s/commits", repoFullName), "--jq", ".[0].commit.committer.date", "--method", "GET", "--paginate", "--cache", "1h")
	if err != nil {
		return time.Time{}, fmt.Errorf("get last commit date: %w", err)
	}

	dates := splitLines(out)
	if len(dates) == 0 {
		return time.Time{}, fmt.Errorf("no commits found")
	}

	lastCommitDate, err := time.Parse(time.RFC3339, dates[0])
	if err != nil {
		return time.Time{}, fmt.Errorf("parse last commit date: %w", err)
	}
	return lastCommitDate, nil
}

func (c *shellGitHubClient) ListContributors(ctx context.Context, repoFullName string) ([]string, error) {
	out, err := c.run(ctx, "gh", "api", fmt.Sprintf("repos/%s/contributors", repoFullName), "--jq", ".[].login")
	if err != nil {
		return nil, fmt.Errorf("get contributors: %w", err)
	}
	return splitLines(out), nil
}

func (c *shellGitHubClient) IsOrgMember(ctx context.Context, orgName, login string) (bool, error) {
	if _, err := c.run(ctx, "gh", "api", fmt.Sprintf("orgs/%s/members/%s", orgName, login), "--silent"); err != nil {
		return false, err
	}
	return true, nil
}

func (c *shellGitHubClient) GetRateLimitState(ctx context.Context) (RateLimitState, error) {
	out, err := c.run(ctx, "gh", "api", "rate_limit")
	if err != nil {
		return RateLimitState{}, fmt.Errorf("get GitHub rate limit: %w", err)
	}

	return parseRateLimitState(out)
}

func (c *tokenGitHubClient) ListOrganizationRepos(ctx context.Context, org string, page, perPage int) ([]string, error) {
	var repos []struct {
		Name string `json:"name"`
	}
	if err := c.get(ctx, fmt.Sprintf("/orgs/%s/repos?per_page=%d&page=%d", org, perPage, page), &repos); err != nil {
		return nil, fmt.Errorf("list organization repositories: %w", err)
	}

	repoNames := make([]string, 0, len(repos))
	for _, repo := range repos {
		if repo.Name != "" {
			repoNames = append(repoNames, repo.Name)
		}
	}
	return repoNames, nil
}

func (c *tokenGitHubClient) GetRepoMetadata(ctx context.Context, repoFullName string) (RepoMetadata, error) {
	var response struct {
		Archived  bool   `json:"archived"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := c.get(ctx, "/repos/"+repoFullName, &response); err != nil {
		return RepoMetadata{}, fmt.Errorf("get repository metadata: %w", err)
	}
	return repoMetadataFromResponse(response.Archived, response.UpdatedAt)
}

func (c *tokenGitHubClient) GetLastCommitDate(ctx context.Context, repoFullName string) (time.Time, error) {
	var commits []struct {
		Commit struct {
			Committer struct {
				Date string `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := c.get(ctx, "/repos/"+repoFullName+"/commits?per_page=1", &commits); err != nil {
		return time.Time{}, fmt.Errorf("get last commit date: %w", err)
	}
	if len(commits) == 0 || commits[0].Commit.Committer.Date == "" {
		return time.Time{}, fmt.Errorf("no commits found")
	}

	lastCommitDate, err := time.Parse(time.RFC3339, commits[0].Commit.Committer.Date)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse last commit date: %w", err)
	}
	return lastCommitDate, nil
}

func (c *tokenGitHubClient) ListContributors(ctx context.Context, repoFullName string) ([]string, error) {
	var contributors []struct {
		Login string `json:"login"`
	}
	if err := c.get(ctx, "/repos/"+repoFullName+"/contributors", &contributors); err != nil {
		return nil, fmt.Errorf("get contributors: %w", err)
	}

	logins := make([]string, 0, len(contributors))
	for _, contributor := range contributors {
		if contributor.Login != "" {
			logins = append(logins, contributor.Login)
		}
	}
	return logins, nil
}

func (c *tokenGitHubClient) IsOrgMember(ctx context.Context, orgName, login string) (bool, error) {
	req, err := c.newRequest(ctx, "/orgs/"+orgName+"/members/"+login)
	if err != nil {
		return false, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return false, fmt.Errorf("read response body: %w", readErr)
	}
	return false, fmt.Errorf("GitHub API %s: %s", resp.Status, strings.TrimSpace(string(body)))
}

func (c *tokenGitHubClient) GetRateLimitState(ctx context.Context) (RateLimitState, error) {
	var response struct {
		Rate struct {
			Limit     int   `json:"limit"`
			Remaining int   `json:"remaining"`
			Used      int   `json:"used"`
			Reset     int64 `json:"reset"`
		} `json:"rate"`
	}
	if err := c.get(ctx, "/rate_limit", &response); err != nil {
		return RateLimitState{}, fmt.Errorf("get GitHub rate limit: %w", err)
	}

	return RateLimitState{
		Limit:     response.Rate.Limit,
		Remaining: response.Rate.Remaining,
		Used:      response.Rate.Used,
		ResetAt:   time.Unix(response.Rate.Reset, 0).UTC(),
	}, nil
}

func (c *tokenGitHubClient) get(ctx context.Context, path string, target any) error {
	req, err := c.newRequest(ctx, path)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub API %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func (c *tokenGitHubClient) newRequest(ctx context.Context, path string) (*http.Request, error) {
	relativeURL, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("parse path: %w", err)
	}
	endpoint := c.baseURL.ResolveReference(relativeURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return req, nil
}

func repoMetadataFromResponse(archived bool, updatedAt string) (RepoMetadata, error) {
	metadata := RepoMetadata{Archived: archived}
	if updatedAt != "" {
		parsedUpdatedAt, err := time.Parse(time.RFC3339, updatedAt)
		if err != nil {
			return RepoMetadata{}, fmt.Errorf("parse repository updated_at: %w", err)
		}
		metadata.UpdatedAt = parsedUpdatedAt
	}
	return metadata, nil
}

func parseRateLimitState(out []byte) (RateLimitState, error) {
	var response struct {
		Rate struct {
			Limit     int   `json:"limit"`
			Remaining int   `json:"remaining"`
			Used      int   `json:"used"`
			Reset     int64 `json:"reset"`
		} `json:"rate"`
	}
	if err := json.Unmarshal(out, &response); err != nil {
		return RateLimitState{}, fmt.Errorf("parse GitHub rate limit: %w", err)
	}

	return RateLimitState{
		Limit:     response.Rate.Limit,
		Remaining: response.Rate.Remaining,
		Used:      response.Rate.Used,
		ResetAt:   time.Unix(response.Rate.Reset, 0).UTC(),
	}, nil
}

func listOrganizationRepos(ctx context.Context, client GitHubClient, org string) ([]string, error) {
	var allRepos []string

	const perPage = 100
	for page := 1; ; page++ {
		repoNames, err := client.ListOrganizationRepos(ctx, org, page, perPage)
		if err != nil {
			return nil, fmt.Errorf("failed to list repositories on page %d: %w", page, err)
		}
		if len(repoNames) == 0 {
			break
		}

		allRepos = append(allRepos, repoNames...)
	}

	return allRepos, nil
}

func splitLines(out []byte) []string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var result []string
	for _, line := range lines {
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}
