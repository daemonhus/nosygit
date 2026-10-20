package storage

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db *gorm.DB
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("empty sqlite path")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, err
	}

	// The glebarez driver ignores mattn-style _journal_mode/_foreign_keys
	// parameters, so pragmas must be passed as _pragma. _txlock=immediate takes
	// the write lock at BEGIN, so read-then-write transactions wait on
	// busy_timeout instead of failing on lock upgrade. synchronous=NORMAL is
	// the usual pairing with WAL: it avoids an fsync per commit, which keeps
	// queued writers inside busy_timeout on slow disks.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate", absPath)
	// The default logger prints failing statements with every value filled in,
	// which spreads commit and author data into logs; errors are returned instead.
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection serialises access in the
	// pool. Inside a Transaction closure always use tx, never s.db: a second
	// query would wait forever for the connection the transaction holds.
	sqlDB.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.runMigrations(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Store) runMigrations() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}

	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}

	goose.SetBaseFS(migrationsFS)
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		return err
	}
	return nil
}

func (s *Store) DB() *gorm.DB {
	return s.db
}

func (s *Store) findRepoID(tx *gorm.DB, ref RepoRef) (uint, error) {
	q := tx.Model(&repoModel{})
	if ref.Driver != "" && ref.Name != "" {
		q = q.Where("driver = ? AND name = ?", ref.Driver, ref.Name)
	} else if ref.Driver != "" && ref.CloneURL != "" {
		q = q.Where("driver = ? AND clone_url = ?", ref.Driver, ref.CloneURL)
	} else {
		return 0, errors.New("repo reference missing required identity fields")
	}

	var repo repoModel
	res := q.Limit(1).Find(&repo)
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return repo.ID, nil
}

func (s *Store) LoadSnapshot() (Snapshot, error) {
	var out Snapshot

	var meta stateMetaModel
	metaRes := s.db.Where("id = 1").Limit(1).Find(&meta)
	if metaRes.Error != nil {
		return out, metaRes.Error
	}
	if metaRes.RowsAffected > 0 && meta.LastRun != nil {
		out.LastRun = *meta.LastRun
	}

	var repos []repoModel
	if err := s.db.Order("id asc").Find(&repos).Error; err != nil {
		return out, err
	}

	out.Repos = make([]RepoSnapshot, 0, len(repos))
	for _, repo := range repos {
		repoSnap := RepoSnapshot{
			Name:         repo.Name,
			CloneURL:     repo.CloneURL,
			Public:       repo.Public,
			Head:         repo.Head,
			Driver:       repo.Driver,
			Contributors: map[string]ContributorSnapshot{},
		}

		var contributors []repoContributorModel
		if err := s.db.Where("repo_id = ?", repo.ID).Find(&contributors).Error; err != nil {
			return out, err
		}

		for _, c := range contributors {
			repoSnap.Contributors[c.IdentityKey] = ContributorSnapshot{
				Name:      c.Name,
				Email:     c.Email,
				FirstSeen: c.FirstSeen,
				LastSeen:  c.LastSeen,
				Commits:   c.Commits,
			}
		}

		out.Repos = append(out.Repos, repoSnap)
	}

	return out, nil
}

func (s *Store) SaveSnapshot(snapshot Snapshot) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var lastRun *time.Time
		if !snapshot.LastRun.IsZero() {
			t := snapshot.LastRun
			lastRun = &t
		}

		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{"last_run": lastRun}),
		}).Create(&stateMetaModel{ID: 1, LastRun: lastRun}).Error; err != nil {
			return err
		}

		seenRepoIDs := make([]uint, 0, len(snapshot.Repos))
		for _, repo := range snapshot.Repos {
			repoModelInput := repoModel{
				Name:     repo.Name,
				CloneURL: repo.CloneURL,
				Public:   repo.Public,
				Head:     repo.Head,
				Driver:   repo.Driver,
			}

			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "driver"}, {Name: "clone_url"}},
				DoUpdates: clause.Assignments(map[string]interface{}{
					"name":       repoModelInput.Name,
					"public":     repoModelInput.Public,
					"head":       repoModelInput.Head,
					"updated_at": time.Now(),
				}),
			}).Create(&repoModelInput).Error; err != nil {
				return err
			}

			var persisted repoModel
			if err := tx.Where("driver = ? AND clone_url = ?", repo.Driver, repo.CloneURL).First(&persisted).Error; err != nil {
				return err
			}
			seenRepoIDs = append(seenRepoIDs, persisted.ID)

			if err := tx.Where("repo_id = ?", persisted.ID).Delete(&repoContributorModel{}).Error; err != nil {
				return err
			}

			if len(repo.Contributors) == 0 {
				continue
			}

			contributors := make([]repoContributorModel, 0, len(repo.Contributors))
			for key, c := range repo.Contributors {
				contributors = append(contributors, repoContributorModel{
					RepoID:      persisted.ID,
					IdentityKey: key,
					Name:        c.Name,
					Email:       c.Email,
					FirstSeen:   c.FirstSeen,
					LastSeen:    c.LastSeen,
					Commits:     c.Commits,
				})
			}

			if err := tx.Create(&contributors).Error; err != nil {
				return err
			}
		}

		if len(seenRepoIDs) == 0 {
			if err := tx.Exec("DELETE FROM repos").Error; err != nil {
				return err
			}
			return nil
		}

		if err := tx.Where("id NOT IN ?", seenRepoIDs).Delete(&repoModel{}).Error; err != nil {
			return err
		}

		return nil
	})
}

func (s *Store) LoadProviderPolls() (map[string]time.Time, error) {
	var rows []providerPollModel
	if err := s.db.Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		out[row.Provider] = row.LastPolledAt
	}
	return out, nil
}

func (s *Store) TouchProviderPoll(provider string, at time.Time) error {
	poll := providerPollModel{
		Provider:     provider,
		LastPolledAt: at,
	}

	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "provider"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"last_polled_at": at}),
	}).Create(&poll).Error
}

func (s *Store) UpsertRepo(in RepoUpsertInput) error {
	if strings.TrimSpace(in.Driver) == "" {
		return errors.New("repo upsert requires driver")
	}
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("repo upsert requires name")
	}
	if strings.TrimSpace(in.CloneURL) == "" {
		return errors.New("repo upsert requires clone_url")
	}

	repo := repoModel{
		Name:     in.Name,
		CloneURL: in.CloneURL,
		Public:   in.Public,
		Head:     in.Head,
		Driver:   in.Driver,
	}

	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "driver"}, {Name: "clone_url"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"name":       in.Name,
			"public":     in.Public,
			"head":       in.Head,
			"updated_at": time.Now(),
		}),
	}).Create(&repo).Error
}

func (s *Store) InsertCommitIfNew(ref RepoRef, in CommitInput) (bool, error) {
	var inserted bool
	err := s.db.Transaction(func(tx *gorm.DB) error {
		repoID, err := s.findRepoID(tx, ref)
		if err != nil {
			return err
		}

		commit := commitModel{
			RepoID:         repoID,
			SHA:            in.SHA,
			AuthorName:     in.AuthorName,
			AuthorEmail:    in.AuthorEmail,
			CommitterName:  in.CommitterName,
			CommitterEmail: in.CommitterEmail,
			CommittedAt:    nullableTime(in.CommittedAt),
			MessageTitle:   in.MessageTitle,
			Message:        in.Message,
			FirstSeenAt:    in.FirstSeenAt,
			Source:         in.Source,
		}

		res := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "repo_id"}, {Name: "sha"}},
			DoNothing: true,
		}).Create(&commit)
		if res.Error != nil {
			return res.Error
		}

		inserted = res.RowsAffected > 0
		return nil
	})

	return inserted, err
}

func (s *Store) UpsertChangeRequest(in ChangeRequestInput) (bool, error) {
	res, err := s.UpsertChangeRequestWithResult(in)
	if err != nil {
		return false, err
	}
	return res.Created, nil
}

func (s *Store) UpsertChangeRequestWithResult(in ChangeRequestInput) (ChangeRequestUpsertResult, error) {
	var out ChangeRequestUpsertResult
	err := s.db.Transaction(func(tx *gorm.DB) error {
		repoID, err := s.findRepoID(tx, in.Repo)
		if err != nil {
			return err
		}

		externalID := strings.TrimSpace(in.ExternalID)
		if externalID == "" {
			if in.Number == nil {
				return errors.New("change request external_id or number is required")
			}
			externalID = fmt.Sprintf("repo:%d:number:%d", repoID, *in.Number)
		}

		cr := changeRequestModel{
			RepoID:            repoID,
			Provider:          in.Provider,
			ExternalID:        externalID,
			Number:            in.Number,
			Kind:              in.Kind,
			Title:             in.Title,
			Description:       in.Description,
			AuthorLogin:       in.AuthorLogin,
			AuthorID:          in.AuthorID,
			SourceBranch:      in.SourceBranch,
			TargetBranch:      in.TargetBranch,
			SourceSHA:         in.SourceSHA,
			CommentsCount:     in.CommentsCount,
			State:             in.State,
			IsDraft:           in.IsDraft,
			OpenedAt:          in.OpenedAt,
			ClosedAt:          in.ClosedAt,
			MergedAt:          in.MergedAt,
			UpdatedAtProvider: in.UpdatedAtProvider,
			FirstSeenAt:       in.FirstSeenAt,
			Source:            in.Source,
		}

		var existing changeRequestModel
		lookup := tx.Where("provider = ? AND external_id = ?", in.Provider, externalID).Limit(1).Find(&existing)
		if lookup.Error != nil {
			return lookup.Error
		}
		if lookup.RowsAffected > 0 {
			out.Previous = &ChangeRequestRecord{
				State:         existing.State,
				Description:   existing.Description,
				SourceSHA:     existing.SourceSHA,
				CommentsCount: existing.CommentsCount,
			}
		}

		res := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "provider"}, {Name: "external_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"repo_id":             repoID,
				"number":              in.Number,
				"kind":                in.Kind,
				"title":               in.Title,
				"description":         in.Description,
				"author_login":        in.AuthorLogin,
				"author_id":           in.AuthorID,
				"source_branch":       in.SourceBranch,
				"target_branch":       in.TargetBranch,
				"source_sha":          in.SourceSHA,
				"comments_count":      in.CommentsCount,
				"state":               in.State,
				"is_draft":            in.IsDraft,
				"opened_at":           in.OpenedAt,
				"closed_at":           in.ClosedAt,
				"merged_at":           in.MergedAt,
				"updated_at_provider": in.UpdatedAtProvider,
				"source":              in.Source,
				"updated_at":          time.Now(),
			}),
		}).Create(&cr)
		if res.Error != nil {
			return res.Error
		}

		out.Created = out.Previous == nil
		out.Current = ChangeRequestRecord{
			State:         in.State,
			Description:   in.Description,
			SourceSHA:     in.SourceSHA,
			CommentsCount: in.CommentsCount,
		}
		return nil
	})

	return out, err
}

func (s *Store) RecordWebhookDelivery(in WebhookDeliveryInput) (uint, bool, error) {
	var id uint
	var created bool
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var repoID *uint
		if in.Repo != nil {
			resolved, err := s.findRepoID(tx, *in.Repo)
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil {
				repoID = &resolved
			}
		}

		rec := webhookDeliveryModel{
			Provider:    in.Provider,
			DeliveryID:  in.DeliveryID,
			EventType:   in.EventType,
			ReceivedAt:  in.ReceivedAt,
			RepoID:      repoID,
			PayloadJSON: in.PayloadJSON,
			Status:      "received",
		}

		res := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "provider"}, {Name: "delivery_id"}}, DoNothing: true}).Create(&rec)
		if res.Error != nil {
			return res.Error
		}

		if res.RowsAffected > 0 {
			id = rec.ID
			created = true
			return nil
		}

		var existing webhookDeliveryModel
		if err := tx.Where("provider = ? AND delivery_id = ?", in.Provider, in.DeliveryID).First(&existing).Error; err != nil {
			return err
		}
		id = existing.ID
		created = false
		return nil
	})

	return id, created, err
}

func (s *Store) MarkWebhookDelivery(id uint, status string) error {
	now := time.Now()
	return s.db.Model(&webhookDeliveryModel{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":       status,
		"processed_at": &now,
		"updated_at":   now,
	}).Error
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	out := t
	return &out
}

func (s *Store) SQLDB() (*sql.DB, error) {
	return s.db.DB()
}
