package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cairnfield/backend/auth"
	"cairnfield/backend/blob"
	"cairnfield/backend/search"
	"cairnfield/backend/store"
)

func TestZipImportUsesFileTimestamps(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	hash, err := auth.HashPassword("password-password")
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(t.Context(), "person@example.com", "Person", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	modified := time.Date(2021, 4, 5, 6, 7, 8, 0, time.UTC)
	zipBytes := zipWithMarkdown(t, "archive/project.md", "# Project\n", modified)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "notes.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(zipBytes); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), currentUserKey, currentUser{User: user}))
	res := httptest.NewRecorder()
	searchService, err := search.OpenPerUser(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer searchService.Close()
	New(Options{Store: db, Search: searchService}).apiImport(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	notes, err := db.ListNotes(t.Context(), user.ID, "/archive", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(notes))
	}
	if !notes[0].CreatedAt.Equal(modified) || !notes[0].UpdatedAt.Equal(modified) {
		t.Fatalf("note timestamps = created %s updated %s, want %s", notes[0].CreatedAt, notes[0].UpdatedAt, modified)
	}
	_, version, err := db.GetNote(t.Context(), user.ID, notes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !version.CreatedAt.Equal(modified) {
		t.Fatalf("version timestamp = %s, want %s", version.CreatedAt, modified)
	}
}

func TestZipImportRewritesObsidianImageEmbeds(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	hash, err := auth.HashPassword("password-password")
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(t.Context(), "person@example.com", "Person", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	zipBytes := zipWithFiles(t, map[string]zipTestFile{
		"journal/day.md": {
			Content:  []byte("Dream text\n![[Pasted image 20230725174457.png]]\nMore text"),
			Modified: time.Date(2023, 7, 25, 17, 45, 0, 0, time.UTC),
		},
		"journal/Pasted image 20230725174457.png": {
			Content:  []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3},
			Modified: time.Date(2023, 7, 25, 17, 44, 57, 0, time.UTC),
		},
	})
	res := runImportRequest(t, db, user, blob.New(t.TempDir()), zipBytes)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	var response struct {
		Notes []store.Note `json:"notes"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Notes) != 1 {
		t.Fatalf("notes = %d, want 1", len(response.Notes))
	}
	_, version, err := db.GetNote(t.Context(), user.ID, response.Notes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(version.Content, "![Pasted image 20230725174457.png](/assets/") {
		t.Fatalf("embed was not rewritten: %s", version.Content)
	}
	if strings.Contains(version.Content, "![[Pasted image") {
		t.Fatalf("obsidian embed remained: %s", version.Content)
	}
	assets, err := db.ListBackupAssets(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 {
		t.Fatalf("assets = %d, want 1", len(assets))
	}
	if assets[0].NoteID != response.Notes[0].ID || assets[0].Filename != "Pasted image 20230725174457.png" {
		t.Fatalf("asset = %+v", assets[0])
	}
}

func TestImportDOCXCreatesSearchableDocumentNote(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	hash, err := auth.HashPassword("password-password")
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(t.Context(), "person@example.com", "Person", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	docx := zipWithFiles(t, map[string]zipTestFile{
		"word/document.xml": {
			Content:  []byte(`<w:document xmlns:w="urn:test"><w:body><w:p><w:r><w:t>Quarterly Forecast Needle</w:t></w:r></w:p></w:body></w:document>`),
			Modified: time.Now().UTC(),
		},
	})
	blobs := blob.New(t.TempDir())
	searchService, err := search.OpenPerUser(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer searchService.Close()
	res := runImportFileRequest(t, db, user, blobs, searchService, "forecast.docx", docx)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	hits, err := searchService.Search(t.Context(), user.ID, "Quarterly Forecast Needle", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	assets, err := db.ListBackupAssets(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || !strings.Contains(assets[0].SearchText, "Quarterly Forecast Needle") {
		t.Fatalf("assets = %+v", assets)
	}
	_, version, err := db.GetNote(t.Context(), user.ID, hits[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(version.HeaderJSON, `"kind":"document"`) || !strings.Contains(version.HeaderJSON, `"content_type"`) {
		t.Fatalf("header_json = %s", version.HeaderJSON)
	}
}

func zipWithMarkdown(t *testing.T, name, content string, modified time.Time) []byte {
	t.Helper()
	return zipWithFiles(t, map[string]zipTestFile{name: {Content: []byte(content), Modified: modified}})
}

type zipTestFile struct {
	Content  []byte
	Modified time.Time
}

func zipWithFiles(t *testing.T, files map[string]zipTestFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, file := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(file.Modified)
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(file.Content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func runImportRequest(t *testing.T, db *store.Store, user store.User, blobs *blob.Store, zipBytes []byte) *httptest.ResponseRecorder {
	t.Helper()
	searchService, err := search.OpenPerUser(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer searchService.Close()
	return runImportFileRequest(t, db, user, blobs, searchService, "notes.zip", zipBytes)
}

func runImportFileRequest(t *testing.T, db *store.Store, user store.User, blobs *blob.Store, searchService *search.Service, filename string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), currentUserKey, currentUser{User: user}))
	res := httptest.NewRecorder()
	New(Options{Store: db, Search: searchService, Blobs: blobs}).apiImport(res, req)
	return res
}
