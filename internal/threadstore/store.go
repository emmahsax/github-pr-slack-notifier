// Package threadstore persists which Slack thread (identified by its root
// message's timestamp) a given PR's notifications belong to, per REQ-015.
// This is separate from — and does not conflict with — the design spec's
// "no persistent database" requirement (REQ-004), which applies only to
// subscription determination; that's still computed live off the GitHub
// API every time.
package threadstore

import (
	"context"
	"strconv"
)

// Store maps a PR key (see Key) to the Slack timestamp of the message that
// started its thread.
type Store interface {
	// Get returns the stored thread timestamp for key, or found=false if
	// no notification has been sent for this PR yet.
	Get(ctx context.Context, key string) (ts string, found bool, err error)
	// Put records ts as the thread timestamp for key.
	Put(ctx context.Context, key, ts string) error
}

// Key returns the stable identifier used to group a PR's notifications
// into one thread.
func Key(owner, repo string, number int) string {
	return owner + "/" + repo + "#" + strconv.Itoa(number)
}
