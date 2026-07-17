package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAndroidLatestJSON(t *testing.T) {
	staticDir := makeAndroidStaticDir(t, `{"versionCode":42,"versionName":"1.2.3","sha256":"abc123"}`)
	db := testStore(t)
	defer db.Close()
	srv := New(Options{Store: db, StaticDir: staticDir})

	req := httptest.NewRequest(http.MethodGet, "/android/latest.json", nil)
	req.Host = "example.com"
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q", got)
	}
	var metadata androidUpdateMetadata
	if err := json.Unmarshal(res.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.VersionCode != 42 {
		t.Fatalf("versionCode = %d", metadata.VersionCode)
	}
	if metadata.VersionName != "1.2.3" {
		t.Fatalf("versionName = %q", metadata.VersionName)
	}
	if metadata.SHA256 != "abc123" {
		t.Fatalf("sha256 = %q", metadata.SHA256)
	}
	want := "http://example.com/android/cairnfield.apk"
	if metadata.APKURL != want {
		t.Fatalf("apkUrl = %q, want %q", metadata.APKURL, want)
	}
}

func TestAndroidLatestJSONWithBasePathAndForwardedHeaders(t *testing.T) {
	staticDir := makeAndroidStaticDir(t, `{"versionCode":7,"versionName":"0.7.0","sha256":"def456"}`)
	db := testStore(t)
	defer db.Close()
	srv := New(Options{Store: db, StaticDir: staticDir, BasePath: "/notes"})

	req := httptest.NewRequest(http.MethodGet, "/notes/android/latest.json", nil)
	req.Host = "internal.local"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "cairnfield.example.com")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	var metadata androidUpdateMetadata
	if err := json.Unmarshal(res.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	want := "https://cairnfield.example.com/notes/android/cairnfield.apk"
	if metadata.APKURL != want {
		t.Fatalf("apkUrl = %q, want %q", metadata.APKURL, want)
	}
}

func TestAndroidAPKDownload(t *testing.T) {
	staticDir := makeAndroidStaticDir(t, `{"versionCode":1,"versionName":"1.0.0","sha256":"000"}`)
	db := testStore(t)
	defer db.Close()
	srv := New(Options{Store: db, StaticDir: staticDir})

	req := httptest.NewRequest(http.MethodGet, "/android/cairnfield.apk", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); got != "application/vnd.android.package-archive" {
		t.Fatalf("content-type = %q", got)
	}
	wantDisposition := `attachment; filename="cairnfield.apk"`
	if got := res.Header().Get("Content-Disposition"); got != wantDisposition {
		t.Fatalf("content-disposition = %q", got)
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q", got)
	}
	if got := res.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("x-content-type-options = %q", got)
	}
	if got := res.Body.String(); got != "apk-bytes" {
		t.Fatalf("body = %q", got)
	}
}

func TestAndroidLatestJSONMissing(t *testing.T) {
	staticDir := t.TempDir()
	db := testStore(t)
	defer db.Close()
	srv := New(Options{Store: db, StaticDir: staticDir})

	req := httptest.NewRequest(http.MethodGet, "/android/latest.json", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d", res.Code)
	}
}

func TestAndroidAPKMissing(t *testing.T) {
	staticDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staticDir, "android"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "android", "latest.json"), []byte(`{"versionCode":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	db := testStore(t)
	defer db.Close()
	srv := New(Options{Store: db, StaticDir: staticDir})

	req := httptest.NewRequest(http.MethodGet, "/android/cairnfield.apk", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("status = %d", res.Code)
	}
}

func TestAndroidLatestJSONMalformed(t *testing.T) {
	staticDir := makeAndroidStaticDir(t, `not json`)
	db := testStore(t)
	defer db.Close()
	srv := New(Options{Store: db, StaticDir: staticDir})

	req := httptest.NewRequest(http.MethodGet, "/android/latest.json", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "invalid android update metadata") {
		t.Fatalf("body = %q", res.Body.String())
	}
}

func makeAndroidStaticDir(t *testing.T, latestJSON string) string {
	t.Helper()
	dir := t.TempDir()
	androidDir := filepath.Join(dir, "android")
	if err := os.MkdirAll(androidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(androidDir, "latest.json"), []byte(latestJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(androidDir, "cairnfield.apk"), []byte("apk-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
