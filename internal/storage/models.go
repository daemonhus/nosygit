package storage

import "time"

type stateMetaModel struct {
	ID      int `gorm:"primaryKey;check:id = 1"`
	LastRun *time.Time
}

func (stateMetaModel) TableName() string { return "state_meta" }

type providerPollModel struct {
	Provider     string `gorm:"primaryKey"`
	LastPolledAt time.Time
}

func (providerPollModel) TableName() string { return "provider_polls" }

type repoModel struct {
	ID             uint `gorm:"primaryKey"`
	Provider       string
	ProviderRepoID string
	Name           string `gorm:"not null"`
	CloneURL       string `gorm:"not null"`
	Public         bool   `gorm:"not null"`
	Head           string `gorm:"not null;default:''"`
	Driver         string `gorm:"not null"`
	DefaultBranch  string
	Archived       bool      `gorm:"not null;default:false"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (repoModel) TableName() string { return "repos" }

type repoContributorModel struct {
	ID          uint      `gorm:"primaryKey"`
	RepoID      uint      `gorm:"not null;index"`
	IdentityKey string    `gorm:"not null"`
	Name        string    `gorm:"not null;default:''"`
	Email       string    `gorm:"not null;default:''"`
	FirstSeen   time.Time `gorm:"not null"`
	LastSeen    time.Time `gorm:"not null"`
	Commits     int       `gorm:"not null;default:0;check:commits >= 0"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
}

func (repoContributorModel) TableName() string { return "repo_contributors" }

type commitModel struct {
	ID             uint   `gorm:"primaryKey"`
	RepoID         uint   `gorm:"not null;index"`
	SHA            string `gorm:"not null"`
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
	CommittedAt    *time.Time
	MessageTitle   string
	Message        string
	FirstSeenAt    time.Time `gorm:"not null"`
	Source         string    `gorm:"not null"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (commitModel) TableName() string { return "commits" }

type changeRequestModel struct {
	ID                uint   `gorm:"primaryKey"`
	RepoID            uint   `gorm:"not null;index"`
	Provider          string `gorm:"not null"`
	ExternalID        string `gorm:"not null"`
	Number            *int
	Kind              string `gorm:"not null"`
	Title             string `gorm:"not null"`
	Description       string
	AuthorLogin       string
	AuthorID          string
	SourceBranch      string
	TargetBranch      string
	SourceSHA         string
	CommentsCount     *int
	State             string `gorm:"not null"`
	IsDraft           bool   `gorm:"not null;default:false"`
	OpenedAt          *time.Time
	ClosedAt          *time.Time
	MergedAt          *time.Time
	UpdatedAtProvider *time.Time
	FirstSeenAt       time.Time `gorm:"not null"`
	Source            string    `gorm:"not null"`
	CreatedAt         time.Time `gorm:"not null"`
	UpdatedAt         time.Time `gorm:"not null"`
}

func (changeRequestModel) TableName() string { return "change_requests" }

type changeRequestCommitModel struct {
	ChangeRequestID uint      `gorm:"primaryKey"`
	CommitID        uint      `gorm:"primaryKey"`
	CreatedAt       time.Time `gorm:"not null"`
}

func (changeRequestCommitModel) TableName() string { return "change_request_commits" }

type webhookDeliveryModel struct {
	ID          uint      `gorm:"primaryKey"`
	Provider    string    `gorm:"not null"`
	DeliveryID  string    `gorm:"not null"`
	EventType   string    `gorm:"not null"`
	ReceivedAt  time.Time `gorm:"not null"`
	RepoID      *uint
	PayloadJSON string
	ProcessedAt *time.Time
	Status      string    `gorm:"not null"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
}

func (webhookDeliveryModel) TableName() string { return "webhook_deliveries" }

type RepoRef struct {
	Driver   string
	Name     string
	CloneURL string
}

type RepoUpsertInput struct {
	Driver   string
	Name     string
	CloneURL string
	Public   bool
	Head     string
}

type ContributorSnapshot struct {
	Name      string
	Email     string
	FirstSeen time.Time
	LastSeen  time.Time
	Commits   int
}

type RepoSnapshot struct {
	Name         string
	CloneURL     string
	Public       bool
	Head         string
	Driver       string
	Contributors map[string]ContributorSnapshot
}

type Snapshot struct {
	LastRun time.Time
	Repos   []RepoSnapshot
}

type CommitInput struct {
	SHA            string
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
	CommittedAt    time.Time
	MessageTitle   string
	Message        string
	FirstSeenAt    time.Time
	Source         string
}

type ChangeRequestInput struct {
	Repo              RepoRef
	Provider          string
	ExternalID        string
	Number            *int
	Kind              string
	Title             string
	Description       string
	AuthorLogin       string
	AuthorID          string
	SourceBranch      string
	TargetBranch      string
	SourceSHA         string
	CommentsCount     *int
	State             string
	IsDraft           bool
	OpenedAt          *time.Time
	ClosedAt          *time.Time
	MergedAt          *time.Time
	UpdatedAtProvider *time.Time
	FirstSeenAt       time.Time
	Source            string
}

type ChangeRequestRecord struct {
	State         string
	Description   string
	SourceSHA     string
	CommentsCount *int
}

type ChangeRequestUpsertResult struct {
	Created  bool
	Previous *ChangeRequestRecord
	Current  ChangeRequestRecord
}

type WebhookDeliveryInput struct {
	Provider    string
	DeliveryID  string
	EventType   string
	ReceivedAt  time.Time
	Repo        *RepoRef
	PayloadJSON string
}
