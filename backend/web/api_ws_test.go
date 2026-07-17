package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"cairnfield/backend/auth"
	"cairnfield/backend/store"
)

type wsTestParticipant struct {
	UserID   int64  `json:"user_id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	SameUser bool   `json:"same_user"`
	Editing  bool   `json:"editing"`
	Sessions int    `json:"sessions"`
}

type wsTestMessage struct {
	Type         string              `json:"type"`
	NoteID       int64               `json:"note_id"`
	Message      string              `json:"message"`
	Participants []wsTestParticipant `json:"participants"`
	VersionID    int64               `json:"version_id"`
	Title        string              `json:"title"`
	ByUserID     int64               `json:"by_user_id"`
	ByName       string              `json:"by_name"`
	ByEmail      string              `json:"by_email"`
	SavedAt      int64               `json:"saved_at"`
}

func wsTestServer(t *testing.T) (*store.Store, *Server, *httptest.Server) {
	t.Helper()
	db := testStore(t)
	srv := New(Options{Store: db})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		db.Close()
	})
	return db, srv, ts
}

func wsSessionToken(t *testing.T, db *store.Store, userID int64) string {
	t.Helper()
	raw, err := auth.NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(t.Context(), userID, store.TokenHash(raw), time.Hour); err != nil {
		t.Fatal(err)
	}
	return raw
}

func wsDial(t *testing.T, ts *httptest.Server, sessionToken string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("Cookie", sessionCookie+"="+sessionToken)
	header.Set("Origin", ts.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

func wsWatch(t *testing.T, conn *websocket.Conn, noteID int64, editing bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, conn, map[string]any{"type": "watch", "note_id": noteID, "editing": editing}); err != nil {
		t.Fatal(err)
	}
}

func wsRead(t *testing.T, conn *websocket.Conn) wsTestMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var msg wsTestMessage
	if err := wsjson.Read(ctx, conn, &msg); err != nil {
		t.Fatal(err)
	}
	return msg
}

func requirePresence(t *testing.T, msg wsTestMessage, noteID int64, want []wsTestParticipant) {
	t.Helper()
	if msg.Type != "presence" {
		t.Fatalf("type = %q, want presence (%+v)", msg.Type, msg)
	}
	if msg.NoteID != noteID {
		t.Fatalf("note_id = %d, want %d", msg.NoteID, noteID)
	}
	if len(msg.Participants) != len(want) {
		t.Fatalf("participants = %+v, want %+v", msg.Participants, want)
	}
	for i, p := range want {
		if msg.Participants[i] != p {
			t.Fatalf("participant[%d] = %+v, want %+v (all: %+v)", i, msg.Participants[i], p, msg.Participants)
		}
	}
}

func TestWSPresenceBetweenUsers(t *testing.T) {
	db, _, ts := wsTestServer(t)
	owner := testUser(t, db)
	friend := createShareUser(t, db, "friend@example.com", "Friend")
	note, _, err := db.CreateNoteWithContent(t.Context(), owner.ID, "Shared Note", "/", "shared body")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertShare(t.Context(), owner.ID, note.ID, "friend@example.com", "write"); err != nil {
		t.Fatal(err)
	}
	connA := wsDial(t, ts, wsSessionToken(t, db, owner.ID))
	connB := wsDial(t, ts, wsSessionToken(t, db, friend.ID))

	wsWatch(t, connA, note.ID, false)
	requirePresence(t, wsRead(t, connA), note.ID, nil)

	wsWatch(t, connB, note.ID, false)
	requirePresence(t, wsRead(t, connA), note.ID, []wsTestParticipant{
		{UserID: friend.ID, Name: "Friend", Email: "friend@example.com", SameUser: false, Editing: false, Sessions: 1},
	})
	requirePresence(t, wsRead(t, connB), note.ID, []wsTestParticipant{
		{UserID: owner.ID, Name: "Person", Email: "person@example.com", SameUser: false, Editing: false, Sessions: 1},
	})
}

func TestWSPresenceSameUserTwoConnections(t *testing.T) {
	db, _, ts := wsTestServer(t)
	owner := testUser(t, db)
	note, _, err := db.CreateNoteWithContent(t.Context(), owner.ID, "Mine", "/", "body")
	if err != nil {
		t.Fatal(err)
	}
	token := wsSessionToken(t, db, owner.ID)
	conn1 := wsDial(t, ts, token)
	conn2 := wsDial(t, ts, token)

	wsWatch(t, conn1, note.ID, false)
	requirePresence(t, wsRead(t, conn1), note.ID, nil)

	wsWatch(t, conn2, note.ID, true)
	requirePresence(t, wsRead(t, conn1), note.ID, []wsTestParticipant{
		{UserID: owner.ID, Name: "Person", Email: "person@example.com", SameUser: true, Editing: true, Sessions: 1},
	})
	requirePresence(t, wsRead(t, conn2), note.ID, []wsTestParticipant{
		{UserID: owner.ID, Name: "Person", Email: "person@example.com", SameUser: true, Editing: false, Sessions: 1},
	})
}

func TestWSPresenceEditingFlipAndUnwatch(t *testing.T) {
	db, _, ts := wsTestServer(t)
	owner := testUser(t, db)
	friend := createShareUser(t, db, "friend@example.com", "Friend")
	note, _, err := db.CreateNoteWithContent(t.Context(), owner.ID, "Shared Note", "/", "shared body")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertShare(t.Context(), owner.ID, note.ID, "friend@example.com", "write"); err != nil {
		t.Fatal(err)
	}
	connA := wsDial(t, ts, wsSessionToken(t, db, owner.ID))
	connB := wsDial(t, ts, wsSessionToken(t, db, friend.ID))
	wsWatch(t, connA, note.ID, false)
	requirePresence(t, wsRead(t, connA), note.ID, nil)
	wsWatch(t, connB, note.ID, false)
	requirePresence(t, wsRead(t, connA), note.ID, []wsTestParticipant{
		{UserID: friend.ID, Name: "Friend", Email: "friend@example.com", Sessions: 1},
	})
	requirePresence(t, wsRead(t, connB), note.ID, []wsTestParticipant{
		{UserID: owner.ID, Name: "Person", Email: "person@example.com", Sessions: 1},
	})

	// Flipping the editing flag rebroadcasts presence to every watcher.
	wsWatch(t, connA, note.ID, true)
	requirePresence(t, wsRead(t, connA), note.ID, []wsTestParticipant{
		{UserID: friend.ID, Name: "Friend", Email: "friend@example.com", Sessions: 1},
	})
	requirePresence(t, wsRead(t, connB), note.ID, []wsTestParticipant{
		{UserID: owner.ID, Name: "Person", Email: "person@example.com", Editing: true, Sessions: 1},
	})

	// Unwatch removes the connection from the note's presence.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, connA, map[string]any{"type": "unwatch", "note_id": note.ID}); err != nil {
		t.Fatal(err)
	}
	requirePresence(t, wsRead(t, connB), note.ID, nil)
}

func TestWSPresenceDisconnect(t *testing.T) {
	db, _, ts := wsTestServer(t)
	owner := testUser(t, db)
	friend := createShareUser(t, db, "friend@example.com", "Friend")
	note, _, err := db.CreateNoteWithContent(t.Context(), owner.ID, "Shared Note", "/", "shared body")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertShare(t.Context(), owner.ID, note.ID, "friend@example.com", "write"); err != nil {
		t.Fatal(err)
	}
	connA := wsDial(t, ts, wsSessionToken(t, db, owner.ID))
	connB := wsDial(t, ts, wsSessionToken(t, db, friend.ID))
	wsWatch(t, connA, note.ID, false)
	requirePresence(t, wsRead(t, connA), note.ID, nil)
	wsWatch(t, connB, note.ID, false)
	requirePresence(t, wsRead(t, connA), note.ID, []wsTestParticipant{
		{UserID: friend.ID, Name: "Friend", Email: "friend@example.com", Sessions: 1},
	})
	requirePresence(t, wsRead(t, connB), note.ID, []wsTestParticipant{
		{UserID: owner.ID, Name: "Person", Email: "person@example.com", Sessions: 1},
	})

	if err := connB.Close(websocket.StatusNormalClosure, ""); err != nil {
		t.Fatal(err)
	}
	requirePresence(t, wsRead(t, connA), note.ID, nil)
}

func TestWSWatchRequiresAccess(t *testing.T) {
	db, srv, ts := wsTestServer(t)
	owner := testUser(t, db)
	stranger := createShareUser(t, db, "stranger@example.com", "Stranger")
	note, _, err := db.CreateNoteWithContent(t.Context(), owner.ID, "Private", "/", "body")
	if err != nil {
		t.Fatal(err)
	}
	connOwner := wsDial(t, ts, wsSessionToken(t, db, owner.ID))
	connStranger := wsDial(t, ts, wsSessionToken(t, db, stranger.ID))

	wsWatch(t, connStranger, note.ID, false)
	msg := wsRead(t, connStranger)
	if msg.Type != "error" || msg.Message == "" {
		t.Fatalf("stranger expected error message, got %+v", msg)
	}

	srv.wsHub.mu.Lock()
	watcherCount := len(srv.wsHub.watchers[note.ID])
	srv.wsHub.mu.Unlock()
	if watcherCount != 0 {
		t.Fatalf("hub recorded %d watchers for inaccessible note, want 0", watcherCount)
	}

	// The owner's presence must not list the stranger.
	wsWatch(t, connOwner, note.ID, false)
	requirePresence(t, wsRead(t, connOwner), note.ID, nil)
}

func TestWSNoteSavedBroadcast(t *testing.T) {
	db, srv, ts := wsTestServer(t)
	owner := testUser(t, db)
	friend := createShareUser(t, db, "friend@example.com", "Friend")
	note, version, err := db.CreateNoteWithContent(t.Context(), owner.ID, "Shared Note", "/", "shared body")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertShare(t.Context(), owner.ID, note.ID, "friend@example.com", "write"); err != nil {
		t.Fatal(err)
	}
	connA := wsDial(t, ts, wsSessionToken(t, db, owner.ID))
	connB := wsDial(t, ts, wsSessionToken(t, db, friend.ID))
	wsWatch(t, connA, note.ID, true)
	requirePresence(t, wsRead(t, connA), note.ID, nil)
	wsWatch(t, connB, note.ID, false)
	requirePresence(t, wsRead(t, connA), note.ID, []wsTestParticipant{
		{UserID: friend.ID, Name: "Friend", Email: "friend@example.com", Sessions: 1},
	})
	requirePresence(t, wsRead(t, connB), note.ID, []wsTestParticipant{
		{UserID: owner.ID, Name: "Person", Email: "person@example.com", Editing: true, Sessions: 1},
	})

	// The friend saves the note through the HTTP API.
	body := `{"title":"Updated Title","folder_path":"/","content":"new body","base_version_id":` + strconv.FormatInt(version.ID, 10) + `,"autosave":false}`
	req := httptest.NewRequest(http.MethodPut, "/api/notes/"+strconv.FormatInt(note.ID, 10), strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), currentUserKey, currentUser{User: friend}))
	res := httptest.NewRecorder()
	srv.apiNotePath(res, req, strconv.FormatInt(note.ID, 10))
	if res.Code != http.StatusOK {
		t.Fatalf("save status = %d body=%s", res.Code, res.Body.String())
	}
	var saved struct {
		Version store.NoteVersion `json:"version"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Version.ID == 0 || saved.Version.ID == version.ID {
		t.Fatalf("expected a new version id, got %d (previous %d)", saved.Version.ID, version.ID)
	}

	for _, conn := range []*websocket.Conn{connA, connB} {
		msg := wsRead(t, conn)
		if msg.Type != "note_saved" {
			t.Fatalf("type = %q, want note_saved (%+v)", msg.Type, msg)
		}
		if msg.NoteID != note.ID || msg.VersionID != saved.Version.ID {
			t.Fatalf("note_id/version_id = %d/%d, want %d/%d", msg.NoteID, msg.VersionID, note.ID, saved.Version.ID)
		}
		if msg.Title != "Updated Title" {
			t.Fatalf("title = %q", msg.Title)
		}
		if msg.ByUserID != friend.ID || msg.ByName != "Friend" || msg.ByEmail != "friend@example.com" {
			t.Fatalf("by fields = %d/%q/%q", msg.ByUserID, msg.ByName, msg.ByEmail)
		}
		if msg.SavedAt <= 0 {
			t.Fatalf("saved_at = %d", msg.SavedAt)
		}
	}
}

func TestWSRequiresAuth(t *testing.T) {
	_, _, ts := wsTestServer(t)
	res, err := http.Get(ts.URL + "/ws")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
}

func TestWSRejectsMismatchedOrigin(t *testing.T) {
	db, _, ts := wsTestServer(t)
	owner := testUser(t, db)
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("Cookie", sessionCookie+"="+wsSessionToken(t, db, owner.ID))
	header.Set("Origin", "https://evil.example.com")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, res, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("dial with mismatched origin succeeded")
	}
	if res == nil || res.StatusCode != http.StatusForbidden {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		t.Fatalf("status = %d, want 403 (err = %v)", status, err)
	}
}
