package oidc

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	Issuer         string
	ClientID       string
	ClientSecret   string
	RedirectURL    string
	Scopes         string
	ProviderName   string
	AllowedEmails  map[string]bool
	AllowedDomains map[string]bool
}

func (c Config) Configured() bool {
	return c.Issuer != "" && c.ClientID != "" && c.ClientSecret != ""
}

func (c Config) WithDefaults() Config {
	c.Issuer = strings.TrimRight(strings.TrimSpace(c.Issuer), "/")
	c.ClientID = strings.TrimSpace(c.ClientID)
	c.ClientSecret = strings.TrimSpace(c.ClientSecret)
	c.RedirectURL = strings.TrimSpace(c.RedirectURL)
	c.Scopes = firstNonEmpty(strings.TrimSpace(c.Scopes), "openid email profile")
	c.ProviderName = firstNonEmpty(strings.TrimSpace(c.ProviderName), "OIDC")
	if c.AllowedEmails == nil {
		c.AllowedEmails = map[string]bool{}
	}
	if c.AllowedDomains == nil {
		c.AllowedDomains = map[string]bool{}
	}
	return c
}

func (c Config) RedirectURLFor(r *http.Request) string {
	if c.RedirectURL != "" {
		return c.RedirectURL
	}
	return RequestBaseURL(r) + "/api/oidc/callback"
}

func (c Config) EmailAllowed(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(c.AllowedEmails) == 0 && len(c.AllowedDomains) == 0 {
		return true
	}
	if c.AllowedEmails[email] {
		return true
	}
	_, domain, ok := strings.Cut(email, "@")
	return ok && c.AllowedDomains[strings.ToLower(domain)]
}

func CSVSet(value string) map[string]bool {
	out := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			out[item] = true
		}
	}
	return out
}

type DiscoveryDoc struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
}

func Discover(ctx context.Context, issuer string) (DiscoveryDoc, error) {
	var doc DiscoveryDoc
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" {
		return doc, errors.New("OIDC issuer is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return doc, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return doc, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return doc, fmt.Errorf("OIDC discovery failed with status %d", res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&doc); err != nil {
		return doc, err
	}
	if strings.TrimRight(doc.Issuer, "/") != issuer {
		return doc, errors.New("OIDC issuer mismatch")
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return doc, errors.New("OIDC discovery document is incomplete")
	}
	return doc, nil
}

func AuthorizationURL(endpoint string, cfg Config, redirectURI, state, nonce string) (string, error) {
	authURL, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := authURL.Query()
	query.Set("client_id", cfg.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("response_type", "code")
	query.Set("scope", cfg.Scopes)
	query.Set("state", state)
	query.Set("nonce", nonce)
	authURL.RawQuery = query.Encode()
	return authURL.String(), nil
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
}

func ExchangeCode(ctx context.Context, tokenEndpoint string, cfg Config, redirectURI, code string) (TokenResponse, error) {
	values := url.Values{}
	values.Set("grant_type", "authorization_code")
	values.Set("code", code)
	values.Set("redirect_uri", redirectURI)
	values.Set("client_id", cfg.ClientID)
	values.Set("client_secret", cfg.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return TokenResponse{}, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		return TokenResponse{}, fmt.Errorf("OIDC token exchange failed with status %d", res.StatusCode)
	}
	var token TokenResponse
	if err := json.Unmarshal(raw, &token); err != nil {
		return TokenResponse{}, err
	}
	if token.IDToken == "" {
		return TokenResponse{}, errors.New("OIDC token response did not include an id_token")
	}
	return token, nil
}

type IDTokenClaims struct {
	Iss           string          `json:"iss"`
	Sub           string          `json:"sub"`
	Aud           json.RawMessage `json:"aud"`
	Exp           int64           `json:"exp"`
	Nbf           int64           `json:"nbf"`
	Nonce         string          `json:"nonce"`
	Email         string          `json:"email"`
	EmailVerified *bool           `json:"email_verified"`
	Name          string          `json:"name"`
}

func ValidateIDToken(ctx context.Context, token, jwksURI, issuer, clientID, nonce string) (IDTokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return IDTokenClaims{}, errors.New("OIDC id_token is malformed")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeJWTPart(parts[0], &header); err != nil {
		return IDTokenClaims{}, err
	}
	if header.Alg != "RS256" {
		return IDTokenClaims{}, fmt.Errorf("OIDC id_token uses unsupported alg %q", header.Alg)
	}
	key, err := fetchRS256Key(ctx, jwksURI, header.Kid)
	if err != nil {
		return IDTokenClaims{}, err
	}
	signed := []byte(parts[0] + "." + parts[1])
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return IDTokenClaims{}, err
	}
	sum := sha256.Sum256(signed)
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return IDTokenClaims{}, errors.New("OIDC id_token signature is invalid")
	}
	var claims IDTokenClaims
	if err := decodeJWTPart(parts[1], &claims); err != nil {
		return IDTokenClaims{}, err
	}
	now := time.Now().Unix()
	if strings.TrimRight(claims.Iss, "/") != strings.TrimRight(issuer, "/") {
		return IDTokenClaims{}, errors.New("OIDC id_token issuer is invalid")
	}
	if !audienceContains(claims.Aud, clientID) {
		return IDTokenClaims{}, errors.New("OIDC id_token audience is invalid")
	}
	if claims.Exp == 0 || now > claims.Exp {
		return IDTokenClaims{}, errors.New("OIDC id_token has expired")
	}
	if claims.Nbf != 0 && now+60 < claims.Nbf {
		return IDTokenClaims{}, errors.New("OIDC id_token is not valid yet")
	}
	if nonce != "" && claims.Nonce != nonce {
		return IDTokenClaims{}, errors.New("OIDC id_token nonce is invalid")
	}
	return claims, nil
}

func fetchRS256Key(ctx context.Context, jwksURI, kid string) (*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("OIDC JWKS fetch failed with status %d", res.StatusCode)
	}
	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&jwks); err != nil {
		return nil, err
	}
	for _, candidate := range jwks.Keys {
		if candidate.Kty != "RSA" || (kid != "" && candidate.Kid != kid) {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(candidate.N)
		if err != nil {
			return nil, err
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(candidate.E)
		if err != nil {
			return nil, err
		}
		e := 0
		for _, b := range eBytes {
			e = e*256 + int(b)
		}
		if e == 0 {
			continue
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
	}
	return nil, errors.New("OIDC signing key was not found")
}

func FetchUserinfo(ctx context.Context, endpoint, accessToken string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return "", "", fmt.Errorf("OIDC userinfo failed with status %d", res.StatusCode)
	}
	var out struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil {
		return "", "", err
	}
	return out.Email, out.Name, nil
}

func NormalizeEmail(value string) (string, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	email := strings.ToLower(strings.TrimSpace(addr.Address))
	if email == "" || !strings.Contains(email, "@") {
		return "", errors.New("invalid email")
	}
	return email, nil
}

func RandomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func RequestBaseURL(r *http.Request) string {
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

func RequestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func decodeJWTPart(part string, dest any) error {
	raw, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.NewDecoder(bytes.NewReader(raw)).Decode(dest)
}

func audienceContains(raw json.RawMessage, clientID string) bool {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return one == clientID
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return false
	}
	for _, item := range many {
		if item == clientID {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
