package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cairnfield/backend/auth"
	"cairnfield/backend/blob"
	"cairnfield/backend/search"
	"cairnfield/backend/store"
)

func TestBackupZipRestore(t *testing.T) {
	// Export a backup for user A: one folder note with an image asset, one trashed note.
	dbA := testStore(t)
	defer dbA.Close()
	userA := testUser(t, dbA)
	blobsA := blob.New(t.TempDir())
	note, _, err := dbA.CreateNoteWithContent(t.Context(), userA.ID, "Release Notes", "/work/plans", "# Release Notes\n")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := blobsA.SaveAsset(userA.ID, "diagram.png", []byte("fake image bytes"))
	if err != nil {
		t.Fatal(err)
	}
	assetA, err := dbA.CreateAsset(t.Context(), store.Asset{
		UserID: userA.ID, NoteID: note.ID, Filename: "diagram.png", ContentType: "image/png",
		BlobPath: saved.Path, SHA256: saved.SHA256, Size: saved.Size,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := "# Release Notes\n\nrestore needle body\n\n![diagram](/assets/" + assetA.Slug + "/diagram.png)\n"
	note, _, err = dbA.ReplaceImportedNoteContentAt(t.Context(), userA.ID, note.ID, body, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	trashed, _, err := dbA.CreateNoteWithContent(t.Context(), userA.ID, "Old Draft", "/work", "trashed draft body")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := dbA.TrashNote(t.Context(), userA.ID, trashed.ID); err != nil {
		t.Fatal(err)
	}
	srvA := New(Options{Store: dbA, Blobs: blobsA})
	zipPath := filepath.Join(t.TempDir(), "backup.zip")
	if err := srvA.writeBackupZip(t.Context(), userA.ID, zipPath); err != nil {
		t.Fatal(err)
	}
	zipBytes, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	// Restore it into a fresh database as user B.
	dbB := testStore(t)
	defer dbB.Close()
	hash, err := auth.HashPassword("password-password")
	if err != nil {
		t.Fatal(err)
	}
	userB, err := dbB.CreateUser(t.Context(), "other@example.com", "Other", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	blobsB := blob.New(t.TempDir())
	searchB, err := search.OpenPerUser(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer searchB.Close()
	res := runImportFileRequest(t, dbB, userB, blobsB, searchB, "backup.zip", zipBytes)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	var out struct {
		Restored bool `json:"restored"`
		Notes    int  `json:"notes"`
		Assets   int  `json:"assets"`
		Folders  int  `json:"folders"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Restored || out.Notes != 2 || out.Assets != 1 || out.Folders != 2 {
		t.Fatalf("restore = %+v", out)
	}

	restored, _, err := dbB.GetNoteBySlug(t.Context(), userB.ID, note.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if restored.FolderPath != "/work/plans" || restored.Title != "Release Notes" || !restored.TrashedAt.IsZero() {
		t.Fatalf("restored note = %+v", restored)
	}
	_, rversion, err := dbB.GetNote(t.Context(), userB.ID, restored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rversion.Content != body {
		t.Fatalf("content = %q, want %q", rversion.Content, body)
	}

	restoredTrash, _, err := dbB.GetNoteBySlug(t.Context(), userB.ID, trashed.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if restoredTrash.TrashedAt.IsZero() {
		t.Fatalf("trashed note lost its trash state: %+v", restoredTrash)
	}

	// The asset is fetchable at its original /assets/<slug>/ URL.
	srvB := New(Options{Store: dbB, Blobs: blobsB, Search: searchB})
	assetReq := httptest.NewRequest(http.MethodGet, "/assets/"+assetA.Slug+"/diagram.png", nil)
	assetReq = assetReq.WithContext(context.WithValue(assetReq.Context(), currentUserKey, currentUser{User: userB}))
	assetRes := httptest.NewRecorder()
	srvB.handleAsset(assetRes, assetReq)
	if assetRes.Code != http.StatusOK || assetRes.Body.String() != "fake image bytes" {
		t.Fatalf("asset status = %d body=%s", assetRes.Code, assetRes.Body.String())
	}

	// Search finds the restored note but not the restored trashed note.
	hits, err := searchB.Search(t.Context(), userB.ID, "needle", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != restored.ID {
		t.Fatalf("hits = %+v", hits)
	}
	hits, err = searchB.Search(t.Context(), userB.ID, "draft", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("trashed note leaked into search: %+v", hits)
	}
}
