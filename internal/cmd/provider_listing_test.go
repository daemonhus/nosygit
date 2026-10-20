package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v37/github"
	"github.com/xanzy/go-gitlab"
	"nosygit/internal/config"
	"nosygit/internal/session"
)

func newTestGithubSession(t *testing.T, mux *http.ServeMux) *session.GithubSession {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := github.NewClient(nil)
	base, _ := url.Parse(srv.URL + "/")
	client.BaseURL = base
	return &session.GithubSession{Client: client}
}

func TestGetReposListsOrgPrivateReposAcrossPages(t *testing.T) {
	mux := http.NewServeMux()
	var base string
	mux.HandleFunc("/orgs/acme/repos", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("type"); got != "all" {
			t.Errorf("type = %q, want all", got)
		}
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/orgs/acme/repos?page=2>; rel="next"`, base))
			fmt.Fprint(w, `[{"name":"public-one"}]`)
		default:
			fmt.Fprint(w, `[{"name":"private-two","private":true}]`)
		}
	})
	gh := newTestGithubSession(t, mux)
	base = gh.Client.BaseURL.String()
	base = base[:len(base)-1]

	repos, err := getRepos(gh, "acme")
	if err != nil {
		t.Fatalf("getRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("got %d repos, want 2 (both pages)", len(repos))
	}
}

func TestGetReposFallsBackToUserAccount(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/orgs/solo/repos", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/users/solo/repos", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"name":"mine"}]`)
	})
	gh := newTestGithubSession(t, mux)

	repos, err := getRepos(gh, "solo")
	if err != nil {
		t.Fatalf("getRepos: %v", err)
	}
	if len(repos) != 1 || repos[0].GetName() != "mine" {
		t.Fatalf("expected user repo fallback, got %v", repos)
	}
}

func TestSyncGitlabMergeRequestsForbiddenIsNotAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"403 Forbidden"}`)
	}))
	t.Cleanup(srv.Close)
	client, err := gitlab.NewClient("token", gitlab.WithBaseURL(srv.URL), gitlab.WithoutRetries())
	if err != nil {
		t.Fatalf("gitlab client: %v", err)
	}
	ctx := context.WithValue(context.Background(), "ctx", config.ContextStruct{})

	err = syncGitlabMergeRequests(ctx, &session.GitlabSession{Client: client}, config.RepoState{Name: "g/p"}, 1)
	if !errors.Is(err, errMergeRequestsForbidden) {
		t.Fatalf("got %v, want errMergeRequestsForbidden", err)
	}
}
