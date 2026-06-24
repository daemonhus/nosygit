package cmd

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/xanzy/go-gitlab"
	"nosygit/internal/config"
	"nosygit/internal/events"
	"nosygit/internal/storage"
)

func handleGitLabWebhook(w http.ResponseWriter, r *http.Request, ctx context.Context, provider config.Provider, store *storage.Store) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodySize))
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusInternalServerError)
		return
	}

	if provider.WebhookSecret != "" {
		token := r.Header.Get("X-Gitlab-Token")
		if subtle.ConstantTimeCompare([]byte(token), []byte(provider.WebhookSecret)) != 1 {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
	} else {
		events.EmitMetaWarning(ctx, "webhook", "WEBHOOK_NO_SECRET",
			"provider has no webhook_secret configured; skipping token validation",
			map[string]interface{}{"provider": provider.Name})
	}

	eventType := r.Header.Get("X-Gitlab-Event")
	if eventType == "" {
		http.Error(w, "missing X-Gitlab-Event header", http.StatusBadRequest)
		return
	}

	deliveryID := r.Header.Get("X-Gitlab-Event-UUID")
	if deliveryID == "" {
		deliveryID = gitlabDeliveryID(provider.Name, eventType, body)
	}

	var payloadJSON string
	if ctx.Value("ctx").(config.ContextStruct).Verbose {
		payloadJSON = string(body)
	}
	id, created, err := store.RecordWebhookDelivery(storage.WebhookDeliveryInput{
		Provider:    provider.Name,
		DeliveryID:  deliveryID,
		EventType:   eventType,
		ReceivedAt:  time.Now().UTC(),
		PayloadJSON: payloadJSON,
	})
	if err != nil {
		http.Error(w, "failed to record delivery", http.StatusInternalServerError)
		return
	}
	if !created {
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := processGitLabEvent(ctx, provider, store, gitlab.EventType(eventType), body); err != nil {
		events.EmitMetaError(ctx, "webhook", "WEBHOOK_PROCESS_FAILED",
			"failed to process gitlab webhook",
			map[string]interface{}{
				"provider":    provider.Name,
				"event":       eventType,
				"delivery_id": deliveryID,
				"error":       err.Error(),
			})
		_ = store.MarkWebhookDelivery(id, "failed")
		http.Error(w, "failed to process event", http.StatusInternalServerError)
		return
	}

	_ = store.MarkWebhookDelivery(id, "processed")
	w.WriteHeader(http.StatusOK)
}

func gitlabDeliveryID(provider, eventType string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(provider))
	h.Write([]byte(eventType))
	h.Write(body)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func processGitLabEvent(ctx context.Context, provider config.Provider, store *storage.Store, eventType gitlab.EventType, body []byte) error {
	event, err := gitlab.ParseHook(eventType, body)
	if err != nil {
		return fmt.Errorf("parsing gitlab webhook payload: %w", err)
	}

	switch e := event.(type) {
	case *gitlab.PushEvent:
		return processGitLabPush(ctx, provider, store, e)
	case *gitlab.TagEvent:
		// Tag events don't map to branch watchers; nothing to do for now.
		return nil
	case *gitlab.MergeEvent:
		return processGitLabMergeRequest(ctx, provider, store, e)
	case *gitlab.ProjectSystemEvent:
		return processGitLabProjectEvent(ctx, provider, e)
	}
	return nil
}

func processGitLabPush(ctx context.Context, provider config.Provider, store *storage.Store, e *gitlab.PushEvent) error {
	cfg := ctx.Value("ctx").(config.ContextStruct).Config

	repoName := e.Project.PathWithNamespace
	cloneURL := e.Project.SSHURL
	public := e.Project.Visibility == gitlab.PublicVisibility

	oldRepoState, _ := getRepoState(ctx, repoName)

	headSHA := e.After

	if err := store.UpsertRepo(storage.RepoUpsertInput{
		Driver:   provider.Name,
		Name:     repoName,
		CloneURL: cloneURL,
		Public:   public,
		Head:     headSHA,
	}); err != nil {
		return fmt.Errorf("upserting repo: %w", err)
	}

	clonePath := cfg.ClonePath
	if clonePath == "" {
		clonePath = "/tmp/nosygit/"
	}

	if _, err := cloneIfNeeded(ctx, repoName, cloneURL, clonePath); err != nil {
		return fmt.Errorf("cloning repo %s: %w", repoName, err)
	}

	repoCtx := context.WithValue(ctx, "ctx", config.ContextStruct{
		Config:    cfg,
		State:     ctx.Value("ctx").(config.ContextStruct).State,
		Provider:  provider,
		RepoState: oldRepoState,
		Store:     store,
		Verbose:   ctx.Value("ctx").(config.ContextStruct).Verbose,
	})

	_, err := RunGit(repoCtx, clonePath+repoName, oldRepoState)
	return err
}

func processGitLabMergeRequest(ctx context.Context, provider config.Provider, store *storage.Store, e *gitlab.MergeEvent) error {
	attrs := e.ObjectAttributes
	repoName := e.Project.PathWithNamespace
	cloneURL := e.Project.SSHURL
	public := e.Project.Visibility == gitlab.PublicVisibility

	oldRepoState, _ := getRepoState(ctx, repoName)
	head := oldRepoState.Head

	if err := store.UpsertRepo(storage.RepoUpsertInput{
		Driver:   provider.Name,
		Name:     repoName,
		CloneURL: cloneURL,
		Public:   public,
		Head:     head,
	}); err != nil {
		return fmt.Errorf("upserting repo: %w", err)
	}

	externalID := strconv.Itoa(attrs.ID)
	number := attrs.IID

	mrState := attrs.State
	if mrState == "opened" {
		mrState = "open"
	}

	openedAt := parseGitLabTime(attrs.CreatedAt)
	updatedAt := parseGitLabTime(attrs.UpdatedAt)
	var mergedAt *time.Time
	if attrs.State == "merged" {
		mergedAt = parseGitLabTime(attrs.UpdatedAt)
	}

	sourceSHA := ""
	if attrs.LastCommit.ID != "" {
		sourceSHA = attrs.LastCommit.ID
	}

	authorLogin := e.User.Username
	authorID := strconv.Itoa(e.User.ID)

	_, err := UpsertChangeRequest(ctx, storage.ChangeRequestInput{
		Repo: storage.RepoRef{
			Driver:   provider.Name,
			Name:     repoName,
			CloneURL: cloneURL,
		},
		Provider:          provider.Name,
		ExternalID:        externalID,
		Number:            &number,
		Kind:              "merge_request",
		Title:             attrs.Title,
		Description:       attrs.Description,
		AuthorLogin:       authorLogin,
		AuthorID:          authorID,
		SourceBranch:      attrs.SourceBranch,
		TargetBranch:      attrs.TargetBranch,
		SourceSHA:         sourceSHA,
		State:             mrState,
		IsDraft:           attrs.WorkInProgress,
		OpenedAt:          openedAt,
		MergedAt:          mergedAt,
		UpdatedAtProvider: updatedAt,
		FirstSeenAt:       time.Now().UTC(),
		Source:            "webhook",
	})
	return err
}

func parseGitLabTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.000Z", "2006-01-02 15:04:05 UTC"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

func processGitLabProjectEvent(ctx context.Context, provider config.Provider, e *gitlab.ProjectSystemEvent) error {
	repoName := e.PathWithNamespace
	if repoName == "" {
		repoName = e.Name
	}
	repoState, _ := getRepoState(ctx, repoName)

	switch e.EventName {
	case "project_create":
		EvaluateRepoWatchers(ctx, RepoChange{
			Provider:   provider,
			RepoState:  repoState,
			RepoName:   repoName,
			Visibility: e.ProjectVisibility,
			Operation:  "ADD",
		})
	case "project_destroy":
		EvaluateRepoWatchers(ctx, RepoChange{
			Provider:   provider,
			RepoState:  repoState,
			RepoName:   repoName,
			Visibility: e.ProjectVisibility,
			Operation:  "REMOVE",
		})
	}
	return nil
}
