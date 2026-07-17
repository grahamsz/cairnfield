package web

import (
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

func TestAPITokenCreateListAndRevoke(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	srv := New(Options{Store: db})

	createBody := strings.NewReader(`{"name":"Chrome clipping tool"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/tokens", createBody)
	createReq = createReq.WithContext(context.WithValue(createReq.Context(), currentUserKey, currentUser{User: user}))
	createRes := httptest.NewRecorder()
	srv.apiTokens(createRes, createReq)
	if createRes.Code != http.StatusOK {
		t.Fatalf("create status = %d body=%s", createRes.Code, createRes.Body.String())
	}
	var created struct {
		Token    store.APIToken `json:"token"`
		RawToken string         `json:"raw_token"`
	}
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.RawToken, "cairnfield_") {
		t.Fatalf("raw token = %q", created.RawToken)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/tokens", nil)
	listReq = listReq.WithContext(context.WithValue(listReq.Context(), currentUserKey, currentUser{User: user}))
	listRes := httptest.NewRecorder()
	srv.apiTokens(listRes, listReq)
	if strings.Contains(listRes.Body.String(), created.RawToken) {
		t.Fatalf("list leaked token material: %s", listRes.Body.String())
	}

	revokeReq := httptest.NewRequest(http.MethodDelete, "/api/tokens/1", nil)
	revokeReq = revokeReq.WithContext(context.WithValue(revokeReq.Context(), currentUserKey, currentUser{User: user}))
	revokeRes := httptest.NewRecorder()
	srv.apiTokenPath(revokeRes, revokeReq, "1")
	if revokeRes.Code != http.StatusOK {
		t.Fatalf("revoke status = %d body=%s", revokeRes.Code, revokeRes.Body.String())
	}
}

func TestRevokedTokenCannotBootstrap(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	raw := createTestAPIToken(t, db, user.ID)
	tokens, err := db.ListAPITokens(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RevokeAPIToken(t.Context(), user.ID, tokens[0].ID); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Store: db})
	req := httptest.NewRequest(http.MethodGet, "/api/clip/bootstrap", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	res := httptest.NewRecorder()
	srv.apiClipBootstrap(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
}

func TestHTMLClipCreatesNoteAssetSearchTextAndSandboxedAsset(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	raw := createTestAPIToken(t, db, user.ID)
	blobs := blob.New(t.TempDir())
	searchService, err := search.OpenPerUser(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer searchService.Close()
	srv := New(Options{Store: db, Blobs: blobs, Search: searchService})

	html := []byte(`<!doctype html><html><head><script>alert(1)</script></head><body><h1>Needle Clip</h1><p>Searchable archive text.</p></body></html>`)
	res := runClipMultipartWithPreview(t, srv, raw, "/api/clip/html", "html", "clip.html", "text/html", clipMetadata{Title: "Needle Clip", SourceURL: "https://example.com/page", PageURL: "https://example.com/page", FolderPath: "/clips", DestinationKind: "folder", CapturedAt: "2024-01-02T03:04:05Z"}, html, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	var out struct {
		Note  store.Note  `json:"note"`
		Asset store.Asset `json:"asset"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	_, version, err := db.GetNote(t.Context(), user.ID, out.Note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(version.Content, "https://example.com/page") {
		t.Fatalf("note content = %s", version.Content)
	}
	if !strings.Contains(version.HeaderJSON, `"kind":"webpage"`) || !strings.Contains(version.HeaderJSON, `"type":"html"`) || !strings.Contains(version.HeaderJSON, `"preview_asset"`) {
		t.Fatalf("header_json = %s", version.HeaderJSON)
	}
	assets, err := db.ListAssetsForNote(t.Context(), user.ID, out.Note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 2 || !strings.Contains(assets[0].SearchText, "Searchable archive text") {
		t.Fatalf("assets = %+v", assets)
	}
	assetReq := httptest.NewRequest(http.MethodGet, "/assets/"+out.Asset.Slug+"/clip.html", nil)
	assetReq = assetReq.WithContext(context.WithValue(assetReq.Context(), currentUserKey, currentUser{User: user}))
	assetRes := httptest.NewRecorder()
	srv.handleAsset(assetRes, assetReq)
	if assetRes.Code != http.StatusOK {
		t.Fatalf("asset status = %d body=%s", assetRes.Code, assetRes.Body.String())
	}
	if !strings.Contains(assetRes.Header().Get("Content-Security-Policy"), "sandbox") || assetRes.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("asset headers = %v", assetRes.Header())
	}
}

func TestPDFClipCreatesWebpageAssetPreviewAndSearchText(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	raw := createTestAPIToken(t, db, user.ID)
	srv := New(Options{Store: db, Blobs: blob.New(t.TempDir())})

	pdf := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n")
	meta := clipMetadata{
		Title: "Printed Clip", SourceURL: "https://example.com/page", PageURL: "https://example.com/page",
		FolderPath: "/clips", DestinationKind: "folder", CapturedAt: "2024-01-02T03:04:05Z",
		SearchText: "Browser rendered searchable needle",
	}
	res := runClipMultipartWithPreview(t, srv, raw, "/api/clip/pdf", "pdf", "clip.pdf", "application/pdf", meta, pdf, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	var out struct {
		Note  store.Note  `json:"note"`
		Asset store.Asset `json:"asset"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	_, version, err := db.GetNote(t.Context(), user.ID, out.Note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(version.HeaderJSON, `"kind":"webpage"`) || !strings.Contains(version.HeaderJSON, `"type":"pdf"`) || !strings.Contains(version.HeaderJSON, `"preview_asset"`) {
		t.Fatalf("header_json = %s", version.HeaderJSON)
	}
	assets, err := db.ListAssetsForNote(t.Context(), user.ID, out.Note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 2 || assets[0].ContentType != "application/pdf" || !strings.Contains(assets[0].SearchText, "searchable needle") {
		t.Fatalf("assets = %+v", assets)
	}
}

func TestImageClipAppearsInMoodboardItems(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	raw := createTestAPIToken(t, db, user.ID)
	if _, err := db.SetFolderSettings(t.Context(), user.ID, "/board", "moodboard", "newest"); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Store: db, Blobs: blob.New(t.TempDir())})
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3}
	res := runClipMultipart(t, srv, raw, "/api/clip/image", "image", "clip.png", "image/png", clipMetadata{Title: "Board Image", SourceURL: "https://example.com/image.png", PageURL: "https://example.com/page", FolderPath: "/board", DestinationKind: "board", CapturedAt: "2024-01-02T03:04:05Z", SearchText: "Image page searchable needle"}, png)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	items, err := db.ListMoodboardItems(t.Context(), user.ID, "/board", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Asset == nil || items[0].Asset.Filename != "clip.png" {
		t.Fatalf("items = %+v", items)
	}
	_, version, err := db.GetNote(t.Context(), user.ID, items[0].Note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(version.HeaderJSON, `"kind":"webpage"`) || !strings.Contains(version.HeaderJSON, `"type":"image"`) {
		t.Fatalf("header_json = %s", version.HeaderJSON)
	}
	assets, err := db.ListAssetsForNote(t.Context(), user.ID, items[0].Note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 || !strings.Contains(assets[0].SearchText, "searchable needle") {
		t.Fatalf("assets = %+v", assets)
	}
}

func runClipMultipart(t *testing.T, srv *Server, rawToken, url, field, filename, contentType string, meta clipMetadata, data []byte) *httptest.ResponseRecorder {
	return runClipMultipartWithPreview(t, srv, rawToken, url, field, filename, contentType, meta, data, nil)
}

func runClipMultipartWithPreview(t *testing.T, srv *Server, rawToken, url, field, filename, contentType string, meta clipMetadata, data []byte, preview []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := buildClipMultipartRequest(t, url, field, filename, contentType, meta, data, preview)
	req.Header.Set("Authorization", "Bearer "+rawToken)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	return res
}

func buildClipMultipartRequest(t *testing.T, url, field, filename, contentType string, meta clipMetadata, data []byte, preview []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	rawMeta, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("metadata", string(rawMeta)); err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		if err := mw.WriteField("content_type", contentType); err != nil {
			t.Fatal(err)
		}
	}
	part, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if len(preview) > 0 {
		previewPart, err := mw.CreateFormFile("preview", "preview.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := previewPart.Write(preview); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, url, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestHTMLClipAcceptsSessionAuth(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	user := testUser(t, db)
	if err := db.CreateSession(t.Context(), user.ID, store.TokenHash("cliphtml-session"), time.Hour); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{Store: db, Blobs: blob.New(t.TempDir())})

	page := []byte(`<!doctype html><html><body><h1>Session Clip</h1></body></html>`)
	meta := clipMetadata{Title: "Session Clip", SourceURL: "https://example.com/page", PageURL: "https://example.com/page", FolderPath: "/clips", DestinationKind: "folder", CapturedAt: "2024-01-02T03:04:05Z"}
	req := buildClipMultipartRequest(t, "/api/clip/html", "html", "clip.html", "text/html", meta, page, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "cliphtml-session"})
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
	if out.Note.Title != "Session Clip" || out.Note.OwnerUserID != user.ID {
		t.Fatalf("note = %+v", out.Note)
	}
}

func TestHTMLClipRequiresAuth(t *testing.T) {
	db := testStore(t)
	defer db.Close()
	srv := New(Options{Store: db, Blobs: blob.New(t.TempDir())})

	page := []byte(`<!doctype html><html><body><h1>Nope</h1></body></html>`)
	req := buildClipMultipartRequest(t, "/api/clip/html", "html", "clip.html", "text/html", clipMetadata{Title: "Nope"}, page, nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
}

func testUser(t *testing.T, db *store.Store) store.User {
	t.Helper()
	hash, err := auth.HashPassword("password-password")
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser(t.Context(), "person@example.com", "Person", hash, true)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func createTestAPIToken(t *testing.T, db *store.Store, userID int64) string {
	t.Helper()
	raw := "cairnfield_test_token"
	if _, err := db.CreateAPIToken(t.Context(), userID, "test token", store.TokenHash(raw)); err != nil {
		t.Fatal(err)
	}
	return raw
}
