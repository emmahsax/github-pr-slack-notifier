package githubapp

import (
	"context"
	"fmt"
)

// User is the subset of GitHub's user object this notifier needs.
type User struct {
	Login string `json:"login"`
}

// PullRequest is the subset of GitHub's pull request object this notifier
// needs to evaluate subscription and notification rules.
type PullRequest struct {
	Number  int    `json:"number"`
	Draft   bool   `json:"draft"`
	Merged  bool   `json:"merged"`
	Author  User   `json:"user"`
	HTMLURL string `json:"html_url"`
	Title   string `json:"title"`
}

// Commit is the subset of a PR commit's object needed to check authorship.
// Author is GitHub's linked account for the commit, and is null when the
// commit's email address isn't associated with any GitHub account.
type Commit struct {
	Author *User `json:"author"`
}

// Review is the subset of a PR review needed to check subscription and
// notification state. State is one of "approved", "changes_requested",
// "commented", "dismissed", or "pending".
type Review struct {
	User    User   `json:"user"`
	State   string `json:"state"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
}

// Comment is the subset of an issue comment or PR review comment needed to
// check authorship.
type Comment struct {
	User User `json:"user"`
}

func (c *Client) PullRequest(ctx context.Context, owner, repo string, number int) (*PullRequest, error) {
	var pr PullRequest
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)
	if err := c.do(ctx, "GET", path, nil, &pr); err != nil {
		return nil, err
	}
	return &pr, nil
}

func (c *Client) PullRequestCommits(ctx context.Context, owner, repo string, number int) ([]Commit, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/commits?per_page=100", owner, repo, number)
	return fetchAllPages[Commit](ctx, c, path)
}

func (c *Client) PullRequestReviews(ctx context.Context, owner, repo string, number int) ([]Review, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=100", owner, repo, number)
	return fetchAllPages[Review](ctx, c, path)
}

func (c *Client) IssueComments(ctx context.Context, owner, repo string, number int) ([]Comment, error) {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=100", owner, repo, number)
	return fetchAllPages[Comment](ctx, c, path)
}

func (c *Client) ReviewComments(ctx context.Context, owner, repo string, number int) ([]Comment, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/comments?per_page=100", owner, repo, number)
	return fetchAllPages[Comment](ctx, c, path)
}
