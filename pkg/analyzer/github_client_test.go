package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeGitHubClient struct {
	reposByPage        map[int][]string
	metadataByRepo     map[string]RepoMetadata
	metadataErr        error
	lastCommitByRepo   map[string]time.Time
	lastCommitErr      error
	contributorsByRepo map[string][]string
	contributorsErr    error
	orgMembers         map[string]map[string]bool
	rateLimitState     RateLimitState
	rateLimitErr       error
}

func (f fakeGitHubClient) ListOrganizationRepos(_ context.Context, _ string, page, _ int) ([]string, error) {
	return append([]string(nil), f.reposByPage[page]...), nil
}

func (f fakeGitHubClient) GetRepoMetadata(_ context.Context, repoFullName string) (RepoMetadata, error) {
	if f.metadataErr != nil {
		return RepoMetadata{}, f.metadataErr
	}
	metadata, ok := f.metadataByRepo[repoFullName]
	if !ok {
		return RepoMetadata{}, errors.New("repo metadata not found")
	}
	return metadata, nil
}

func (f fakeGitHubClient) GetLastCommitDate(_ context.Context, repoFullName string) (time.Time, error) {
	if f.lastCommitErr != nil {
		return time.Time{}, f.lastCommitErr
	}
	date, ok := f.lastCommitByRepo[repoFullName]
	if !ok {
		return time.Time{}, errors.New("last commit not found")
	}
	return date, nil
}

func (f fakeGitHubClient) ListContributors(_ context.Context, repoFullName string) ([]string, error) {
	if f.contributorsErr != nil {
		return nil, f.contributorsErr
	}
	return append([]string(nil), f.contributorsByRepo[repoFullName]...), nil
}

func (f fakeGitHubClient) IsOrgMember(_ context.Context, orgName, login string) (bool, error) {
	return f.orgMembers[orgName][login], nil
}

func (f fakeGitHubClient) GetRateLimitState(_ context.Context) (RateLimitState, error) {
	if f.rateLimitErr != nil {
		return RateLimitState{}, f.rateLimitErr
	}
	return f.rateLimitState, nil
}

func TestGitHubClientContractWithFakeClient(t *testing.T) {
	client := fakeGitHubClient{
		reposByPage: map[int][]string{
			1: {"service-a", "service-b"},
			2: {"service-c"},
		},
		metadataByRepo: map[string]RepoMetadata{
			"acme/service-a": {
				Archived:  true,
				UpdatedAt: time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC),
			},
		},
		lastCommitByRepo: map[string]time.Time{
			"acme/service-a": time.Date(2026, 4, 10, 8, 30, 0, 0, time.UTC),
		},
		contributorsByRepo: map[string][]string{
			"acme/service-a": {"octocat", "hubot"},
		},
		orgMembers: map[string]map[string]bool{
			"acme": {
				"octocat": true,
				"hubot":   false,
			},
		},
		rateLimitState: RateLimitState{Limit: 5000, Remaining: 4999, Used: 1, ResetAt: time.Date(2026, 4, 14, 11, 0, 0, 0, time.UTC)},
	}

	repos, err := listOrganizationRepos(context.Background(), client, "acme")
	if err != nil {
		t.Fatalf("listOrganizationRepos returned error: %v", err)
	}
	if got, want := strings.Join(repos, ","), "service-a,service-b,service-c"; got != want {
		t.Fatalf("listOrganizationRepos = %q, want %q", got, want)
	}

	lastCommit, err := getLastCommitDate(context.Background(), client, "acme/service-a")
	if err != nil {
		t.Fatalf("getLastCommitDate returned error: %v", err)
	}
	if want := client.lastCommitByRepo["acme/service-a"]; !lastCommit.Equal(want) {
		t.Fatalf("getLastCommitDate = %s, want %s", lastCommit, want)
	}

	active, inactive, err := getContributorsStatusImpl(context.Background(), client, "acme/service-a", "acme", nil, time.Time{}, false)
	if err != nil {
		t.Fatalf("getContributorsStatusImpl returned error: %v", err)
	}
	if active != 1 || inactive != 1 {
		t.Fatalf("getContributorsStatusImpl = (%d, %d), want (1, 1)", active, inactive)
	}

	archived, err := isRepositoryArchived(context.Background(), client, "acme/service-a")
	if err != nil {
		t.Fatalf("isRepositoryArchived returned error: %v", err)
	}
	if !archived {
		t.Fatal("expected repository to be archived")
	}
}

func TestShellGitHubClientGetRepoMetadataSmoke(t *testing.T) {
	calls := 0
	client := newShellGitHubClient(func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls++
		if name != "gh" {
			t.Fatalf("command name = %q, want gh", name)
		}
		if got, want := strings.Join(args, " "), "api repos/acme/service-a"; got != want {
			t.Fatalf("args = %q, want %q", got, want)
		}

		payload, err := json.Marshal(struct {
			Archived  bool   `json:"archived"`
			UpdatedAt string `json:"updated_at"`
		}{
			Archived:  true,
			UpdatedAt: "2026-04-14T10:00:00Z",
		})
		if err != nil {
			t.Fatalf("Marshal returned error: %v", err)
		}
		return payload, nil
	})

	metadata, err := client.GetRepoMetadata(context.Background(), "acme/service-a")
	if err != nil {
		t.Fatalf("GetRepoMetadata returned error: %v", err)
	}
	if !metadata.Archived {
		t.Fatal("expected archived repository")
	}
	if calls != 1 {
		t.Fatalf("runner calls = %d, want 1", calls)
	}
}

func TestShellGitHubClientScanCriticalMethods(t *testing.T) {
	calls := make([]string, 0, 3)
	client := newShellGitHubClient(func(_ context.Context, name string, args ...string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		calls = append(calls, call)

		switch call {
		case "gh api repos/acme/service-a/commits --jq .[0].commit.committer.date --method GET --paginate --cache 1h":
			return []byte("2026-04-10T08:30:00Z\n"), nil
		case "gh api repos/acme/service-a/contributors --jq .[].login":
			return []byte("octocat\nhubot\n"), nil
		case "gh api orgs/acme/members/octocat --silent":
			return nil, nil
		default:
			t.Fatalf("unexpected command: %s", call)
		}
		return nil, nil
	})

	lastCommit, err := client.GetLastCommitDate(context.Background(), "acme/service-a")
	if err != nil {
		t.Fatalf("GetLastCommitDate returned error: %v", err)
	}
	if want := time.Date(2026, 4, 10, 8, 30, 0, 0, time.UTC); !lastCommit.Equal(want) {
		t.Fatalf("GetLastCommitDate = %s, want %s", lastCommit, want)
	}

	contributors, err := client.ListContributors(context.Background(), "acme/service-a")
	if err != nil {
		t.Fatalf("ListContributors returned error: %v", err)
	}
	if got, want := strings.Join(contributors, ","), "octocat,hubot"; got != want {
		t.Fatalf("ListContributors = %q, want %q", got, want)
	}

	isMember, err := client.IsOrgMember(context.Background(), "acme", "octocat")
	if err != nil {
		t.Fatalf("IsOrgMember returned error: %v", err)
	}
	if !isMember {
		t.Fatal("expected org member to be true")
	}

	if got, want := len(calls), 3; got != want {
		t.Fatalf("runner call count = %d, want %d", got, want)
	}
}

func TestTokenGitHubClientScanCriticalMethods(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer test-token"; got != want {
			t.Fatalf("authorization header = %q, want %q", got, want)
		}

		switch r.URL.Path {
		case "/repos/acme/service-a/commits":
			if got, want := r.URL.RawQuery, "per_page=1"; got != want {
				t.Fatalf("commits query = %q, want %q", got, want)
			}
			if err := json.NewEncoder(w).Encode([]map[string]any{
				{"commit": map[string]any{"committer": map[string]any{"date": "2026-04-10T08:30:00Z"}}},
			}); err != nil {
				t.Fatalf("Encode returned error: %v", err)
			}
		case "/repos/acme/service-a/contributors":
			if err := json.NewEncoder(w).Encode([]map[string]any{
				{"login": "octocat"},
				{"login": "hubot"},
			}); err != nil {
				t.Fatalf("Encode returned error: %v", err)
			}
		case "/orgs/acme/members/octocat":
			w.WriteHeader(http.StatusNoContent)
		case "/orgs/acme/members/hubot":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := newTokenGitHubClient("test-token", server.URL, server.Client())
	if err != nil {
		t.Fatalf("newTokenGitHubClient returned error: %v", err)
	}

	lastCommit, err := client.GetLastCommitDate(context.Background(), "acme/service-a")
	if err != nil {
		t.Fatalf("GetLastCommitDate returned error: %v", err)
	}
	if want := time.Date(2026, 4, 10, 8, 30, 0, 0, time.UTC); !lastCommit.Equal(want) {
		t.Fatalf("GetLastCommitDate = %s, want %s", lastCommit, want)
	}

	contributors, err := client.ListContributors(context.Background(), "acme/service-a")
	if err != nil {
		t.Fatalf("ListContributors returned error: %v", err)
	}
	if got, want := strings.Join(contributors, ","), "octocat,hubot"; got != want {
		t.Fatalf("ListContributors = %q, want %q", got, want)
	}

	isMember, err := client.IsOrgMember(context.Background(), "acme", "octocat")
	if err != nil {
		t.Fatalf("IsOrgMember returned error: %v", err)
	}
	if !isMember {
		t.Fatal("expected octocat to be an org member")
	}

	isMember, err = client.IsOrgMember(context.Background(), "acme", "hubot")
	if err != nil {
		t.Fatalf("IsOrgMember returned error: %v", err)
	}
	if isMember {
		t.Fatal("expected hubot to not be an org member")
	}
}

func TestNewGitHubClientUsesTokenClientWhenEnvSet(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")

	client, err := NewGitHubClient(context.Background())
	if err != nil {
		t.Fatalf("NewGitHubClient returned error: %v", err)
	}

	if _, ok := client.(*tokenGitHubClient); !ok {
		t.Fatalf("client type = %T, want *tokenGitHubClient", client)
	}
}

func TestNewGitHubClientFallsBackToShellWithoutToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")

	client, err := NewGitHubClient(context.Background())
	if err != nil {
		t.Fatalf("NewGitHubClient returned error: %v", err)
	}

	if _, ok := client.(*shellGitHubClient); !ok {
		t.Fatalf("client type = %T, want *shellGitHubClient", client)
	}
}

func TestNewTokenGitHubClientReturnsErrorOnInvalidBaseURL(t *testing.T) {
	client, err := newTokenGitHubClient("test-token", "http://[::1", http.DefaultClient)
	if err == nil {
		t.Fatal("expected error for invalid base URL")
	}
	if client != nil {
		t.Fatalf("client = %#v, want nil", client)
	}
}

func TestTokenGitHubClientGetRateLimitState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/rate_limit"; got != want {
			t.Fatalf("request path = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer test-token"; got != want {
			t.Fatalf("authorization header = %q, want %q", got, want)
		}

		if err := json.NewEncoder(w).Encode(map[string]any{
			"rate": map[string]any{
				"limit":     5000,
				"remaining": 4321,
				"used":      679,
				"reset":     time.Date(2026, 4, 14, 11, 0, 0, 0, time.UTC).Unix(),
			},
		}); err != nil {
			t.Fatalf("Encode returned error: %v", err)
		}
	}))
	defer server.Close()

	client, err := newTokenGitHubClient("test-token", server.URL, server.Client())
	if err != nil {
		t.Fatalf("newTokenGitHubClient returned error: %v", err)
	}

	state, err := client.GetRateLimitState(context.Background())
	if err != nil {
		t.Fatalf("GetRateLimitState returned error: %v", err)
	}

	if state.Limit != 5000 || state.Remaining != 4321 || state.Used != 679 {
		t.Fatalf("unexpected rate limit state: %+v", state)
	}
	if want := time.Date(2026, 4, 14, 11, 0, 0, 0, time.UTC); !state.ResetAt.Equal(want) {
		t.Fatalf("ResetAt = %s, want %s", state.ResetAt, want)
	}
}
