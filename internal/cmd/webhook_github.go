package cmd

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v37/github"
	"nosygit/internal/config"
	"nosygit/internal/events"
	"nosygit/internal/storage"
)

const maxWebhookBodySize = 25 * 1024 * 1024

func handleGitHubWebhook(w http.ResponseWriter, r *http.Request, ctx context.Context, provider config.Provider, store *storage.Store) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodySize))
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusInternalServerError)
		return
	}

	if provider.WebhookSecret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if !validateGitHubSignature(body, provider.WebhookSecret, sig) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	} else {
		events.EmitMetaWarning(ctx, "webhook", "WEBHOOK_NO_SECRET",
			"provider has no webhook_secret configured; skipping signature validation",
			map[string]interface{}{"provider": provider.Name})
	}

	eventType := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	if eventType == "" || deliveryID == "" {
		http.Error(w, "missing X-GitHub-Event or X-GitHub-Delivery headers", http.StatusBadRequest)
		return
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

	if err := processGitHubEvent(ctx, provider, store, eventType, body); err != nil {
		events.EmitMetaError(ctx, "webhook", "WEBHOOK_PROCESS_FAILED",
			"failed to process github webhook",
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

func validateGitHubSignature(body []byte, secret, sig string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(sig, prefix) {
		return false
	}
	expected, err := hex.DecodeString(sig[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(expected, mac.Sum(nil))
}

func processGitHubEvent(ctx context.Context, provider config.Provider, store *storage.Store, eventType string, body []byte) error {
	event, err := github.ParseWebHook(eventType, body)
	if err != nil {
		return fmt.Errorf("parsing github webhook payload: %w", err)
	}

	switch e := event.(type) {
	case *github.PushEvent:
		return processGitHubPush(ctx, provider, store, e)
	case *github.PullRequestEvent:
		return processGitHubPullRequest(ctx, provider, store, e)
	case *github.CreateEvent:
		if e.GetRefType() == "branch" {
			repoName := e.GetRepo().GetFullName()
			repoState, _ := getRepoState(ctx, repoName)
			EvaluateBranchWatchers(ctx, BranchChange{
				Provider:  provider,
				RepoState: repoState,
				RepoName:  repoName,
				Branch:    e.GetRef(),
				Operation: "ADD",
			})
		}
	case *github.DeleteEvent:
		if e.GetRefType() == "branch" {
			repoName := e.GetRepo().GetFullName()
			repoState, _ := getRepoState(ctx, repoName)
			EvaluateBranchWatchers(ctx, BranchChange{
				Provider:  provider,
				RepoState: repoState,
				RepoName:  repoName,
				Branch:    e.GetRef(),
				Operation: "DELETE",
			})
		}
	case *github.RepositoryEvent:
		return processGitHubRepositoryEvent(ctx, provider, e)
	}
	return nil
}

func processGitHubPush(ctx context.Context, provider config.Provider, store *storage.Store, e *github.PushEvent) error {
	if e.GetRepo() == nil {
		return nil
	}

	cfg := ctx.Value("ctx").(config.ContextStruct).Config

	repoName := e.GetRepo().GetFullName()
	cloneURL := e.GetRepo().GetSSHURL()
	public := !e.GetRepo().GetPrivate()

	oldRepoState, _ := getRepoState(ctx, repoName)

	headSHA := ""
	if e.GetHeadCommit() != nil {
		headSHA = e.GetHeadCommit().GetID()
	}
	if headSHA == "" && e.GetAfter() != "" {
		headSHA = e.GetAfter()
	}

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

func processGitHubPullRequest(ctx context.Context, provider config.Provider, store *storage.Store, e *github.PullRequestEvent) error {
	if e.GetPullRequest() == nil || e.GetRepo() == nil {
		return nil
	}

	pr := e.GetPullRequest()
	repoName := e.GetRepo().GetFullName()
	cloneURL := e.GetRepo().GetSSHURL()
	public := !e.GetRepo().GetPrivate()

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

	number := pr.GetNumber()
	externalID := strconv.FormatInt(pr.GetID(), 10)
	if externalID == "0" {
		externalID = ""
	}

	prState := strings.TrimSpace(pr.GetState())
	if pr.MergedAt != nil {
		prState = "merged"
	}

	author := ""
	authorID := ""
	if pr.User != nil {
		author = pr.User.GetLogin()
		if id := pr.User.GetID(); id != 0 {
			authorID = strconv.FormatInt(id, 10)
		}
	}

	var openedAt *time.Time
	if pr.CreatedAt != nil {
		t := *pr.CreatedAt
		openedAt = &t
	}
	var updatedAt *time.Time
	if pr.UpdatedAt != nil {
		t := *pr.UpdatedAt
		updatedAt = &t
	}
	var closedAt *time.Time
	if pr.ClosedAt != nil {
		t := *pr.ClosedAt
		closedAt = &t
	}
	var mergedAt *time.Time
	if pr.MergedAt != nil {
		t := *pr.MergedAt
		mergedAt = &t
	}

	comments := pr.GetComments() + pr.GetReviewComments()

	_, err := UpsertChangeRequest(ctx, storage.ChangeRequestInput{
		Repo: storage.RepoRef{
			Driver:   provider.Name,
			Name:     repoName,
			CloneURL: cloneURL,
		},
		Provider:          provider.Name,
		ExternalID:        externalID,
		Number:            &number,
		Kind:              "pull_request",
		Title:             pr.GetTitle(),
		Description:       pr.GetBody(),
		AuthorLogin:       author,
		AuthorID:          authorID,
		SourceBranch:      pr.GetHead().GetRef(),
		TargetBranch:      pr.GetBase().GetRef(),
		SourceSHA:         pr.GetHead().GetSHA(),
		CommentsCount:     &comments,
		State:             prState,
		IsDraft:           pr.GetDraft(),
		OpenedAt:          openedAt,
		ClosedAt:          closedAt,
		MergedAt:          mergedAt,
		UpdatedAtProvider: updatedAt,
		FirstSeenAt:       time.Now().UTC(),
		Source:            "webhook",
	})
	return err
}

func processGitHubRepositoryEvent(ctx context.Context, provider config.Provider, e *github.RepositoryEvent) error {
	if e.GetRepo() == nil {
		return nil
	}

	repoName := e.GetRepo().GetFullName()
	visibility := "private"
	if e.GetRepo().GetVisibility() == "public" {
		visibility = "public"
	}
	repoState, _ := getRepoState(ctx, repoName)

	switch e.GetAction() {
	case "created":
		EvaluateRepoWatchers(ctx, RepoChange{
			Provider:   provider,
			RepoState:  repoState,
			RepoName:   repoName,
			Visibility: visibility,
			Operation:  "ADD",
		})
	case "deleted":
		EvaluateRepoWatchers(ctx, RepoChange{
			Provider:   provider,
			RepoState:  repoState,
			RepoName:   repoName,
			Visibility: visibility,
			Operation:  "REMOVE",
		})
	case "publicized":
		EvaluateRepoVisibilityWatchers(ctx, RepoVisibilityChange{
			Provider:       provider,
			RepoState:      repoState,
			RepoName:       repoName,
			FromVisibility: "private",
			ToVisibility:   "public",
			Operation:      "EDIT",
		})
	case "privatized":
		EvaluateRepoVisibilityWatchers(ctx, RepoVisibilityChange{
			Provider:       provider,
			RepoState:      repoState,
			RepoName:       repoName,
			FromVisibility: "public",
			ToVisibility:   "private",
			Operation:      "EDIT",
		})
	}
	return nil
}

func getRepoState(ctx context.Context, repoName string) (config.RepoState, bool) {
	state := ctx.Value("ctx").(config.ContextStruct).State
	repoState, err := state.GetRepoStateByName(repoName)
	return repoState, err == nil
}
