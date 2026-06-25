package web

import (
	"archive/zip"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"cairnfield/backend/auth"
	"cairnfield/backend/blob"
	"cairnfield/backend/store"
)

func TestWriteBackupZipIncludesCurrentMarkdownAssetsAndManifest(t *testing.T) {
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
	note, _, err := db.CreateNoteWithContent(t.Context(), user.ID, "Project Plan", "/work", "# Plan\n\nCurrent body.")
	if err != nil {
		t.Fatal(err)
	}
	blobs := blob.New(t.TempDir())
	saved, err := blobs.SaveAsset(user.ID, "diagram.png", []byte("fake image bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAsset(t.Context(), store.Asset{
		UserID: user.ID, NoteID: note.ID, Filename: "diagram.png", ContentType: "image/png",
		BlobPath: saved.Path, SHA256: saved.SHA256, Size: saved.Size, Encrypted: true,
	}); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Store: db, Blobs: blobs})
	target := filepath.Join(t.TempDir(), "backup.zip")
	if err := srv.writeBackupZip(t.Context(), user.ID, target); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(target)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	files := map[string]string{}
	for _, file := range zr.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[file.Name] = string(raw)
	}
	var notePath, assetPath string
	for name, content := range files {
		if strings.HasPrefix(name, "notes/work/Project_Plan-") && strings.HasSuffix(name, ".md") {
			notePath = name
			if content != "# Plan\n\nCurrent body." {
				t.Fatalf("note content = %q", content)
			}
		}
		if strings.HasPrefix(name, "assets/") && strings.HasSuffix(name, "-diagram.png") {
			assetPath = name
			if content != "fake image bytes" {
				t.Fatalf("asset content = %q", content)
			}
		}
	}
	if notePath == "" {
		t.Fatalf("note markdown was not exported; files=%v", keys(files))
	}
	if assetPath == "" {
		t.Fatalf("asset was not exported; files=%v", keys(files))
	}
	var manifest backupManifest
	if err := json.Unmarshal([]byte(files["manifest.json"]), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Notes) != 1 || manifest.Notes[0].ZipPath != notePath {
		t.Fatalf("manifest notes = %+v", manifest.Notes)
	}
	if len(manifest.Assets) != 1 || manifest.Assets[0].ZipPath != assetPath {
		t.Fatalf("manifest assets = %+v", manifest.Assets)
	}
	if !manifest.Assets[0].Encrypted {
		t.Fatalf("manifest asset should be marked encrypted")
	}
}

func keys(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	return out
}
