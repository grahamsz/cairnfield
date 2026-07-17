package web

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cairnfield/backend/blob"
	"cairnfield/backend/search"
	"cairnfield/backend/store"
)

const clipURLTestPage = `<!doctype html><html><head><title>Needle Page Title</title></head><body><h1>Heading</h1><p>unique clipurl needle text</p></body></html>`

// allowClipURLHosts installs the test hook that lets the SSRF guard dial the
// given host:port addresses (httptest servers listen on loopback).
func allowClipURLHosts(t *testing.T, addrs ...string) {
	t.Helper()
	allowed := map[string]bool{}
	for _, addr := range addrs {
		allowed[addr] = true
	}
	prev := clipURLAllowPrivateHook
	clipURLAllowPrivateHook = func(addr string) bool { return allowed[addr] }
	t.Cleanup(func() { clipURLAllowPrivateHook = prev })
}

func serveClipURLPage(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func postClipURL(t *testing.T, srv *Server, rawToken, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/clip/url", strings.NewReader(body))
	if rawToken != "" {
		req.Header.Set("Authorization", "Bearer "+rawToken)
	}
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	return res
}

func TestClipURLCreatesWebpageNote(t *testing.T) {
	page := serveClipURLPage(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(clipURLTestPage))
	})
	allowClipURLHosts(t, strings.TrimPrefix(page.URL, "http://"))

	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	raw := createTestAPIToken(t, db, user.ID)
	searchService, err := search.OpenPerUser(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer searchService.Close()
	srv := New(Options{Store: db, Blobs: blob.New(t.TempDir()), Search: searchService})

	res := postClipURL(t, srv, raw, fmt.Sprintf(`{"url":%q,"folder_path":"/clips"}`, page.URL+"/page"))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	var out struct {
		Note    store.Note        `json:"note"`
		Version store.NoteVersion `json:"version"`
		Asset   store.Asset       `json:"asset"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Note.Title != "Needle Page Title" {
		t.Fatalf("title = %q", out.Note.Title)
	}
	if out.Note.FolderPath != "/clips" {
		t.Fatalf("folder = %q", out.Note.FolderPath)
	}
	if !strings.Contains(out.Version.HeaderJSON, `"kind":"webpage"`) || !strings.Contains(out.Version.HeaderJSON, `"type":"html"`) {
		t.Fatalf("header_json = %s", out.Version.HeaderJSON)
	}
	if !strings.Contains(out.Version.HeaderJSON, `"page_url":"`+page.URL+`/page"`) || !strings.Contains(out.Version.HeaderJSON, `"source_url":"`+page.URL+`/page"`) {
		t.Fatalf("header_json = %s", out.Version.HeaderJSON)
	}
	if !strings.Contains(out.Version.Content, page.URL+"/page") {
		t.Fatalf("note content = %s", out.Version.Content)
	}
	if out.Asset.ContentType != "text/html; charset=utf-8" || out.Asset.Filename != "clip.html" {
		t.Fatalf("asset = %+v", out.Asset)
	}
	assets, err := db.ListAssetsForNote(t.Context(), user.ID, out.Note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || !strings.Contains(assets[0].SearchText, "unique clipurl needle text") {
		t.Fatalf("assets = %+v", assets)
	}
	hits, err := searchService.Search(t.Context(), user.ID, "clipurl needle", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	folders, err := db.ListFolders(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, folder := range folders {
		if folder.Path == "/clips" {
			found = true
		}
	}
	if !found {
		t.Fatalf("folders = %+v", folders)
	}
}

func TestClipURLAcceptsSessionAuth(t *testing.T) {
	page := serveClipURLPage(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(clipURLTestPage))
	})
	allowClipURLHosts(t, strings.TrimPrefix(page.URL, "http://"))

	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	if err := db.CreateSession(t.Context(), user.ID, store.TokenHash("clipurl-session"), time.Hour); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Store: db, Blobs: blob.New(t.TempDir())})

	req := httptest.NewRequest(http.MethodPost, "/api/clip/url", strings.NewReader(fmt.Sprintf(`{"url":%q,"title":"Session Override"}`, page.URL)))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "clipurl-session"})
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	var out struct {
		Note store.Note `json:"note"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Note.Title != "Session Override" {
		t.Fatalf("title = %q", out.Note.Title)
	}
}

func TestClipURLRejectsBadRequests(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	raw := createTestAPIToken(t, db, user.ID)
	srv := New(Options{Store: db, Blobs: blob.New(t.TempDir())})

	blocked := map[string]string{
		"missing url":            `{}`,
		"loopback ipv4":          `{"url":"http://127.0.0.1/"}`,
		"cloud metadata":         `{"url":"http://169.254.169.254/"}`,
		"loopback ipv6":          `{"url":"http://[::1]/"}`,
		"private resolving host": `{"url":"http://localhost/"}`,
		"non-http scheme":        `{"url":"ftp://example.com/file"}`,
		"userinfo":               `{"url":"http://user:pass@example.com/"}`,
		"fragment":               `{"url":"http://example.com/#frag"}`,
	}
	for name, body := range blocked {
		res := postClipURL(t, srv, raw, body)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d body=%s", name, res.Code, res.Body.String())
		}
	}

	unauthenticated := postClipURL(t, srv, "", `{"url":"http://example.com/"}`)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status = %d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}
}

func TestClipURLNonHTMLResponse(t *testing.T) {
	page := serveClipURLPage(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain text"))
	})
	allowClipURLHosts(t, strings.TrimPrefix(page.URL, "http://"))

	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	raw := createTestAPIToken(t, db, user.ID)
	srv := New(Options{Store: db, Blobs: blob.New(t.TempDir())})

	res := postClipURL(t, srv, raw, fmt.Sprintf(`{"url":%q}`, page.URL))
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "HTML") {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
}

func TestClipURLFetchFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	allowClipURLHosts(t, addr)

	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	raw := createTestAPIToken(t, db, user.ID)
	srv := New(Options{Store: db, Blobs: blob.New(t.TempDir())})

	res := postClipURL(t, srv, raw, fmt.Sprintf(`{"url":"http://%s/"}`, addr))
	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
}

func TestClipURLOversizePage(t *testing.T) {
	page := serveClipURLPage(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(strings.Repeat("a", maxClipURLBytes+1)))
	})
	allowClipURLHosts(t, strings.TrimPrefix(page.URL, "http://"))

	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	raw := createTestAPIToken(t, db, user.ID)
	srv := New(Options{Store: db, Blobs: blob.New(t.TempDir())})

	res := postClipURL(t, srv, raw, fmt.Sprintf(`{"url":%q}`, page.URL))
	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
}

func TestClipURLRejectsRedirectToPrivateAddress(t *testing.T) {
	redirector := serveClipURLPage(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	})
	allowClipURLHosts(t, strings.TrimPrefix(redirector.URL, "http://"))

	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	raw := createTestAPIToken(t, db, user.ID)
	srv := New(Options{Store: db, Blobs: blob.New(t.TempDir())})

	res := postClipURL(t, srv, raw, fmt.Sprintf(`{"url":%q}`, redirector.URL))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
}

func TestClipURLFollowsRedirectToFinalPage(t *testing.T) {
	target := serveClipURLPage(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Redirect Target</title></head><body>redirected body</body></html>`))
	})
	redirector := serveClipURLPage(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/final", http.StatusFound)
	})
	allowClipURLHosts(t, strings.TrimPrefix(redirector.URL, "http://"), strings.TrimPrefix(target.URL, "http://"))

	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	raw := createTestAPIToken(t, db, user.ID)
	srv := New(Options{Store: db, Blobs: blob.New(t.TempDir())})

	res := postClipURL(t, srv, raw, fmt.Sprintf(`{"url":%q}`, redirector.URL))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	var out struct {
		Note    store.Note        `json:"note"`
		Version store.NoteVersion `json:"version"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Note.Title != "Redirect Target" {
		t.Fatalf("title = %q", out.Note.Title)
	}
	if !strings.Contains(out.Version.HeaderJSON, `"page_url":"`+target.URL+`/final"`) {
		t.Fatalf("header_json = %s", out.Version.HeaderJSON)
	}
}
