package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emmahsax/github-pr-slack-notifier/internal/config"
	"github.com/emmahsax/github-pr-slack-notifier/internal/githubapp"
	"github.com/emmahsax/github-pr-slack-notifier/internal/slack"
)

const testSecret = "test-webhook-secret"

// testEnv wires a Handler against a fake GitHub API server and a fake
// Slack incoming webhook, recording every notification text sent.
type testEnv struct {
	handler *Handler
	sent    []string
}

func newTestEnv(t *testing.T, pr githubapp.PullRequest, commits []githubapp.Commit, reviews []githubapp.Review, issueComments, reviewComments []githubapp.Comment) *testEnv {
	t.Helper()

	ghMux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	ghMux.HandleFunc("/repos/acme/widgets/pulls/42", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, pr) })
	ghMux.HandleFunc("/repos/acme/widgets/pulls/42/commits", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, commits) })
	ghMux.HandleFunc("/repos/acme/widgets/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, reviews) })
	ghMux.HandleFunc("/repos/acme/widgets/issues/42/comments", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, issueComments) })
	ghMux.HandleFunc("/repos/acme/widgets/pulls/42/comments", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, reviewComments) })
	ghServer := httptest.NewServer(ghMux)
	t.Cleanup(ghServer.Close)

	env := &testEnv{}
	slackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		env.sent = append(env.sent, body.Text)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(slackServer.Close)

	notifier, err := slack.New(slack.Config{Method: slack.MethodIncomingWebhook, WebhookURL: slackServer.URL})
	if err != nil {
		t.Fatalf("slack.New() error = %v", err)
	}

	cfg := config.Config{
		GitHubWebhookSecret: testSecret,
		GitHubUsername:      "emmahsax",
		OrgAllowlist:        map[string]bool{"acme": true},
	}
	h := &Handler{cfg: cfg, notifier: notifier}
	h.installationFor = func(ctx context.Context, installationID int64) (*githubapp.Client, error) {
		return githubapp.NewInstallationClient("fake-token", githubapp.WithBaseURL(ghServer.URL)), nil
	}

	env.handler = h
	return env
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestHandle_RejectsInvalidSignature(t *testing.T) {
	env := newTestEnv(t, githubapp.PullRequest{Number: 42, Author: githubapp.User{Login: "emmahsax"}}, nil, nil, nil, nil)
	body := []byte(`{"action":"created"}`)

	err := env.handler.Handle(context.Background(), "issue_comment", "sha256=deadbeef", body)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
	if len(env.sent) != 0 {
		t.Errorf("expected no Slack notification, got %v", env.sent)
	}
}

func TestHandle_IssueComment_AuthoredAndSubscribed(t *testing.T) {
	pr := githubapp.PullRequest{Number: 42, Draft: false, Author: githubapp.User{Login: "emmahsax"}, HTMLURL: "https://github.com/acme/widgets/pull/42", Title: "Add widget support"}
	env := newTestEnv(t, pr, nil, nil, nil, nil)

	body := []byte(`{
		"action": "created",
		"issue": {"number": 42, "pull_request": {}},
		"comment": {"user": {"login": "reviewer1"}, "body": "I like this!"},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1},
		"sender": {"login": "reviewer1"}
	}`)

	if err := env.handler.Handle(context.Background(), "issue_comment", sign(testSecret, body), body); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 1 {
		t.Fatalf("expected 1 notification, got %d: %v", len(env.sent), env.sent)
	}
	want := "*<https://github.com/acme/widgets/pull/42|acme/widgets#42> (@emmahsax):* Add widget support\n> *@reviewer1 commented:* I like this!"
	if env.sent[0] != want {
		t.Errorf("notification text = %q, want %q", env.sent[0], want)
	}
}

func TestHandle_IssueComment_DraftPRSuppressed(t *testing.T) {
	pr := githubapp.PullRequest{Number: 42, Draft: true, Author: githubapp.User{Login: "emmahsax"}}
	env := newTestEnv(t, pr, nil, nil, nil, nil)

	body := []byte(`{
		"action": "created",
		"issue": {"number": 42, "pull_request": {}},
		"comment": {"user": {"login": "reviewer1"}},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1}
	}`)

	if err := env.handler.Handle(context.Background(), "issue_comment", sign(testSecret, body), body); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 0 {
		t.Errorf("expected no notification for draft PR, got %v", env.sent)
	}
}

func TestHandle_IssueComment_NotSubscribedSuppressed(t *testing.T) {
	pr := githubapp.PullRequest{Number: 42, Draft: false, Author: githubapp.User{Login: "someone-else"}}
	env := newTestEnv(t, pr, nil, nil, nil, nil)

	body := []byte(`{
		"action": "created",
		"issue": {"number": 42, "pull_request": {}},
		"comment": {"user": {"login": "reviewer1"}},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1}
	}`)

	if err := env.handler.Handle(context.Background(), "issue_comment", sign(testSecret, body), body); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 0 {
		t.Errorf("expected no notification when not subscribed (review-requested/mentioned don't count), got %v", env.sent)
	}
}

func TestHandle_IssueComment_OwnCommentSuppressed(t *testing.T) {
	pr := githubapp.PullRequest{Number: 42, Draft: false, Author: githubapp.User{Login: "emmahsax"}}
	env := newTestEnv(t, pr, nil, nil, nil, nil)

	body := []byte(`{
		"action": "created",
		"issue": {"number": 42, "pull_request": {}},
		"comment": {"user": {"login": "emmahsax"}},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1}
	}`)

	if err := env.handler.Handle(context.Background(), "issue_comment", sign(testSecret, body), body); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 0 {
		t.Errorf("expected no notification about your own comment, got %v", env.sent)
	}
}

func TestHandle_IssueComment_OrgNotAllowlisted(t *testing.T) {
	pr := githubapp.PullRequest{Number: 42, Draft: false, Author: githubapp.User{Login: "emmahsax"}}
	env := newTestEnv(t, pr, nil, nil, nil, nil)

	body := []byte(`{
		"action": "created",
		"issue": {"number": 42, "pull_request": {}},
		"comment": {"user": {"login": "reviewer1"}},
		"repository": {"name": "widgets", "owner": {"login": "other-org"}},
		"installation": {"id": 1}
	}`)

	if err := env.handler.Handle(context.Background(), "issue_comment", sign(testSecret, body), body); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 0 {
		t.Errorf("expected no notification for a non-allowlisted org, got %v", env.sent)
	}
}

func TestHandle_PullRequestReview_Approved(t *testing.T) {
	env := newTestEnv(t, githubapp.PullRequest{}, nil, nil, nil, nil)

	body := []byte(`{
		"action": "submitted",
		"review": {"state": "approved", "user": {"login": "reviewer1"}},
		"pull_request": {"number": 42, "draft": false, "user": {"login": "emmahsax"}},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1}
	}`)

	if err := env.handler.Handle(context.Background(), "pull_request_review", sign(testSecret, body), body); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 1 {
		t.Fatalf("expected 1 notification, got %d: %v", len(env.sent), env.sent)
	}
}

func TestHandle_PullRequestReview_OwnReviewSuppressed(t *testing.T) {
	env := newTestEnv(t, githubapp.PullRequest{}, nil, nil, nil, nil)

	body := []byte(`{
		"action": "submitted",
		"review": {"state": "approved", "user": {"login": "emmahsax"}},
		"pull_request": {"number": 42, "draft": false, "user": {"login": "someone-else"}},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1}
	}`)

	if err := env.handler.Handle(context.Background(), "pull_request_review", sign(testSecret, body), body); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 0 {
		t.Errorf("expected no notification about your own review, got %v", env.sent)
	}
}

func TestHandle_PullRequest_Merged_NotifiesAuthorRegardlessOfMerger(t *testing.T) {
	env := newTestEnv(t, githubapp.PullRequest{}, nil, nil, nil, nil)

	body := []byte(`{
		"action": "closed",
		"pull_request": {"number": 42, "draft": false, "merged": true, "user": {"login": "emmahsax"}},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1}
	}`)

	if err := env.handler.Handle(context.Background(), "pull_request", sign(testSecret, body), body); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 1 {
		t.Fatalf("expected 1 notification for merged PR, got %d: %v", len(env.sent), env.sent)
	}
}

func TestHandle_PullRequest_CommentAfterMergeStillSubscribed(t *testing.T) {
	// REQ-011: a merged PR remains eligible for further comment notifications.
	pr := githubapp.PullRequest{Number: 42, Draft: false, Merged: true, Author: githubapp.User{Login: "emmahsax"}}
	env := newTestEnv(t, pr, nil, nil, nil, nil)

	body := []byte(`{
		"action": "created",
		"issue": {"number": 42, "pull_request": {}},
		"comment": {"user": {"login": "reviewer1"}},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1}
	}`)

	if err := env.handler.Handle(context.Background(), "issue_comment", sign(testSecret, body), body); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 1 {
		t.Fatalf("expected 1 notification for post-merge comment, got %d: %v", len(env.sent), env.sent)
	}
}

func TestHandle_PullRequest_Labeled(t *testing.T) {
	pr := githubapp.PullRequest{Number: 42, Draft: false, Author: githubapp.User{Login: "emmahsax"}}
	env := newTestEnv(t, pr, nil, nil, nil, nil)

	body := []byte(`{
		"action": "labeled",
		"label": {"name": "needs-review"},
		"pull_request": {"number": 42, "draft": false, "user": {"login": "emmahsax"}},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1}
	}`)

	if err := env.handler.Handle(context.Background(), "pull_request", sign(testSecret, body), body); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 1 {
		t.Fatalf("expected 1 notification for labeled PR, got %d: %v", len(env.sent), env.sent)
	}
}

func TestHandle_PullRequest_LabeledDraftSuppressed(t *testing.T) {
	pr := githubapp.PullRequest{Number: 42, Draft: true, Author: githubapp.User{Login: "emmahsax"}}
	env := newTestEnv(t, pr, nil, nil, nil, nil)

	body := []byte(`{
		"action": "labeled",
		"label": {"name": "needs-review"},
		"pull_request": {"number": 42, "draft": true, "user": {"login": "emmahsax"}},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1}
	}`)

	if err := env.handler.Handle(context.Background(), "pull_request", sign(testSecret, body), body); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 0 {
		t.Errorf("expected no notification for a labeled draft PR, got %v", env.sent)
	}
}

func TestHandle_UnknownEventTypeIgnored(t *testing.T) {
	env := newTestEnv(t, githubapp.PullRequest{}, nil, nil, nil, nil)
	body := []byte(`{}`)

	if err := env.handler.Handle(context.Background(), "star", sign(testSecret, body), body); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 0 {
		t.Errorf("expected no notification for an unhandled event type, got %v", env.sent)
	}
}

// --- threading (bot token delivery) ---

type fakeThreadStore struct {
	data map[string]string
}

func newFakeThreadStore() *fakeThreadStore {
	return &fakeThreadStore{data: map[string]string{}}
}

func (f *fakeThreadStore) Get(ctx context.Context, key string) (string, bool, error) {
	ts, ok := f.data[key]
	return ts, ok, nil
}

func (f *fakeThreadStore) Put(ctx context.Context, key, ts string) error {
	f.data[key] = ts
	return nil
}

type sentMessage struct {
	text     string
	threadTS string
}

type sentUpdate struct {
	ts   string
	text string
}

// newBotTokenTestEnv wires a Handler for bot-token delivery with an
// in-memory thread store, backed by a fake Slack API that assigns each new
// (non-reply) message an incrementing timestamp, mimicking real Slack, and
// separately records chat.update calls (identified by a non-empty "ts" in
// the request body, which only chat.update sends).
func newBotTokenTestEnv(t *testing.T, pr githubapp.PullRequest) (*Handler, *[]sentMessage, *[]sentUpdate) {
	t.Helper()

	ghMux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	ghMux.HandleFunc("/repos/acme/widgets/pulls/42", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, pr) })
	ghMux.HandleFunc("/repos/acme/widgets/pulls/42/commits", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, []githubapp.Commit{}) })
	ghMux.HandleFunc("/repos/acme/widgets/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, []githubapp.Review{}) })
	ghMux.HandleFunc("/repos/acme/widgets/issues/42/comments", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, []githubapp.Comment{}) })
	ghMux.HandleFunc("/repos/acme/widgets/pulls/42/comments", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, []githubapp.Comment{}) })
	ghServer := httptest.NewServer(ghMux)
	t.Cleanup(ghServer.Close)

	var sent []sentMessage
	var updates []sentUpdate
	var tsCounter int
	slackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text     string `json:"text"`
			ThreadTS string `json:"thread_ts"`
			TS       string `json:"ts"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		if body.TS != "" {
			updates = append(updates, sentUpdate{ts: body.TS, text: body.Text})
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}

		sent = append(sent, sentMessage{text: body.Text, threadTS: body.ThreadTS})
		ts := body.ThreadTS
		if ts == "" {
			tsCounter++
			ts = fmt.Sprintf("1000.%04d", tsCounter)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": ts})
	}))
	t.Cleanup(slackServer.Close)

	notifier, err := slack.NewForTesting(slack.Config{Method: slack.MethodBotToken, BotToken: "xoxb-1", Target: "C123"}, slackServer.URL, slackServer.URL)
	if err != nil {
		t.Fatalf("slack.NewForTesting() error = %v", err)
	}

	h := &Handler{
		cfg: config.Config{
			GitHubWebhookSecret: testSecret,
			GitHubUsername:      "emmahsax",
			OrgAllowlist:        map[string]bool{"acme": true},
		},
		notifier:    notifier,
		threadStore: newFakeThreadStore(),
	}
	h.installationFor = func(ctx context.Context, installationID int64) (*githubapp.Client, error) {
		return githubapp.NewInstallationClient("fake-token", githubapp.WithBaseURL(ghServer.URL)), nil
	}

	return h, &sent, &updates
}

func TestNotify_Threading_RootHasLinkAndReplyDoesNot(t *testing.T) {
	pr := githubapp.PullRequest{Number: 42, Draft: false, Author: githubapp.User{Login: "emmahsax"}, HTMLURL: "https://github.com/acme/widgets/pull/42", Title: "Add widget support"}
	h, sent, _ := newBotTokenTestEnv(t, pr)

	firstComment := []byte(`{
		"action": "created",
		"issue": {"number": 42, "pull_request": {}},
		"comment": {"user": {"login": "reviewer1"}, "body": "first"},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1},
		"sender": {"login": "reviewer1"}
	}`)
	if err := h.Handle(context.Background(), "issue_comment", sign(testSecret, firstComment), firstComment); err != nil {
		t.Fatalf("Handle() first comment error = %v", err)
	}

	secondComment := []byte(`{
		"action": "created",
		"issue": {"number": 42, "pull_request": {}},
		"comment": {"user": {"login": "reviewer2"}, "body": "second"},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1},
		"sender": {"login": "reviewer2"}
	}`)
	if err := h.Handle(context.Background(), "issue_comment", sign(testSecret, secondComment), secondComment); err != nil {
		t.Fatalf("Handle() second comment error = %v", err)
	}

	if len(*sent) != 3 {
		t.Fatalf("expected 3 messages (header + 2 replies), got %d: %+v", len(*sent), *sent)
	}

	header := (*sent)[0]
	if header.threadTS != "" {
		t.Errorf("expected header message to have no thread_ts, got %q", header.threadTS)
	}
	if header.text != "*<https://github.com/acme/widgets/pull/42|acme/widgets#42> (@emmahsax):* Add widget support" {
		t.Errorf("header text = %q", header.text)
	}

	firstReply := (*sent)[1]
	if firstReply.threadTS != "1000.0001" {
		t.Errorf("expected first reply to thread against the header's ts, got %q", firstReply.threadTS)
	}
	if strings.Contains(firstReply.text, "http") {
		t.Errorf("expected threaded reply to omit the PR link, got %q", firstReply.text)
	}
	if firstReply.text != "*@reviewer1 commented:* first" {
		t.Errorf("first reply text = %q", firstReply.text)
	}

	secondReply := (*sent)[2]
	if secondReply.threadTS != "1000.0001" {
		t.Errorf("expected second reply to thread against the same header ts, got %q", secondReply.threadTS)
	}
	if secondReply.text != "*@reviewer2 commented:* second" {
		t.Errorf("second reply text = %q", secondReply.text)
	}
}

// --- title-change thread header refresh (FUT-002) ---

func TestHandle_PullRequestTitleEdited_UpdatesExistingThreadHeader(t *testing.T) {
	pr := githubapp.PullRequest{Number: 42, Draft: false, Author: githubapp.User{Login: "emmahsax"}, HTMLURL: "https://github.com/acme/widgets/pull/42", Title: "Add widget support"}
	h, sent, updates := newBotTokenTestEnv(t, pr)

	comment := []byte(`{
		"action": "created",
		"issue": {"number": 42, "pull_request": {}},
		"comment": {"user": {"login": "reviewer1"}, "body": "first"},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1},
		"sender": {"login": "reviewer1"}
	}`)
	if err := h.Handle(context.Background(), "issue_comment", sign(testSecret, comment), comment); err != nil {
		t.Fatalf("Handle() comment error = %v", err)
	}
	if len(*sent) != 2 {
		t.Fatalf("expected header + reply from the setup comment, got %d: %+v", len(*sent), *sent)
	}

	edited := []byte(`{
		"action": "edited",
		"changes": {"title": {"from": "Add widget support"}},
		"pull_request": {"number": 42, "draft": false, "title": "Add much better widget support", "user": {"login": "emmahsax"}, "html_url": "https://github.com/acme/widgets/pull/42"},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1},
		"sender": {"login": "emmahsax"}
	}`)
	if err := h.Handle(context.Background(), "pull_request", sign(testSecret, edited), edited); err != nil {
		t.Fatalf("Handle() edited error = %v", err)
	}

	if len(*sent) != 2 {
		t.Errorf("expected no new Send() calls from a title edit, got %d: %+v", len(*sent), *sent)
	}
	if len(*updates) != 1 {
		t.Fatalf("expected 1 chat.update call, got %d: %+v", len(*updates), *updates)
	}
	if (*updates)[0].ts != "1000.0001" {
		t.Errorf("update ts = %q, want the header's ts 1000.0001", (*updates)[0].ts)
	}
	want := "*<https://github.com/acme/widgets/pull/42|acme/widgets#42> (@emmahsax):* Add much better widget support"
	if (*updates)[0].text != want {
		t.Errorf("update text = %q, want %q", (*updates)[0].text, want)
	}
}

func TestHandle_PullRequestTitleEdited_NoThreadYet_NoOp(t *testing.T) {
	pr := githubapp.PullRequest{Number: 42, Draft: false, Author: githubapp.User{Login: "emmahsax"}, HTMLURL: "https://github.com/acme/widgets/pull/42", Title: "Add widget support"}
	h, sent, updates := newBotTokenTestEnv(t, pr)

	edited := []byte(`{
		"action": "edited",
		"changes": {"title": {"from": "Add widget support"}},
		"pull_request": {"number": 42, "draft": false, "title": "New title", "user": {"login": "emmahsax"}, "html_url": "https://github.com/acme/widgets/pull/42"},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1},
		"sender": {"login": "emmahsax"}
	}`)
	if err := h.Handle(context.Background(), "pull_request", sign(testSecret, edited), edited); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(*sent) != 0 || len(*updates) != 0 {
		t.Errorf("expected no messages when no thread exists yet, got sent=%v updates=%v", *sent, *updates)
	}
}

func TestHandle_PullRequestEdited_NonTitleChange_Ignored(t *testing.T) {
	pr := githubapp.PullRequest{Number: 42, Draft: false, Author: githubapp.User{Login: "emmahsax"}}
	env := newTestEnv(t, pr, nil, nil, nil, nil)

	edited := []byte(`{
		"action": "edited",
		"changes": {"base": {"ref": {"from": "main"}}},
		"pull_request": {"number": 42, "draft": false, "title": "Add widget support", "user": {"login": "emmahsax"}},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1},
		"sender": {"login": "emmahsax"}
	}`)
	if err := env.handler.Handle(context.Background(), "pull_request", sign(testSecret, edited), edited); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 0 {
		t.Errorf("expected no notification for a non-title edit, got %v", env.sent)
	}
}

func TestHandle_PullRequestTitleEdited_FlatDeliveryNoOp(t *testing.T) {
	pr := githubapp.PullRequest{Number: 42, Draft: false, Author: githubapp.User{Login: "emmahsax"}}
	env := newTestEnv(t, pr, nil, nil, nil, nil)

	edited := []byte(`{
		"action": "edited",
		"changes": {"title": {"from": "Add widget support"}},
		"pull_request": {"number": 42, "draft": false, "title": "New title", "user": {"login": "emmahsax"}},
		"repository": {"name": "widgets", "owner": {"login": "acme"}},
		"installation": {"id": 1},
		"sender": {"login": "emmahsax"}
	}`)
	if err := env.handler.Handle(context.Background(), "pull_request", sign(testSecret, edited), edited); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if len(env.sent) != 0 {
		t.Errorf("expected no message for incoming-webhook delivery (no header to refresh), got %v", env.sent)
	}
}

func TestPreview(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"collapses newlines and repeated whitespace", "line one\n\nline   two", "line one line two"},
		{"short text unchanged", "hello", "hello"},
		{"truncates long text", strings.Repeat("a", previewMaxLen+50), strings.Repeat("a", previewMaxLen) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := preview(tt.in); got != tt.want {
				t.Errorf("preview() = %q, want %q", got, tt.want)
			}
		})
	}
}
