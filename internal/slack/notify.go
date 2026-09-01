// Package slack delivers notifications to Slack via either a bot token
// (chat.postMessage, which supports threading a PR's activity into a
// single reply thread) or an incoming webhook URL (which does not — Slack
// incoming webhooks never return a message timestamp to thread against),
// per REQ-013/REQ-014/REQ-017 of the design spec.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Method selects which Slack delivery mechanism a Config uses.
type Method string

const (
	MethodBotToken        Method = "bot_token"
	MethodIncomingWebhook Method = "incoming_webhook"
)

// Config holds exactly the fields needed for one delivery method. Target is
// a Slack channel or user ID when Method is MethodBotToken (chat.postMessage
// posts there), or unused when Method is MethodIncomingWebhook (the
// destination is baked into the webhook URL itself).
type Config struct {
	Method     Method
	BotToken   string // required when Method == MethodBotToken
	WebhookURL string // required when Method == MethodIncomingWebhook
	Target     string // Slack channel or user ID; required when Method == MethodBotToken
}

const (
	chatPostMessageURL = "https://slack.com/api/chat.postMessage"
	chatUpdateURL      = "https://slack.com/api/chat.update"
)

type Notifier struct {
	cfg        Config
	httpClient *http.Client
	// postMessageURL/updateMessageURL are the real Slack endpoints in
	// production; tests override them to point at an httptest server.
	postMessageURL   string
	updateMessageURL string
}

func New(cfg Config) (*Notifier, error) {
	switch cfg.Method {
	case MethodBotToken:
		if cfg.BotToken == "" || cfg.Target == "" {
			return nil, errors.New("slack: bot_token delivery requires BotToken and Target")
		}
	case MethodIncomingWebhook:
		if cfg.WebhookURL == "" {
			return nil, errors.New("slack: incoming_webhook delivery requires WebhookURL")
		}
	default:
		return nil, fmt.Errorf("slack: unknown delivery method %q", cfg.Method)
	}
	return &Notifier{
		cfg:              cfg,
		httpClient:       &http.Client{Timeout: 10 * time.Second},
		postMessageURL:   chatPostMessageURL,
		updateMessageURL: chatUpdateURL,
	}, nil
}

// NewForTesting builds a Notifier exactly like New, but points the
// chat.postMessage/chat.update calls at the given URLs instead of the real
// Slack API — for other packages' tests that need a Notifier backed by a
// local httptest server (in-package tests just set the fields directly).
func NewForTesting(cfg Config, postMessageURL, updateMessageURL string) (*Notifier, error) {
	n, err := New(cfg)
	if err != nil {
		return nil, err
	}
	n.postMessageURL = postMessageURL
	n.updateMessageURL = updateMessageURL
	return n, nil
}

// Result is what a delivery attempt returns. ThreadTS is the Slack
// timestamp of the message that was sent, usable as a future thread_ts to
// reply into the same thread — but it's only ever populated for
// MethodBotToken; MethodIncomingWebhook always returns an empty ThreadTS
// since Slack's incoming webhook response carries no message identity.
type Result struct {
	ThreadTS string
}

// Send delivers text via whichever method the Notifier was configured
// with. threadTS, if non-empty, posts text as a reply within that
// existing thread (bot token delivery only — ignored for incoming
// webhooks, which always post a new flat message).
func (n *Notifier) Send(ctx context.Context, text, threadTS string) (Result, error) {
	switch n.cfg.Method {
	case MethodBotToken:
		return n.sendViaBotToken(ctx, text, threadTS)
	case MethodIncomingWebhook:
		return Result{}, n.sendViaWebhook(ctx, text)
	default:
		return Result{}, fmt.Errorf("slack: unknown delivery method %q", n.cfg.Method)
	}
}

func (n *Notifier) sendViaBotToken(ctx context.Context, text, threadTS string) (Result, error) {
	payload := map[string]string{
		"channel": n.cfg.Target,
		"text":    text,
	}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.postMessageURL, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+n.cfg.BotToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		TS    string `json:"ts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Result{}, fmt.Errorf("slack: decode chat.postMessage response: %w", err)
	}
	if !out.OK {
		return Result{}, fmt.Errorf("slack: chat.postMessage failed: %s", out.Error)
	}
	return Result{ThreadTS: out.TS}, nil
}

// Update edits a previously sent message's text in place via chat.update
// (FUT-002). Only meaningful for bot-token delivery: Slack incoming
// webhooks have no concept of editing a message they sent, since the
// webhook response never identifies the message. ts is the target
// message's own timestamp (for a thread header, that's the same value
// used elsewhere as thread_ts).
func (n *Notifier) Update(ctx context.Context, ts, text string) error {
	if n.cfg.Method != MethodBotToken {
		return fmt.Errorf("slack: chat.update requires bot_token delivery, got %q", n.cfg.Method)
	}

	payload, err := json.Marshal(map[string]string{
		"channel": n.cfg.Target,
		"ts":      ts,
		"text":    text,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.updateMessageURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+n.cfg.BotToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("slack: decode chat.update response: %w", err)
	}
	if !out.OK {
		return fmt.Errorf("slack: chat.update failed: %s", out.Error)
	}
	return nil
}

func (n *Notifier) sendViaWebhook(ctx context.Context, text string) error {
	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.cfg.WebhookURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("slack: incoming webhook returned status %d: %s", resp.StatusCode, body)
	}
	return nil
}
