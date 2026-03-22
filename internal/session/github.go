package session

import (
	"context"
	"errors"
	"github.com/google/go-github/v37/github"
	"golang.org/x/oauth2"
	"os"
)

type GithubSession struct {
	TokenValue string
	Client     *github.Client
}

func InitGithubSession() (*GithubSession, error) {
	token, success := os.LookupEnv("GITHUB_TOKEN")
	if !success {
		return &GithubSession{}, errors.New("GITHUB_TOKEN not present in environment.")
	}

	gh := GithubSession{
		TokenValue: token,
	}

	err := gh.getClient()
	if err != nil {
		return &GithubSession{}, err
	}

	return &gh, nil
}

func (session *GithubSession) getClient() error {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: session.TokenValue},
	)
	tc := oauth2.NewClient(ctx, ts)
	session.Client = github.NewClient(tc)

	return nil
}
