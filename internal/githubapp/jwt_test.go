package githubapp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func testPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
}

func TestAppJWT(t *testing.T) {
	pemBytes := testPrivateKeyPEM(t)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	token, err := AppJWT("12345", pemBytes, now)
	if err != nil {
		t.Fatalf("AppJWT() error = %v", err)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT segments, got %d", len(parts))
	}

	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims jwtClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}

	if claims.Iss != "12345" {
		t.Errorf("iss = %q, want %q", claims.Iss, "12345")
	}
	wantIat := now.Add(-60 * time.Second).Unix()
	if claims.Iat != wantIat {
		t.Errorf("iat = %d, want %d", claims.Iat, wantIat)
	}
	if claims.Exp <= claims.Iat {
		t.Errorf("exp (%d) must be after iat (%d)", claims.Exp, claims.Iat)
	}
	if claims.Exp-claims.Iat > 10*60 {
		t.Errorf("token lifetime %ds exceeds GitHub's 10 minute cap", claims.Exp-claims.Iat)
	}
}

func TestAppJWT_InvalidKey(t *testing.T) {
	if _, err := AppJWT("12345", []byte("not a pem key"), time.Now()); err == nil {
		t.Fatal("expected error for invalid PEM, got nil")
	}
}
