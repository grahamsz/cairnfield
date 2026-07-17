package web

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cairnfield/backend/auth"
	"cairnfield/backend/blob"
	"cairnfield/backend/document"
	"cairnfield/backend/oidc"
	"cairnfield/backend/search"
	"cairnfield/backend/store"
)

const (
	sessionCookie   = "cairnfield_session"
	csrfCookie      = "cairnfield_csrf"
	oidcStateCookie = "cairnfield_oidc_state"
)

var obsidianEmbedPattern = regexp.MustCompile(`!\[\[([^\]]+)\]\]`)

type Server struct {
	store        *store.Store
	blobs        *blob.Store
	search       *search.Service
	wsHub        *wsHub
	sessionTTL   time.Duration
	cookieSecure bool
	basePath     string
	staticDir    string
	oidc         oidc.Config
}

type Options struct {
	Store        *store.Store
	Blobs        *blob.Store
	Search       *search.Service
	SessionTTL   time.Duration
	CookieSecure bool
	BasePath     string
	StaticDir    string
	OIDC         oidc.Config
}

type currentUser struct {
	User store.User
	CSRF string
}

type contextKey string

const currentUserKey contextKey = "current-user"

func New(opts Options) *Server {
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = 30 * 24 * time.Hour
	}
	srv := &Server{store: opts.Store, blobs: opts.Blobs, search: opts.Search, wsHub: newWSHub(), sessionTTL: opts.SessionTTL, cookieSecure: opts.CookieSecure, basePath: normalizeBasePath(opts.BasePath), staticDir: opts.StaticDir, oidc: opts.OIDC.WithDefaults()}
	srv.startBackupCleanup()
	return srv
}

func (s *Server) Handler() http.Handler {
	appMux := http.NewServeMux()
	appMux.HandleFunc("/api/", s.handleAPI)
	appMux.HandleFunc("/assets/", s.handleAsset)
	appMux.HandleFunc("/android/latest.json", s.handleAndroidLatest)
	appMux.HandleFunc("/android/cairnfield.apk", s.handleAndroidAPK)
	// /ws lives outside /api/ so the 15s API request timeout does not kill long-lived connections.
	appMux.HandleFunc("/ws", s.handleWS)
	appMux.HandleFunc("/", s.handleSPA)
	var handler http.Handler = appMux
	if s.basePath != "" {
		mux := http.NewServeMux()
		mux.HandleFunc(s.basePath, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, s.basePath+"/", http.StatusMovedPermanently)
		})
		mux.Handle(s.basePath+"/", http.StripPrefix(s.basePath, appMux))
		mux.Handle("/", appMux)
		handler = mux
	}
	return s.withCurrentUser(s.securityHeaders(handler))
}

func normalizeBasePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return ""
	}
	value = "/" + strings.Trim(value, "/")
	value = path.Clean(value)
	if value == "/" || value == "." {
		return ""
	}
	return value
}

func (s *Server) appPath(value string) string {
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return s.basePath + value
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withCurrentUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var cu currentUser
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			if user, err := s.store.UserBySessionTokenHash(r.Context(), store.TokenHash(c.Value)); err == nil {
				cu.User = user
			}
		}
		if c, err := r.Cookie(csrfCookie); err == nil {
			cu.CSRF = c.Value
		}
		if cu.CSRF == "" {
			token, err := auth.NewOpaqueToken()
			if err == nil {
				cu.CSRF = token
				http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: token, Path: s.appPath("/"), SameSite: http.SameSiteLaxMode, Secure: s.cookieSecure})
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), currentUserKey, cu)))
	})
}

func current(r *http.Request) currentUser {
	cu, _ := r.Context().Value(currentUserKey).(currentUser)
	return cu
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	if r.Method != http.MethodGet && !s.validCSRF(r) && !isClipAPIPath(path) {
		writeAPIError(w, http.StatusForbidden, "invalid CSRF token")
		return
	}
	switch {
	case path == "bootstrap":
		s.apiBootstrap(w, r)
	case path == "setup":
		s.apiSetup(w, r)
	case path == "login":
		s.apiLogin(w, r)
	case path == "logout":
		s.apiLogout(w, r)
	case path == "profile":
		s.apiProfile(w, r)
	case path == "tokens":
		s.apiTokens(w, r)
	case strings.HasPrefix(path, "tokens/"):
		s.apiTokenPath(w, r, strings.TrimPrefix(path, "tokens/"))
	case path == "extension/zip":
		s.apiExtensionZip(w, r)
	case path == "clip/bootstrap":
		s.apiClipBootstrap(w, r)
	case path == "clip/html":
		s.apiClipHTML(w, r)
	case path == "clip/pdf":
		s.apiClipPDF(w, r)
	case path == "clip/image":
		s.apiClipImage(w, r)
	case path == "oidc/login":
		s.apiOIDCLogin(w, r)
	case path == "oidc/callback":
		s.apiOIDCCallback(w, r)
	case path == "backups":
		s.apiBackups(w, r)
	case strings.HasPrefix(path, "backups/"):
		s.apiBackupPath(w, r, strings.TrimPrefix(path, "backups/"))
	case path == "admin/users":
		s.apiAdminUsers(w, r)
	case path == "users":
		s.apiUsers(w, r)
	case path == "notes":
		s.apiNotes(w, r)
	case strings.HasPrefix(path, "notes/"):
		s.apiNotePath(w, r, strings.TrimPrefix(path, "notes/"))
	case path == "folders":
		s.apiFolders(w, r)
	case path == "folders/move":
		s.apiMoveFolder(w, r)
	case path == "folders/mode":
		s.apiFolderMode(w, r)
	case path == "moodboard":
		s.apiMoodboard(w, r)
	case path == "moodboard/order":
		s.apiMoodboardOrder(w, r)
	case path == "import":
		s.apiImport(w, r)
	case path == "templates":
		s.apiTemplates(w, r)
	case strings.HasPrefix(path, "templates/"):
		s.apiTemplatePath(w, r, strings.TrimPrefix(path, "templates/"))
	case path == "assets":
		s.apiAssets(w, r)
	case path == "keys":
		s.apiKeys(w, r)
	case strings.HasPrefix(path, "keys/"):
		s.apiKeyPath(w, r, strings.TrimPrefix(path, "keys/"))
	case path == "search":
		s.apiSearch(w, r)
	case path == "sync/bootstrap":
		s.apiSyncBootstrap(w, r)
	case path == "sync/push":
		s.apiSyncPush(w, r)
	default:
		http.NotFound(w, r)
	}
}

func isClipAPIPath(path string) bool {
	return path == "clip/bootstrap" || path == "clip/html" || path == "clip/pdf" || path == "clip/image"
}

func (s *Server) validCSRF(r *http.Request) bool {
	cu := current(r)
	header := r.Header.Get("X-CSRF-Token")
	return cu.CSRF != "" && header != "" && subtle.ConstantTimeCompare([]byte(cu.CSRF), []byte(header)) == 1
}

func (s *Server) requireAuth(w http.ResponseWriter, r *http.Request) (currentUser, bool) {
	cu := current(r)
	if cu.User.ID == 0 {
		writeAPIError(w, http.StatusUnauthorized, "login required")
		return currentUser{}, false
	}
	return cu, true
}

func (s *Server) requireBearerAuth(w http.ResponseWriter, r *http.Request) (currentUser, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		writeAPIError(w, http.StatusUnauthorized, "bearer token required")
		return currentUser{}, false
	}
	raw := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if raw == "" {
		writeAPIError(w, http.StatusUnauthorized, "bearer token required")
		return currentUser{}, false
	}
	user, _, err := s.store.UserByAPITokenHash(r.Context(), store.TokenHash(raw))
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid bearer token")
		return currentUser{}, false
	}
	return currentUser{User: user}, true
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (currentUser, bool) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return currentUser{}, false
	}
	if !cu.User.IsAdmin {
		writeAPIError(w, http.StatusForbidden, "admin required")
		return currentUser{}, false
	}
	return cu, true
}

func (s *Server) apiBootstrap(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.CountUsers(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cu := current(r)
	templates := []store.Template{}
	if cu.User.ID != 0 {
		templates, _ = s.store.ListTemplates(r.Context(), cu.User.ID)
	}
	writeJSON(w, map[string]any{"users_exist": count > 0, "user": userOrNil(cu.User), "csrf": cu.CSRF, "templates": templates, "auth_providers": s.authProviders()})
}

func (s *Server) authProviders() []map[string]string {
	if !s.oidc.Configured() {
		return []map[string]string{}
	}
	return []map[string]string{{
		"id":        "oidc",
		"name":      s.oidc.ProviderName,
		"login_url": s.appPath("/api/oidc/login"),
	}}
}

func userOrNil(user store.User) any {
	if user.ID == 0 {
		return nil
	}
	return user
}

func (s *Server) apiSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	count, err := s.store.CountUsers(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if count > 0 {
		writeAPIError(w, http.StatusForbidden, "setup is complete")
		return
	}
	var body struct{ Email, Name, Password string }
	if !decodeJSON(w, r, &body) {
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := s.store.CreateUser(r.Context(), body.Email, body.Name, hash, true)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.setSession(w, r, user.ID)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var body struct{ Email, Password string }
	if !decodeJSON(w, r, &body) {
		return
	}
	user, err := s.store.GetUserByEmail(r.Context(), body.Email)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	ok, err := auth.VerifyPassword(user.PasswordHash, body.Password)
	if err != nil || !ok {
		writeAPIError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	s.setSession(w, r, user.ID)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, userID int64) {
	token, err := auth.NewOpaqueToken()
	if err != nil {
		return
	}
	_ = s.store.CreateSession(r.Context(), userID, store.TokenHash(token), s.sessionTTL)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: s.appPath("/"), HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.cookieSecure, Expires: time.Now().Add(s.sessionTTL)})
}

func (s *Server) apiLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(r.Context(), store.TokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: s.appPath("/"), HttpOnly: true, MaxAge: -1, SameSite: http.SameSiteLaxMode, Secure: s.cookieSecure})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiAdminUsers(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	_ = cu
	switch r.Method {
	case http.MethodGet:
		users, err := s.store.ListUsers(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"users": users})
	case http.MethodPost:
		var body struct {
			Email, Name, Password string
			IsAdmin               bool `json:"is_admin"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		hash, err := auth.HashPassword(body.Password)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		user, err := s.store.CreateUser(r.Context(), body.Email, body.Name, hash, body.IsAdmin)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"user": user})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) apiUsers(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	filtered := []store.User{}
	for _, user := range users {
		if user.ID != cu.User.ID {
			filtered = append(filtered, user)
		}
	}
	writeJSON(w, map[string]any{"users": filtered})
}

func (s *Server) apiProfile(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodPut:
		var body struct {
			DateFormat string `json:"date_format"`
			Theme      string `json:"theme"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		user, err := s.store.UpdateUserPreferences(r.Context(), cu.User.ID, body.DateFormat, body.Theme)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"user": user})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) apiTokens(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		tokens, err := s.store.ListAPITokens(r.Context(), cu.User.ID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"tokens": apiTokenViews(tokens)})
	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		opaque, err := auth.NewOpaqueToken()
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		raw := "cairnfield_" + opaque
		token, err := s.store.CreateAPIToken(r.Context(), cu.User.ID, body.Name, store.TokenHash(raw))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"token": apiTokenView(token), "raw_token": raw})
	default:
		methodNotAllowed(w)
	}
}

type apiTokenResponse struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

func apiTokenViews(tokens []store.APIToken) []apiTokenResponse {
	out := make([]apiTokenResponse, 0, len(tokens))
	for _, token := range tokens {
		out = append(out, apiTokenView(token))
	}
	return out
}

func apiTokenView(token store.APIToken) apiTokenResponse {
	out := apiTokenResponse{ID: token.ID, UserID: token.UserID, Name: token.Name, CreatedAt: token.CreatedAt}
	if !token.LastUsedAt.IsZero() {
		lastUsed := token.LastUsedAt
		out.LastUsedAt = &lastUsed
	}
	if !token.RevokedAt.IsZero() {
		revoked := token.RevokedAt
		out.RevokedAt = &revoked
	}
	return out
}

func (s *Server) apiTokenPath(w http.ResponseWriter, r *http.Request, tail string) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(strings.Trim(tail, "/"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodDelete {
		methodNotAllowed(w)
		return
	}
	if err := s.store.RevokeAPIToken(r.Context(), cu.User.ID, id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiExtensionZip(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	dir, err := extensionDir(s.staticDir)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="cairnfield-clipper-extension.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	err = filepath.WalkDir(dir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, filePath)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = rel
		header.Method = zip.Deflate
		part, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		f, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(part, f)
		return err
	})
	if err != nil {
		// Headers may already be sent, but this keeps tests and early failures visible.
		return
	}
}

func extensionDir(staticDir string) (string, error) {
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "extension"), filepath.Join(filepath.Dir(cwd), "extension"))
	}
	if staticDir != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(staticDir), "extension"), filepath.Join(staticDir, "extension"))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(filepath.Join(candidate, "manifest.json")); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("extension files are not available on this server")
}

func (s *Server) apiNotes(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		page := parsePositiveInt(r.URL.Query().Get("page"), 1)
		const pageSize = 25
		var notes []store.NoteSummary
		var err error
		if r.URL.Query().Get("trash") == "1" {
			notes, err = s.store.ListTrashSummaries(r.Context(), cu.User.ID, pageSize+1, (page-1)*pageSize)
		} else if r.URL.Query().Get("starred") == "1" {
			notes, err = s.store.ListStarredSummaries(r.Context(), cu.User.ID, pageSize+1, (page-1)*pageSize)
		} else {
			includeDescendants := r.URL.Query().Get("descendants") == "1"
			notes, err = s.store.ListNoteSummaries(r.Context(), cu.User.ID, r.URL.Query().Get("folder"), includeDescendants, pageSize+1, (page-1)*pageSize)
		}
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		hasMore := len(notes) > pageSize
		if hasMore {
			notes = notes[:pageSize]
		}
		writeJSON(w, map[string]any{"notes": notes, "page": page, "page_size": pageSize, "has_more": hasMore})
	case http.MethodPost:
		var body struct {
			TemplateID     int64  `json:"template_id"`
			SelectedFolder string `json:"selected_folder"`
		}
		_ = decodeJSON(w, r, &body)
		note, version, reused, err := s.store.CreateNoteFromTemplate(r.Context(), cu.User.ID, body.TemplateID, body.SelectedFolder)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !reused {
			s.indexCurrent(r.Context(), note, version)
		}
		writeJSON(w, map[string]any{"note": note, "version": version, "reused": reused})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) apiNotePath(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	key := parts[0]
	id, _ := strconv.ParseInt(key, 10, 64)
	if key == "" {
		http.NotFound(w, r)
		return
	}
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			note, version, err := s.noteByKey(r.Context(), cu.User.ID, key)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			shares, _ := s.store.ListShares(r.Context(), note.OwnerUserID, note.ID)
			assets, _ := s.store.ListAssetsForNote(r.Context(), cu.User.ID, note.ID)
			writeJSON(w, map[string]any{"note": note, "version": version, "shares": shares, "assets": assets})
		case http.MethodPut:
			var body struct {
				Title         string `json:"title"`
				FolderPath    string `json:"folder_path"`
				Content       string `json:"content"`
				HeaderJSON    string `json:"header_json"`
				BaseVersionID int64  `json:"base_version_id"`
				ClientID      string `json:"client_id"`
				Encrypted     bool   `json:"is_encrypted"`
				Autosave      bool   `json:"autosave"`
				EditorID      string `json:"editor_id"`
			}
			if !decodeJSON(w, r, &body) {
				return
			}
			note, version, conflict, err := s.store.SaveNote(r.Context(), cu.User.ID, id, body.BaseVersionID, body.Title, body.FolderPath, body.Content, body.HeaderJSON, body.ClientID, body.Encrypted, !body.Autosave)
			if err != nil {
				writeStoreError(w, err)
				return
			}
			if !conflict {
				s.indexCurrent(r.Context(), note, version)
				s.wsHub.broadcastNoteSaved(note.ID, version.ID, note.Title, cu.User, noteSavedAt(version), version.BodySHA256, body.EditorID)
			}
			writeJSON(w, map[string]any{"note": note, "version": version, "conflict": conflict})
		default:
			methodNotAllowed(w)
		}
		return
	}
	switch parts[1] {
	case "folder":
		if id == 0 {
			writeAPIError(w, http.StatusBadRequest, "numeric note id required")
			return
		}
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		var body struct {
			FolderPath string `json:"folder_path"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		note, version, err := s.store.MoveNote(r.Context(), cu.User.ID, id, body.FolderPath)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		s.indexCurrent(r.Context(), note, version)
		writeJSON(w, map[string]any{"note": note, "version": version})
	case "versions":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		versions, err := s.store.ListVersions(r.Context(), cu.User.ID, id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, map[string]any{"versions": versions})
	case "untrash":
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		note, version, err := s.store.RestoreNote(r.Context(), cu.User.ID, id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if note.OwnerUserID == cu.User.ID {
			s.indexCurrent(r.Context(), note, version)
		}
		writeJSON(w, map[string]any{"note": note, "version": version})
	case "share":
		if len(parts) == 3 {
			s.handleDeleteShare(w, r, cu, key, parts[2])
			return
		}
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		var body struct{ Email, Permission string }
		if !decodeJSON(w, r, &body) {
			return
		}
		if err := s.store.UpsertShare(r.Context(), cu.User.ID, id, body.Email, body.Permission); err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	case "trash":
		if id == 0 {
			writeAPIError(w, http.StatusBadRequest, "numeric note id required")
			return
		}
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		note, version, err := s.store.TrashNote(r.Context(), cu.User.ID, id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if note.OwnerUserID == cu.User.ID {
			s.deleteFromIndex(r.Context(), cu.User.ID, note.ID)
		}
		writeJSON(w, map[string]any{"note": note, "version": version})
	case "restore":
		if id == 0 {
			writeAPIError(w, http.StatusBadRequest, "numeric note id required")
			return
		}
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		var body struct {
			VersionID int64 `json:"version_id"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		note, version, err := s.store.RestoreVersion(r.Context(), cu.User.ID, id, body.VersionID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		s.wsHub.broadcastNoteSaved(note.ID, version.ID, note.Title, cu.User, noteSavedAt(version), version.BodySHA256, "")
		writeJSON(w, map[string]any{"note": note, "version": version})
	case "star":
		if id == 0 {
			writeAPIError(w, http.StatusBadRequest, "numeric note id required")
			return
		}
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		var body struct {
			Starred bool `json:"starred"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		note, version, err := s.store.SetNoteStarred(r.Context(), cu.User.ID, id, body.Starred)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, map[string]any{"note": note, "version": version})
	case "wipe":
		if id == 0 {
			writeAPIError(w, http.StatusBadRequest, "numeric note id required")
			return
		}
		if r.Method != http.MethodDelete {
			methodNotAllowed(w)
			return
		}
		note, _, err := s.store.GetNote(r.Context(), cu.User.ID, id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if err := s.store.WipeNote(r.Context(), cu.User.ID, id); err != nil {
			writeStoreError(w, err)
			return
		}
		if note.OwnerUserID == cu.User.ID {
			s.deleteFromIndex(r.Context(), note.OwnerUserID, note.ID)
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.NotFound(w, r)
	}
}

// handleDeleteShare removes a share from a note. The owner may remove any
// recipient; anyone else may only remove their own user id (leave a note
// shared with them).
func (s *Server) handleDeleteShare(w http.ResponseWriter, r *http.Request, cu currentUser, key, userIDRaw string) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w)
		return
	}
	targetID, err := strconv.ParseInt(userIDRaw, 10, 64)
	if err != nil || targetID <= 0 {
		http.NotFound(w, r)
		return
	}
	note, _, err := s.noteByKey(r.Context(), cu.User.ID, key)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if cu.User.ID != note.OwnerUserID && targetID != cu.User.ID {
		writeAPIError(w, http.StatusForbidden, "owner access required")
		return
	}
	if err := s.store.DeleteShare(r.Context(), note.OwnerUserID, note.ID, targetID); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) apiFolders(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		folders, err := s.store.ListFolders(r.Context(), cu.User.ID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"folders": folders})
	case http.MethodPost:
		var body struct {
			Path string `json:"path"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		folder, err := s.store.CreateFolder(r.Context(), cu.User.ID, body.Path)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"folder": folder})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) apiMoveFolder(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var body struct {
		Source       string `json:"source"`
		TargetParent string `json:"target_parent"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.store.MoveFolder(r.Context(), cu.User.ID, body.Source, body.TargetParent); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	folders, err := s.store.ListFolders(r.Context(), cu.User.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"folders": folders})
}

func (s *Server) apiFolderMode(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var body struct {
		Path     string `json:"path"`
		Mode     string `json:"mode"`
		SortMode string `json:"sort_mode"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	folder, err := s.store.SetFolderSettings(r.Context(), cu.User.ID, body.Path, body.Mode, body.SortMode)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"folder": folder})
}

func (s *Server) apiMoodboard(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	folder := r.URL.Query().Get("folder")
	items, err := s.store.ListMoodboardItems(r.Context(), cu.User.ID, folder, r.URL.Query().Get("descendants") == "1")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"items": items, "folder": normalizeFolderPath(folder)})
}

func (s *Server) apiMoodboardOrder(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var body struct {
		Folder  string  `json:"folder"`
		NoteIDs []int64 `json:"note_ids"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.store.SaveMoodboardOrder(r.Context(), cu.User.ID, body.Folder, body.NoteIDs); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.store.ListMoodboardItems(r.Context(), cu.User.ID, body.Folder, false)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"items": items})
}

type clipMetadata struct {
	Title           string `json:"title"`
	SourceURL       string `json:"source_url"`
	PageURL         string `json:"page_url"`
	SelectionText   string `json:"selection_text"`
	SearchText      string `json:"search_text"`
	FolderPath      string `json:"folder_path"`
	DestinationKind string `json:"destination_kind"`
	CapturedAt      string `json:"captured_at"`
}

type clipUploadFile struct {
	Filename    string
	ContentType string
	Data        []byte
}

func (s *Server) apiClipBootstrap(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireBearerAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	folders, err := s.store.ListFolders(r.Context(), cu.User.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	boardFolders := []store.Folder{}
	for _, folder := range folders {
		if folder.DisplayMode == "moodboard" {
			boardFolders = append(boardFolders, folder)
		}
	}
	writeJSON(w, map[string]any{
		"user":          cu.User,
		"folders":       folders,
		"board_folders": boardFolders,
		"app_version":   "dev",
	})
}

func (s *Server) apiClipHTML(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireBearerAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if s.blobs == nil {
		writeAPIError(w, http.StatusInternalServerError, "blob storage is not configured")
		return
	}
	meta, data, filename, err := readClipUpload(r, "html", 50<<20)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if filename == "" {
		filename = "clip.html"
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".html") && !strings.HasSuffix(strings.ToLower(filename), ".htm") {
		filename += ".html"
	}
	preview, err := readOptionalClipUpload(r, "preview", 8<<20)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	note, version, asset, err := s.createHTMLClip(r.Context(), cu.User.ID, meta, filename, data, preview)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"note": note, "version": version, "asset": asset, "url": s.appPath(fmt.Sprintf("/notes/%s", note.Slug))})
}

func (s *Server) apiClipPDF(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireBearerAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if s.blobs == nil {
		writeAPIError(w, http.StatusInternalServerError, "blob storage is not configured")
		return
	}
	meta, data, filename, err := readClipUpload(r, "pdf", 80<<20)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if filename == "" {
		filename = "clip.pdf"
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".pdf") {
		filename += ".pdf"
	}
	contentType := r.FormValue("content_type")
	if contentType == "" {
		contentType = contentTypeForFile(filename, data)
	}
	if !strings.EqualFold(contentType, "application/pdf") && !strings.EqualFold(contentType, "application/x-pdf") {
		writeAPIError(w, http.StatusBadRequest, "pdf upload must be a PDF")
		return
	}
	preview, err := readOptionalClipUpload(r, "preview", 8<<20)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	note, version, asset, err := s.createDocumentClip(r.Context(), cu.User.ID, "pdf", meta, filename, "application/pdf", data, preview)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"note": note, "version": version, "asset": asset, "url": s.appPath(fmt.Sprintf("/notes/%s", note.Slug))})
}

func (s *Server) apiClipImage(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireBearerAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if s.blobs == nil {
		writeAPIError(w, http.StatusInternalServerError, "blob storage is not configured")
		return
	}
	meta, data, filename, err := readClipUpload(r, "image", 25<<20)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	contentType := r.FormValue("content_type")
	if contentType == "" {
		contentType = contentTypeForFile(filename, data)
	}
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		writeAPIError(w, http.StatusBadRequest, "image upload must be an image")
		return
	}
	note, version, asset, err := s.createImageClip(r.Context(), cu.User.ID, meta, filename, contentType, data)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"note": note, "version": version, "asset": asset, "url": s.appPath(fmt.Sprintf("/notes/%s", note.Slug))})
}

func (s *Server) apiImport(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 100<<20))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	var preview *importArchiveFile
	if previewFile, previewHeader, err := r.FormFile("preview"); err == nil {
		defer previewFile.Close()
		body, readErr := io.ReadAll(io.LimitReader(previewFile, 10<<20))
		if readErr != nil {
			writeAPIError(w, http.StatusBadRequest, readErr.Error())
			return
		}
		preview = &importArchiveFile{Name: previewHeader.Filename, Data: body, Modified: time.Now().UTC()}
	}
	targetFolder := r.FormValue("folder_path")
	imported := []store.Note{}
	importOne := func(name string, content []byte, timestamp time.Time, archiveFiles map[string]importArchiveFile) error {
		clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
		if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(name), ".md") {
			note, _, err := s.importDocumentNote(r.Context(), cu.User.ID, clean, targetFolder, content, preview, timestamp)
			if err != nil {
				return err
			}
			imported = append(imported, note)
			return nil
		}
		title := strings.TrimSuffix(path.Base(clean), path.Ext(clean))
		folder := targetFolder
		if dir := path.Dir(clean); dir != "." {
			folder = strings.TrimRight(targetFolder, "/") + "/" + dir
		}
		note, version, err := s.store.CreateNoteWithContentAt(r.Context(), cu.User.ID, title, folder, string(content), timestamp)
		if err != nil {
			return err
		}
		if archiveFiles != nil {
			rewritten, changed, err := s.rewriteObsidianEmbeds(r.Context(), cu.User.ID, note.ID, clean, string(content), archiveFiles)
			if err != nil {
				return err
			}
			if changed {
				note, version, err = s.store.ReplaceImportedNoteContentAt(r.Context(), cu.User.ID, note.ID, rewritten, timestamp)
				if err != nil {
					return err
				}
			}
		}
		s.indexCurrent(r.Context(), note, version)
		imported = append(imported, note)
		return nil
	}
	if strings.EqualFold(filepath.Ext(header.Filename), ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid zip archive")
			return
		}
		if manifest, ok := readBackupManifest(zr); ok {
			result, err := s.restoreBackupZip(r.Context(), cu.User.ID, manifest, zr)
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, result)
			return
		}
		archiveFiles := map[string]importArchiveFile{}
		hasMarkdown := false
		for _, zf := range zr.File {
			if zf.FileInfo().IsDir() {
				continue
			}
			rc, err := zf.Open()
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, err.Error())
				return
			}
			body, err := io.ReadAll(io.LimitReader(rc, 5<<20))
			_ = rc.Close()
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, err.Error())
				return
			}
			clean := path.Clean(strings.ReplaceAll(zf.Name, "\\", "/"))
			if clean != "." && !strings.HasPrefix(clean, "../") && !strings.HasPrefix(clean, "/") {
				archiveFiles[strings.ToLower(clean)] = importArchiveFile{Name: clean, Data: body, Modified: zf.Modified}
				if strings.EqualFold(filepath.Ext(clean), ".md") {
					hasMarkdown = true
				}
			}
		}
		for _, file := range archiveFiles {
			if hasMarkdown && !strings.EqualFold(filepath.Ext(file.Name), ".md") {
				continue
			}
			if err := importOne(file.Name, file.Data, file.Modified, archiveFiles); err != nil {
				writeAPIError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	} else if err := importOne(header.Filename, data, time.Now().UTC(), nil); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"notes": imported, "count": len(imported)})
}

type importArchiveFile struct {
	Name     string
	Data     []byte
	Modified time.Time
}

func (s *Server) rewriteObsidianEmbeds(ctx context.Context, userID, noteID int64, notePath, content string, archiveFiles map[string]importArchiveFile) (string, bool, error) {
	if len(archiveFiles) == 0 || !obsidianEmbedPattern.MatchString(content) {
		return content, false, nil
	}
	changed := false
	var firstErr error
	rewritten := obsidianEmbedPattern.ReplaceAllStringFunc(content, func(match string) string {
		if firstErr != nil {
			return match
		}
		parts := obsidianEmbedPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		target := cleanObsidianTarget(parts[1])
		if target == "" {
			return match
		}
		file, ok := resolveImportAttachment(notePath, target, archiveFiles)
		if !ok {
			return match
		}
		contentType := mime.TypeByExtension(filepath.Ext(file.Name))
		if contentType == "" {
			contentType = http.DetectContentType(file.Data)
		}
		saved, err := s.blobs.SaveAsset(userID, path.Base(file.Name), file.Data)
		if err != nil {
			firstErr = err
			return match
		}
		searchText := document.SearchableText(path.Base(file.Name), contentType, file.Data)
		asset, err := s.store.CreateAsset(ctx, store.Asset{
			UserID: userID, NoteID: noteID, Filename: path.Base(file.Name), ContentType: contentType,
			BlobPath: saved.Path, SHA256: saved.SHA256, Size: saved.Size, SearchText: searchText,
		})
		if err != nil {
			firstErr = err
			return match
		}
		url := s.appPath(fmt.Sprintf("/assets/%s/%s", asset.Slug, urlPathSegment(asset.Filename)))
		changed = true
		if strings.HasPrefix(contentType, "image/") {
			return fmt.Sprintf("![%s](%s)", path.Base(file.Name), url)
		}
		return fmt.Sprintf("[%s](%s)", path.Base(file.Name), url)
	})
	if firstErr != nil {
		return content, false, firstErr
	}
	return rewritten, changed, nil
}

func (s *Server) importDocumentNote(ctx context.Context, userID int64, cleanName, targetFolder string, data []byte, preview *importArchiveFile, timestamp time.Time) (store.Note, store.NoteVersion, error) {
	if s.blobs == nil {
		return store.Note{}, store.NoteVersion{}, errors.New("blob storage is not configured")
	}
	contentType := contentTypeForFile(cleanName, data)
	title := strings.TrimSuffix(path.Base(cleanName), path.Ext(cleanName))
	if strings.TrimSpace(title) == "" {
		title = path.Base(cleanName)
	}
	folder := targetFolder
	if dir := path.Dir(cleanName); dir != "." {
		folder = strings.TrimRight(targetFolder, "/") + "/" + dir
	}
	body := documentNoteMarkdown(path.Base(cleanName), contentType)
	note, version, err := s.store.CreateNoteWithContentAt(ctx, userID, title, folder, body, timestamp)
	if err != nil {
		return store.Note{}, store.NoteVersion{}, err
	}
	saved, err := s.blobs.SaveAsset(userID, path.Base(cleanName), data)
	if err != nil {
		return store.Note{}, store.NoteVersion{}, err
	}
	searchText := document.SearchableText(path.Base(cleanName), contentType, data)
	asset, err := s.store.CreateAsset(ctx, store.Asset{
		UserID: userID, NoteID: note.ID, VersionID: version.ID, Filename: path.Base(cleanName),
		ContentType: contentType, BlobPath: saved.Path, SHA256: saved.SHA256, Size: saved.Size,
		SearchText: searchText,
	})
	if err != nil {
		return store.Note{}, store.NoteVersion{}, err
	}
	var previewAsset *store.Asset
	if preview != nil && len(preview.Data) > 0 {
		previewName := strings.TrimSpace(preview.Name)
		if previewName == "" {
			previewName = strings.TrimSuffix(path.Base(cleanName), path.Ext(cleanName)) + "-preview.png"
		}
		previewType := contentTypeForFile(previewName, preview.Data)
		savedPreview, err := s.blobs.SaveAsset(userID, previewName, preview.Data)
		if err != nil {
			return store.Note{}, store.NoteVersion{}, err
		}
		created, err := s.store.CreateAsset(ctx, store.Asset{
			UserID: userID, NoteID: note.ID, VersionID: version.ID, Filename: previewName,
			ContentType: previewType, BlobPath: savedPreview.Path, SHA256: savedPreview.SHA256, Size: savedPreview.Size,
		})
		if err != nil {
			return store.Note{}, store.NoteVersion{}, err
		}
		previewAsset = &created
	}
	url := s.appPath(fmt.Sprintf("/assets/%s/%s", asset.Slug, urlPathSegment(asset.Filename)))
	body = documentNoteMarkdownWithURL(asset.Filename, asset.ContentType, url)
	headerJSON, err := documentNoteHeaderJSON(asset, previewAsset)
	if err != nil {
		return store.Note{}, store.NoteVersion{}, err
	}
	note, version, err = s.store.ReplaceImportedNoteContentAndHeaderAt(ctx, userID, note.ID, body, headerJSON, timestamp)
	if err != nil {
		return store.Note{}, store.NoteVersion{}, err
	}
	s.indexCurrent(ctx, note, version)
	return note, version, nil
}

func documentNoteHeaderJSON(asset store.Asset, previewAsset *store.Asset) (string, error) {
	header := map[string]any{
		"kind": "document",
		"asset": map[string]any{
			"id":           asset.ID,
			"slug":         asset.Slug,
			"filename":     asset.Filename,
			"content_type": asset.ContentType,
			"size":         asset.Size,
		},
	}
	if previewAsset != nil {
		header["preview_asset"] = map[string]any{
			"id":           previewAsset.ID,
			"slug":         previewAsset.Slug,
			"filename":     previewAsset.Filename,
			"content_type": previewAsset.ContentType,
			"size":         previewAsset.Size,
		}
	}
	data, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func documentNoteMarkdown(filename, contentType string) string {
	return documentNoteMarkdownWithURL(filename, contentType, "")
}

func documentNoteMarkdownWithURL(filename, contentType, url string) string {
	label := strings.TrimSpace(filename)
	if label == "" {
		label = "Document"
	}
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(label)
	b.WriteString("\n\n")
	if strings.HasPrefix(contentType, "image/") && url != "" {
		b.WriteString("![")
		b.WriteString(label)
		b.WriteString("](")
		b.WriteString(url)
		b.WriteString(")\n")
		return b.String()
	}
	if url != "" {
		b.WriteString("[")
		b.WriteString(label)
		b.WriteString("](")
		b.WriteString(url)
		b.WriteString(")\n")
		return b.String()
	}
	b.WriteString(label)
	b.WriteByte('\n')
	return b.String()
}

func readClipUpload(r *http.Request, field string, maxBytes int64) (clipMetadata, []byte, string, error) {
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		return clipMetadata{}, nil, "", err
	}
	var meta clipMetadata
	rawMeta := strings.TrimSpace(r.FormValue("metadata"))
	if rawMeta == "" {
		return clipMetadata{}, nil, "", errors.New("metadata is required")
	}
	if err := json.Unmarshal([]byte(rawMeta), &meta); err != nil {
		return clipMetadata{}, nil, "", errors.New("invalid metadata")
	}
	file, header, err := r.FormFile(field)
	if err != nil && field != "file" {
		file, header, err = r.FormFile("file")
	}
	if err != nil {
		return clipMetadata{}, nil, "", errors.New("file is required")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return clipMetadata{}, nil, "", err
	}
	if int64(len(data)) > maxBytes {
		return clipMetadata{}, nil, "", errors.New("file is too large")
	}
	return meta, data, path.Base(header.Filename), nil
}

func readOptionalClipUpload(r *http.Request, field string, maxBytes int64) (*clipUploadFile, error) {
	file, header, err := r.FormFile(field)
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New(field + " file is too large")
	}
	contentType := header.Header.Get("Content-Type")
	if contentType == "" || strings.EqualFold(contentType, "application/octet-stream") {
		contentType = contentTypeForFile(header.Filename, data)
	}
	return &clipUploadFile{Filename: path.Base(header.Filename), ContentType: contentType, Data: data}, nil
}

func (s *Server) createHTMLClip(ctx context.Context, userID int64, meta clipMetadata, filename string, data []byte, preview *clipUploadFile) (store.Note, store.NoteVersion, store.Asset, error) {
	return s.createDocumentClip(ctx, userID, "html", meta, filename, "text/html; charset=utf-8", data, preview)
}

func (s *Server) createDocumentClip(ctx context.Context, userID int64, kind string, meta clipMetadata, filename, contentType string, data []byte, preview *clipUploadFile) (store.Note, store.NoteVersion, store.Asset, error) {
	title, folder, capturedAt, err := normalizeClipMetadata(meta, "Clipped page")
	if err != nil {
		return store.Note{}, store.NoteVersion{}, store.Asset{}, err
	}
	note, version, err := s.store.CreateNoteWithContentAt(ctx, userID, title, folder, clipPlaceholderMarkdown(title), capturedAt)
	if err != nil {
		return store.Note{}, store.NoteVersion{}, store.Asset{}, err
	}
	saved, err := s.blobs.SaveAsset(userID, filename, data)
	if err != nil {
		return store.Note{}, store.NoteVersion{}, store.Asset{}, err
	}
	searchText := mergedClipSearchText(document.SearchableText(filename, contentType, data), meta.SearchText)
	asset, err := s.store.CreateAsset(ctx, store.Asset{
		UserID: userID, NoteID: note.ID, VersionID: version.ID, Filename: filename, ContentType: contentType,
		BlobPath: saved.Path, SHA256: saved.SHA256, Size: saved.Size, SearchText: searchText,
	})
	if err != nil {
		return store.Note{}, store.NoteVersion{}, store.Asset{}, err
	}
	var previewAsset *store.Asset
	if preview != nil && len(preview.Data) > 0 {
		if !strings.HasPrefix(strings.ToLower(preview.ContentType), "image/") {
			return store.Note{}, store.NoteVersion{}, store.Asset{}, errors.New("preview must be an image")
		}
		previewName := strings.TrimSpace(preview.Filename)
		if previewName == "" || previewName == "." {
			previewName = strings.TrimSuffix(filename, path.Ext(filename)) + "-preview.png"
		}
		savedPreview, err := s.blobs.SaveAsset(userID, previewName, preview.Data)
		if err != nil {
			return store.Note{}, store.NoteVersion{}, store.Asset{}, err
		}
		created, err := s.store.CreateAsset(ctx, store.Asset{
			UserID: userID, NoteID: note.ID, VersionID: version.ID, Filename: previewName,
			ContentType: preview.ContentType, BlobPath: savedPreview.Path, SHA256: savedPreview.SHA256, Size: savedPreview.Size,
		})
		if err != nil {
			return store.Note{}, store.NoteVersion{}, store.Asset{}, err
		}
		previewAsset = &created
	}
	headerJSON, err := documentClipHeaderJSON(kind, asset, previewAsset, meta)
	if err != nil {
		return store.Note{}, store.NoteVersion{}, store.Asset{}, err
	}
	body := htmlClipMarkdown(title, meta, capturedAt)
	note, version, err = s.store.ReplaceImportedNoteContentAndHeaderAt(ctx, userID, note.ID, body, headerJSON, capturedAt)
	if err != nil {
		return store.Note{}, store.NoteVersion{}, store.Asset{}, err
	}
	s.indexCurrent(ctx, note, version)
	return note, version, asset, nil
}

func (s *Server) createImageClip(ctx context.Context, userID int64, meta clipMetadata, filename, contentType string, data []byte) (store.Note, store.NoteVersion, store.Asset, error) {
	title, folder, capturedAt, err := normalizeClipMetadata(meta, "Clipped image")
	if err != nil {
		return store.Note{}, store.NoteVersion{}, store.Asset{}, err
	}
	if strings.TrimSpace(filename) == "" || filename == "." {
		filename = "image"
	}
	note, version, err := s.store.CreateNoteWithContentAt(ctx, userID, title, folder, clipPlaceholderMarkdown(title), capturedAt)
	if err != nil {
		return store.Note{}, store.NoteVersion{}, store.Asset{}, err
	}
	saved, err := s.blobs.SaveAsset(userID, filename, data)
	if err != nil {
		return store.Note{}, store.NoteVersion{}, store.Asset{}, err
	}
	asset, err := s.store.CreateAsset(ctx, store.Asset{
		UserID: userID, NoteID: note.ID, VersionID: version.ID, Filename: filename, ContentType: contentType,
		BlobPath: saved.Path, SHA256: saved.SHA256, Size: saved.Size, SearchText: strings.TrimSpace(meta.SearchText),
	})
	if err != nil {
		return store.Note{}, store.NoteVersion{}, store.Asset{}, err
	}
	assetURL := s.appPath(fmt.Sprintf("/assets/%s/%s", asset.Slug, urlPathSegment(asset.Filename)))
	headerJSON, err := documentClipHeaderJSON("image", asset, nil, meta)
	if err != nil {
		return store.Note{}, store.NoteVersion{}, store.Asset{}, err
	}
	body := imageClipMarkdown(title, meta, capturedAt, asset.Filename, assetURL)
	note, version, err = s.store.ReplaceImportedNoteContentAndHeaderAt(ctx, userID, note.ID, body, headerJSON, capturedAt)
	if err != nil {
		return store.Note{}, store.NoteVersion{}, store.Asset{}, err
	}
	s.indexCurrent(ctx, note, version)
	return note, version, asset, nil
}

func normalizeClipMetadata(meta clipMetadata, fallbackTitle string) (string, string, time.Time, error) {
	title := strings.TrimSpace(meta.Title)
	if title == "" {
		title = fallbackTitle
	}
	folder := normalizeFolderPath(meta.FolderPath)
	kind := strings.TrimSpace(meta.DestinationKind)
	if kind == "" {
		kind = "folder"
	}
	if kind != "folder" && kind != "board" {
		return "", "", time.Time{}, errors.New("destination_kind must be folder or board")
	}
	capturedAt := time.Now().UTC()
	if strings.TrimSpace(meta.CapturedAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(meta.CapturedAt))
		if err != nil {
			return "", "", time.Time{}, errors.New("captured_at must be RFC3339")
		}
		capturedAt = parsed.UTC()
	}
	return title, folder, capturedAt, nil
}

func clipPlaceholderMarkdown(title string) string {
	return "# " + strings.TrimSpace(title) + "\n\n"
}

func htmlClipMarkdown(title string, meta clipMetadata, capturedAt time.Time) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(markdownLine(title))
	b.WriteString("\n\n")
	writeClipAttribution(&b, meta, capturedAt)
	selection := strings.TrimSpace(meta.SelectionText)
	if selection != "" {
		b.WriteString("\n> ")
		b.WriteString(strings.ReplaceAll(markdownLine(limitRunes(selection, 800)), "\n", "\n> "))
		b.WriteString("\n")
	}
	return b.String()
}

func imageClipMarkdown(title string, meta clipMetadata, capturedAt time.Time, filename, assetURL string) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(markdownLine(title))
	b.WriteString("\n\n")
	writeClipAttribution(&b, meta, capturedAt)
	b.WriteString("\n![")
	b.WriteString(markdownLine(filename))
	b.WriteString("](")
	b.WriteString(assetURL)
	b.WriteString(")\n")
	return b.String()
}

func writeClipAttribution(b *strings.Builder, meta clipMetadata, capturedAt time.Time) {
	source := strings.TrimSpace(meta.SourceURL)
	if source == "" {
		source = strings.TrimSpace(meta.PageURL)
	}
	if source != "" {
		b.WriteString("Source: ")
		b.WriteString(source)
		b.WriteByte('\n')
	}
	if page := strings.TrimSpace(meta.PageURL); page != "" && page != source {
		b.WriteString("Page: ")
		b.WriteString(page)
		b.WriteByte('\n')
	}
	b.WriteString("Captured: ")
	b.WriteString(capturedAt.Format(time.RFC3339))
	b.WriteByte('\n')
}

func mergedClipSearchText(extracted, supplied string) string {
	parts := []string{}
	if text := strings.TrimSpace(extracted); text != "" {
		parts = append(parts, text)
	}
	if text := strings.TrimSpace(supplied); text != "" && !strings.Contains(extracted, text) {
		parts = append(parts, text)
	}
	return strings.TrimSpace(limitRunes(strings.Join(parts, "\n"), 250000))
}

func clipNoteHeaderJSON(kind string, asset store.Asset, meta clipMetadata, extra map[string]any) (string, error) {
	header := map[string]any{
		"kind": "clip",
		"clip": map[string]any{
			"type":             kind,
			"source_url":       strings.TrimSpace(meta.SourceURL),
			"page_url":         strings.TrimSpace(meta.PageURL),
			"destination_kind": strings.TrimSpace(meta.DestinationKind),
			"captured_at":      strings.TrimSpace(meta.CapturedAt),
		},
		"asset": map[string]any{
			"id":           asset.ID,
			"slug":         asset.Slug,
			"filename":     asset.Filename,
			"content_type": asset.ContentType,
			"size":         asset.Size,
		},
	}
	for key, value := range extra {
		header[key] = value
	}
	data, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func documentClipHeaderJSON(kind string, asset store.Asset, previewAsset *store.Asset, meta clipMetadata) (string, error) {
	header := map[string]any{
		"kind": "webpage",
		"clip": map[string]any{
			"type":             kind,
			"source_url":       strings.TrimSpace(meta.SourceURL),
			"page_url":         strings.TrimSpace(meta.PageURL),
			"destination_kind": strings.TrimSpace(meta.DestinationKind),
			"captured_at":      strings.TrimSpace(meta.CapturedAt),
		},
		"asset": map[string]any{
			"id":           asset.ID,
			"slug":         asset.Slug,
			"filename":     asset.Filename,
			"content_type": asset.ContentType,
			"size":         asset.Size,
		},
	}
	if previewAsset != nil {
		header["preview_asset"] = map[string]any{
			"id":           previewAsset.ID,
			"slug":         previewAsset.Slug,
			"filename":     previewAsset.Filename,
			"content_type": previewAsset.ContentType,
			"size":         previewAsset.Size,
		}
	}
	data, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func markdownLine(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

func limitRunes(value string, n int) string {
	runes := []rune(value)
	if len(runes) <= n {
		return value
	}
	return string(runes[:n]) + "..."
}

func contentTypeForFile(filename string, data []byte) string {
	contentType := mime.TypeByExtension(filepath.Ext(filename))
	if contentType == "" && len(data) > 0 {
		contentType = http.DetectContentType(data)
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return contentType
}

func normalizeFolderPath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || value == "." {
		return "/"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	clean := path.Clean(value)
	if clean == "." {
		return "/"
	}
	return clean
}

func cleanObsidianTarget(value string) string {
	value = strings.TrimSpace(value)
	if before, _, ok := strings.Cut(value, "|"); ok {
		value = before
	}
	if before, _, ok := strings.Cut(value, "#"); ok {
		value = before
	}
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	clean := path.Clean(value)
	if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return ""
	}
	return clean
}

func resolveImportAttachment(notePath, target string, files map[string]importArchiveFile) (importArchiveFile, bool) {
	noteDir := path.Dir(notePath)
	candidates := []string{
		path.Clean(path.Join(noteDir, target)),
		path.Clean(target),
		path.Clean(path.Join(noteDir, "attachments", target)),
		path.Clean(path.Join(noteDir, "Attachments", target)),
	}
	for _, candidate := range candidates {
		if file, ok := files[strings.ToLower(candidate)]; ok && !strings.EqualFold(path.Ext(file.Name), ".md") {
			return file, true
		}
	}
	targetBase := strings.ToLower(path.Base(target))
	var found importArchiveFile
	matches := 0
	for _, file := range files {
		if strings.EqualFold(path.Ext(file.Name), ".md") {
			continue
		}
		if strings.ToLower(path.Base(file.Name)) == targetBase {
			found = file
			matches++
		}
	}
	return found, matches == 1
}

func (s *Server) apiTemplates(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		templates, err := s.store.ListTemplates(r.Context(), cu.User.ID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"templates": templates})
	case http.MethodPost:
		var t store.Template
		if !decodeJSON(w, r, &t) {
			return
		}
		t.UserID = cu.User.ID
		out, err := s.store.UpsertTemplate(r.Context(), t)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"template": out})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) apiTemplatePath(w http.ResponseWriter, r *http.Request, path string) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	id, _ := strconv.ParseInt(strings.Trim(path, "/"), 10, 64)
	if id == 0 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var t store.Template
		if !decodeJSON(w, r, &t) {
			return
		}
		t.ID = id
		t.UserID = cu.User.ID
		out, err := s.store.UpsertTemplate(r.Context(), t)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"template": out})
	case http.MethodDelete:
		if err := s.store.DeleteTemplate(r.Context(), cu.User.ID, id); err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) apiAssets(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 25<<20))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	contentType := header.Header.Get("Content-Type")
	if override := strings.TrimSpace(r.FormValue("content_type")); override != "" {
		contentType = override
	}
	if contentType == "" {
		contentType = contentTypeForFile(header.Filename, data)
	}
	saved, err := s.blobs.SaveAsset(cu.User.ID, header.Filename, data)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	noteID, _ := strconv.ParseInt(r.FormValue("note_id"), 10, 64)
	encrypted := strings.EqualFold(strings.TrimSpace(r.FormValue("encrypted")), "true") || r.FormValue("encrypted") == "1"
	searchText := ""
	if !encrypted {
		searchText = document.SearchableText(header.Filename, contentType, data)
	}
	asset, err := s.store.CreateAsset(r.Context(), store.Asset{UserID: cu.User.ID, NoteID: noteID, Filename: header.Filename, ContentType: contentType, BlobPath: saved.Path, SHA256: saved.SHA256, Size: saved.Size, Encrypted: encrypted, SearchText: searchText})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if noteID > 0 {
		if note, version, err := s.store.GetNote(r.Context(), cu.User.ID, noteID); err == nil {
			s.indexCurrent(r.Context(), note, version)
		}
	}
	writeJSON(w, map[string]any{"asset": asset, "url": s.appPath(fmt.Sprintf("/assets/%s/%s", asset.Slug, urlPathSegment(asset.Filename)))})
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	raw := strings.Trim(strings.TrimPrefix(r.URL.Path, "/assets/"), "/")
	key := strings.Split(raw, "/")[0]
	if key == "" {
		http.FileServer(http.Dir(s.staticDir)).ServeHTTP(w, r)
		return
	}
	if _, err := strconv.ParseInt(key, 10, 64); err != nil && !isAlphaSlug(key) {
		http.FileServer(http.Dir(s.staticDir)).ServeHTTP(w, r)
		return
	}
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	asset, err := s.assetByKey(r.Context(), cu.User.ID, key)
	if errors.Is(err, store.ErrNotFound) {
		asset, err = s.sharedAssetByKey(r.Context(), cu.User.ID, key)
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	f, err := s.blobs.OpenUserBlob(asset.UserID, asset.BlobPath)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "asset not found")
		return
	}
	defer f.Close()
	if asset.Encrypted {
		w.Header().Set("Content-Type", "application/octet-stream")
	} else {
		w.Header().Set("Content-Type", asset.ContentType)
	}
	if strings.HasPrefix(strings.ToLower(asset.ContentType), "text/html") {
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src data: blob:; media-src data: blob:; font-src data:; style-src 'unsafe-inline'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}
	http.ServeContent(w, r, asset.Filename, asset.CreatedAt, f)
}

func (s *Server) apiKeys(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		keys, err := s.store.ListEncryptionKeys(r.Context(), cu.User.ID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"keys": keys})
	case http.MethodPost:
		var k store.EncryptionKey
		if !decodeJSON(w, r, &k) {
			return
		}
		k.UserID = cu.User.ID
		out, err := s.store.UpsertEncryptionKey(r.Context(), k)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"key": out})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) apiKeyPath(w http.ResponseWriter, r *http.Request, tail string) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(tail, "/"), "/")
	if len(parts) != 2 || parts[1] != "default" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := s.store.SetDefaultEncryptionKey(r.Context(), cu.User.ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "key not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	keys, err := s.store.ListEncryptionKeys(r.Context(), cu.User.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"keys": keys})
}

func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	q := r.URL.Query().Get("q")
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	const pageSize = 25
	hits, err := s.search.Search(r.Context(), cu.User.ID, q, pageSize+1, (page-1)*pageSize)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	hasMore := len(hits) > pageSize
	if hasMore {
		hits = hits[:pageSize]
	}
	notes := []store.NoteSummary{}
	for _, hit := range hits {
		note, _, err := s.store.GetNote(r.Context(), cu.User.ID, hit.ID)
		if err == nil {
			summary := store.NoteSummary{Note: note}
			if _, version, err := s.store.GetNote(r.Context(), cu.User.ID, hit.ID); err == nil {
				summary.HeaderJSON = version.HeaderJSON
				if note.IsEncrypted {
					summary.Preview = "Encrypted note"
				} else {
					summary.Preview = version.Content
				}
			}
			notes = append(notes, summary)
		}
	}
	writeJSON(w, map[string]any{"notes": notes, "page": page, "page_size": pageSize, "has_more": hasMore, "query": q})
}

func (s *Server) apiSyncBootstrap(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	notes, err := s.store.ListCurrentNotes(r.Context(), cu.User.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	folders, err := s.store.ListFolders(r.Context(), cu.User.ID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"notes": notes, "folders": folders, "server_time": time.Now().UTC().Format(time.RFC3339)})
}

func (s *Server) apiSyncPush(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	var body struct {
		Edits []struct {
			Op            string `json:"op"`
			NoteID        int64  `json:"note_id"`
			BaseVersionID int64  `json:"base_version_id"`
			Title         string `json:"title"`
			FolderPath    string `json:"folder_path"`
			Content       string `json:"content"`
			HeaderJSON    string `json:"header_json"`
			ClientID      string `json:"client_id"`
			Encrypted     bool   `json:"is_encrypted"`
			Autosave      bool   `json:"autosave"`
		} `json:"edits"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	var results []map[string]any
	for _, edit := range body.Edits {
		if edit.Op == "create" {
			note, version, reused, err := s.store.CreateNoteWithClientID(r.Context(), cu.User.ID, edit.Title, edit.FolderPath, edit.Content, edit.HeaderJSON, edit.ClientID, edit.Encrypted)
			if err != nil {
				results = append(results, map[string]any{"client_id": edit.ClientID, "note_id": edit.NoteID, "op": "create", "error": err.Error()})
				continue
			}
			s.indexCurrent(r.Context(), note, version)
			results = append(results, map[string]any{"op": "create", "client_id": edit.ClientID, "note_id": edit.NoteID, "note": note, "version": version, "reused": reused})
			continue
		}
		note, version, conflict, err := s.store.SaveNote(r.Context(), cu.User.ID, edit.NoteID, edit.BaseVersionID, edit.Title, edit.FolderPath, edit.Content, edit.HeaderJSON, edit.ClientID, edit.Encrypted, !edit.Autosave)
		if err != nil {
			results = append(results, map[string]any{"op": "update", "note_id": edit.NoteID, "client_id": edit.ClientID, "error": err.Error()})
			continue
		}
		if !conflict {
			s.indexCurrent(r.Context(), note, version)
		}
		results = append(results, map[string]any{"op": "update", "note_id": edit.NoteID, "client_id": edit.ClientID, "note": note, "version": version, "conflict": conflict})
	}
	writeJSON(w, map[string]any{"results": results})
}

func (s *Server) indexCurrent(ctx context.Context, note store.Note, version store.NoteVersion) {
	if s.search == nil {
		return
	}
	assetText, _ := s.store.AssetSearchTextForNote(ctx, note.OwnerUserID, note.ID)
	_ = s.search.Index(ctx, store.SearchDocument{
		NoteID: note.ID, UserID: note.OwnerUserID, Title: note.Title, FolderPath: note.FolderPath,
		Content: version.Content, HeaderJSON: version.HeaderJSON, AssetText: assetText, UpdatedAt: note.UpdatedAt,
		Encrypted: note.IsEncrypted, Shared: note.IsShared,
	})
}

func (s *Server) deleteFromIndex(ctx context.Context, userID, noteID int64) {
	if s.search == nil {
		return
	}
	_ = s.search.Delete(ctx, userID, noteID)
}

func parsePositiveInt(value string, fallback int) int {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dest); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": message})
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "not found")
		return
	}
	writeAPIError(w, http.StatusBadRequest, err.Error())
}

func methodNotAllowed(w http.ResponseWriter) {
	writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func (s *Server) noteByKey(ctx context.Context, userID int64, key string) (store.Note, store.NoteVersion, error) {
	if id, err := strconv.ParseInt(key, 10, 64); err == nil && id > 0 {
		return s.store.GetNote(ctx, userID, id)
	}
	return s.store.GetNoteBySlug(ctx, userID, key)
}

func (s *Server) assetByKey(ctx context.Context, userID int64, key string) (store.Asset, error) {
	if id, err := strconv.ParseInt(key, 10, 64); err == nil && id > 0 {
		return s.store.GetAsset(ctx, userID, id)
	}
	return s.store.GetAssetBySlug(ctx, userID, key)
}

// sharedAssetByKey resolves an asset owned by another user. The asset is only
// served when it is attached to a note the caller can access (e.g. it was
// shared with them); unattached assets stay owner-only.
func (s *Server) sharedAssetByKey(ctx context.Context, userID int64, key string) (store.Asset, error) {
	asset, err := s.store.GetAssetUnscoped(ctx, key)
	if err != nil {
		return store.Asset{}, err
	}
	if asset.NoteID == 0 {
		return store.Asset{}, store.ErrNotFound
	}
	if _, _, err := s.store.GetNote(ctx, userID, asset.NoteID); err != nil {
		return store.Asset{}, store.ErrNotFound
	}
	return asset, nil
}

func urlPathSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
	}
	if b.Len() == 0 {
		return "file"
	}
	return b.String()
}

func isAlphaSlug(value string) bool {
	if len(value) != 8 {
		return false
	}
	for _, r := range value {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

type androidUpdateMetadata struct {
	VersionCode int    `json:"versionCode"`
	VersionName string `json:"versionName"`
	APKURL      string `json:"apkUrl"`
	SHA256      string `json:"sha256"`
}

func (s *Server) handleAndroidLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	data, err := os.ReadFile(filepath.Join(s.staticDir, "android", "latest.json"))
	if err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var metadata androidUpdateMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "invalid android update metadata")
		return
	}
	metadata.APKURL = oidc.RequestBaseURL(r) + s.appPath("/android/cairnfield.apk")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, metadata)
}

func (s *Server) handleAndroidAPK(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	apkPath := filepath.Join(s.staticDir, "android", "cairnfield.apk")
	if _, err := os.Stat(apkPath); err != nil {
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="cairnfield.apk"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, apkPath)
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, ".") {
		if r.URL.Path == "/sw.js" {
			w.Header().Set("Cache-Control", "no-store")
		} else if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.FileServer(http.Dir(s.staticDir)).ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	data, err := os.ReadFile(filepath.Join(s.staticDir, "index.html"))
	if err != nil {
		http.Error(w, "index not found", http.StatusInternalServerError)
		return
	}
	baseHref := s.appPath("/")
	if baseHref == "" {
		baseHref = "/"
	}
	baseTag := []byte(`<base href="` + html.EscapeString(baseHref) + `">`)
	if !bytes.Contains(data, []byte("<base ")) {
		data = bytes.Replace(data, []byte("<head>"), []byte("<head>\n    "+string(baseTag)), 1)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
