package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"cairnfield/backend/store"
)

// WebSocket protocol (GET /ws, session cookie required).
//
// Client -> server:
//   {"type":"watch","note_id":123,"editing":false}
//   {"type":"unwatch","note_id":123}
//
// Server -> client:
//   {"type":"presence","note_id":123,"participants":[{"user_id":2,"name":"Bob","email":"b@x","same_user":false,"editing":true,"sessions":1}]}
//   {"type":"note_saved","note_id":123,"version_id":456,"title":"Note Title","by_user_id":2,"by_name":"Bob","by_email":"b@x","saved_at":1721320000}
//   {"type":"error","message":"..."}

type wsClientMessage struct {
	Type    string `json:"type"`
	NoteID  int64  `json:"note_id"`
	Editing bool   `json:"editing"`
}

type wsParticipant struct {
	UserID   int64  `json:"user_id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	SameUser bool   `json:"same_user"`
	Editing  bool   `json:"editing"`
	Sessions int    `json:"sessions"`
}

type wsPresenceMessage struct {
	Type         string          `json:"type"`
	NoteID       int64           `json:"note_id"`
	Participants []wsParticipant `json:"participants"`
}

type wsNoteSavedMessage struct {
	Type      string `json:"type"`
	NoteID    int64  `json:"note_id"`
	VersionID int64  `json:"version_id"`
	Title     string `json:"title"`
	ByUserID  int64  `json:"by_user_id"`
	ByName    string `json:"by_name"`
	ByEmail   string `json:"by_email"`
	SavedAt   int64  `json:"saved_at"`
}

type wsErrorMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type wsConn struct {
	ws      *websocket.Conn
	user    store.User
	send    chan []byte
	watches map[int64]bool // noteID -> editing
}

// enqueue queues a message for the connection; a connection that does not
// drain its buffer is dropped.
func (c *wsConn) enqueue(data []byte) {
	select {
	case c.send <- data:
	default:
		c.ws.Close(websocket.StatusGoingAway, "slow consumer")
	}
}

type wsHub struct {
	mu       sync.Mutex
	conns    map[*wsConn]struct{}
	watchers map[int64]map[*wsConn]struct{} // noteID -> watching connections
}

func newWSHub() *wsHub {
	return &wsHub{
		conns:    map[*wsConn]struct{}{},
		watchers: map[int64]map[*wsConn]struct{}{},
	}
}

func (h *wsHub) add(c *wsConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[c] = struct{}{}
}

// remove drops the connection and returns the notes it watched so callers can
// rebroadcast presence for them.
func (h *wsHub) remove(c *wsConn) []int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, c)
	noteIDs := make([]int64, 0, len(c.watches))
	for noteID := range c.watches {
		if set, ok := h.watchers[noteID]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(h.watchers, noteID)
			}
		}
		noteIDs = append(noteIDs, noteID)
	}
	c.watches = map[int64]bool{}
	return noteIDs
}

// setWatch records (or updates) a watch; it reports whether the watch state
// changed and presence should be rebroadcast.
func (h *wsHub) setWatch(c *wsConn, noteID int64, editing bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if current, ok := c.watches[noteID]; ok && current == editing {
		return false
	}
	c.watches[noteID] = editing
	set, ok := h.watchers[noteID]
	if !ok {
		set = map[*wsConn]struct{}{}
		h.watchers[noteID] = set
	}
	set[c] = struct{}{}
	return true
}

// clearWatch removes a watch; it reports whether the connection was watching.
func (h *wsHub) clearWatch(c *wsConn, noteID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := c.watches[noteID]; !ok {
		return false
	}
	delete(c.watches, noteID)
	if set, ok := h.watchers[noteID]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(h.watchers, noteID)
		}
	}
	return true
}

// broadcastPresence sends every watcher of the note a participant list that
// excludes the receiving connection itself. A user's other connections are
// aggregated into one entry with same_user set and a sessions count; editing
// is the OR across that user's connections.
func (h *wsHub) broadcastPresence(noteID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	watchers := h.watchers[noteID]
	for recipient := range watchers {
		byUser := map[int64]*wsParticipant{}
		userIDs := []int64{}
		for other := range watchers {
			if other == recipient {
				continue
			}
			p, ok := byUser[other.user.ID]
			if !ok {
				p = &wsParticipant{
					UserID:   other.user.ID,
					Name:     other.user.Name,
					Email:    other.user.Email,
					SameUser: other.user.ID == recipient.user.ID,
				}
				byUser[other.user.ID] = p
				userIDs = append(userIDs, other.user.ID)
			}
			p.Sessions++
			if other.watches[noteID] {
				p.Editing = true
			}
		}
		sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })
		participants := make([]wsParticipant, 0, len(userIDs))
		for _, id := range userIDs {
			participants = append(participants, *byUser[id])
		}
		data, err := json.Marshal(wsPresenceMessage{Type: "presence", NoteID: noteID, Participants: participants})
		if err != nil {
			continue
		}
		recipient.enqueue(data)
	}
}

// broadcastNoteSaved notifies all watchers of the note (including the saver's
// own connections) that a save succeeded.
func (h *wsHub) broadcastNoteSaved(noteID, versionID int64, title string, by store.User, savedAt time.Time) {
	data, err := json.Marshal(wsNoteSavedMessage{
		Type:      "note_saved",
		NoteID:    noteID,
		VersionID: versionID,
		Title:     title,
		ByUserID:  by.ID,
		ByName:    by.Name,
		ByEmail:   by.Email,
		SavedAt:   savedAt.Unix(),
	})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.watchers[noteID] {
		c.enqueue(data)
	}
}

func (h *wsHub) sendError(c *wsConn, message string) {
	data, err := json.Marshal(wsErrorMessage{Type: "error", Message: message})
	if err != nil {
		return
	}
	c.enqueue(data)
}

// noteSavedAt picks the save timestamp for a note_saved broadcast.
func noteSavedAt(version store.NoteVersion) time.Time {
	if !version.CreatedAt.IsZero() {
		return version.CreatedAt
	}
	return time.Now()
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	cu := current(r)
	if cu.User.ID == 0 {
		writeAPIError(w, http.StatusUnauthorized, "login required")
		return
	}
	origins := make([]string, 0, 2)
	if r.Host != "" {
		origins = append(origins, r.Host)
	}
	for _, host := range strings.Split(r.Header.Get("X-Forwarded-Host"), ",") {
		if host = strings.TrimSpace(host); host != "" {
			origins = append(origins, host)
		}
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: origins})
	if err != nil {
		return
	}
	conn.SetReadLimit(4096)

	c := &wsConn{ws: conn, user: cu.User, send: make(chan []byte, 16), watches: map[int64]bool{}}
	s.wsHub.add(c)
	defer func() {
		for _, noteID := range s.wsHub.remove(c) {
			s.wsHub.broadcastPresence(noteID)
		}
		conn.Close(websocket.StatusNormalClosure, "")
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Writer goroutine: drains the send queue onto the socket.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case data := <-c.send:
				wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Write(wctx, websocket.MessageText, data)
				wcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Keepalive: browsers answer pings automatically; a missing pong kills the connection.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pctx)
				pcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg wsClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "watch":
			if msg.NoteID <= 0 {
				continue
			}
			// Presence is read-only: anyone who can open the note may watch it.
			if _, _, err := s.store.GetNote(ctx, cu.User.ID, msg.NoteID); err != nil {
				s.wsHub.sendError(c, "note not accessible")
				continue
			}
			if s.wsHub.setWatch(c, msg.NoteID, msg.Editing) {
				s.wsHub.broadcastPresence(msg.NoteID)
			}
		case "unwatch":
			if s.wsHub.clearWatch(c, msg.NoteID) {
				s.wsHub.broadcastPresence(msg.NoteID)
			}
		}
	}
}
