// Package config loads runtime configuration from environment variables.
// Non-secret values (org allowlist, username, delivery method) come in as
// plain env vars; secret values (GitHub App private key, webhook secret,
// Slack credential) are expected to already be resolved into env vars by
// the deployment layer (e.g. Terraform reading SSM SecureString params) —
// this package does not talk to SSM itself.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/emmahsax/github-pr-slack-notifier/internal/slack"
)

type Config struct {
	// GitHubWebhookSecret verifies inbound webhook signatures (SEC-002).
	GitHubWebhookSecret string
	// GitHubAppID is the GitHub App's numeric ID, used as the JWT issuer.
	GitHubAppID string
	// GitHubAppPrivateKey is the App's PEM-encoded RSA private key.
	GitHubAppPrivateKey []byte
	// GitHubUsername is the GitHub login whose subscriptions are evaluated (REQ-001).
	GitHubUsername string
	// OrgAllowlist is the set of GitHub organizations this deployment watches (REQ-009).
	OrgAllowlist map[string]bool
	// Slack is the notification delivery configuration (REQ-013/REQ-014).
	Slack slack.Config
	// ThreadStoreTableName is the DynamoDB table used to group a PR's
	// notifications into one Slack thread (REQ-015). Required when
	// Slack.Method is bot_token; unused for incoming_webhook, which can't
	// thread (REQ-017).
	ThreadStoreTableName string
}

// Load reads Config from environment variables, returning an error naming
// every missing or invalid required value.
func Load() (Config, error) {
	var errs []string
	req := func(name string) string {
		v := os.Getenv(name)
		if v == "" {
			errs = append(errs, name)
		}
		return v
	}

	cfg := Config{
		GitHubWebhookSecret: req("GITHUB_WEBHOOK_SECRET"),
		GitHubAppID:         req("GITHUB_APP_ID"),
		GitHubAppPrivateKey: []byte(req("GITHUB_APP_PRIVATE_KEY")),
		GitHubUsername:      req("GITHUB_USERNAME"),
	}

	orgs := req("GITHUB_ORG_ALLOWLIST")
	cfg.OrgAllowlist = map[string]bool{}
	for _, org := range strings.Split(orgs, ",") {
		org = strings.TrimSpace(org)
		if org != "" {
			cfg.OrgAllowlist[org] = true
		}
	}

	method := slack.Method(req("SLACK_DELIVERY_METHOD"))
	cfg.Slack.Method = method
	switch method {
	case slack.MethodBotToken:
		cfg.Slack.BotToken = req("SLACK_BOT_TOKEN")
		cfg.Slack.Target = req("SLACK_TARGET")
		cfg.ThreadStoreTableName = req("THREAD_STORE_TABLE_NAME")
	case slack.MethodIncomingWebhook:
		cfg.Slack.WebhookURL = req("SLACK_WEBHOOK_URL")
	default:
		if method != "" {
			errs = append(errs, fmt.Sprintf("SLACK_DELIVERY_METHOD=%q (must be %q or %q)", method, slack.MethodBotToken, slack.MethodIncomingWebhook))
		}
	}

	if len(errs) > 0 {
		return Config{}, errors.New("config: missing/invalid required values: " + strings.Join(errs, ", "))
	}
	return cfg, nil
}
