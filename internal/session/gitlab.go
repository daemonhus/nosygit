package session

import (
	"errors"
	"github.com/xanzy/go-gitlab"
	"log"
	"os"
)

type GitlabSession struct {
	TokenValue string
	Client     *gitlab.Client
}

func InitGitlabSession() (*GitlabSession, error) {
	token, success := os.LookupEnv("GITLAB_TOKEN")
	if !success {
		return &GitlabSession{}, errors.New("GITLAB_TOKEN not present in environment.")
	}

	gl := GitlabSession{
		TokenValue: token,
	}

	err := gl.getClient()
	if err != nil {
		return &GitlabSession{}, err
	}

	return &gl, nil
}

func (session *GitlabSession) getClient() error {
	gl, err := gitlab.NewClient(session.TokenValue)
	if err != nil {
		log.Fatal(err)
	}
	session.Client = gl
	return nil
}
