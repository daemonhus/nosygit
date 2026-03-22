package logging

import (
	"fmt"
	"time"
)

type EventType int

const (
	// Repo events
	Repo EventType = iota + 1
	RepoVisibility

	// Commit events
	CommitNew
	File
	Regex
	CommitMessage
	CommitMetrics
	MergeStats
	Blob
	ContributorFirstSeen
	ChangeRequestOpened
	Branch
	MetaInfo
	MetaWarning
	MetaError
)

func (et EventType) String() string {
	values := [...]string{
		"repo",
		"repo-visibility",
		"commit-new",
		"file",
		"regex",
		"commit-message",
		"commit-metrics",
		"merge-stats",
		"blob",
		"contributor-first-seen",
		"change-request-opened",
		"branch",
		"meta-info",
		"meta-warning",
		"meta-error",
	}
	if et < Repo || et > MetaError {
		return fmt.Sprintf("EventType(%d)", int(et))
	}
	return values[et-1]
}

type Event struct {
	Name    string
	Type    EventType
	Meta    interface{}
	Message string
}

type AbstractMeta struct {
	Provider  interface{}
	RepoState interface{}
}

type RepoEventMeta struct {
	RepoName   string
	Visibility string
	Operation  string
	Provider   interface{}
	RepoState  interface{}
}

type RepoVisibilityEventMeta struct {
	RepoName  string
	From      string
	To        string
	Provider  interface{}
	RepoState interface{}
}

type FileEventMeta struct {
	Path       string
	Operation  string
	CommitHash string
	FirstLine  string
	Author     string
	Time       time.Time
	Provider   interface{}
	RepoState  interface{}
}

func (m FileEventMeta) GetProvider() interface{} {
	return m.Provider
}

type ContributorEventMeta struct {
	RepoName      string
	CommitHash    string
	FirstLine     string
	CommitterName string
	CommitterMail string
	Time          time.Time
	Provider      interface{}
	RepoState     interface{}
}

type CommitEventMeta struct {
	RepoName      string
	CommitHash    string
	FirstLine     string
	AuthorName    string
	AuthorEmail   string
	CommitterName string
	CommitterMail string
	Time          time.Time
	Provider      interface{}
	RepoState     interface{}
}

type ChangeRequestEventMeta struct {
	RepoName     string
	Provider     interface{}
	RepoState    interface{}
	ExternalID   string
	Number       *int
	Kind         string
	Action       string
	Title        string
	Description  string
	AuthorLogin  string
	State        string
	SourceBranch string
	TargetBranch string
	SourceSHA    string
	Comments     *int
	OpenedAt     *time.Time
	UpdatedAt    *time.Time
}

type BranchEventMeta struct {
	RepoName   string
	BranchName string
	Operation  string
	Provider   interface{}
	RepoState  interface{}
}

type MetaEventMeta struct {
	Severity  string
	Component string
	Code      string
	Details   map[string]interface{}
	Time      time.Time
	Provider  interface{}
	RepoState interface{}
}
