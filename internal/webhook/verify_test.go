package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	body := []byte(`{"action":"created"}`)
	secret := "shhh"

	tests := []struct {
		name   string
		secret string
		sig    string
		body   []byte
		want   bool
	}{
		{"valid signature", secret, sign(secret, body), body, true},
		{"wrong secret", secret, sign("other", body), body, false},
		{"tampered body", secret, sign(secret, body), []byte(`{"action":"deleted"}`), false},
		{"missing prefix", secret, hex.EncodeToString([]byte("deadbeef")), body, false},
		{"empty signature", secret, "", body, false},
		{"empty secret", "", sign(secret, body), body, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VerifySignature(tt.secret, tt.sig, tt.body); got != tt.want {
				t.Errorf("VerifySignature() = %v, want %v", got, tt.want)
			}
		})
	}
}
