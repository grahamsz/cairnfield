package web

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cairnfield/backend/auth"
	"cairnfield/backend/oidc"
	"cairnfield/backend/store"
)

type fakeOIDCIssuer struct {
	t              *testing.T
	server         *httptest.Server
	key            *rsa.PrivateKey
	emailByCode    map[string]string
	verifiedByCode map[string]bool
}

func newFakeOIDCIssuer(t *testing.T) *fakeOIDCIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeOIDCIssuer{
		t:              t,
		key:            key,
		emailByCode:    map[string]string{},
		verifiedByCode: map[string]bool{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	return f
}

func (f *fakeOIDCIssuer) Close() {
	f.server.Close()
}

func (f *fakeOIDCIssuer) URL() string {
	return f.server.URL
}

func (f *fakeOIDCIssuer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 f.server.URL,
			"authorization_endpoint": f.server.URL + "/authorize",
			"token_endpoint":         f.server.URL + "/token",
			"jwks_uri":               f.server.URL + "/jwks",
			"userinfo_endpoint":      f.server.URL + "/userinfo",
		})
	case "/authorize":
		redirectURI := r.URL.Query().Get("redirect_uri")
		state := r.URL.Query().Get("state")
		target, err := url.Parse(redirectURI)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		q := target.Query()
		q.Set("code", "existing")
		q.Set("state", state)
		target.RawQuery = q.Encode()
		http.Redirect(w, r, target.String(), http.StatusFound)
	case "/token":
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		code := r.FormValue("code")
		email := f.emailByCode[code]
		verified := f.verifiedByCode[code]
		nonce := "nonce-value"
		claims := map[string]any{
			"iss":            f.server.URL,
			"aud":            "client-id",
			"exp":            time.Now().Add(time.Hour).Unix(),
			"nonce":          nonce,
			"email":          email,
			"email_verified": verified,
			"name":           "OIDC User",
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "access-token",
			"id_token":     signedWebTestJWT(f.t, f.key, "kid-1", claims),
			"token_type":   "Bearer",
		})
	case "/jwks":
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{webTestJWK("kid-1", &f.key.PublicKey)}})
	case "/userinfo":
		_ = json.NewEncoder(w).Encode(map[string]string{"email": "userinfo@example.com", "name": "User Info"})
	default:
		http.NotFound(w, r)
	}
}

func TestOIDCLoginAdvertisedAndRedirects(t *testing.T) {
	fake := newFakeOIDCIssuer(t)
	defer fake.Close()
	db := testStore(t)
	defer db.Close()
	srv := New(Options{Store: db, OIDC: oidc.Config{Issuer: fake.URL(), ClientID: "client-id", ClientSecret: "secret", ProviderName: "ExampleID"}.WithDefaults()})

	bootstrap := httptest.NewRecorder()
	srv.Handler().ServeHTTP(bootstrap, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil))
	if bootstrap.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d", bootstrap.Code)
	}
	if !strings.Contains(bootstrap.Body.String(), `"login_url":"/api/oidc/login"`) {
		t.Fatalf("bootstrap did not advertise OIDC provider: %s", bootstrap.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/oidc/login", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusFound {
		t.Fatalf("login status = %d body=%s", res.Code, res.Body.String())
	}
	loc := res.Header().Get("Location")
	if !strings.HasPrefix(loc, fake.URL()+"/authorize?") {
		t.Fatalf("location = %q", loc)
	}
	if cookie := findCookie(res.Result().Cookies(), oidcStateCookie); cookie == nil || cookie.Value == "" {
		t.Fatalf("state cookie was not set")
	}
}

func TestOIDCCallbackLogsInExistingUserOnly(t *testing.T) {
	fake := newFakeOIDCIssuer(t)
	defer fake.Close()
	fake.emailByCode["existing"] = "person@example.com"
	fake.verifiedByCode["existing"] = true
	fake.emailByCode["missing"] = "missing@example.com"
	fake.verifiedByCode["missing"] = true

	db := testStore(t)
	defer db.Close()
	hash, err := auth.HashPassword("password-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser(t.Context(), "person@example.com", "Person", hash, true); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Store: db, OIDC: oidc.Config{Issuer: fake.URL(), ClientID: "client-id", ClientSecret: "secret"}.WithDefaults()})

	res := oidcCallback(t, srv, "existing", "state-value", "nonce-value")
	if res.Code != http.StatusFound {
		t.Fatalf("callback status = %d body=%s", res.Code, res.Body.String())
	}
	if loc := res.Header().Get("Location"); loc != "/" {
		t.Fatalf("callback location = %q", loc)
	}
	session := findCookie(res.Result().Cookies(), sessionCookie)
	if session == nil || session.Value == "" {
		t.Fatalf("session cookie was not set")
	}
	user, err := db.UserBySessionTokenHash(t.Context(), store.TokenHash(session.Value))
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "person@example.com" {
		t.Fatalf("session user = %q", user.Email)
	}

	missing := oidcCallback(t, srv, "missing", "state-value", "nonce-value")
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing user status = %d body=%s", missing.Code, missing.Body.String())
	}
	if session := findCookie(missing.Result().Cookies(), sessionCookie); session != nil && session.Value != "" {
		t.Fatalf("missing user received a session")
	}
	count, err := db.CountUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("user count = %d, want 1", count)
	}
}

func TestOIDCCallbackRejectsBadStateAndUnverifiedEmail(t *testing.T) {
	fake := newFakeOIDCIssuer(t)
	defer fake.Close()
	fake.emailByCode["unverified"] = "person@example.com"
	fake.verifiedByCode["unverified"] = false

	db := testStore(t)
	defer db.Close()
	hash, err := auth.HashPassword("password-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateUser(t.Context(), "person@example.com", "Person", hash, true); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Store: db, OIDC: oidc.Config{Issuer: fake.URL(), ClientID: "client-id", ClientSecret: "secret"}.WithDefaults()})

	badState := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/oidc/callback?code=unverified&state=sent-state", nil)
	req.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: "different-state.nonce-value", Path: "/api/oidc"})
	srv.Handler().ServeHTTP(badState, req)
	if badState.Code != http.StatusBadRequest {
		t.Fatalf("bad state status = %d body=%s", badState.Code, badState.Body.String())
	}

	unverified := oidcCallback(t, srv, "unverified", "state-value", "nonce-value")
	if unverified.Code != http.StatusUnauthorized {
		t.Fatalf("unverified status = %d body=%s", unverified.Code, unverified.Body.String())
	}
	if !strings.Contains(unverified.Body.String(), "not verified") {
		t.Fatalf("unexpected unverified response: %s", unverified.Body.String())
	}
}

func oidcCallback(t *testing.T, srv *Server, code, state, nonce string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/oidc/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), nil)
	req.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: state + "." + nonce, Path: "/api/oidc"})
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	return res
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func signedWebTestJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	headerJSON, err := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
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

func webTestJWK(kid string, key *rsa.PublicKey) map[string]string {
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
