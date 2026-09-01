package githubapp

import "context"

// IsSubscribed implements the spec's subscription rule (REQ-001): username
// is subscribed to pr if and only if they authored it, pushed a commit to
// it, submitted a review on it, or posted a comment on it. Being requested
// for review or @mentioned does not, by itself, subscribe them (REQ-002,
// REQ-003) — this function never inspects reviewer requests or mentions.
func IsSubscribed(ctx context.Context, c *Client, owner, repo string, pr *PullRequest, username string) (bool, error) {
	if pr.Author.Login == username {
		return true, nil
	}

	commits, err := c.PullRequestCommits(ctx, owner, repo, pr.Number)
	if err != nil {
		return false, err
	}
	for _, commit := range commits {
		if commit.Author != nil && commit.Author.Login == username {
			return true, nil
		}
	}

	reviews, err := c.PullRequestReviews(ctx, owner, repo, pr.Number)
	if err != nil {
		return false, err
	}
	for _, review := range reviews {
		if review.User.Login == username {
			return true, nil
		}
	}

	issueComments, err := c.IssueComments(ctx, owner, repo, pr.Number)
	if err != nil {
		return false, err
	}
	for _, comment := range issueComments {
		if comment.User.Login == username {
			return true, nil
		}
	}

	reviewComments, err := c.ReviewComments(ctx, owner, repo, pr.Number)
	if err != nil {
		return false, err
	}
	for _, comment := range reviewComments {
		if comment.User.Login == username {
			return true, nil
		}
	}

	return false, nil
}
