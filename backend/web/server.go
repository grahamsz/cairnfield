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
	srv := &Server{store: opts.Store, blobs: opts.Blobs, search: opts.Search, sessionTTL: opts.SessionTTL, cookieSecure: opts.CookieSecure, basePath: normalizeBasePath(opts.BasePath), staticDir: opts.StaticDir, oidc: opts.OIDC.WithDefaults()}
	srv.startBackupCleanup()
	return srv
}

func (s *Server) Handler() http.Handler {
	appMux := http.NewServeMux()
	appMux.HandleFunc("/api/", s.handleAPI)
	appMux.HandleFunc("/assets/", s.handleAsset)
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
	if r.Method != http.MethodGet && !s.validCSRF(r) {
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
			writeJSON(w, map[string]any{"note": note, "version": version, "shares": shares})
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
		s.indexCurrent(r.Context(), note, version)
		writeJSON(w, map[string]any{"note": note, "version": version})
	case "share":
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
		if err := s.store.WipeNote(r.Context(), cu.User.ID, id); err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.NotFound(w, r)
	}
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
	targetFolder := r.FormValue("folder_path")
	imported := []store.Note{}
	importOne := func(name string, content []byte, timestamp time.Time, archiveFiles map[string]importArchiveFile) error {
		if !strings.EqualFold(filepath.Ext(name), ".md") {
			return nil
		}
		clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
		if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
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
		archiveFiles := map[string]importArchiveFile{}
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
			}
		}
		for _, file := range archiveFiles {
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
		asset, err := s.store.CreateAsset(ctx, store.Asset{
			UserID: userID, NoteID: noteID, Filename: path.Base(file.Name), ContentType: contentType,
			BlobPath: saved.Path, SHA256: saved.SHA256, Size: saved.Size,
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
		contentType = mime.TypeByExtension(filepath.Ext(header.Filename))
	}
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	saved, err := s.blobs.SaveAsset(cu.User.ID, header.Filename, data)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	noteID, _ := strconv.ParseInt(r.FormValue("note_id"), 10, 64)
	encrypted := strings.EqualFold(strings.TrimSpace(r.FormValue("encrypted")), "true") || r.FormValue("encrypted") == "1"
	asset, err := s.store.CreateAsset(r.Context(), store.Asset{UserID: cu.User.ID, NoteID: noteID, Filename: header.Filename, ContentType: contentType, BlobPath: saved.Path, SHA256: saved.SHA256, Size: saved.Size, Encrypted: encrypted})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
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
	if err != nil {
		writeStoreError(w, err)
		return
	}
	f, err := s.blobs.OpenUserBlob(cu.User.ID, asset.BlobPath)
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
	_ = s.search.Index(ctx, store.SearchDocument{
		NoteID: note.ID, UserID: note.OwnerUserID, Title: note.Title, FolderPath: note.FolderPath,
		Content: version.Content, HeaderJSON: version.HeaderJSON, UpdatedAt: note.UpdatedAt,
		Encrypted: note.IsEncrypted, Shared: note.IsShared,
	})
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
