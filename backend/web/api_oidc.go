package web

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"cairnfield/backend/oidc"
	"cairnfield/backend/store"
)

func (s *Server) apiOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	cfg := s.oidc
	if !cfg.Configured() {
		writeAPIError(w, http.StatusServiceUnavailable, "OIDC is not configured")
		return
	}
	discovery, err := oidc.Discover(r.Context(), cfg.Issuer)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	state, err := oidc.RandomToken()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nonce, err := oidc.RandomToken()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	redirectURI := s.oidcRedirectURL(r)
	authURL, err := oidc.AuthorizationURL(discovery.AuthorizationEndpoint, cfg, redirectURI, state, nonce)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    state + "." + nonce,
		Path:     s.appPath("/api/oidc"),
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure || oidc.RequestIsHTTPS(r),
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) apiOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	cfg := s.oidc
	if !cfg.Configured() {
		writeAPIError(w, http.StatusServiceUnavailable, "OIDC is not configured")
		return
	}
	if oidcErr := strings.TrimSpace(r.URL.Query().Get("error")); oidcErr != "" {
		writeAPIError(w, http.StatusUnauthorized, "OIDC sign-in failed: "+oidcErr)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		writeAPIError(w, http.StatusBadRequest, "OIDC callback is missing code or state")
		return
	}
	cookie, err := r.Cookie(oidcStateCookie)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "OIDC sign-in state has expired")
		return
	}
	s.clearOIDCStateCookie(w, r)
	expectedState, nonce, ok := strings.Cut(cookie.Value, ".")
	if !ok || subtle.ConstantTimeCompare([]byte(state), []byte(expectedState)) != 1 {
		writeAPIError(w, http.StatusBadRequest, "OIDC sign-in state is invalid")
		return
	}
	discovery, err := oidc.Discover(r.Context(), cfg.Issuer)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, err.Error())
		return
	}
	token, err := oidc.ExchangeCode(r.Context(), discovery.TokenEndpoint, cfg, s.oidcRedirectURL(r), code)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, err.Error())
		return
	}
	claims, err := oidc.ValidateIDToken(r.Context(), token.IDToken, discovery.JWKSURI, cfg.Issuer, cfg.ClientID, nonce)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if claims.Email == "" && discovery.UserinfoEndpoint != "" && token.AccessToken != "" {
		claims.Email, claims.Name, _ = oidc.FetchUserinfo(r.Context(), discovery.UserinfoEndpoint, token.AccessToken)
	}
	email, err := oidc.NormalizeEmail(claims.Email)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "OIDC account has no usable email address")
		return
	}
	if claims.EmailVerified != nil && !*claims.EmailVerified {
		writeAPIError(w, http.StatusUnauthorized, "OIDC email address is not verified")
		return
	}
	if !cfg.EmailAllowed(email) {
		writeAPIError(w, http.StatusForbidden, "OIDC email address is not allowed")
		return
	}
	user, err := s.store.GetUserByEmail(r.Context(), email)
	if errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, http.StatusUnauthorized, "No Cairnfield user exists for this OIDC account")
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setSession(w, r, user.ID)
	http.Redirect(w, r, s.appPath("/"), http.StatusFound)
}

func (s *Server) oidcRedirectURL(r *http.Request) string {
	if s.oidc.RedirectURL != "" {
		return s.oidc.RedirectURL
	}
	return oidc.RequestBaseURL(r) + s.appPath("/api/oidc/callback")
}

func (s *Server) clearOIDCStateCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    "",
		Path:     s.appPath("/api/oidc"),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure || oidc.RequestIsHTTPS(r),
	})
}
