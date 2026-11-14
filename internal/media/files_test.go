package media

import (
	"bytes"
	"context"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newFilesMux returns a mux with the file routes and the compat handler's
// catch-all pattern they must coexist with.
func newFilesMux(t *testing.T, root string) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{segment}/{status}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	NewFileServer(root).RegisterRoutes(mux)
	return mux
}

func writeFile(t *testing.T, root, name string, data []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func assertFileHeaders(t *testing.T, header http.Header) {
	t.Helper()
	want := map[string]string{
		"Cache-Control":           "public, max-age=2419200, immutable",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; form-action 'none'",
	}
	for name, value := range want {
		if got := header.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestFileServerServesStoredFiles(t *testing.T) {
	root := t.TempDir()
	pngData := createTestImage(8, 8)
	htmlData := []byte("<!DOCTYPE html><html><script>alert(1)</script></html>")
	writeFile(t, root, "media_attachments/files/1/1/2026/11/14/photo.png", pngData)
	writeFile(t, root, "top.png", pngData)
	writeFile(t, root, "custom_emojis/images/a b.png", pngData)
	writeFile(t, root, "misnamed/image.txt", pngData)
	writeFile(t, root, "misnamed/page.png", htmlData)
	mux := newFilesMux(t, root)

	tests := []struct {
		name            string
		path            string
		wantBody        []byte
		wantContentType string
	}{
		{name: "nested path", path: "/files/media_attachments/files/1/1/2026/11/14/photo.png", wantBody: pngData, wantContentType: "image/png"},
		{name: "top-level file", path: "/files/top.png", wantBody: pngData, wantContentType: "image/png"},
		{name: "escaped segment", path: "/files/custom_emojis/images/a%20b.png", wantBody: pngData, wantContentType: "image/png"},
		{name: "type from bytes, not extension", path: "/files/misnamed/image.txt", wantBody: pngData, wantContentType: "image/png"},
		{name: "markup is not rendered", path: "/files/misnamed/page.png", wantBody: htmlData, wantContentType: "application/octet-stream"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body %q", rec.Code, http.StatusOK, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != tt.wantContentType {
				t.Errorf("Content-Type = %q, want %q", got, tt.wantContentType)
			}
			if !bytes.Equal(rec.Body.Bytes(), tt.wantBody) {
				t.Errorf("body differs from the stored file")
			}
			if rec.Header().Get("ETag") == "" || rec.Header().Get("Last-Modified") == "" {
				t.Errorf("ETag = %q, Last-Modified = %q, want both set", rec.Header().Get("ETag"), rec.Header().Get("Last-Modified"))
			}
			assertFileHeaders(t, rec.Header())
		})
	}
}

func TestFileServerHeadRangeAndConditional(t *testing.T) {
	root := t.TempDir()
	data := createTestImage(16, 16)
	writeFile(t, root, "a/b.png", data)
	mux := newFilesMux(t, root)

	head := httptest.NewRecorder()
	mux.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/files/a/b.png", nil))
	if head.Code != http.StatusOK {
		t.Fatalf("HEAD status = %d, want %d", head.Code, http.StatusOK)
	}
	if head.Body.Len() != 0 {
		t.Errorf("HEAD body has %d bytes, want none", head.Body.Len())
	}
	if got := head.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("HEAD Content-Type = %q, want image/png", got)
	}
	assertFileHeaders(t, head.Header())

	rangeReq := httptest.NewRequest(http.MethodGet, "/files/a/b.png", nil)
	rangeReq.Header.Set("Range", "bytes=0-3")
	partial := httptest.NewRecorder()
	mux.ServeHTTP(partial, rangeReq)
	if partial.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want %d", partial.Code, http.StatusPartialContent)
	}
	if !bytes.Equal(partial.Body.Bytes(), data[:4]) {
		t.Errorf("range body = %v, want %v", partial.Body.Bytes(), data[:4])
	}

	conditional := httptest.NewRequest(http.MethodGet, "/files/a/b.png", nil)
	conditional.Header.Set("If-None-Match", head.Header().Get("ETag"))
	notModified := httptest.NewRecorder()
	mux.ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want %d", notModified.Code, http.StatusNotModified)
	}
}

func TestFileServerRejects(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "uploads")
	writeFile(t, root, "media_attachments/files/photo.png", createTestImage(4, 4))
	writeFile(t, root, ".hidden/secret.png", createTestImage(4, 4))
	writeFile(t, root, "dir/.env", []byte("SECRET=1"))
	writeFile(t, parent, "secret.txt", []byte("outside the root"))
	if err := os.Symlink(filepath.Join(parent, "secret.txt"), filepath.Join(root, "escape.png")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Symlink(parent, filepath.Join(root, "parent")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	mux := newFilesMux(t, root)
	server := NewFileServer(root)

	tests := []struct {
		name string
		path string
	}{
		{name: "missing file", path: "/files/media_attachments/files/missing.png"},
		{name: "directory", path: "/files/media_attachments"},
		{name: "directory with slash", path: "/files/media_attachments/files/"},
		{name: "storage root", path: "/files/"},
		{name: "path through a file", path: "/files/media_attachments/files/photo.png/x"},
		{name: "symlink out of the root", path: "/files/escape.png"},
		{name: "symlinked directory out of the root", path: "/files/parent/secret.txt"},
		{name: "dot directory", path: "/files/.hidden/secret.png"},
		{name: "dot-file", path: "/files/dir/.env"},
		{name: "encoded traversal", path: "/files/media_attachments/..%2F..%2Fsecret.txt"},
		{name: "encoded slash traversal", path: "/files/..%2fsecret.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d; body %q", rec.Code, http.StatusNotFound, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "outside the root") || strings.Contains(rec.Body.String(), "SECRET") {
				t.Fatalf("body leaked a protected file: %q", rec.Body.String())
			}
		})
	}

	// ServeMux redirects dot-dot paths before routing; the handler refuses
	// them on its own as well.
	for _, raw := range []string{"/files/../secret.txt", "/files/media_attachments/../../secret.txt", "/files//etc/passwd"} {
		t.Run("direct "+raw, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.URL = &url.URL{Path: raw}
			rec := httptest.NewRecorder()
			server.ServeFile(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
			}
		})
	}
	for _, raw := range []string{"/files/../secret.txt", "/files/media_attachments/../../secret.txt"} {
		t.Run("routed "+raw, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, raw, nil))
			if rec.Code == http.StatusOK {
				t.Fatalf("status = %d, want the request refused or redirected", rec.Code)
			}
		})
	}
}

func TestFileServerMethods(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/b.png", createTestImage(4, 4))
	mux := newFilesMux(t, root)
	server := NewFileServer(root)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(method, "/files/a/b.png", nil))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("routed status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
			}
			if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
				t.Errorf("routed Allow = %q, want %q", got, "GET, HEAD")
			}

			direct := httptest.NewRecorder()
			server.ServeFile(direct, httptest.NewRequest(method, "/files/a/b.png", nil))
			if direct.Code != http.StatusMethodNotAllowed {
				t.Fatalf("direct status = %d, want %d", direct.Code, http.StatusMethodNotAllowed)
			}
			if got := direct.Header().Get("Allow"); got != "GET, HEAD" {
				t.Errorf("direct Allow = %q, want %q", got, "GET, HEAD")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b.png")); err != nil {
		t.Fatalf("file changed by a refused request: %v", err)
	}
}

func TestFileServerMissingRoot(t *testing.T) {
	mux := newFilesMux(t, filepath.Join(t.TempDir(), "not-created-yet"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/files/a/b.png", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestFileServerFallbackImages(t *testing.T) {
	mux := newFilesMux(t, t.TempDir())
	tests := []struct {
		path       string
		wantWidth  int
		wantHeight int
	}{
		{path: MissingAvatarPath, wantWidth: missingAvatarSize, wantHeight: missingAvatarSize},
		{path: MissingHeaderPath, wantWidth: 1, wantHeight: 1},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got := rec.Header().Get("Content-Type"); got != "image/png" {
				t.Errorf("Content-Type = %q, want image/png", got)
			}
			assertFileHeaders(t, rec.Header())
			img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if b := img.Bounds(); b.Dx() != tt.wantWidth || b.Dy() != tt.wantHeight {
				t.Errorf("size = %dx%d, want %dx%d", b.Dx(), b.Dy(), tt.wantWidth, tt.wantHeight)
			}

			head := httptest.NewRecorder()
			mux.ServeHTTP(head, httptest.NewRequest(http.MethodHead, tt.path, nil))
			if head.Code != http.StatusOK || head.Body.Len() != 0 {
				t.Errorf("HEAD status = %d with %d body bytes, want 200 and none", head.Code, head.Body.Len())
			}

			conditional := httptest.NewRequest(http.MethodGet, tt.path, nil)
			conditional.Header.Set("If-None-Match", rec.Header().Get("ETag"))
			notModified := httptest.NewRecorder()
			mux.ServeHTTP(notModified, conditional)
			if notModified.Code != http.StatusNotModified {
				t.Errorf("conditional status = %d, want %d", notModified.Code, http.StatusNotModified)
			}

			post := httptest.NewRecorder()
			mux.ServeHTTP(post, httptest.NewRequest(http.MethodPost, tt.path, nil))
			if post.Code != http.StatusMethodNotAllowed {
				t.Errorf("POST status = %d, want %d", post.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestMissingAvatarShowsAFigure(t *testing.T) {
	img := renderMissingAvatar()
	if got := img.RGBAAt(0, 0); got != missingAvatarBackground {
		t.Errorf("corner = %v, want background %v", got, missingAvatarBackground)
	}
	center := missingAvatarSize / 2
	if got := img.RGBAAt(center, int(float64(missingAvatarSize)*0.38)); got != missingAvatarFigure {
		t.Errorf("head = %v, want figure %v", got, missingAvatarFigure)
	}
	if got := img.RGBAAt(center, missingAvatarSize-1); got != missingAvatarFigure {
		t.Errorf("shoulders = %v, want figure %v", got, missingAvatarFigure)
	}
}

func TestServedContentType(t *testing.T) {
	tests := []struct {
		detected string
		want     string
	}{
		{"image/png", "image/png"},
		{"image/jpeg", "image/jpeg"},
		{"image/webp", "image/webp"},
		{"video/mp4", "video/mp4"},
		{"video/webm", "video/webm"},
		{"audio/mpeg", "audio/mpeg"},
		{"application/ogg", "application/ogg"},
		{"image/svg+xml", "application/octet-stream"},
		{"text/html; charset=utf-8", "application/octet-stream"},
		{"text/plain; charset=utf-8", "application/octet-stream"},
		{"application/pdf", "application/octet-stream"},
		{"", "application/octet-stream"},
	}
	for _, tt := range tests {
		if got := servedContentType(tt.detected); got != tt.want {
			t.Errorf("servedContentType(%q) = %q, want %q", tt.detected, got, tt.want)
		}
	}
}

// TestUploadedFileIsServedAtItsURL uploads a file and fetches it at the URL
// the upload returns, through the routes the server registers.
func TestUploadedFileIsServedAtItsURL(t *testing.T) {
	root := t.TempDir()
	storage := NewLocalStorage(root, "https://example.com/files")
	uploader := NewUploader(storage)
	data := createTestImage(20, 10)

	result, err := uploader.Upload(context.Background(), 42, "holiday photo.png", data)
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	parsed, err := url.Parse(result.URL)
	if err != nil {
		t.Fatalf("parse URL %q: %v", result.URL, err)
	}
	if parsed.Scheme != "https" || parsed.Host != "example.com" {
		t.Fatalf("URL = %q, want an absolute https://example.com URL", result.URL)
	}

	rec := httptest.NewRecorder()
	newFilesMux(t, root).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", parsed.RequestURI(), rec.Code, http.StatusOK)
	}
	body, _ := io.ReadAll(rec.Body)
	if !bytes.Equal(body, data) {
		t.Fatal("served body differs from the upload")
	}
	if got := rec.Header().Get("Content-Type"); got != result.ContentType {
		t.Errorf("Content-Type = %q, want the stored %q", got, result.ContentType)
	}
	assertFileHeaders(t, rec.Header())
}
