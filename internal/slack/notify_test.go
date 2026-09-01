package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNew_ValidatesConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"bot token missing target", Config{Method: MethodBotToken, BotToken: "xoxb-1"}, true},
		{"bot token complete", Config{Method: MethodBotToken, BotToken: "xoxb-1", Target: "C123"}, false},
		{"webhook missing url", Config{Method: MethodIncomingWebhook}, true},
		{"webhook complete", Config{Method: MethodIncomingWebhook, WebhookURL: "https://hooks.slack.com/x"}, false},
		{"unknown method", Config{Method: "carrier_pigeon"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSend_BotToken(t *testing.T) {
	var gotAuth, gotChannel, gotThreadTS string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Channel  string `json:"channel"`
			Text     string `json:"text"`
			ThreadTS string `json:"thread_ts"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotChannel = body.Channel
		gotThreadTS = body.ThreadTS
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": "1111.2222"})
	}))
	defer server.Close()

	n, err := New(Config{Method: MethodBotToken, BotToken: "xoxb-secret", Target: "C123"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	n.postMessageURL = server.URL

	result, err := n.Send(context.Background(), "hello", "")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if gotAuth != "Bearer xoxb-secret" {
		t.Errorf("Authorization header = %q", gotAuth)
	}
	if gotChannel != "C123" {
		t.Errorf("channel = %q, want C123", gotChannel)
	}
	if gotThreadTS != "" {
		t.Errorf("expected no thread_ts on a new message, got %q", gotThreadTS)
	}
	if result.ThreadTS != "1111.2222" {
		t.Errorf("result.ThreadTS = %q, want 1111.2222", result.ThreadTS)
	}
}

func TestSend_BotToken_ThreadedReply(t *testing.T) {
	var gotThreadTS string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ThreadTS string `json:"thread_ts"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotThreadTS = body.ThreadTS
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": "1111.3333"})
	}))
	defer server.Close()

	n, err := New(Config{Method: MethodBotToken, BotToken: "xoxb-secret", Target: "C123"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	n.postMessageURL = server.URL

	if _, err := n.Send(context.Background(), "reply", "1111.2222"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if gotThreadTS != "1111.2222" {
		t.Errorf("thread_ts sent = %q, want 1111.2222", gotThreadTS)
	}
}

func TestSend_BotToken_SlackError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "channel_not_found"})
	}))
	defer server.Close()

	n, err := New(Config{Method: MethodBotToken, BotToken: "xoxb-secret", Target: "C123"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	n.postMessageURL = server.URL

	if _, err := n.Send(context.Background(), "hello", ""); err == nil {
		t.Fatal("expected error when Slack reports ok:false, got nil")
	}
}

func TestUpdate_BotToken(t *testing.T) {
	var gotTS, gotText, gotChannel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Channel string `json:"channel"`
			TS      string `json:"ts"`
			Text    string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotChannel = body.Channel
		gotTS = body.TS
		gotText = body.Text
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer server.Close()

	n, err := New(Config{Method: MethodBotToken, BotToken: "xoxb-secret", Target: "C123"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	n.updateMessageURL = server.URL

	if err := n.Update(context.Background(), "1111.2222", "new header text"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if gotChannel != "C123" {
		t.Errorf("channel = %q, want C123", gotChannel)
	}
	if gotTS != "1111.2222" {
		t.Errorf("ts = %q, want 1111.2222", gotTS)
	}
	if gotText != "new header text" {
		t.Errorf("text = %q", gotText)
	}
}

func TestUpdate_BotToken_SlackError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "message_not_found"})
	}))
	defer server.Close()

	n, err := New(Config{Method: MethodBotToken, BotToken: "xoxb-secret", Target: "C123"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	n.updateMessageURL = server.URL

	if err := n.Update(context.Background(), "1111.2222", "text"); err == nil {
		t.Fatal("expected error when Slack reports ok:false, got nil")
	}
}

func TestUpdate_IncomingWebhook_Rejected(t *testing.T) {
	n, err := New(Config{Method: MethodIncomingWebhook, WebhookURL: "https://hooks.slack.com/x"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := n.Update(context.Background(), "1111.2222", "text"); err == nil {
		t.Fatal("expected error since incoming webhooks can't edit messages, got nil")
	}
}

func TestSend_IncomingWebhook(t *testing.T) {
	var gotText string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotText = body.Text
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n, err := New(Config{Method: MethodIncomingWebhook, WebhookURL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// threadTS is ignored for incoming webhooks (REQ-017): passing one must
	// not error, and the result must never claim a thread ts exists.
	result, err := n.Send(context.Background(), "new comment on PR #42", "1111.2222")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if gotText != "new comment on PR #42" {
		t.Errorf("text = %q", gotText)
	}
	if result.ThreadTS != "" {
		t.Errorf("expected no ThreadTS from incoming webhook delivery, got %q", result.ThreadTS)
	}
}
