package githubapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient serves fixed JSON bodies for the four subscription-check
// endpoints, keyed by the last path segment before any query string.
func newTestClient(t *testing.T, commits []Commit, reviews []Review, issueComments, reviewComments []Comment) *Client {
	t.Helper()

	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/repos/acme/widgets/pulls/42/commits", func(w http.ResponseWriter, r *http.Request) { write(w, commits) })
	mux.HandleFunc("/repos/acme/widgets/pulls/42/reviews", func(w http.ResponseWriter, r *http.Request) { write(w, reviews) })
	mux.HandleFunc("/repos/acme/widgets/issues/42/comments", func(w http.ResponseWriter, r *http.Request) { write(w, issueComments) })
	mux.HandleFunc("/repos/acme/widgets/pulls/42/comments", func(w http.ResponseWriter, r *http.Request) { write(w, reviewComments) })

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return &Client{httpClient: server.Client(), token: "test-token", baseURL: server.URL}
}

func TestIsSubscribed_Author(t *testing.T) {
	client := newTestClient(t, nil, nil, nil, nil)
	pr := &PullRequest{Number: 42, Author: User{Login: "emmahsax"}}

	subscribed, err := IsSubscribed(context.Background(), client, "acme", "widgets", pr, "emmahsax")
	if err != nil {
		t.Fatalf("IsSubscribed() error = %v", err)
	}
	if !subscribed {
		t.Error("expected PR author to be subscribed")
	}
}

func TestIsSubscribed_Committer(t *testing.T) {
	client := newTestClient(t, []Commit{{Author: &User{Login: "emmahsax"}}}, nil, nil, nil)
	pr := &PullRequest{Number: 42, Author: User{Login: "someone-else"}}

	subscribed, err := IsSubscribed(context.Background(), client, "acme", "widgets", pr, "emmahsax")
	if err != nil {
		t.Fatalf("IsSubscribed() error = %v", err)
	}
	if !subscribed {
		t.Error("expected committer to be subscribed even though they didn't author the PR")
	}
}

func TestIsSubscribed_Reviewer(t *testing.T) {
	client := newTestClient(t, nil, []Review{{User: User{Login: "emmahsax"}, State: "approved"}}, nil, nil)
	pr := &PullRequest{Number: 42, Author: User{Login: "someone-else"}}

	subscribed, err := IsSubscribed(context.Background(), client, "acme", "widgets", pr, "emmahsax")
	if err != nil {
		t.Fatalf("IsSubscribed() error = %v", err)
	}
	if !subscribed {
		t.Error("expected reviewer to be subscribed")
	}
}

func TestIsSubscribed_Commenter(t *testing.T) {
	client := newTestClient(t, nil, nil, []Comment{{User: User{Login: "emmahsax"}}}, nil)
	pr := &PullRequest{Number: 42, Author: User{Login: "someone-else"}}

	subscribed, err := IsSubscribed(context.Background(), client, "acme", "widgets", pr, "emmahsax")
	if err != nil {
		t.Fatalf("IsSubscribed() error = %v", err)
	}
	if !subscribed {
		t.Error("expected issue commenter to be subscribed")
	}
}

func TestIsSubscribed_ReviewCommenter(t *testing.T) {
	client := newTestClient(t, nil, nil, nil, []Comment{{User: User{Login: "emmahsax"}}})
	pr := &PullRequest{Number: 42, Author: User{Login: "someone-else"}}

	subscribed, err := IsSubscribed(context.Background(), client, "acme", "widgets", pr, "emmahsax")
	if err != nil {
		t.Fatalf("IsSubscribed() error = %v", err)
	}
	if !subscribed {
		t.Error("expected review commenter to be subscribed")
	}
}

// TestIsSubscribed_ReviewRequestedOnly verifies REQ-002: a user who has
// never authored, committed to, reviewed, or commented on a PR is NOT
// subscribed, even though in real usage they may have been requested as a
// reviewer or @mentioned — this function has no way to see those signals
// at all, by design.
func TestIsSubscribed_ReviewRequestedOnly(t *testing.T) {
	client := newTestClient(t, nil, nil, nil, nil)
	pr := &PullRequest{Number: 42, Author: User{Login: "someone-else"}}

	subscribed, err := IsSubscribed(context.Background(), client, "acme", "widgets", pr, "emmahsax")
	if err != nil {
		t.Fatalf("IsSubscribed() error = %v", err)
	}
	if subscribed {
		t.Error("expected no subscription when the user has taken no explicit action on the PR")
	}
}

func TestIsSubscribed_CommitWithNoLinkedAccount(t *testing.T) {
	client := newTestClient(t, []Commit{{Author: nil}}, nil, nil, nil)
	pr := &PullRequest{Number: 42, Author: User{Login: "someone-else"}}

	subscribed, err := IsSubscribed(context.Background(), client, "acme", "widgets", pr, "emmahsax")
	if err != nil {
		t.Fatalf("IsSubscribed() error = %v", err)
	}
	if subscribed {
		t.Error("expected no subscription when a commit has no linked GitHub account")
	}
}
