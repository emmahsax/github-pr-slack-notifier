package handler

import "github.com/emmahsax/github-pr-slack-notifier/internal/githubapp"

// Repository is the subset of GitHub's repository object present on every
// webhook payload this notifier handles.
type Repository struct {
	Name  string         `json:"name"`
	Owner githubapp.User `json:"owner"`
}

// Installation identifies which GitHub App installation delivered an event,
// needed to exchange the App JWT for an installation access token.
type Installation struct {
	ID int64 `json:"id"`
}

// IssueCommentEvent is the payload for the "issue_comment" webhook event.
// Issue.PullRequest is non-nil only when the comment is on a pull request
// (GitHub represents PRs as issues for the comments API).
type IssueCommentEvent struct {
	Action string `json:"action"`
	Issue  struct {
		Number      int       `json:"number"`
		PullRequest *struct{} `json:"pull_request"`
	} `json:"issue"`
	Comment struct {
		User githubapp.User `json:"user"`
		Body string         `json:"body"`
	} `json:"comment"`
	Repository   Repository     `json:"repository"`
	Installation Installation   `json:"installation"`
	Sender       githubapp.User `json:"sender"`
}

// PullRequestReviewEvent is the payload for the "pull_request_review" event.
type PullRequestReviewEvent struct {
	Action       string                `json:"action"`
	Review       githubapp.Review      `json:"review"`
	PullRequest  githubapp.PullRequest `json:"pull_request"`
	Repository   Repository            `json:"repository"`
	Installation Installation          `json:"installation"`
	Sender       githubapp.User        `json:"sender"`
}

// PullRequestReviewCommentEvent is the payload for the
// "pull_request_review_comment" event — an inline comment on a specific
// line of a PR's diff (GitHub's UI links to these via "#discussion_r..."
// URLs, as opposed to "#issuecomment-..." for plain issue_comment). Unlike
// IssueCommentEvent, the full PullRequest object is included directly, no
// separate fetch needed. This covers both a thread's first inline comment
// and any replies within that thread — GitHub delivers both the same way.
type PullRequestReviewCommentEvent struct {
	Action  string `json:"action"`
	Comment struct {
		User githubapp.User `json:"user"`
		Body string         `json:"body"`
	} `json:"comment"`
	PullRequest  githubapp.PullRequest `json:"pull_request"`
	Repository   Repository            `json:"repository"`
	Installation Installation          `json:"installation"`
	Sender       githubapp.User        `json:"sender"`
}

// PullRequestEvent is the payload for the "pull_request" event, covering
// the "closed" (merge), "labeled", and "edited" actions this notifier
// handles. Changes.Title is non-nil only when the title itself was part of
// the edit (GitHub omits keys in "changes" for anything that didn't change).
type PullRequestEvent struct {
	Action  string `json:"action"`
	Changes struct {
		Title *struct {
			From string `json:"from"`
		} `json:"title"`
	} `json:"changes"`
	Label struct {
		Name string `json:"name"`
	} `json:"label"`
	PullRequest  githubapp.PullRequest `json:"pull_request"`
	Repository   Repository            `json:"repository"`
	Installation Installation          `json:"installation"`
	Sender       githubapp.User        `json:"sender"`
}
