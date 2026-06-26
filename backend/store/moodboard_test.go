package store

import (
	"path/filepath"
	"testing"
)

func TestMoodboardFolderModeItemsAndOrder(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, err := db.CreateUser(t.Context(), "person@example.com", "Person", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := db.SetFolderDisplayMode(t.Context(), user.ID, "/ideas", "moodboard")
	if err != nil {
		t.Fatal(err)
	}
	if folder.DisplayMode != "moodboard" {
		t.Fatalf("display mode = %q", folder.DisplayMode)
	}
	folder, err = db.SetFolderSettings(t.Context(), user.ID, "/ideas", "moodboard", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if folder.SortMode != "custom" {
		t.Fatalf("sort mode = %q", folder.SortMode)
	}
	first, _, err := db.CreateNoteWithContent(t.Context(), user.ID, "First", "/ideas", "first body")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := db.CreateNoteWithContent(t.Context(), user.ID, "Second", "/ideas", "second body")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAsset(t.Context(), Asset{UserID: user.ID, NoteID: first.ID, Filename: "image.png", ContentType: "image/png", BlobPath: "users/1/blobs/image.png", SHA256: "abc", Size: 3}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveMoodboardOrder(t.Context(), user.ID, "/ideas", []int64{second.ID, first.ID}); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListMoodboardItems(t.Context(), user.ID, "/ideas", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].Note.ID != second.ID || items[1].Note.ID != first.ID {
		t.Fatalf("order = [%d, %d], want [%d, %d]", items[0].Note.ID, items[1].Note.ID, second.ID, first.ID)
	}
	if items[1].Asset == nil || items[1].Asset.Filename != "image.png" {
		t.Fatalf("asset = %+v", items[1].Asset)
	}
}

func TestGalleryCanIncludeDescendantsButMoodboardRequiresLeafFolder(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, err := db.CreateUser(t.Context(), "gallery@example.com", "Gallery", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	parent, _, err := db.CreateNoteWithContent(t.Context(), user.ID, "Parent", "/ideas", "parent body")
	if err != nil {
		t.Fatal(err)
	}
	child, _, err := db.CreateNoteWithContent(t.Context(), user.ID, "Child", "/ideas/child", "child body")
	if err != nil {
		t.Fatal(err)
	}
	exact, err := db.ListMoodboardItems(t.Context(), user.ID, "/ideas", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(exact) != 1 || exact[0].Note.ID != parent.ID {
		t.Fatalf("exact items = %+v, want only parent %d", exact, parent.ID)
	}
	descendants, err := db.ListMoodboardItems(t.Context(), user.ID, "/ideas", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(descendants) != 2 {
		t.Fatalf("descendant items = %d, want 2", len(descendants))
	}
	seen := map[int64]bool{}
	for _, item := range descendants {
		seen[item.Note.ID] = true
	}
	if !seen[parent.ID] || !seen[child.ID] {
		t.Fatalf("descendant IDs = %+v, want parent %d and child %d", seen, parent.ID, child.ID)
	}
	if _, err := db.SetFolderSettings(t.Context(), user.ID, "/ideas", "moodboard", "newest"); err == nil {
		t.Fatal("expected moodboard mode to reject folders with child folders")
	}
}
