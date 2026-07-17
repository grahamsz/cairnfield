package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"cairnfield/backend/search"
	"cairnfield/backend/store"
)

func TestTrashedNoteDisappearsFromSearchUntilUntrashed(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	searchService, err := search.OpenPerUser(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer searchService.Close()
	srv := New(Options{Store: db, Search: searchService})

	note, version, err := db.CreateNoteWithContent(t.Context(), user.ID, "Needle Note", "/", "# Needle Note\n\na unique needle body")
	if err != nil {
		t.Fatal(err)
	}
	srv.indexCurrent(t.Context(), note, version)

	assertHits := func(want int) {
		t.Helper()
		hits, err := searchService.Search(t.Context(), user.ID, "needle", 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != want {
			t.Fatalf("hits = %d, want %d", len(hits), want)
		}
	}
	noteReq := func(method, tail string) {
		t.Helper()
		req := httptest.NewRequest(method, "/api/notes/"+tail, nil)
		req = req.WithContext(context.WithValue(req.Context(), currentUserKey, currentUser{User: user}))
		res := httptest.NewRecorder()
		srv.apiNotePath(res, req, tail)
		if res.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d body=%s", method, tail, res.Code, res.Body.String())
		}
	}

	assertHits(1)
	noteReq(http.MethodPost, fmt.Sprintf("%d/trash", note.ID))
	assertHits(0)
	noteReq(http.MethodPost, fmt.Sprintf("%d/untrash", note.ID))
	assertHits(1)
	noteReq(http.MethodPost, fmt.Sprintf("%d/trash", note.ID))
	assertHits(0)
	noteReq(http.MethodDelete, fmt.Sprintf("%d/wipe", note.ID))
	assertHits(0)
	if _, _, err := db.GetNote(t.Context(), user.ID, note.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("wiped note should be gone, err = %v", err)
	}
}
