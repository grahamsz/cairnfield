package web

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cairnfield/backend/store"
)

const backupRetention = 7 * 24 * time.Hour

func (s *Server) apiBackups(w http.ResponseWriter, r *http.Request) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	s.cleanupExpiredBackups(r.Context())
	switch r.Method {
	case http.MethodGet:
		backups, err := s.store.ListBackupExports(r.Context(), cu.User.ID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, map[string]any{"backups": s.backupResponses(backups)})
	case http.MethodPost:
		running, err := s.store.UserHasRunningBackupExport(r.Context(), cu.User.ID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if running {
			writeAPIError(w, http.StatusConflict, "A backup is already running")
			return
		}
		now := time.Now().UTC()
		filename := "cairnfield-backup-" + now.Format("20060102-150405") + ".zip"
		backup, err := s.store.CreateBackupExport(r.Context(), cu.User.ID, filename, now.Add(backupRetention))
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		go s.runBackupExport(cu.User.ID, backup.ID, filename)
		writeJSON(w, map[string]any{"backup": s.backupResponse(backup)})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) apiBackupPath(w http.ResponseWriter, r *http.Request, raw string) {
	cu, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) != 2 || parts[1] != "download" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	s.cleanupExpiredBackups(r.Context())
	backup, err := s.store.GetBackupExport(r.Context(), cu.User.ID, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if backup.Status != "ready" || backup.FilePath == "" {
		writeAPIError(w, http.StatusConflict, "backup is not ready")
		return
	}
	if !backup.ExpiresAt.IsZero() && time.Now().UTC().After(backup.ExpiresAt) {
		writeAPIError(w, http.StatusGone, "backup has expired")
		return
	}
	abs, err := s.backupAbsPath(backup.FilePath)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "backup file not found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", backup.Filename))
	http.ServeContent(w, r, backup.Filename, backup.CompletedAt, f)
}

func (s *Server) runBackupExport(userID, backupID int64, filename string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	rel := filepath.Join("backups", strconv.FormatInt(userID, 10), fmt.Sprintf("%d-%s", backupID, filename))
	abs, err := s.backupAbsPath(rel)
	if err != nil {
		_ = s.store.FailBackupExport(ctx, userID, backupID, err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		_ = s.store.FailBackupExport(ctx, userID, backupID, err.Error())
		return
	}
	tmp := abs + ".tmp"
	if err := s.writeBackupZip(ctx, userID, tmp); err != nil {
		_ = os.Remove(tmp)
		_ = s.store.FailBackupExport(context.Background(), userID, backupID, err.Error())
		return
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		_ = s.store.FailBackupExport(context.Background(), userID, backupID, err.Error())
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		_ = s.store.FailBackupExport(context.Background(), userID, backupID, err.Error())
		return
	}
	_ = s.store.CompleteBackupExport(context.Background(), userID, backupID, rel, info.Size())
}

func (s *Server) writeBackupZip(ctx context.Context, userID int64, target string) error {
	notes, err := s.store.ListBackupNotes(ctx, userID)
	if err != nil {
		return err
	}
	assets, err := s.store.ListBackupAssets(ctx, userID)
	if err != nil {
		return err
	}
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	manifest := backupManifest{
		GeneratedAt: time.Now().UTC(),
		Notes:       make([]backupManifestNote, 0, len(notes)),
		Assets:      make([]backupManifestAsset, 0, len(assets)),
	}
	used := map[string]bool{}
	for _, item := range notes {
		if err := ctx.Err(); err != nil {
			_ = zw.Close()
			return err
		}
		name := backupNoteZipPath(item.Note)
		name = uniqueZipName(used, name)
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(item.Note.UpdatedAt)
		w, err := zw.CreateHeader(header)
		if err != nil {
			_ = zw.Close()
			return err
		}
		if _, err := io.WriteString(w, item.Version.Content); err != nil {
			_ = zw.Close()
			return err
		}
		manifest.Notes = append(manifest.Notes, backupManifestNote{
			ID: item.Note.ID, Slug: item.Note.Slug, Title: item.Note.Title, FolderPath: item.Note.FolderPath, ZipPath: name,
			IsEncrypted: item.Note.IsEncrypted, TrashedAt: item.Note.TrashedAt, UpdatedAt: item.Note.UpdatedAt,
		})
	}
	for _, asset := range assets {
		if err := ctx.Err(); err != nil {
			_ = zw.Close()
			return err
		}
		name := uniqueZipName(used, "assets/"+asset.Slug+"-"+safeBackupSegment(asset.Filename))
		if err := s.addAssetToBackupZip(zw, userID, asset, name); err != nil {
			_ = zw.Close()
			return err
		}
		manifest.Assets = append(manifest.Assets, backupManifestAsset{
			ID: asset.ID, Slug: asset.Slug, NoteID: asset.NoteID, Filename: asset.Filename, ContentType: asset.ContentType,
			ZipPath: name, SHA256: asset.SHA256, Size: asset.Size, Encrypted: asset.Encrypted, CreatedAt: asset.CreatedAt,
		})
	}
	rawManifest, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		_ = zw.Close()
		return err
	}
	mw, err := zw.Create("manifest.json")
	if err != nil {
		_ = zw.Close()
		return err
	}
	if _, err := mw.Write(rawManifest); err != nil {
		_ = zw.Close()
		return err
	}
	return zw.Close()
}

func (s *Server) addAssetToBackupZip(zw *zip.Writer, userID int64, asset store.Asset, name string) error {
	f, err := s.blobs.OpenUserBlob(userID, asset.BlobPath)
	if err != nil {
		return fmt.Errorf("open asset %s: %w", asset.Filename, err)
	}
	defer f.Close()
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetModTime(asset.CreatedAt)
	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

func (s *Server) cleanupExpiredBackups(ctx context.Context) {
	if s.store == nil {
		return
	}
	paths, err := s.store.CleanupExpiredBackupExports(ctx, time.Now().UTC())
	if err != nil {
		return
	}
	for _, rel := range paths {
		abs, err := s.backupAbsPath(rel)
		if err == nil {
			_ = os.Remove(abs)
		}
	}
}

func (s *Server) startBackupCleanup() {
	if s.store == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			s.cleanupExpiredBackups(ctx)
			cancel()
		}
	}()
}

func (s *Server) backupAbsPath(rel string) (string, error) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || clean == "." || strings.Contains(clean, "..") {
		return "", errors.New("unsafe backup path")
	}
	root := ""
	if s.blobs != nil {
		root = s.blobs.Root
	}
	if root == "" {
		root = "."
	}
	return filepath.Join(root, clean), nil
}

type backupResponseItem struct {
	ID          int64     `json:"id"`
	Status      string    `json:"status"`
	Filename    string    `json:"filename"`
	Size        int64     `json:"size"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
	DownloadURL string    `json:"download_url,omitempty"`
}

func (s *Server) backupResponses(items []store.BackupExport) []backupResponseItem {
	out := make([]backupResponseItem, 0, len(items))
	for _, item := range items {
		out = append(out, s.backupResponse(item))
	}
	return out
}

func (s *Server) backupResponse(item store.BackupExport) backupResponseItem {
	resp := backupResponseItem{
		ID: item.ID, Status: item.Status, Filename: item.Filename, Size: item.Size, Error: item.Error,
		CreatedAt: item.CreatedAt, CompletedAt: item.CompletedAt, ExpiresAt: item.ExpiresAt,
	}
	if item.Status == "ready" {
		resp.DownloadURL = s.appPath(fmt.Sprintf("/api/backups/%d/download", item.ID))
	}
	return resp
}

type backupManifest struct {
	GeneratedAt time.Time             `json:"generated_at"`
	Notes       []backupManifestNote  `json:"notes"`
	Assets      []backupManifestAsset `json:"assets"`
}

type backupManifestNote struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	FolderPath  string    `json:"folder_path"`
	ZipPath     string    `json:"zip_path"`
	IsEncrypted bool      `json:"is_encrypted"`
	TrashedAt   time.Time `json:"trashed_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type backupManifestAsset struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	NoteID      int64     `json:"note_id,omitempty"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	ZipPath     string    `json:"zip_path"`
	SHA256      string    `json:"sha256"`
	Size        int64     `json:"size"`
	Encrypted   bool      `json:"encrypted"`
	CreatedAt   time.Time `json:"created_at"`
}

func backupNoteZipPath(note store.Note) string {
	prefix := "notes"
	if !note.TrashedAt.IsZero() {
		prefix = "trash"
	}
	parts := []string{prefix}
	for _, segment := range strings.Split(strings.Trim(note.FolderPath, "/"), "/") {
		if segment != "" {
			parts = append(parts, safeBackupSegment(segment))
		}
	}
	title := safeBackupSegment(note.Title)
	parts = append(parts, title+"-"+note.Slug+".md")
	return path.Join(parts...)
}

func uniqueZipName(used map[string]bool, name string) string {
	name = strings.TrimLeft(path.Clean(name), "/")
	if name == "." || name == "" {
		name = "file"
	}
	if !used[name] {
		used[name] = true
		return name
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

var backupUnsafeSegment = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeBackupSegment(value string) string {
	value = strings.Trim(backupUnsafeSegment.ReplaceAllString(strings.TrimSpace(value), "_"), "._-")
	if value == "" {
		return "untitled"
	}
	if len(value) > 96 {
		value = value[:96]
	}
	return value
}
