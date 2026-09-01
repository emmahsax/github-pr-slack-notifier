// Package handler wires together webhook verification, GitHub API calls,
// subscription evaluation, and Slack delivery for each supported event
// type, implementing the notification rules in the design spec (REQ-005
// through REQ-012).
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/emmahsax/github-pr-slack-notifier/internal/config"
	"github.com/emmahsax/github-pr-slack-notifier/internal/githubapp"
	"github.com/emmahsax/github-pr-slack-notifier/internal/slack"
	"github.com/emmahsax/github-pr-slack-notifier/internal/threadstore"
	"github.com/emmahsax/github-pr-slack-notifier/internal/webhook"
)

// ErrInvalidSignature is returned by Handle when the webhook signature
// doesn't verify. Callers should map it to an HTTP 401 (not a 5xx) since
// GitHub retries deliveries on 5xx — a bad signature will never succeed on
// retry, unlike a transient GitHub/Slack API failure.
var ErrInvalidSignature = errors.New("handler: invalid webhook signature")

// installationClientFunc builds an authenticated GitHub client for an
// installation. It's a field (not a plain function call) so tests can
// substitute a fake without hitting the real GitHub API.
type installationClientFunc func(ctx context.Context, installationID int64) (*githubapp.Client, error)

type Handler struct {
	cfg             config.Config
	notifier        *slack.Notifier
	threadStore     threadstore.Store // nil when threading isn't configured (incoming webhook delivery)
	installationFor installationClientFunc
}

func New(cfg config.Config) (*Handler, error) {
	notifier, err := slack.New(cfg.Slack)
	if err != nil {
		return nil, err
	}

	var store threadstore.Store
	if cfg.Slack.Method == slack.MethodBotToken {
		// Threading (REQ-015/REQ-017) is only possible with bot token
		// delivery, since only chat.postMessage returns a message ts to
		// thread future replies against.
		awsCfg, err := awsconfig.LoadDefaultConfig(context.Background())
		if err != nil {
			return nil, fmt.Errorf("load aws config: %w", err)
		}
		store = threadstore.NewDynamoDBStore(dynamodb.NewFromConfig(awsCfg), cfg.ThreadStoreTableName)
	}

	h := &Handler{cfg: cfg, notifier: notifier, threadStore: store}
	h.installationFor = h.defaultInstallationClient
	return h, nil
}

func (h *Handler) defaultInstallationClient(ctx context.Context, installationID int64) (*githubapp.Client, error) {
	appJWT, err := githubapp.AppJWT(h.cfg.GitHubAppID, h.cfg.GitHubAppPrivateKey, time.Now())
	if err != nil {
		return nil, fmt.Errorf("build app jwt: %w", err)
	}
	token, err := githubapp.NewAppClient(appJWT).InstallationToken(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("exchange installation token: %w", err)
	}
	return githubapp.NewInstallationClient(token), nil
}

// Handle verifies and routes a single webhook delivery. eventType is the
// value of the "X-GitHub-Event" header; signature is the raw value of the
// "X-Hub-Signature-256" header.
func (h *Handler) Handle(ctx context.Context, eventType, signature string, body []byte) error {
	if !webhook.VerifySignature(h.cfg.GitHubWebhookSecret, signature, body) {
		return ErrInvalidSignature
	}

	switch eventType {
	case "issue_comment":
		return h.handleIssueComment(ctx, body)
	case "pull_request_review":
		return h.handlePullRequestReview(ctx, body)
	case "pull_request_review_comment":
		return h.handlePullRequestReviewComment(ctx, body)
	case "pull_request":
		return h.handlePullRequest(ctx, body)
	default:
		return nil
	}
}

func (h *Handler) orgAllowed(login string) bool {
	return h.cfg.OrgAllowlist[login]
}

func (h *Handler) handleIssueComment(ctx context.Context, body []byte) error {
	var evt IssueCommentEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return fmt.Errorf("decode issue_comment: %w", err)
	}
	if evt.Action != "created" || evt.Issue.PullRequest == nil || !h.orgAllowed(evt.Repository.Owner.Login) {
		return nil
	}
	if evt.Comment.User.Login == h.cfg.GitHubUsername {
		return nil // don't notify the user about their own comment
	}

	client, err := h.installationFor(ctx, evt.Installation.ID)
	if err != nil {
		return err
	}

	pr, err := client.PullRequest(ctx, evt.Repository.Owner.Login, evt.Repository.Name, evt.Issue.Number)
	if err != nil {
		return err
	}
	if pr.Draft { // REQ-005
		return nil
	}

	subscribed, err := githubapp.IsSubscribed(ctx, client, evt.Repository.Owner.Login, evt.Repository.Name, pr, h.cfg.GitHubUsername)
	if err != nil {
		return err
	}
	if !subscribed {
		return nil
	}

	return h.notify(ctx, evt.Repository.Owner.Login, evt.Repository.Name, pr, evt.Sender.Login, "commented", preview(evt.Comment.Body))
}

// handlePullRequestReviewComment handles inline comments on a specific line
// of a PR's diff — both a thread's first comment and any replies within it,
// since GitHub delivers both via this same event with no distinguishing
// action. Unlike handleIssueComment, the payload already carries the full
// PullRequest object, so no separate fetch is needed.
func (h *Handler) handlePullRequestReviewComment(ctx context.Context, body []byte) error {
	var evt PullRequestReviewCommentEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return fmt.Errorf("decode pull_request_review_comment: %w", err)
	}
	if evt.Action != "created" || !h.orgAllowed(evt.Repository.Owner.Login) {
		return nil
	}
	if evt.Comment.User.Login == h.cfg.GitHubUsername {
		return nil // don't notify the user about their own comment
	}
	if evt.PullRequest.Draft { // REQ-005
		return nil
	}

	client, err := h.installationFor(ctx, evt.Installation.ID)
	if err != nil {
		return err
	}

	pr := &evt.PullRequest
	subscribed, err := githubapp.IsSubscribed(ctx, client, evt.Repository.Owner.Login, evt.Repository.Name, pr, h.cfg.GitHubUsername)
	if err != nil {
		return err
	}
	if !subscribed {
		return nil
	}

	return h.notify(ctx, evt.Repository.Owner.Login, evt.Repository.Name, pr, evt.Sender.Login, "commented", preview(evt.Comment.Body))
}

func (h *Handler) handlePullRequestReview(ctx context.Context, body []byte) error {
	var evt PullRequestReviewEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return fmt.Errorf("decode pull_request_review: %w", err)
	}
	if evt.Action != "submitted" || !h.orgAllowed(evt.Repository.Owner.Login) {
		return nil
	}
	if evt.PullRequest.Draft { // REQ-005
		return nil
	}
	if evt.Review.User.Login == h.cfg.GitHubUsername {
		return nil // don't notify the user about their own review
	}

	var verb string
	switch evt.Review.State {
	case "approved":
		verb = "approved this PR"
	case "changes_requested":
		verb = "requested changes"
	case "commented":
		// A plain review comment carries feedback text the same way an
		// issue/review comment does, so it's treated as comment-equivalent.
		verb = "commented"
	default:
		return nil // e.g. "dismissed", "pending" — not a notification trigger
	}
	var detail string
	if evt.Review.Body != "" {
		detail = preview(evt.Review.Body)
	}

	client, err := h.installationFor(ctx, evt.Installation.ID)
	if err != nil {
		return err
	}

	pr := &evt.PullRequest
	subscribed, err := githubapp.IsSubscribed(ctx, client, evt.Repository.Owner.Login, evt.Repository.Name, pr, h.cfg.GitHubUsername)
	if err != nil {
		return err
	}
	if !subscribed {
		return nil
	}

	return h.notify(ctx, evt.Repository.Owner.Login, evt.Repository.Name, pr, evt.Sender.Login, verb, detail)
}

func (h *Handler) handlePullRequest(ctx context.Context, body []byte) error {
	var evt PullRequestEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return fmt.Errorf("decode pull_request: %w", err)
	}
	if !h.orgAllowed(evt.Repository.Owner.Login) {
		return nil
	}

	if evt.Action == "edited" && evt.Changes.Title != nil {
		// REQ-018: keep an existing thread header's title in sync. Not a
		// notification in its own right — no subscription check, no new
		// message — so it's handled entirely separately from the
		// action-determining switch below.
		return h.refreshThreadHeader(ctx, evt.Repository.Owner.Login, evt.Repository.Name, &evt.PullRequest)
	}

	var verb string
	switch {
	case evt.Action == "closed" && evt.PullRequest.Merged:
		verb = "merged this PR" // REQ-011/REQ-012: notified regardless of who merged it
	case evt.Action == "labeled":
		if evt.PullRequest.Draft { // REQ-005
			return nil
		}
		verb = fmt.Sprintf("added the %q label", evt.Label.Name)
	default:
		return nil // e.g. "opened", "closed" without merge, "ready_for_review" — not triggers (REQ-007)
	}

	client, err := h.installationFor(ctx, evt.Installation.ID)
	if err != nil {
		return err
	}

	pr := &evt.PullRequest
	subscribed, err := githubapp.IsSubscribed(ctx, client, evt.Repository.Owner.Login, evt.Repository.Name, pr, h.cfg.GitHubUsername)
	if err != nil {
		return err
	}
	if !subscribed {
		return nil
	}

	return h.notify(ctx, evt.Repository.Owner.Login, evt.Repository.Name, pr, evt.Sender.Login, verb, "")
}

// notify sends "@senderLogin verb[: detail]" to Slack, grouping it into the
// PR's thread (REQ-015) when threading is available. The thread root is a
// generic header identifying the PR ("owner/repo#number (@author): Title"),
// posted once; every real notification for that PR, including the very
// first one, is a plain threaded reply under that header. Delivery methods
// that can't thread (incoming webhook) have no thread store at all, so
// every message they send is flat: the same header text plus the action as
// a blockquoted line underneath, in one message.
//
// verb is always present ("commented", "approved this PR", ...); detail is
// the optional freeform comment/review body preview. Only "@sender verb:"
// is bold — matching the header's bold-up-to-colon style — with detail (if
// any) left plain; when there's no detail, the whole line is bold instead.
func (h *Handler) notify(ctx context.Context, owner, repo string, pr *githubapp.PullRequest, senderLogin, verb, detail string) error {
	header := buildHeader(owner, repo, pr)
	var actionLine string
	if detail != "" {
		actionLine = fmt.Sprintf("*@%s %s:* %s", senderLogin, verb, detail)
	} else {
		actionLine = fmt.Sprintf("*@%s %s*", senderLogin, verb)
	}

	if h.threadStore == nil {
		_, err := h.notifier.Send(ctx, header+"\n> "+actionLine, "")
		return err
	}

	threadTS, err := h.threadOrHeaderTS(ctx, owner, repo, pr, header)
	if err != nil {
		return err
	}

	_, err = h.notifier.Send(ctx, actionLine, threadTS)
	return err
}

// threadOrHeaderTS returns the existing thread ts for a PR, or creates the
// thread by posting header and returns its ts.
func (h *Handler) threadOrHeaderTS(ctx context.Context, owner, repo string, pr *githubapp.PullRequest, header string) (string, error) {
	key := threadstore.Key(owner, repo, pr.Number)

	ts, found, err := h.threadStore.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("look up thread for %s: %w", key, err)
	}
	if found {
		return ts, nil
	}

	result, err := h.notifier.Send(ctx, header, "")
	if err != nil {
		return "", fmt.Errorf("create thread header for %s: %w", key, err)
	}
	if result.ThreadTS == "" {
		return "", nil // shouldn't happen for bot_token delivery; caller's reply loses header context if it does
	}

	if err := h.threadStore.Put(ctx, key, result.ThreadTS); err != nil {
		return "", fmt.Errorf("save thread for %s: %w", key, err)
	}
	return result.ThreadTS, nil
}

// refreshThreadHeader implements REQ-018: when a PR's title changes, update
// its thread header to match, if a thread already exists for it. If no
// thread exists yet — the PR was never subscribed-and-notified, or this
// deployment uses incoming-webhook delivery, which has no headers to keep
// in sync since every flat message rebuilds its header fresh — this is a
// silent no-op, not an error.
func (h *Handler) refreshThreadHeader(ctx context.Context, owner, repo string, pr *githubapp.PullRequest) error {
	if h.threadStore == nil {
		return nil
	}

	key := threadstore.Key(owner, repo, pr.Number)
	ts, found, err := h.threadStore.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("look up thread for %s: %w", key, err)
	}
	if !found {
		return nil
	}

	return h.notifier.Update(ctx, ts, buildHeader(owner, repo, pr))
}

// buildHeader formats the "owner/repo#number (@author):" portion in bold
// via Slack mrkdwn's single-asterisk syntax (wrapping the link markup
// itself still renders as a clickable bold link) — the title after it
// stays unstyled. Shared by both the thread-root header (REQ-015) and the
// first line of a flat message (REQ-017), so this one change covers both.
func buildHeader(owner, repo string, pr *githubapp.PullRequest) string {
	return fmt.Sprintf("*<%s|%s/%s#%d> (@%s):* %s", pr.HTMLURL, owner, repo, pr.Number, pr.Author.Login, pr.Title)
}

// previewMaxLen bounds how much of a comment/review body appears inline in
// a Slack notification — long PR discussions shouldn't produce walls of
// text in a channel.
const previewMaxLen = 200

// preview collapses a comment/review body to a single line and truncates
// it for inline display in a Slack message.
func preview(body string) string {
	body = strings.Join(strings.Fields(body), " ")
	runes := []rune(body)
	if len(runes) > previewMaxLen {
		return string(runes[:previewMaxLen]) + "…"
	}
	return body
}
