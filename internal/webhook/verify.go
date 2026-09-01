// Package webhook verifies inbound GitHub webhook deliveries.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// VerifySignature reports whether sig (the raw value of the
// "X-Hub-Signature-256" header) is a valid HMAC-SHA256 signature of body
// under secret. Returns false for any malformed or mismatched signature.
func VerifySignature(secret, sig string, body []byte) bool {
	const prefix = "sha256="
	if secret == "" || !strings.HasPrefix(sig, prefix) {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(strings.TrimPrefix(sig, prefix)))
}
