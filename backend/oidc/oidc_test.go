package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConfigDefaultsAndEmailAllowed(t *testing.T) {
	cfg := Config{
		Issuer:         " https://issuer.example.com/ ",
		ClientID:       " client ",
		ClientSecret:   " secret ",
		AllowedEmails:  CSVSet("Person@Example.com"),
		AllowedDomains: CSVSet("Example.org"),
	}.WithDefaults()
	if !cfg.Configured() {
		t.Fatalf("config should be configured")
	}
	if cfg.Issuer != "https://issuer.example.com" || cfg.ClientID != "client" || cfg.ClientSecret != "secret" {
		t.Fatalf("config was not normalized: %+v", cfg)
	}
	if cfg.Scopes != "openid email profile" || cfg.ProviderName != "OIDC" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	for _, email := range []string{"person@example.com", "other@example.org"} {
		if !cfg.EmailAllowed(email) {
			t.Fatalf("%s should be allowed", email)
		}
	}
	if cfg.EmailAllowed("other@example.net") {
		t.Fatalf("unexpected email allowed")
	}
}

func TestRedirectURLForUsesForwardedHeaders(t *testing.T) {
	cfg := Config{}.WithDefaults()
	req := httptest.NewRequest(http.MethodGet, "http://internal/api/oidc/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "notes.example.com")
	if got := cfg.RedirectURLFor(req); got != "https://notes.example.com/api/oidc/callback" {
		t.Fatalf("redirect url = %q", got)
	}
	cfg.RedirectURL = "https://configured.example/callback"
	if got := cfg.RedirectURLFor(req); got != cfg.RedirectURL {
		t.Fatalf("explicit redirect url = %q", got)
	}
}

func TestNormalizeEmail(t *testing.T) {
	got, err := NormalizeEmail("Person Name <Person@Example.com>")
	if err != nil {
		t.Fatal(err)
	}
	if got != "person@example.com" {
		t.Fatalf("normalized email = %q", got)
	}
	if _, err := NormalizeEmail("not-email"); err == nil {
		t.Fatalf("invalid email was accepted")
	}
}

func TestValidateIDToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{jwkForKey("kid-1", &key.PublicKey)}})
	}))
	defer jwks.Close()

	token := signedJWT(t, key, "kid-1", map[string]any{
		"iss":   "https://issuer.example",
		"aud":   []string{"other", "client-id"},
		"exp":   time.Now().Add(time.Hour).Unix(),
		"nonce": "nonce-value",
		"email": "person@example.com",
	})
	claims, err := ValidateIDToken(context.Background(), token, jwks.URL, "https://issuer.example", "client-id", "nonce-value")
	if err != nil {
		t.Fatal(err)
	}
	if claims.Email != "person@example.com" {
		t.Fatalf("email = %q", claims.Email)
	}
	if _, err := ValidateIDToken(context.Background(), token, jwks.URL, "https://issuer.example", "client-id", "wrong"); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("expected nonce error, got %v", err)
	}
	expired := signedJWT(t, key, "kid-1", map[string]any{
		"iss": "https://issuer.example",
		"aud": "client-id",
		"exp": time.Now().Add(-time.Minute).Unix(),
	})
	if _, err := ValidateIDToken(context.Background(), expired, jwks.URL, "https://issuer.example", "client-id", ""); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expiry error, got %v", err)
	}
}

func signedJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func jwkForKey(kid string, key *rsa.PublicKey) map[string]string {
	e := []byte{byte(key.E >> 16), byte(key.E >> 8), byte(key.E)}
	for len(e) > 0 && e[0] == 0 {
		e = e[1:]
	}
	return map[string]string{
		"kty": "RSA",
		"kid": kid,
		"alg": "RS256",
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(e),
	}
}
