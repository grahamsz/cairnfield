package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			theme TEXT NOT NULL DEFAULT 'classic',
			date_format TEXT NOT NULL DEFAULT 'ymd_slash',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			expires_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			last_seen_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS api_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			created_at INTEGER NOT NULL,
			last_used_at INTEGER NOT NULL DEFAULT 0,
			revoked_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS folders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			path TEXT NOT NULL,
			display_mode TEXT NOT NULL DEFAULT 'list',
			sort_mode TEXT NOT NULL DEFAULT 'newest',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(user_id, path)
		)`,
		`CREATE TABLE IF NOT EXISTS moodboard_items (
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			folder_path TEXT NOT NULL,
			note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
			position INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(user_id, folder_path, note_id)
		)`,
		`CREATE TABLE IF NOT EXISTS notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			folder_path TEXT NOT NULL DEFAULT '/',
			title TEXT NOT NULL,
			slug TEXT NOT NULL,
			current_version_id INTEGER NOT NULL DEFAULT 0,
			is_encrypted INTEGER NOT NULL DEFAULT 0,
			trashed_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS note_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			content_blob BLOB NOT NULL,
			header_json TEXT NOT NULL DEFAULT '{}',
			body_sha256 TEXT NOT NULL,
			base_version_id INTEGER NOT NULL DEFAULT 0,
			client_id TEXT NOT NULL DEFAULT '',
			conflicted INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS note_shares (
			note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
			owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			shared_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			permission TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(note_id, shared_user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS note_user_state (
			note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			folder_path TEXT NOT NULL DEFAULT '',
			trashed_at INTEGER NOT NULL DEFAULT 0,
			starred_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(note_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS note_templates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			title_template TEXT NOT NULL,
			folder_template TEXT NOT NULL,
			body_template TEXT NOT NULL,
			is_default INTEGER NOT NULL DEFAULT 0,
			create_once INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			slug TEXT NOT NULL DEFAULT '',
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			note_id INTEGER NOT NULL DEFAULT 0,
			version_id INTEGER NOT NULL DEFAULT 0,
			filename TEXT NOT NULL,
			content_type TEXT NOT NULL,
			blob_path TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			size INTEGER NOT NULL,
			encrypted INTEGER NOT NULL DEFAULT 0,
			search_text TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS encryption_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			label TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			public_key_armored TEXT NOT NULL,
			encrypted_private_key TEXT NOT NULL DEFAULT '',
			storage_mode TEXT NOT NULL DEFAULT 'download',
			is_default INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS outbox_edits (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			note_id INTEGER NOT NULL,
			payload_json TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS backup_exports (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			status TEXT NOT NULL,
			file_path TEXT NOT NULL DEFAULT '',
			filename TEXT NOT NULL,
			size INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			completed_at INTEGER NOT NULL DEFAULT 0,
			expires_at INTEGER NOT NULL
		)`,
		`ALTER TABLE assets ADD COLUMN slug TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE notes ADD COLUMN trashed_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE encryption_keys ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE note_user_state ADD COLUMN starred_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE assets ADD COLUMN encrypted INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE assets ADD COLUMN search_text TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN date_format TEXT NOT NULL DEFAULT 'ymd_slash'`,
		`ALTER TABLE folders ADD COLUMN display_mode TEXT NOT NULL DEFAULT 'list'`,
		`ALTER TABLE folders ADD COLUMN sort_mode TEXT NOT NULL DEFAULT 'newest'`,
		`ALTER TABLE note_templates ADD COLUMN create_once INTEGER NOT NULL DEFAULT 0`,
		`UPDATE note_templates SET body_template = replace(body_template, '\n', char(10)) WHERE instr(body_template, '\n') > 0`,
		`UPDATE note_templates SET create_once = 1 WHERE lower(name) IN ('daily note', 'diary', 'diary post') AND instr(title_template, '{date}') > 0`,
		`DELETE FROM note_templates WHERE name = 'Untitled' AND title_template = 'Untitled' AND folder_template = '/' AND body_template = ''`,
		`CREATE INDEX IF NOT EXISTS idx_notes_owner_updated ON notes(owner_user_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_note_versions_note_created ON note_versions(note_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_note_shares_user ON note_shares(shared_user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_note_user_state_user ON note_user_state(user_id, trashed_at)`,
		`CREATE INDEX IF NOT EXISTS idx_backup_exports_user_created ON backup_exports(user_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_backup_exports_expires ON backup_exports(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_api_tokens_user_created ON api_tokens(user_id, created_at DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			if strings.HasPrefix(stmt, "ALTER TABLE") && strings.Contains(err.Error(), "duplicate column name") {
				continue
			}
			return err
		}
	}
	if err := s.backfillSlugs(ctx); err != nil {
		return err
	}
	for _, stmt := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_notes_slug ON notes(slug)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_assets_slug ON assets(slug)`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func nowUnix() int64 { return time.Now().UTC().Unix() }
func unixTime(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func cleanEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func randomAlphaSlug() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, v := range buf {
		b.WriteByte('a' + byte(v%26))
	}
	return b.String(), nil
}

func (s *Store) uniqueSlug(ctx context.Context, table string) (string, error) {
	for i := 0; i < 32; i++ {
		slug, err := randomAlphaSlug()
		if err != nil {
			return "", err
		}
		var n int
		if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE slug = ?`, table), slug).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return slug, nil
		}
	}
	return "", errors.New("could not generate unique slug")
}

func (s *Store) backfillSlugs(ctx context.Context) error {
	targets := []struct {
		table string
		index string
	}{
		{"notes", "idx_notes_slug"},
		{"assets", "idx_assets_slug"},
	}
	for _, target := range targets {
		indexed, err := s.indexExists(ctx, target.index)
		if err != nil {
			return err
		}
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT id, slug FROM %s`, target.table))
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			var slug string
			if err := rows.Scan(&id, &slug); err != nil {
				_ = rows.Close()
				return err
			}
			if !indexed || !isEightAlpha(slug) {
				ids = append(ids, id)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, id := range ids {
			slug, err := s.uniqueSlug(ctx, target.table)
			if err != nil {
				return err
			}
			if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET slug = ? WHERE id = ?`, target.table), slug, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) indexExists(ctx context.Context, name string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&n)
	return n > 0, err
}

func isEightAlpha(value string) bool {
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

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, email, name, passwordHash string, admin bool) (User, error) {
	email = cleanEmail(email)
	name = strings.TrimSpace(name)
	if email == "" || name == "" || passwordHash == "" {
		return User{}, errors.New("email, name, and password are required")
	}
	ts := nowUnix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO users (email, name, password_hash, is_admin, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, email, name, passwordHash, boolInt(admin), ts, ts)
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	user, err := s.GetUserByID(ctx, id)
	if err != nil {
		return User{}, err
	}
	return user, s.SeedDefaultTemplates(ctx, user.ID)
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id, email, name, password_hash, is_admin, theme, date_format, created_at, updated_at FROM users WHERE id = ?`, id))
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id, email, name, password_hash, is_admin, theme, date_format, created_at, updated_at FROM users WHERE email = ?`, cleanEmail(email)))
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, email, name, password_hash, is_admin, theme, date_format, created_at, updated_at FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) UpdateUserPreferences(ctx context.Context, userID int64, dateFormat, theme string) (User, error) {
	dateFormat = strings.TrimSpace(dateFormat)
	switch dateFormat {
	case "ymd_slash", "mdy_slash", "dmy_slash", "iso", "long":
	default:
		return User{}, errors.New("unsupported date format")
	}
	theme = strings.TrimSpace(theme)
	switch theme {
	case "", "classic":
		theme = "classic"
	case "dark":
	default:
		return User{}, errors.New("unsupported theme")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET date_format = ?, theme = ?, updated_at = ? WHERE id = ?`, dateFormat, theme, nowUnix(), userID)
	if err != nil {
		return User{}, err
	}
	return s.GetUserByID(ctx, userID)
}

type scanner interface{ Scan(...any) error }

func scanUser(row scanner) (User, error) {
	var u User
	var admin int
	var created, updated int64
	if err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &admin, &u.Theme, &u.DateFormat, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	u.IsAdmin = admin != 0
	u.CreatedAt = unixTime(created)
	u.UpdatedAt = unixTime(updated)
	if u.Theme == "" {
		u.Theme = "classic"
	}
	if u.DateFormat == "" {
		u.DateFormat = "ymd_slash"
	}
	return u, nil
}

func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash string, ttl time.Duration) error {
	ts := nowUnix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (user_id, token_hash, expires_at, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`, userID, tokenHash, time.Now().UTC().Add(ttl).Unix(), ts, ts)
	return err
}

func (s *Store) UserBySessionTokenHash(ctx context.Context, tokenHash string) (User, error) {
	var userID int64
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM sessions WHERE token_hash = ? AND expires_at > ?`, tokenHash, nowUnix()).Scan(&userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`, nowUnix(), tokenHash)
	return s.GetUserByID(ctx, userID)
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) CreateAPIToken(ctx context.Context, userID int64, name, tokenHash string) (APIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return APIToken{}, errors.New("token name is required")
	}
	if tokenHash == "" {
		return APIToken{}, errors.New("token hash is required")
	}
	ts := nowUnix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO api_tokens (user_id, name, token_hash, created_at) VALUES (?, ?, ?, ?)`, userID, name, tokenHash, ts)
	if err != nil {
		return APIToken{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return APIToken{}, err
	}
	return s.GetAPIToken(ctx, userID, id)
}

func (s *Store) GetAPIToken(ctx context.Context, userID, id int64) (APIToken, error) {
	return scanAPIToken(s.db.QueryRowContext(ctx, `SELECT id, user_id, name, token_hash, created_at, last_used_at, revoked_at FROM api_tokens WHERE user_id = ? AND id = ?`, userID, id))
}

func (s *Store) ListAPITokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, name, token_hash, created_at, last_used_at, revoked_at FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		token, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, token)
	}
	return out, rows.Err()
}

func (s *Store) UserByAPITokenHash(ctx context.Context, tokenHash string) (User, APIToken, error) {
	token, err := scanAPIToken(s.db.QueryRowContext(ctx, `SELECT id, user_id, name, token_hash, created_at, last_used_at, revoked_at FROM api_tokens WHERE token_hash = ? AND revoked_at = 0`, tokenHash))
	if err != nil {
		return User{}, APIToken{}, err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, nowUnix(), token.ID)
	user, err := s.GetUserByID(ctx, token.UserID)
	if err != nil {
		return User{}, APIToken{}, err
	}
	token.LastUsedAt = time.Now().UTC()
	return user, token, nil
}

func (s *Store) RevokeAPIToken(ctx context.Context, userID, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = CASE WHEN revoked_at = 0 THEN ? ELSE revoked_at END WHERE user_id = ? AND id = ?`, nowUnix(), userID, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func scanAPIToken(row scanner) (APIToken, error) {
	var token APIToken
	var created, lastUsed, revoked int64
	if err := row.Scan(&token.ID, &token.UserID, &token.Name, &token.TokenHash, &created, &lastUsed, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return APIToken{}, ErrNotFound
		}
		return APIToken{}, err
	}
	token.CreatedAt = unixTime(created)
	token.LastUsedAt = unixTime(lastUsed)
	token.RevokedAt = unixTime(revoked)
	return token, nil
}

func normalizeFolder(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	path = "/" + strings.Trim(path, "/")
	path = regexp.MustCompile(`/+`).ReplaceAllString(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

func normalizeOptionalFolderTemplate(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return normalizeFolder(path)
}

func normalizeTemplateBody(body string) string {
	return strings.ReplaceAll(body, `\n`, "\n")
}

func slugify(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	var b strings.Builder
	dash := false
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
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
		return "untitled"
	}
	return b.String()
}

func (s *Store) SeedDefaultTemplates(ctx context.Context, userID int64) error {
	count := 0
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM note_templates WHERE user_id = ?`, userID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	ts := nowUnix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO note_templates (user_id, name, title_template, folder_template, body_template, is_default, create_once, created_at, updated_at) VALUES
		(?, 'Daily Note', '{date}', '/journal/{year}/{month}', ?, 1, 1, ?, ?)`,
		userID, "# {date}\n\n", ts, ts)
	return err
}

func (s *Store) ListTemplates(ctx context.Context, userID int64) ([]Template, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, name, title_template, folder_template, body_template, is_default, create_once, created_at, updated_at FROM note_templates WHERE user_id = ? ORDER BY is_default DESC, name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ListFolders(ctx context.Context, userID int64) ([]Folder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, path, display_mode, sort_mode, created_at, updated_at FROM folders WHERE user_id = ? ORDER BY path`, userID)
	if err != nil {
		return nil, err
	}
	out := []Folder{}
	for rows.Next() {
		var f Folder
		var created, updated int64
		if err := rows.Scan(&f.ID, &f.UserID, &f.Path, &f.DisplayMode, &f.SortMode, &created, &updated); err != nil {
			_ = rows.Close()
			return nil, err
		}
		f.CreatedAt = unixTime(created)
		f.UpdatedAt = unixTime(updated)
		if f.DisplayMode == "" {
			f.DisplayMode = "list"
		}
		if f.SortMode == "" {
			f.SortMode = "newest"
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		f, err := s.CreateFolder(ctx, userID, "/")
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	stateRows, err := s.db.QueryContext(ctx, `SELECT DISTINCT COALESCE(NULLIF(folder_path, ''), '/') FROM note_user_state WHERE user_id = ? AND folder_path != ''`, userID)
	if err != nil {
		return nil, err
	}
	defer stateRows.Close()
	seen := map[string]bool{}
	for _, folder := range out {
		seen[normalizeFolder(folder.Path)] = true
	}
	for stateRows.Next() {
		var path string
		if err := stateRows.Scan(&path); err != nil {
			return nil, err
		}
		path = normalizeFolder(path)
		if !seen[path] {
			out = append(out, Folder{UserID: userID, Path: path, DisplayMode: "list", SortMode: "newest", CreatedAt: time.Now(), UpdatedAt: time.Now()})
			seen[path] = true
		}
	}
	return out, nil
}

func (s *Store) CreateFolder(ctx context.Context, userID int64, path string) (Folder, error) {
	path = normalizeFolder(path)
	ts := nowUnix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO folders (user_id, path, created_at, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, path) DO UPDATE SET updated_at = excluded.updated_at`, userID, path, ts, ts)
	if err != nil {
		return Folder{}, err
	}
	var f Folder
	var created, updated int64
	err = s.db.QueryRowContext(ctx, `SELECT id, user_id, path, display_mode, sort_mode, created_at, updated_at FROM folders WHERE user_id = ? AND path = ?`, userID, path).
		Scan(&f.ID, &f.UserID, &f.Path, &f.DisplayMode, &f.SortMode, &created, &updated)
	if err != nil {
		return Folder{}, err
	}
	f.CreatedAt = unixTime(created)
	f.UpdatedAt = unixTime(updated)
	if f.DisplayMode == "" {
		f.DisplayMode = "list"
	}
	if f.SortMode == "" {
		f.SortMode = "newest"
	}
	return f, nil
}

func (s *Store) SetFolderDisplayMode(ctx context.Context, userID int64, path, mode string) (Folder, error) {
	path = normalizeFolder(path)
	sortMode := "newest"
	_ = s.db.QueryRowContext(ctx, `SELECT sort_mode FROM folders WHERE user_id = ? AND path = ?`, userID, path).Scan(&sortMode)
	return s.SetFolderSettings(ctx, userID, path, mode, sortMode)
}

func (s *Store) SetFolderSettings(ctx context.Context, userID int64, path, mode, sortMode string) (Folder, error) {
	path = normalizeFolder(path)
	mode = strings.TrimSpace(mode)
	sortMode = strings.TrimSpace(sortMode)
	if mode == "" {
		mode = "list"
	}
	if sortMode == "" {
		sortMode = "newest"
	}
	if mode != "list" && mode != "gallery" && mode != "moodboard" {
		return Folder{}, errors.New("unsupported folder display mode")
	}
	if sortMode != "newest" && sortMode != "oldest" && sortMode != "alphabetical" && sortMode != "custom" {
		return Folder{}, errors.New("unsupported folder sort mode")
	}
	if mode == "moodboard" {
		hasChildren, err := s.folderHasChildFolders(ctx, userID, path)
		if err != nil {
			return Folder{}, err
		}
		if hasChildren {
			return Folder{}, errors.New("moodboard view is only available for folders without child folders")
		}
	}
	ts := nowUnix()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO folders (user_id, path, display_mode, sort_mode, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, path) DO UPDATE SET display_mode = excluded.display_mode, sort_mode = excluded.sort_mode, updated_at = excluded.updated_at`, userID, path, mode, sortMode, ts, ts); err != nil {
		return Folder{}, err
	}
	folders, err := s.ListFolders(ctx, userID)
	if err != nil {
		return Folder{}, err
	}
	for _, folder := range folders {
		if normalizeFolder(folder.Path) == path {
			return folder, nil
		}
	}
	return Folder{}, ErrNotFound
}

func (s *Store) folderHasChildFolders(ctx context.Context, userID int64, path string) (bool, error) {
	path = normalizeFolder(path)
	prefix := strings.TrimRight(path, "/") + "/%"
	if path == "/" {
		prefix = "/%"
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
		SELECT path FROM folders WHERE user_id = ? AND path != ? AND path LIKE ?
		UNION
		SELECT folder_path FROM notes WHERE owner_user_id = ? AND trashed_at = 0 AND folder_path != ? AND folder_path LIKE ?
		UNION
		SELECT folder_path FROM note_user_state WHERE user_id = ? AND folder_path != '' AND folder_path != ? AND folder_path LIKE ?
	)`, userID, path, prefix, userID, path, prefix, userID, path, prefix).Scan(&count)
	return count > 0, err
}

func (s *Store) MoveFolder(ctx context.Context, userID int64, source, targetParent string) error {
	source = normalizeFolder(source)
	targetParent = normalizeFolder(targetParent)
	if source == "/" {
		return errors.New("cannot move All Notes")
	}
	if source == targetParent || strings.HasPrefix(targetParent+"/", source+"/") {
		return errors.New("cannot move a folder into itself")
	}
	name := strings.TrimPrefix(source[strings.LastIndex(source, "/"):], "/")
	target := normalizeFolder(strings.TrimRight(targetParent, "/") + "/" + name)
	if target == source {
		return nil
	}
	ts := nowUnix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT path, display_mode, sort_mode FROM folders WHERE user_id = ? AND (path = ? OR path LIKE ?) ORDER BY length(path)`, userID, source, source+"/%")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	type movingFolder struct {
		path     string
		mode     string
		sortMode string
	}
	folderPaths := []movingFolder{}
	for rows.Next() {
		var oldPath, mode, sortMode string
		if err := rows.Scan(&oldPath, &mode, &sortMode); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return err
		}
		if mode == "" {
			mode = "list"
		}
		if sortMode == "" {
			sortMode = "newest"
		}
		folderPaths = append(folderPaths, movingFolder{path: normalizeFolder(oldPath), mode: mode, sortMode: sortMode})
	}
	if err := rows.Close(); err != nil {
		_ = tx.Rollback()
		return err
	}
	if len(folderPaths) == 0 {
		folderPaths = append(folderPaths, movingFolder{path: source, mode: "list", sortMode: "newest"})
	}
	for _, oldFolder := range folderPaths {
		newPath := replaceFolderPrefix(oldFolder.path, source, target)
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO folders (user_id, path, display_mode, sort_mode, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, userID, newPath, oldFolder.mode, oldFolder.sortMode, ts, ts); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE folders SET display_mode = ?, sort_mode = ?, updated_at = ? WHERE user_id = ? AND path = ?`, oldFolder.mode, oldFolder.sortMode, ts, userID, newPath); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE user_id = ? AND (path = ? OR path LIKE ?)`, userID, source, source+"/%"); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notes SET folder_path = ? || substr(folder_path, ?), updated_at = ? WHERE owner_user_id = ? AND (folder_path = ? OR folder_path LIKE ?)`,
		target, len(source)+1, ts, userID, source, source+"/%"); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE moodboard_items SET folder_path = ? || substr(folder_path, ?) WHERE user_id = ? AND (folder_path = ? OR folder_path LIKE ?)`,
		target, len(source)+1, userID, source, source+"/%"); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE note_user_state SET folder_path = ? || substr(folder_path, ?), updated_at = ? WHERE user_id = ? AND (folder_path = ? OR folder_path LIKE ?)`,
		target, len(source)+1, ts, userID, source, source+"/%"); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func replaceFolderPrefix(path, source, target string) string {
	path = normalizeFolder(path)
	source = normalizeFolder(source)
	target = normalizeFolder(target)
	if path == source {
		return target
	}
	if strings.HasPrefix(path, source+"/") {
		return normalizeFolder(target + strings.TrimPrefix(path, source))
	}
	return path
}

func scanTemplate(row scanner) (Template, error) {
	var t Template
	var def, createOnce int
	var created, updated int64
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.TitleTemplate, &t.FolderTemplate, &t.BodyTemplate, &def, &createOnce, &created, &updated)
	t.IsDefault = def != 0
	t.CreateOnce = createOnce != 0
	t.CreatedAt = unixTime(created)
	t.UpdatedAt = unixTime(updated)
	return t, err
}

func (s *Store) UpsertTemplate(ctx context.Context, t Template) (Template, error) {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return Template{}, errors.New("template name is required")
	}
	if strings.TrimSpace(t.TitleTemplate) == "" {
		t.TitleTemplate = "Untitled"
	}
	t.FolderTemplate = normalizeOptionalFolderTemplate(t.FolderTemplate)
	t.BodyTemplate = normalizeTemplateBody(t.BodyTemplate)
	ts := nowUnix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Template{}, err
	}
	if t.IsDefault {
		if _, err := tx.ExecContext(ctx, `UPDATE note_templates SET is_default = 0, updated_at = ? WHERE user_id = ?`, ts, t.UserID); err != nil {
			_ = tx.Rollback()
			return Template{}, err
		}
	}
	if t.ID == 0 {
		res, err := tx.ExecContext(ctx, `INSERT INTO note_templates (user_id, name, title_template, folder_template, body_template, is_default, create_once, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, t.UserID, t.Name, t.TitleTemplate, t.FolderTemplate, t.BodyTemplate, boolInt(t.IsDefault), boolInt(t.CreateOnce), ts, ts)
		if err != nil {
			_ = tx.Rollback()
			return Template{}, err
		}
		t.ID, _ = res.LastInsertId()
	} else if _, err := tx.ExecContext(ctx, `UPDATE note_templates SET name = ?, title_template = ?, folder_template = ?, body_template = ?, is_default = ?, create_once = ?, updated_at = ? WHERE user_id = ? AND id = ?`, t.Name, t.TitleTemplate, t.FolderTemplate, t.BodyTemplate, boolInt(t.IsDefault), boolInt(t.CreateOnce), ts, t.UserID, t.ID); err != nil {
		_ = tx.Rollback()
		return Template{}, err
	}
	if err := tx.Commit(); err != nil {
		return Template{}, err
	}
	return scanTemplate(s.db.QueryRowContext(ctx, `SELECT id, user_id, name, title_template, folder_template, body_template, is_default, create_once, created_at, updated_at FROM note_templates WHERE user_id = ? AND id = ?`, t.UserID, t.ID))
}

func (s *Store) DeleteTemplate(ctx context.Context, userID, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM note_templates WHERE user_id = ? AND id = ?`, userID, id)
	return err
}

func renderTemplate(value string, now time.Time, sequence int) string {
	replacer := strings.NewReplacer(
		"{date}", now.Format("2006-01-02"),
		"{datetime}", now.Format("2006-01-02 15:04"),
		"{year}", now.Format("2006"),
		"{month}", now.Format("01"),
		"{day}", now.Format("02"),
		"{sequence}", fmt.Sprintf("%d", sequence),
	)
	return normalizeTemplateBody(replacer.Replace(value))
}

func (s *Store) CreateNote(ctx context.Context, userID, templateID int64) (Note, NoteVersion, error) {
	note, version, _, err := s.CreateNoteFromTemplate(ctx, userID, templateID, "/")
	return note, version, err
}

func (s *Store) CreateNoteFromTemplate(ctx context.Context, userID, templateID int64, selectedFolder string) (Note, NoteVersion, bool, error) {
	if err := s.SeedDefaultTemplates(ctx, userID); err != nil {
		return Note{}, NoteVersion{}, false, err
	}
	now := time.Now().UTC()
	title := "Untitled"
	folder := normalizeFolder(selectedFolder)
	content := ""
	createOnce := false
	templateFolderSpecified := false
	if templateID > 0 {
		t, err := scanTemplate(s.db.QueryRowContext(ctx, `SELECT id, user_id, name, title_template, folder_template, body_template, is_default, create_once, created_at, updated_at FROM note_templates WHERE user_id = ? AND id = ? LIMIT 1`, userID, templateID))
		if err != nil {
			return Note{}, NoteVersion{}, false, err
		}
		title = strings.TrimSpace(renderTemplate(t.TitleTemplate, now, 1))
		folderTemplate := strings.TrimSpace(renderTemplate(t.FolderTemplate, now, 1))
		if folderTemplate != "" {
			folder = normalizeFolder(folderTemplate)
			templateFolderSpecified = true
		}
		content = renderTemplate(t.BodyTemplate, now, 1)
		createOnce = t.CreateOnce
	}
	if title == "" {
		title = "Untitled"
	}
	if createOnce {
		note, version, err := s.GetCurrentNoteByTitle(ctx, userID, title)
		if err == nil {
			if templateFolderSpecified && normalizeFolder(note.FolderPath) != folder {
				movedNote, movedVersion, moveErr := s.MoveNote(ctx, userID, note.ID, folder)
				if moveErr != nil {
					return Note{}, NoteVersion{}, false, moveErr
				}
				return movedNote, movedVersion, true, nil
			}
			return note, version, true, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Note{}, NoteVersion{}, false, err
		}
	}
	ts := nowUnix()
	slug, err := s.uniqueSlug(ctx, "notes")
	if err != nil {
		return Note{}, NoteVersion{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Note{}, NoteVersion{}, false, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO notes (owner_user_id, folder_path, title, slug, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, userID, folder, title, slug, ts, ts)
	if err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, false, err
	}
	noteID, _ := res.LastInsertId()
	versionID, err := insertVersion(ctx, tx, noteID, userID, content, "{}", 0, "", false)
	if err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notes SET current_version_id = ? WHERE id = ?`, versionID, noteID); err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO folders (user_id, path, created_at, updated_at) VALUES (?, ?, ?, ?)`, userID, folder, ts, ts); err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Note{}, NoteVersion{}, false, err
	}
	note, version, err := s.GetNote(ctx, userID, noteID)
	return note, version, false, err
}

func (s *Store) CreateNoteWithContent(ctx context.Context, userID int64, title, folder, content string) (Note, NoteVersion, error) {
	return s.CreateNoteWithContentAt(ctx, userID, title, folder, content, time.Now().UTC())
}

func (s *Store) CreateNoteWithClientID(ctx context.Context, userID int64, title, folder, content, header, clientID string, encrypted bool) (Note, NoteVersion, bool, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID != "" {
		var noteID int64
		err := s.db.QueryRowContext(ctx, `SELECT nv.note_id FROM note_versions nv JOIN notes n ON n.id = nv.note_id WHERE n.owner_user_id = ? AND nv.client_id = ? ORDER BY nv.id LIMIT 1`, userID, clientID).Scan(&noteID)
		if err == nil {
			note, version, err := s.GetNote(ctx, userID, noteID)
			return note, version, true, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Note{}, NoteVersion{}, false, err
		}
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled"
	}
	folder = normalizeFolder(folder)
	if strings.TrimSpace(header) == "" {
		header = "{}"
	}
	ts := nowUnix()
	slug, err := s.uniqueSlug(ctx, "notes")
	if err != nil {
		return Note{}, NoteVersion{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Note{}, NoteVersion{}, false, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO notes (owner_user_id, folder_path, title, slug, is_encrypted, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, userID, folder, title, slug, boolInt(encrypted), ts, ts)
	if err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, false, err
	}
	noteID, _ := res.LastInsertId()
	versionID, err := insertVersion(ctx, tx, noteID, userID, content, header, 0, clientID, false)
	if err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notes SET current_version_id = ? WHERE id = ?`, versionID, noteID); err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO folders (user_id, path, created_at, updated_at) VALUES (?, ?, ?, ?)`, userID, folder, ts, ts); err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Note{}, NoteVersion{}, false, err
	}
	note, version, err := s.GetNote(ctx, userID, noteID)
	return note, version, false, err
}

func (s *Store) CreateNoteWithContentAt(ctx context.Context, userID int64, title, folder, content string, timestamp time.Time) (Note, NoteVersion, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled"
	}
	folder = normalizeFolder(folder)
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	ts := timestamp.UTC().Unix()
	slug, err := s.uniqueSlug(ctx, "notes")
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO notes (owner_user_id, folder_path, title, slug, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, userID, folder, title, slug, ts, ts)
	if err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, err
	}
	noteID, _ := res.LastInsertId()
	versionID, err := insertVersionAt(ctx, tx, noteID, userID, content, "{}", 0, "import", false, ts)
	if err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notes SET current_version_id = ? WHERE id = ?`, versionID, noteID); err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO folders (user_id, path, created_at, updated_at) VALUES (?, ?, ?, ?)`, userID, folder, ts, ts); err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return Note{}, NoteVersion{}, err
	}
	return s.GetNote(ctx, userID, noteID)
}

func (s *Store) ReplaceImportedNoteContentAt(ctx context.Context, userID, noteID int64, content string, timestamp time.Time) (Note, NoteVersion, error) {
	return s.ReplaceImportedNoteContentAndHeaderAt(ctx, userID, noteID, content, "", timestamp)
}

func (s *Store) ReplaceImportedNoteContentAndHeaderAt(ctx context.Context, userID, noteID int64, content, header string, timestamp time.Time) (Note, NoteVersion, error) {
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	ts := timestamp.UTC().Unix()
	note, version, err := s.GetNote(ctx, userID, noteID)
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	if strings.TrimSpace(header) == "" {
		header = version.HeaderJSON
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	if err := updateVersion(ctx, tx, version.ID, content, header, "import"); err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE note_versions SET created_at = ? WHERE id = ?`, ts, version.ID); err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notes SET updated_at = ? WHERE id = ? AND owner_user_id = ?`, ts, note.ID, userID); err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return Note{}, NoteVersion{}, err
	}
	return s.GetNote(ctx, userID, noteID)
}

func insertVersion(ctx context.Context, tx *sql.Tx, noteID, userID int64, content, header string, baseVersionID int64, clientID string, conflicted bool) (int64, error) {
	return insertVersionAt(ctx, tx, noteID, userID, content, header, baseVersionID, clientID, conflicted, nowUnix())
}

func insertVersionAt(ctx context.Context, tx *sql.Tx, noteID, userID int64, content, header string, baseVersionID int64, clientID string, conflicted bool, ts int64) (int64, error) {
	sum := sha256.Sum256([]byte(content))
	res, err := tx.ExecContext(ctx, `INSERT INTO note_versions (note_id, user_id, content_blob, header_json, body_sha256, base_version_id, client_id, conflicted, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		noteID, userID, []byte(content), header, hex.EncodeToString(sum[:]), baseVersionID, clientID, boolInt(conflicted), ts)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func updateVersion(ctx context.Context, tx *sql.Tx, versionID int64, content, header, clientID string) error {
	sum := sha256.Sum256([]byte(content))
	_, err := tx.ExecContext(ctx, `UPDATE note_versions SET content_blob = ?, header_json = ?, body_sha256 = ?, client_id = ? WHERE id = ?`,
		[]byte(content), header, hex.EncodeToString(sum[:]), clientID, versionID)
	return err
}

func (s *Store) canAccessNote(ctx context.Context, userID, noteID int64) (Note, string, error) {
	note, err := scanNote(s.db.QueryRowContext(ctx, `SELECT n.id, n.owner_user_id,
		CASE WHEN n.owner_user_id = ? THEN n.folder_path ELSE COALESCE(NULLIF(us.folder_path, ''), n.folder_path) END,
		n.title, n.slug, n.current_version_id, n.is_encrypted,
		EXISTS(SELECT 1 FROM note_shares ns WHERE ns.note_id = n.id),
		COALESCE((SELECT permission FROM note_shares ns WHERE ns.note_id = n.id AND ns.shared_user_id = ?), ''),
		CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END,
		COALESCE(us.starred_at, 0),
		n.created_at, n.updated_at
		FROM notes n
		LEFT JOIN note_user_state us ON us.note_id = n.id AND us.user_id = ?
		WHERE n.id = ? AND (n.owner_user_id = ? OR EXISTS(SELECT 1 FROM note_shares ns WHERE ns.note_id = n.id AND ns.shared_user_id = ?))`, userID, userID, userID, userID, noteID, userID, userID))
	if err != nil {
		return Note{}, "", err
	}
	perm := "owner"
	if note.OwnerUserID != userID {
		perm = note.SharedPermission
	}
	return note, perm, nil
}

func (s *Store) GetNote(ctx context.Context, userID, noteID int64) (Note, NoteVersion, error) {
	note, _, err := s.canAccessNote(ctx, userID, noteID)
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	version, err := s.GetVersion(ctx, note.CurrentVersionID)
	return note, version, err
}

func (s *Store) GetNoteBySlug(ctx context.Context, userID int64, slug string) (Note, NoteVersion, error) {
	var noteID int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM notes WHERE slug = ?`, slug).Scan(&noteID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Note{}, NoteVersion{}, ErrNotFound
		}
		return Note{}, NoteVersion{}, err
	}
	return s.GetNote(ctx, userID, noteID)
}

func (s *Store) GetCurrentNoteByTitle(ctx context.Context, userID int64, title string) (Note, NoteVersion, error) {
	var noteID int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM notes WHERE owner_user_id = ? AND title = ? AND trashed_at = 0 ORDER BY updated_at DESC, id DESC LIMIT 1`, userID, title).Scan(&noteID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Note{}, NoteVersion{}, ErrNotFound
		}
		return Note{}, NoteVersion{}, err
	}
	return s.GetNote(ctx, userID, noteID)
}

func (s *Store) GetVersion(ctx context.Context, versionID int64) (NoteVersion, error) {
	return scanVersion(s.db.QueryRowContext(ctx, `SELECT id, note_id, user_id, content_blob, header_json, body_sha256, base_version_id, client_id, conflicted, created_at FROM note_versions WHERE id = ?`, versionID))
}

func scanNote(row scanner) (Note, error) {
	var n Note
	var encrypted, shared int
	var trashed, starred, created, updated int64
	err := row.Scan(&n.ID, &n.OwnerUserID, &n.FolderPath, &n.Title, &n.Slug, &n.CurrentVersionID, &encrypted, &shared, &n.SharedPermission, &trashed, &starred, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Note{}, ErrNotFound
		}
		return Note{}, err
	}
	n.IsEncrypted = encrypted != 0
	n.IsShared = shared != 0
	n.IsStarred = starred != 0
	n.TrashedAt = unixTime(trashed)
	n.CreatedAt = unixTime(created)
	n.UpdatedAt = unixTime(updated)
	return n, nil
}

func scanVersion(row scanner) (NoteVersion, error) {
	var v NoteVersion
	var content []byte
	var conflicted int
	var created int64
	err := row.Scan(&v.ID, &v.NoteID, &v.UserID, &content, &v.HeaderJSON, &v.BodySHA256, &v.BaseVersionID, &v.ClientID, &conflicted, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NoteVersion{}, ErrNotFound
		}
		return NoteVersion{}, err
	}
	v.Content = string(content)
	v.Conflicted = conflicted != 0
	v.CreatedAt = unixTime(created)
	return v, nil
}

func scanVersionWithUser(row scanner) (NoteVersion, error) {
	var v NoteVersion
	var content []byte
	var conflicted int
	var created int64
	err := row.Scan(&v.ID, &v.NoteID, &v.UserID, &content, &v.HeaderJSON, &v.BodySHA256, &v.BaseVersionID, &v.ClientID, &conflicted, &created, &v.UserEmail, &v.UserName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NoteVersion{}, ErrNotFound
		}
		return NoteVersion{}, err
	}
	v.Content = string(content)
	v.Conflicted = conflicted != 0
	v.CreatedAt = unixTime(created)
	return v, nil
}

func (s *Store) ListNotes(ctx context.Context, userID int64, folder string, limit, offset int) ([]Note, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	folder = normalizeFolder(folder)
	whereFolder := ""
	args := []any{userID, userID, userID, userID, userID, userID}
	if folder != "/" {
		whereFolder = " AND CASE WHEN n.owner_user_id = ? THEN n.folder_path ELSE COALESCE(NULLIF(us.folder_path, ''), n.folder_path) END = ?"
		args = append(args, userID)
		args = append(args, folder)
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT n.id, n.owner_user_id,
		CASE WHEN n.owner_user_id = ? THEN n.folder_path ELSE COALESCE(NULLIF(us.folder_path, ''), n.folder_path) END,
		n.title, n.slug, n.current_version_id, n.is_encrypted,
		EXISTS(SELECT 1 FROM note_shares sx WHERE sx.note_id = n.id),
		COALESCE(ns.permission, ''),
		CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END,
		COALESCE(us.starred_at, 0),
		n.created_at, n.updated_at
		FROM notes n
		LEFT JOIN note_shares ns ON ns.note_id = n.id AND ns.shared_user_id = ?
		LEFT JOIN note_user_state us ON us.note_id = n.id AND us.user_id = ?
		WHERE CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END = 0 AND (n.owner_user_id = ? OR ns.shared_user_id IS NOT NULL)`+whereFolder+`
		ORDER BY n.updated_at DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) ListNoteSummaries(ctx context.Context, userID int64, folder string, includeDescendants bool, limit, offset int) ([]NoteSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	folder = normalizeFolder(folder)
	sortMode := "newest"
	_ = s.db.QueryRowContext(ctx, `SELECT sort_mode FROM folders WHERE user_id = ? AND path = ?`, userID, folder).Scan(&sortMode)
	orderBy := "n.updated_at DESC, n.id DESC"
	switch sortMode {
	case "oldest":
		orderBy = "n.created_at ASC, n.id ASC"
	case "alphabetical":
		orderBy = "lower(n.title) ASC, n.id ASC"
	case "custom":
		orderBy = "COALESCE(m.position, 2147483647), n.updated_at DESC, n.id DESC"
	}
	whereFolder := ""
	args := []any{userID, userID, userID, userID, userID, folder, userID, userID}
	if folder != "/" {
		folderExpr := "CASE WHEN n.owner_user_id = ? THEN n.folder_path ELSE COALESCE(NULLIF(us.folder_path, ''), n.folder_path) END"
		if includeDescendants {
			whereFolder = " AND (" + folderExpr + " = ? OR " + folderExpr + " LIKE ?)"
			args = append(args, userID, folder, userID, folder+"/%")
		} else {
			whereFolder = " AND " + folderExpr + " = ?"
			args = append(args, userID, folder)
		}
	}
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT n.id, n.owner_user_id,
		CASE WHEN n.owner_user_id = ? THEN n.folder_path ELSE COALESCE(NULLIF(us.folder_path, ''), n.folder_path) END,
		n.title, n.slug, n.current_version_id, n.is_encrypted,
		EXISTS(SELECT 1 FROM note_shares sx WHERE sx.note_id = n.id),
		COALESCE(ns.permission, ''),
		CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END,
		COALESCE(us.starred_at, 0),
		n.created_at, n.updated_at, nv.header_json,
		substr(CAST(nv.content_blob AS TEXT), 1, 240)
		FROM notes n
		JOIN note_versions nv ON nv.id = n.current_version_id
		LEFT JOIN note_shares ns ON ns.note_id = n.id AND ns.shared_user_id = ?
		LEFT JOIN note_user_state us ON us.note_id = n.id AND us.user_id = ?
		LEFT JOIN moodboard_items m ON m.user_id = ? AND m.folder_path = ? AND m.note_id = n.id
		WHERE CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END = 0 AND (n.owner_user_id = ? OR ns.shared_user_id IS NOT NULL)`+whereFolder+`
		ORDER BY `+orderBy+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NoteSummary{}
	for rows.Next() {
		var n NoteSummary
		var encrypted, shared int
		var trashed, starred, created, updated int64
		if err := rows.Scan(&n.ID, &n.OwnerUserID, &n.FolderPath, &n.Title, &n.Slug, &n.CurrentVersionID, &encrypted, &shared, &n.SharedPermission, &trashed, &starred, &created, &updated, &n.HeaderJSON, &n.Preview); err != nil {
			return nil, err
		}
		n.IsEncrypted = encrypted != 0
		n.IsShared = shared != 0
		n.IsStarred = starred != 0
		n.TrashedAt = unixTime(trashed)
		n.CreatedAt = unixTime(created)
		n.UpdatedAt = unixTime(updated)
		if n.IsEncrypted {
			n.Preview = "Encrypted note"
		} else {
			n.Preview = previewText(n.Preview)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) ListCurrentNotes(ctx context.Context, userID int64) ([]CurrentNote, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT n.id, n.owner_user_id,
		CASE WHEN n.owner_user_id = ? THEN n.folder_path ELSE COALESCE(NULLIF(us.folder_path, ''), n.folder_path) END,
		n.title, n.slug, n.current_version_id, n.is_encrypted,
		EXISTS(SELECT 1 FROM note_shares sx WHERE sx.note_id = n.id),
		COALESCE(ns.permission, ''),
		CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END,
		COALESCE(us.starred_at, 0),
		n.created_at, n.updated_at,
		nv.id, nv.note_id, nv.user_id, nv.content_blob, nv.header_json, nv.body_sha256, nv.base_version_id, nv.client_id, nv.conflicted, nv.created_at
		FROM notes n
		JOIN note_versions nv ON nv.id = n.current_version_id
		LEFT JOIN note_shares ns ON ns.note_id = n.id AND ns.shared_user_id = ?
		LEFT JOIN note_user_state us ON us.note_id = n.id AND us.user_id = ?
		WHERE CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END = 0 AND (n.owner_user_id = ? OR ns.shared_user_id IS NOT NULL)
		ORDER BY n.updated_at DESC`, userID, userID, userID, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CurrentNote{}
	for rows.Next() {
		var item CurrentNote
		var encrypted, shared, conflicted int
		var trashed, starred, noteCreated, noteUpdated, versionCreated int64
		var content []byte
		if err := rows.Scan(&item.Note.ID, &item.Note.OwnerUserID, &item.Note.FolderPath, &item.Note.Title, &item.Note.Slug, &item.Note.CurrentVersionID, &encrypted, &shared, &item.Note.SharedPermission, &trashed, &starred, &noteCreated, &noteUpdated,
			&item.Version.ID, &item.Version.NoteID, &item.Version.UserID, &content, &item.Version.HeaderJSON, &item.Version.BodySHA256, &item.Version.BaseVersionID, &item.Version.ClientID, &conflicted, &versionCreated); err != nil {
			return nil, err
		}
		item.Note.IsEncrypted = encrypted != 0
		item.Note.IsShared = shared != 0
		item.Note.IsStarred = starred != 0
		item.Note.TrashedAt = unixTime(trashed)
		item.Note.CreatedAt = unixTime(noteCreated)
		item.Note.UpdatedAt = unixTime(noteUpdated)
		item.Version.Content = string(content)
		item.Version.Conflicted = conflicted != 0
		item.Version.CreatedAt = unixTime(versionCreated)
		out = append(out, item)
	}
	return out, rows.Err()
}

func previewText(value string) string {
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Fields(strings.ReplaceAll(value, "\n", " "))
	out := strings.Join(lines, " ")
	if len(out) > 180 {
		return out[:180] + "..."
	}
	return out
}

func (s *Store) SaveNote(ctx context.Context, userID, noteID, baseVersionID int64, title, folder, content, header, clientID string, encrypted bool, forceVersion bool) (Note, NoteVersion, bool, error) {
	note, perm, err := s.canAccessNote(ctx, userID, noteID)
	if err != nil {
		return Note{}, NoteVersion{}, false, err
	}
	if perm != "owner" && perm != "write" {
		return Note{}, NoteVersion{}, false, errors.New("write access required")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled"
	}
	folder = normalizeFolder(folder)
	if strings.TrimSpace(header) == "" {
		header = "{}"
	}
	conflict := baseVersionID > 0 && note.CurrentVersionID != baseVersionID
	ts := nowUnix()
	var current NoteVersion
	if !conflict && !forceVersion && note.CurrentVersionID != 0 {
		current, err = s.GetVersion(ctx, note.CurrentVersionID)
		if err != nil {
			return Note{}, NoteVersion{}, false, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Note{}, NoteVersion{}, false, err
	}
	versionID := note.CurrentVersionID
	if !conflict && !forceVersion && note.CurrentVersionID != 0 {
		if time.Since(current.CreatedAt) < time.Hour && !current.Conflicted {
			if err := updateVersion(ctx, tx, current.ID, content, header, clientID); err != nil {
				_ = tx.Rollback()
				return Note{}, NoteVersion{}, false, err
			}
		} else {
			versionID, err = insertVersion(ctx, tx, noteID, userID, content, header, baseVersionID, clientID, false)
			if err != nil {
				_ = tx.Rollback()
				return Note{}, NoteVersion{}, false, err
			}
		}
	} else {
		versionID, err = insertVersion(ctx, tx, noteID, userID, content, header, baseVersionID, clientID, conflict)
		if err != nil {
			_ = tx.Rollback()
			return Note{}, NoteVersion{}, false, err
		}
	}
	if !conflict {
		if note.OwnerUserID == userID {
			if _, err := tx.ExecContext(ctx, `UPDATE notes SET folder_path = ?, title = ?, current_version_id = ?, is_encrypted = ?, updated_at = ? WHERE id = ?`, folder, title, versionID, boolInt(encrypted), ts, noteID); err != nil {
				_ = tx.Rollback()
				return Note{}, NoteVersion{}, false, err
			}
		} else if _, err := tx.ExecContext(ctx, `UPDATE notes SET title = ?, current_version_id = ?, is_encrypted = ?, updated_at = ? WHERE id = ?`, title, versionID, boolInt(encrypted), ts, noteID); err != nil {
			_ = tx.Rollback()
			return Note{}, NoteVersion{}, false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO folders (user_id, path, created_at, updated_at) VALUES (?, ?, ?, ?)`, userID, folder, ts, ts); err != nil {
			_ = tx.Rollback()
			return Note{}, NoteVersion{}, false, err
		}
		if note.OwnerUserID != userID {
			if _, err := tx.ExecContext(ctx, `INSERT INTO note_user_state (note_id, user_id, folder_path, trashed_at, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?)
				ON CONFLICT(note_id, user_id) DO UPDATE SET folder_path = excluded.folder_path, updated_at = excluded.updated_at`, noteID, userID, folder, ts, ts); err != nil {
				_ = tx.Rollback()
				return Note{}, NoteVersion{}, false, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return Note{}, NoteVersion{}, false, err
	}
	updated, current, err := s.GetNote(ctx, userID, noteID)
	if conflict {
		current, _ = s.GetVersion(ctx, versionID)
	}
	return updated, current, conflict, err
}

func (s *Store) MoveNote(ctx context.Context, userID, noteID int64, folder string) (Note, NoteVersion, error) {
	note, perm, err := s.canAccessNote(ctx, userID, noteID)
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	if perm != "owner" && perm != "write" {
		return Note{}, NoteVersion{}, errors.New("write access required")
	}
	folder = normalizeFolder(folder)
	ts := nowUnix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO folders (user_id, path, created_at, updated_at) VALUES (?, ?, ?, ?)`, userID, folder, ts, ts); err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, err
	}
	if note.OwnerUserID == userID {
		_, err = tx.ExecContext(ctx, `UPDATE notes SET folder_path = ?, updated_at = ? WHERE id = ?`, folder, ts, noteID)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO note_user_state (note_id, user_id, folder_path, trashed_at, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?)
			ON CONFLICT(note_id, user_id) DO UPDATE SET folder_path = excluded.folder_path, updated_at = excluded.updated_at`, noteID, userID, folder, ts, ts)
	}
	if err != nil {
		_ = tx.Rollback()
		return Note{}, NoteVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return Note{}, NoteVersion{}, err
	}
	return s.GetNote(ctx, userID, noteID)
}

func (s *Store) TrashNote(ctx context.Context, userID, noteID int64) (Note, NoteVersion, error) {
	note, perm, err := s.canAccessNote(ctx, userID, noteID)
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	ts := nowUnix()
	if perm == "owner" && note.OwnerUserID == userID {
		_, err = s.db.ExecContext(ctx, `UPDATE notes SET trashed_at = ?, updated_at = ? WHERE id = ?`, ts, ts, noteID)
	} else {
		_, err = s.db.ExecContext(ctx, `INSERT INTO note_user_state (note_id, user_id, folder_path, trashed_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(note_id, user_id) DO UPDATE SET trashed_at = excluded.trashed_at, updated_at = excluded.updated_at`, noteID, userID, note.FolderPath, ts, ts, ts)
	}
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	return s.GetNote(ctx, userID, noteID)
}

func (s *Store) RestoreNote(ctx context.Context, userID, noteID int64) (Note, NoteVersion, error) {
	note, perm, err := s.canAccessNote(ctx, userID, noteID)
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	ts := nowUnix()
	if perm == "owner" && note.OwnerUserID == userID {
		_, err = s.db.ExecContext(ctx, `UPDATE notes SET trashed_at = 0, updated_at = ? WHERE id = ?`, ts, noteID)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE note_user_state SET trashed_at = 0, updated_at = ? WHERE note_id = ? AND user_id = ?`, ts, noteID, userID)
	}
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	return s.GetNote(ctx, userID, noteID)
}

func (s *Store) SetNoteStarred(ctx context.Context, userID, noteID int64, starred bool) (Note, NoteVersion, error) {
	note, _, err := s.canAccessNote(ctx, userID, noteID)
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	ts := nowUnix()
	starredAt := int64(0)
	if starred {
		starredAt = ts
	}
	folder := note.FolderPath
	if folder == "" {
		folder = "/"
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO note_user_state (note_id, user_id, folder_path, trashed_at, starred_at, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?, ?)
		ON CONFLICT(note_id, user_id) DO UPDATE SET starred_at = excluded.starred_at, updated_at = excluded.updated_at`, noteID, userID, folder, starredAt, ts, ts); err != nil {
		return Note{}, NoteVersion{}, err
	}
	return s.GetNote(ctx, userID, noteID)
}

func (s *Store) WipeNote(ctx context.Context, userID, noteID int64) error {
	note, perm, err := s.canAccessNote(ctx, userID, noteID)
	if err != nil {
		return err
	}
	if note.TrashedAt.IsZero() {
		return errors.New("note must be in trash before wiping")
	}
	if perm == "owner" && note.OwnerUserID == userID {
		_, err = s.db.ExecContext(ctx, `DELETE FROM notes WHERE id = ? AND owner_user_id = ?`, noteID, userID)
	} else {
		_, err = s.db.ExecContext(ctx, `DELETE FROM note_user_state WHERE note_id = ? AND user_id = ?`, noteID, userID)
		if err == nil {
			_, err = s.db.ExecContext(ctx, `DELETE FROM note_shares WHERE note_id = ? AND shared_user_id = ?`, noteID, userID)
		}
	}
	return err
}

func (s *Store) ListTrashSummaries(ctx context.Context, userID int64, limit, offset int) ([]NoteSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT n.id, n.owner_user_id,
		CASE WHEN n.owner_user_id = ? THEN n.folder_path ELSE COALESCE(NULLIF(us.folder_path, ''), n.folder_path) END,
		n.title, n.slug, n.current_version_id, n.is_encrypted,
		EXISTS(SELECT 1 FROM note_shares sx WHERE sx.note_id = n.id),
		COALESCE(ns.permission, ''),
		CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END,
		COALESCE(us.starred_at, 0),
		n.created_at, n.updated_at, nv.header_json,
		substr(CAST(nv.content_blob AS TEXT), 1, 240)
		FROM notes n
		JOIN note_versions nv ON nv.id = n.current_version_id
		LEFT JOIN note_shares ns ON ns.note_id = n.id AND ns.shared_user_id = ?
		LEFT JOIN note_user_state us ON us.note_id = n.id AND us.user_id = ?
		WHERE CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END != 0
			AND (n.owner_user_id = ? OR ns.shared_user_id IS NOT NULL)
		ORDER BY CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END DESC LIMIT ? OFFSET ?`, userID, userID, userID, userID, userID, userID, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NoteSummary{}
	for rows.Next() {
		var n NoteSummary
		var encrypted, shared int
		var trashed, starred, created, updated int64
		if err := rows.Scan(&n.ID, &n.OwnerUserID, &n.FolderPath, &n.Title, &n.Slug, &n.CurrentVersionID, &encrypted, &shared, &n.SharedPermission, &trashed, &starred, &created, &updated, &n.HeaderJSON, &n.Preview); err != nil {
			return nil, err
		}
		n.IsEncrypted = encrypted != 0
		n.IsShared = shared != 0
		n.IsStarred = starred != 0
		n.TrashedAt = unixTime(trashed)
		n.CreatedAt = unixTime(created)
		n.UpdatedAt = unixTime(updated)
		if n.IsEncrypted {
			n.Preview = "Encrypted note"
		} else {
			n.Preview = previewText(n.Preview)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) ListStarredSummaries(ctx context.Context, userID int64, limit, offset int) ([]NoteSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT n.id, n.owner_user_id,
		CASE WHEN n.owner_user_id = ? THEN n.folder_path ELSE COALESCE(NULLIF(us.folder_path, ''), n.folder_path) END,
		n.title, n.slug, n.current_version_id, n.is_encrypted,
		EXISTS(SELECT 1 FROM note_shares sx WHERE sx.note_id = n.id),
		COALESCE(ns.permission, ''),
		CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END,
		COALESCE(us.starred_at, 0),
		n.created_at, n.updated_at, nv.header_json,
		substr(CAST(nv.content_blob AS TEXT), 1, 240)
		FROM notes n
		JOIN note_versions nv ON nv.id = n.current_version_id
		JOIN note_user_state us ON us.note_id = n.id AND us.user_id = ?
		LEFT JOIN note_shares ns ON ns.note_id = n.id AND ns.shared_user_id = ?
		WHERE us.starred_at != 0
			AND CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END = 0
			AND (n.owner_user_id = ? OR ns.shared_user_id IS NOT NULL)
		ORDER BY us.starred_at DESC LIMIT ? OFFSET ?`, userID, userID, userID, userID, userID, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NoteSummary{}
	for rows.Next() {
		var n NoteSummary
		var encrypted, shared int
		var trashed, starred, created, updated int64
		if err := rows.Scan(&n.ID, &n.OwnerUserID, &n.FolderPath, &n.Title, &n.Slug, &n.CurrentVersionID, &encrypted, &shared, &n.SharedPermission, &trashed, &starred, &created, &updated, &n.HeaderJSON, &n.Preview); err != nil {
			return nil, err
		}
		n.IsEncrypted = encrypted != 0
		n.IsShared = shared != 0
		n.IsStarred = starred != 0
		n.TrashedAt = unixTime(trashed)
		n.CreatedAt = unixTime(created)
		n.UpdatedAt = unixTime(updated)
		if n.IsEncrypted {
			n.Preview = "Encrypted note"
		} else {
			n.Preview = previewText(n.Preview)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) ListVersions(ctx context.Context, userID, noteID int64) ([]NoteVersion, error) {
	if _, _, err := s.canAccessNote(ctx, userID, noteID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT nv.id, nv.note_id, nv.user_id, nv.content_blob, nv.header_json, nv.body_sha256, nv.base_version_id, nv.client_id, nv.conflicted, nv.created_at, u.email, u.name
		FROM note_versions nv JOIN users u ON u.id = nv.user_id
		WHERE nv.note_id = ? ORDER BY nv.created_at DESC, nv.id DESC`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NoteVersion{}
	for rows.Next() {
		v, err := scanVersionWithUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) RestoreVersion(ctx context.Context, userID, noteID, versionID int64) (Note, NoteVersion, error) {
	_, perm, err := s.canAccessNote(ctx, userID, noteID)
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	if perm != "owner" && perm != "write" {
		return Note{}, NoteVersion{}, errors.New("write access required")
	}
	v, err := s.GetVersion(ctx, versionID)
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	if v.NoteID != noteID {
		return Note{}, NoteVersion{}, ErrNotFound
	}
	ts := nowUnix()
	if _, err := s.db.ExecContext(ctx, `UPDATE notes SET current_version_id = ?, updated_at = ? WHERE id = ?`, versionID, ts, noteID); err != nil {
		return Note{}, NoteVersion{}, err
	}
	restoredNote, _, err := s.canAccessNote(ctx, userID, noteID)
	if err != nil {
		return Note{}, NoteVersion{}, err
	}
	restoredVersion, err := s.GetVersion(ctx, versionID)
	return restoredNote, restoredVersion, err
}

func (s *Store) UpsertShare(ctx context.Context, ownerID, noteID int64, email, permission string) error {
	note, perm, err := s.canAccessNote(ctx, ownerID, noteID)
	if err != nil {
		return err
	}
	if perm != "owner" || note.OwnerUserID != ownerID {
		return errors.New("owner access required")
	}
	if note.IsEncrypted {
		return errors.New("encrypted notes cannot be shared")
	}
	if permission != "read" && permission != "write" {
		return errors.New("permission must be read or write")
	}
	u, err := s.GetUserByEmail(ctx, email)
	if err != nil {
		return err
	}
	if u.ID == ownerID {
		return errors.New("cannot share with yourself")
	}
	ts := nowUnix()
	_, err = s.db.ExecContext(ctx, `INSERT INTO note_shares (note_id, owner_user_id, shared_user_id, permission, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(note_id, shared_user_id) DO UPDATE SET permission = excluded.permission, updated_at = excluded.updated_at`, noteID, ownerID, u.ID, permission, ts, ts)
	return err
}

func (s *Store) DeleteShare(ctx context.Context, ownerID, noteID, sharedUserID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM note_shares WHERE owner_user_id = ? AND note_id = ? AND shared_user_id = ?`, ownerID, noteID, sharedUserID)
	return err
}

func (s *Store) ListShares(ctx context.Context, ownerID, noteID int64) ([]Share, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ns.note_id, ns.owner_user_id, ns.shared_user_id, ns.permission, u.email, u.name FROM note_shares ns JOIN users u ON u.id = ns.shared_user_id WHERE ns.owner_user_id = ? AND ns.note_id = ? ORDER BY u.email`, ownerID, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		var sh Share
		if err := rows.Scan(&sh.NoteID, &sh.OwnerUserID, &sh.SharedUserID, &sh.Permission, &sh.Email, &sh.Name); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

func (s *Store) CreateAsset(ctx context.Context, a Asset) (Asset, error) {
	ts := nowUnix()
	slug, err := s.uniqueSlug(ctx, "assets")
	if err != nil {
		return Asset{}, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO assets (slug, user_id, note_id, version_id, filename, content_type, blob_path, sha256, size, encrypted, search_text, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		slug, a.UserID, a.NoteID, a.VersionID, a.Filename, a.ContentType, a.BlobPath, a.SHA256, a.Size, boolInt(a.Encrypted), strings.TrimSpace(a.SearchText), ts)
	if err != nil {
		return Asset{}, err
	}
	a.ID, _ = res.LastInsertId()
	a.Slug = slug
	a.CreatedAt = unixTime(ts)
	return a, nil
}

func (s *Store) GetAsset(ctx context.Context, userID, assetID int64) (Asset, error) {
	var a Asset
	var created int64
	var encrypted int
	err := s.db.QueryRowContext(ctx, `SELECT id, slug, user_id, note_id, version_id, filename, content_type, blob_path, sha256, size, encrypted, search_text, created_at FROM assets WHERE id = ? AND user_id = ?`, assetID, userID).
		Scan(&a.ID, &a.Slug, &a.UserID, &a.NoteID, &a.VersionID, &a.Filename, &a.ContentType, &a.BlobPath, &a.SHA256, &a.Size, &encrypted, &a.SearchText, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Asset{}, ErrNotFound
		}
		return Asset{}, err
	}
	a.Encrypted = encrypted != 0
	a.CreatedAt = unixTime(created)
	return a, nil
}

func (s *Store) GetAssetBySlug(ctx context.Context, userID int64, slug string) (Asset, error) {
	var a Asset
	var created int64
	var encrypted int
	err := s.db.QueryRowContext(ctx, `SELECT id, slug, user_id, note_id, version_id, filename, content_type, blob_path, sha256, size, encrypted, search_text, created_at FROM assets WHERE slug = ? AND user_id = ?`, slug, userID).
		Scan(&a.ID, &a.Slug, &a.UserID, &a.NoteID, &a.VersionID, &a.Filename, &a.ContentType, &a.BlobPath, &a.SHA256, &a.Size, &encrypted, &a.SearchText, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Asset{}, ErrNotFound
		}
		return Asset{}, err
	}
	a.Encrypted = encrypted != 0
	a.CreatedAt = unixTime(created)
	return a, nil
}

func (s *Store) AssetSearchTextForNote(ctx context.Context, userID, noteID int64) (string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT search_text FROM assets WHERE user_id = ? AND note_id = ? AND encrypted = 0 AND search_text != '' ORDER BY created_at, id`, userID, noteID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return "", err
		}
		text = strings.TrimSpace(text)
		if text != "" {
			parts = append(parts, text)
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return strings.Join(parts, " "), nil
}

func (s *Store) ListAssetsForNote(ctx context.Context, userID, noteID int64) ([]Asset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, slug, user_id, note_id, version_id, filename, content_type, blob_path, sha256, size, encrypted, search_text, created_at FROM assets WHERE user_id = ? AND note_id = ? ORDER BY created_at, id`, userID, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Asset{}
	for rows.Next() {
		var a Asset
		var encrypted int
		var created int64
		if err := rows.Scan(&a.ID, &a.Slug, &a.UserID, &a.NoteID, &a.VersionID, &a.Filename, &a.ContentType, &a.BlobPath, &a.SHA256, &a.Size, &encrypted, &a.SearchText, &created); err != nil {
			return nil, err
		}
		a.Encrypted = encrypted != 0
		a.CreatedAt = unixTime(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ListMoodboardItems(ctx context.Context, userID int64, folder string, includeDescendants bool) ([]MoodboardItem, error) {
	folder = normalizeFolder(folder)
	sortMode := "newest"
	_ = s.db.QueryRowContext(ctx, `SELECT sort_mode FROM folders WHERE user_id = ? AND path = ?`, userID, folder).Scan(&sortMode)
	orderBy := "n.updated_at DESC, n.id DESC"
	switch sortMode {
	case "oldest":
		orderBy = "n.created_at ASC, n.id ASC"
	case "alphabetical":
		orderBy = "lower(n.title) ASC, n.id ASC"
	case "custom":
		orderBy = "COALESCE(m.position, 2147483647), n.updated_at DESC, n.id DESC"
	}
	folderExpr := "CASE WHEN n.owner_user_id = ? THEN n.folder_path ELSE COALESCE(NULLIF(us.folder_path, ''), n.folder_path) END"
	whereFolder := "AND " + folderExpr + " = ?"
	args := []any{userID, userID, userID, userID, userID, folder, userID, userID, userID, folder}
	if includeDescendants && folder != "/" {
		whereFolder = "AND (" + folderExpr + " = ? OR " + folderExpr + " LIKE ?)"
		args = []any{userID, userID, userID, userID, userID, folder, userID, userID, userID, folder, userID, folder + "/%"}
	} else if includeDescendants && folder == "/" {
		whereFolder = ""
		args = []any{userID, userID, userID, userID, userID, folder, userID, userID}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT n.id, n.owner_user_id,
		CASE WHEN n.owner_user_id = ? THEN n.folder_path ELSE COALESCE(NULLIF(us.folder_path, ''), n.folder_path) END,
		n.title, n.slug, n.current_version_id, n.is_encrypted,
		EXISTS(SELECT 1 FROM note_shares sx WHERE sx.note_id = n.id),
		COALESCE(ns.permission, ''),
		CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END,
		COALESCE(us.starred_at, 0),
		n.created_at, n.updated_at,
		nv.id, nv.note_id, nv.user_id, nv.content_blob, nv.header_json, nv.body_sha256, nv.base_version_id, nv.client_id, nv.conflicted, nv.created_at,
		a.id, a.slug, a.user_id, a.note_id, a.version_id, a.filename, a.content_type, a.blob_path, a.sha256, a.size, a.encrypted, a.search_text, a.created_at,
		pa.id, pa.slug, pa.user_id, pa.note_id, pa.version_id, pa.filename, pa.content_type, pa.blob_path, pa.sha256, pa.size, pa.encrypted, pa.search_text, pa.created_at,
		COALESCE(m.position, 2147483647)
		FROM notes n
		JOIN note_versions nv ON nv.id = n.current_version_id
		LEFT JOIN note_shares ns ON ns.note_id = n.id AND ns.shared_user_id = ?
		LEFT JOIN note_user_state us ON us.note_id = n.id AND us.user_id = ?
		LEFT JOIN moodboard_items m ON m.user_id = ? AND m.folder_path = ? AND m.note_id = n.id
		LEFT JOIN assets a ON a.id = (SELECT ax.id FROM assets ax WHERE ax.user_id = n.owner_user_id AND ax.note_id = n.id ORDER BY ax.created_at, ax.id LIMIT 1)
		LEFT JOIN assets pa ON pa.id = CAST(json_extract(nv.header_json, '$.preview_asset.id') AS INTEGER)
		WHERE CASE WHEN n.owner_user_id = ? THEN n.trashed_at ELSE COALESCE(us.trashed_at, 0) END = 0
		AND (n.owner_user_id = ? OR ns.shared_user_id IS NOT NULL)
		`+whereFolder+`
		ORDER BY `+orderBy, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MoodboardItem{}
	for rows.Next() {
		var item MoodboardItem
		var noteEncrypted, shared, starred, conflicted int
		var trashed, noteCreated, noteUpdated, versionCreated int64
		var content []byte
		var assetID, assetUserID, assetNoteID, assetVersionID, assetSize, assetCreated sql.NullInt64
		var assetSlug, assetFilename, assetContentType, assetBlobPath, assetSHA, assetSearchText sql.NullString
		var assetEncrypted sql.NullInt64
		var previewID, previewUserID, previewNoteID, previewVersionID, previewSize, previewCreated sql.NullInt64
		var previewSlug, previewFilename, previewContentType, previewBlobPath, previewSHA, previewSearchText sql.NullString
		var previewEncrypted sql.NullInt64
		if err := rows.Scan(&item.Note.ID, &item.Note.OwnerUserID, &item.Note.FolderPath, &item.Note.Title, &item.Note.Slug, &item.Note.CurrentVersionID, &noteEncrypted, &shared, &item.Note.SharedPermission, &trashed, &starred, &noteCreated, &noteUpdated,
			&item.Version.ID, &item.Version.NoteID, &item.Version.UserID, &content, &item.Version.HeaderJSON, &item.Version.BodySHA256, &item.Version.BaseVersionID, &item.Version.ClientID, &conflicted, &versionCreated,
			&assetID, &assetSlug, &assetUserID, &assetNoteID, &assetVersionID, &assetFilename, &assetContentType, &assetBlobPath, &assetSHA, &assetSize, &assetEncrypted, &assetSearchText, &assetCreated,
			&previewID, &previewSlug, &previewUserID, &previewNoteID, &previewVersionID, &previewFilename, &previewContentType, &previewBlobPath, &previewSHA, &previewSize, &previewEncrypted, &previewSearchText, &previewCreated,
			&item.Position); err != nil {
			return nil, err
		}
		item.Note.IsEncrypted = noteEncrypted != 0
		item.Note.IsShared = shared != 0
		item.Note.IsStarred = starred != 0
		item.Note.TrashedAt = unixTime(trashed)
		item.Note.CreatedAt = unixTime(noteCreated)
		item.Note.UpdatedAt = unixTime(noteUpdated)
		item.Version.Content = string(content)
		item.Version.Conflicted = conflicted != 0
		item.Version.CreatedAt = unixTime(versionCreated)
		if assetID.Valid {
			item.Asset = &Asset{
				ID: assetID.Int64, Slug: assetSlug.String, UserID: assetUserID.Int64, NoteID: assetNoteID.Int64, VersionID: assetVersionID.Int64,
				Filename: assetFilename.String, ContentType: assetContentType.String, BlobPath: assetBlobPath.String, SHA256: assetSHA.String,
				Size: assetSize.Int64, Encrypted: assetEncrypted.Int64 != 0, SearchText: assetSearchText.String, CreatedAt: unixTime(assetCreated.Int64),
			}
		}
		if previewID.Valid {
			item.PreviewAsset = &Asset{
				ID: previewID.Int64, Slug: previewSlug.String, UserID: previewUserID.Int64, NoteID: previewNoteID.Int64, VersionID: previewVersionID.Int64,
				Filename: previewFilename.String, ContentType: previewContentType.String, BlobPath: previewBlobPath.String, SHA256: previewSHA.String,
				Size: previewSize.Int64, Encrypted: previewEncrypted.Int64 != 0, SearchText: previewSearchText.String, CreatedAt: unixTime(previewCreated.Int64),
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SaveMoodboardOrder(ctx context.Context, userID int64, folder string, noteIDs []int64) error {
	folder = normalizeFolder(folder)
	ts := nowUnix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for position, noteID := range noteIDs {
		if noteID <= 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO moodboard_items (user_id, folder_path, note_id, position, updated_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(user_id, folder_path, note_id) DO UPDATE SET position = excluded.position, updated_at = excluded.updated_at`,
			userID, folder, noteID, position, ts); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertEncryptionKey(ctx context.Context, k EncryptionKey) (EncryptionKey, error) {
	if k.UserID == 0 || strings.TrimSpace(k.PublicKeyArmored) == "" {
		return EncryptionKey{}, errors.New("public key is required")
	}
	if k.Label == "" {
		k.Label = "OpenPGP key"
	}
	if k.StorageMode == "" {
		k.StorageMode = "download"
	}
	var existing int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM encryption_keys WHERE user_id = ?`, k.UserID).Scan(&existing); err != nil {
		return EncryptionKey{}, err
	}
	if existing == 0 {
		k.IsDefault = true
	}
	ts := nowUnix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EncryptionKey{}, err
	}
	defer tx.Rollback()
	if k.IsDefault {
		if _, err := tx.ExecContext(ctx, `UPDATE encryption_keys SET is_default = 0 WHERE user_id = ?`, k.UserID); err != nil {
			return EncryptionKey{}, err
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO encryption_keys (user_id, label, fingerprint, public_key_armored, encrypted_private_key, storage_mode, is_default, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		k.UserID, k.Label, k.Fingerprint, k.PublicKeyArmored, k.EncryptedPrivateKey, k.StorageMode, boolInt(k.IsDefault), ts)
	if err != nil {
		return EncryptionKey{}, err
	}
	if err := tx.Commit(); err != nil {
		return EncryptionKey{}, err
	}
	k.ID, _ = res.LastInsertId()
	k.CreatedAt = unixTime(ts)
	return k, nil
}

func (s *Store) ListEncryptionKeys(ctx context.Context, userID int64) ([]EncryptionKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, label, fingerprint, public_key_armored, encrypted_private_key, storage_mode, is_default, created_at FROM encryption_keys WHERE user_id = ? ORDER BY is_default DESC, created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EncryptionKey{}
	for rows.Next() {
		var k EncryptionKey
		var created int64
		if err := rows.Scan(&k.ID, &k.UserID, &k.Label, &k.Fingerprint, &k.PublicKeyArmored, &k.EncryptedPrivateKey, &k.StorageMode, &k.IsDefault, &created); err != nil {
			return nil, err
		}
		k.CreatedAt = unixTime(created)
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) SetDefaultEncryptionKey(ctx context.Context, userID, keyID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE encryption_keys SET is_default = CASE WHEN id = ? THEN 1 ELSE 0 END WHERE user_id = ?`, keyID, userID)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM encryption_keys WHERE id = ? AND user_id = ?`, keyID, userID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) SearchDocumentsForCurrentNotes(ctx context.Context, userID int64) ([]SearchDocument, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT n.id, n.owner_user_id, n.title, n.folder_path, nv.content_blob, nv.header_json, COALESCE(group_concat(a.search_text, ' '), ''), n.updated_at, n.is_encrypted,
		EXISTS(SELECT 1 FROM note_shares ns WHERE ns.note_id = n.id),
		EXISTS(SELECT 1 FROM assets a WHERE a.note_id = n.id)
		FROM notes n JOIN note_versions nv ON nv.id = n.current_version_id
		LEFT JOIN assets a ON a.note_id = n.id AND a.encrypted = 0 AND a.search_text != ''
		WHERE n.owner_user_id = ? AND n.is_encrypted = 0
		GROUP BY n.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchDocument{}
	for rows.Next() {
		var d SearchDocument
		var content []byte
		var updated int64
		var encrypted, shared, hasImage int
		if err := rows.Scan(&d.NoteID, &d.UserID, &d.Title, &d.FolderPath, &content, &d.HeaderJSON, &d.AssetText, &updated, &encrypted, &shared, &hasImage); err != nil {
			return nil, err
		}
		d.Content = string(content)
		d.UpdatedAt = unixTime(updated)
		d.Encrypted = encrypted != 0
		d.Shared = shared != 0
		d.HasImage = hasImage != 0
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) CreateBackupExport(ctx context.Context, userID int64, filename string, expiresAt time.Time) (BackupExport, error) {
	ts := nowUnix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO backup_exports (user_id, status, filename, created_at, expires_at) VALUES (?, 'running', ?, ?, ?)`, userID, strings.TrimSpace(filename), ts, expiresAt.UTC().Unix())
	if err != nil {
		return BackupExport{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return BackupExport{}, err
	}
	return s.GetBackupExport(ctx, userID, id)
}

func (s *Store) UserHasRunningBackupExport(ctx context.Context, userID int64) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_exports WHERE user_id = ? AND status = 'running'`, userID).Scan(&count)
	return count > 0, err
}

func (s *Store) CompleteBackupExport(ctx context.Context, userID, id int64, filePath string, size int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE backup_exports SET status = 'ready', file_path = ?, size = ?, error = '', completed_at = ? WHERE user_id = ? AND id = ?`,
		filePath, size, nowUnix(), userID, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) FailBackupExport(ctx context.Context, userID, id int64, message string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE backup_exports SET status = 'failed', error = ?, completed_at = ? WHERE user_id = ? AND id = ?`,
		strings.TrimSpace(message), nowUnix(), userID, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListBackupExports(ctx context.Context, userID int64) ([]BackupExport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, status, file_path, filename, size, error, created_at, completed_at, expires_at FROM backup_exports WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BackupExport{}
	for rows.Next() {
		item, err := scanBackupExport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetBackupExport(ctx context.Context, userID, id int64) (BackupExport, error) {
	return scanBackupExport(s.db.QueryRowContext(ctx, `SELECT id, user_id, status, file_path, filename, size, error, created_at, completed_at, expires_at FROM backup_exports WHERE user_id = ? AND id = ?`, userID, id))
}

func (s *Store) CleanupExpiredBackupExports(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT file_path FROM backup_exports WHERE expires_at <= ? AND file_path != ''`, now.UTC().Unix())
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if strings.TrimSpace(path) != "" {
			paths = append(paths, path)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM backup_exports WHERE expires_at <= ?`, now.UTC().Unix())
	return paths, err
}

func scanBackupExport(row scanner) (BackupExport, error) {
	var item BackupExport
	var created, completed, expires int64
	if err := row.Scan(&item.ID, &item.UserID, &item.Status, &item.FilePath, &item.Filename, &item.Size, &item.Error, &created, &completed, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BackupExport{}, ErrNotFound
		}
		return BackupExport{}, err
	}
	item.CreatedAt = unixTime(created)
	item.CompletedAt = unixTime(completed)
	item.ExpiresAt = unixTime(expires)
	return item, nil
}

func (s *Store) ListBackupNotes(ctx context.Context, userID int64) ([]BackupNote, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT n.id, n.owner_user_id, n.folder_path, n.title, n.slug, n.current_version_id, n.is_encrypted,
		EXISTS(SELECT 1 FROM note_shares sx WHERE sx.note_id = n.id),
		'', n.trashed_at, 0, n.created_at, n.updated_at,
		nv.id, nv.note_id, nv.user_id, nv.content_blob, nv.header_json, nv.body_sha256, nv.base_version_id, nv.client_id, nv.conflicted, nv.created_at
		FROM notes n
		JOIN note_versions nv ON nv.id = n.current_version_id
		WHERE n.owner_user_id = ?
		ORDER BY n.folder_path, n.title, n.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BackupNote{}
	for rows.Next() {
		var item BackupNote
		var encrypted, shared, starred, conflicted int
		var trashed, noteCreated, noteUpdated, versionCreated int64
		var content []byte
		if err := rows.Scan(&item.Note.ID, &item.Note.OwnerUserID, &item.Note.FolderPath, &item.Note.Title, &item.Note.Slug, &item.Note.CurrentVersionID, &encrypted,
			&shared, &item.Note.SharedPermission, &trashed, &starred, &noteCreated, &noteUpdated,
			&item.Version.ID, &item.Version.NoteID, &item.Version.UserID, &content, &item.Version.HeaderJSON, &item.Version.BodySHA256, &item.Version.BaseVersionID, &item.Version.ClientID, &conflicted, &versionCreated); err != nil {
			return nil, err
		}
		item.Note.IsEncrypted = encrypted != 0
		item.Note.IsShared = shared != 0
		item.Note.IsStarred = starred != 0
		item.Note.TrashedAt = unixTime(trashed)
		item.Note.CreatedAt = unixTime(noteCreated)
		item.Note.UpdatedAt = unixTime(noteUpdated)
		item.Version.Content = string(content)
		item.Version.Conflicted = conflicted != 0
		item.Version.CreatedAt = unixTime(versionCreated)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListBackupAssets(ctx context.Context, userID int64) ([]Asset, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, slug, user_id, note_id, version_id, filename, content_type, blob_path, sha256, size, encrypted, search_text, created_at FROM assets WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Asset{}
	for rows.Next() {
		var a Asset
		var created int64
		var encrypted int
		if err := rows.Scan(&a.ID, &a.Slug, &a.UserID, &a.NoteID, &a.VersionID, &a.Filename, &a.ContentType, &a.BlobPath, &a.SHA256, &a.Size, &encrypted, &a.SearchText, &created); err != nil {
			return nil, err
		}
		a.Encrypted = encrypted != 0
		a.CreatedAt = unixTime(created)
		out = append(out, a)
	}
	return out, rows.Err()
}
