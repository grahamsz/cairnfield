package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"cairnfield/backend/auth"
	"cairnfield/backend/blob"
	"cairnfield/backend/store"
)

func TestUnshareOwnerRecipientAndPermissionRejection(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	owner := testUser(t, db)
	recipient := createShareUser(t, db, "friend@example.com", "Friend")
	third := createShareUser(t, db, "third@example.com", "Third")
	stranger := createShareUser(t, db, "stranger@example.com", "Stranger")
	note, _, err := db.CreateNoteWithContent(t.Context(), owner.ID, "Shared Note", "/", "shared body")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertShare(t.Context(), owner.ID, note.ID, "friend@example.com", "read"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertShare(t.Context(), owner.ID, note.ID, "third@example.com", "read"); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Store: db})

	// A recipient may not remove someone else's share.
	res := deleteShareRequest(t, srv, recipient, fmt.Sprintf("%d/share/%d", note.ID, third.ID))
	if res.Code != http.StatusForbidden {
		t.Fatalf("recipient removing third status = %d body=%s", res.Code, res.Body.String())
	}

	// A user with no access to the note gets a 404.
	res = deleteShareRequest(t, srv, stranger, fmt.Sprintf("%d/share/%d", note.ID, stranger.ID))
	if res.Code != http.StatusNotFound {
		t.Fatalf("stranger status = %d body=%s", res.Code, res.Body.String())
	}

	// A recipient may remove their own share (leave the note).
	res = deleteShareRequest(t, srv, recipient, fmt.Sprintf("%d/share/%d", note.ID, recipient.ID))
	if res.Code != http.StatusOK {
		t.Fatalf("self-unshare status = %d body=%s", res.Code, res.Body.String())
	}
	if _, _, err := db.GetNote(t.Context(), recipient.ID, note.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("recipient should have lost access, err = %v", err)
	}

	// The owner may remove any share; the note key may be a slug.
	res = deleteShareRequest(t, srv, owner, note.Slug+"/share/"+strconv.FormatInt(third.ID, 10))
	if res.Code != http.StatusOK {
		t.Fatalf("owner unshare status = %d body=%s", res.Code, res.Body.String())
	}
	shares, err := db.ListShares(t.Context(), owner.ID, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 0 {
		t.Fatalf("shares = %+v", shares)
	}

	// Removing a share that no longer exists is a 404.
	res = deleteShareRequest(t, srv, owner, fmt.Sprintf("%d/share/%d", note.ID, third.ID))
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing share status = %d body=%s", res.Code, res.Body.String())
	}
}

func TestDeleteShareCleansRecipientState(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	owner := testUser(t, db)
	recipient := createShareUser(t, db, "friend@example.com", "Friend")
	note, _, err := db.CreateNoteWithContent(t.Context(), owner.ID, "Shared Note", "/", "shared body")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertShare(t.Context(), owner.ID, note.ID, "friend@example.com", "read"); err != nil {
		t.Fatal(err)
	}
	// The recipient builds up private overlay state by trashing their view.
	if _, _, err := db.TrashNote(t.Context(), recipient.ID, note.ID); err != nil {
		t.Fatal(err)
	}
	trash, err := db.ListTrashSummaries(t.Context(), recipient.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 1 {
		t.Fatalf("trash before unshare = %d, want 1", len(trash))
	}
	srv := New(Options{Store: db})
	res := deleteShareRequest(t, srv, owner, fmt.Sprintf("%d/share/%d", note.ID, recipient.ID))
	if res.Code != http.StatusOK {
		t.Fatalf("owner unshare status = %d body=%s", res.Code, res.Body.String())
	}
	// Re-sharing must not resurrect the stale trashed overlay.
	if err := db.UpsertShare(t.Context(), owner.ID, note.ID, "friend@example.com", "read"); err != nil {
		t.Fatal(err)
	}
	trash, err = db.ListTrashSummaries(t.Context(), recipient.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 0 {
		t.Fatalf("note_user_state was not cleaned; trash = %+v", trash)
	}
}

func TestShareRecipientCanFetchNoteAssets(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	owner := testUser(t, db)
	recipient := createShareUser(t, db, "friend@example.com", "Friend")
	stranger := createShareUser(t, db, "stranger@example.com", "Stranger")
	note, version, err := db.CreateNoteWithContent(t.Context(), owner.ID, "Illustrated", "/", "# Illustrated\n")
	if err != nil {
		t.Fatal(err)
	}
	blobs := blob.New(t.TempDir())
	saved, err := blobs.SaveAsset(owner.ID, "pic.png", []byte("fake png bytes"))
	if err != nil {
		t.Fatal(err)
	}
	asset, err := db.CreateAsset(t.Context(), store.Asset{
		UserID: owner.ID, NoteID: note.ID, VersionID: version.ID, Filename: "pic.png", ContentType: "image/png",
		BlobPath: saved.Path, SHA256: saved.SHA256, Size: saved.Size,
	})
	if err != nil {
		t.Fatal(err)
	}
	looseSaved, err := blobs.SaveAsset(owner.ID, "loose.png", []byte("unattached bytes"))
	if err != nil {
		t.Fatal(err)
	}
	loose, err := db.CreateAsset(t.Context(), store.Asset{
		UserID: owner.ID, Filename: "loose.png", ContentType: "image/png",
		BlobPath: looseSaved.Path, SHA256: looseSaved.SHA256, Size: looseSaved.Size,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertShare(t.Context(), owner.ID, note.ID, "friend@example.com", "read"); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Store: db, Blobs: blobs})

	getAsset := func(user store.User, slug, name string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/assets/"+slug+"/"+name, nil)
		req = req.WithContext(context.WithValue(req.Context(), currentUserKey, currentUser{User: user}))
		res := httptest.NewRecorder()
		srv.handleAsset(res, req)
		return res
	}
	if res := getAsset(owner, asset.Slug, "pic.png"); res.Code != http.StatusOK || res.Body.String() != "fake png bytes" {
		t.Fatalf("owner status = %d body=%s", res.Code, res.Body.String())
	}
	if res := getAsset(recipient, asset.Slug, "pic.png"); res.Code != http.StatusOK || res.Body.String() != "fake png bytes" {
		t.Fatalf("recipient status = %d body=%s", res.Code, res.Body.String())
	}
	if res := getAsset(stranger, asset.Slug, "pic.png"); res.Code != http.StatusNotFound {
		t.Fatalf("stranger status = %d body=%s", res.Code, res.Body.String())
	}
	if res := getAsset(recipient, loose.Slug, "loose.png"); res.Code != http.StatusNotFound {
		t.Fatalf("unattached asset status = %d body=%s", res.Code, res.Body.String())
	}
}

func createShareUser(t *testing.T, db *store.Store, email, name string) store.User {
	t.Helper()
	hash, err := auth.HashPassword("password-password")
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(t.Context(), email, name, hash, false)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func deleteShareRequest(t *testing.T, srv *Server, user store.User, tail string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/notes/"+tail, nil)
	req = req.WithContext(context.WithValue(req.Context(), currentUserKey, currentUser{User: user}))
	res := httptest.NewRecorder()
	srv.apiNotePath(res, req, tail)
	return res
}
