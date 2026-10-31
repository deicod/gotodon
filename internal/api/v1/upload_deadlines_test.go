package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/web"
)

// Uploads below take several times longer than the server's shrunken
// ordinary read and write timeouts.
const (
	tightServerTimeout = 100 * time.Millisecond
	slowUploadPieces   = 10
	slowUploadPause    = 50 * time.Millisecond
)

// serveWithTightTimeouts serves handler as accountID on a server built by
// web.NewServer with shrunken read and write timeouts.
func serveWithTightTimeouts(t *testing.T, accountID int64, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := web.NewServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), middleware.ContextKeyAccountID, accountID)
		handler(w, r.WithContext(ctx))
	}))
	srv.ReadTimeout = tightServerTimeout
	srv.WriteTimeout = tightServerTimeout
	ts := httptest.NewUnstartedServer(srv.Handler)
	ts.Config = srv
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

// sendSlowly sends body in pieces with a pause before each one, like a client
// on a slow link.
func sendSlowly(t *testing.T, ts *httptest.Server, method, path, contentType string, body []byte) *http.Response {
	t.Helper()
	pr, pw := io.Pipe()
	go func() {
		size := (len(body) + slowUploadPieces - 1) / slowUploadPieces
		for start := 0; start < len(body); start += size {
			time.Sleep(slowUploadPause)
			if _, err := pw.Write(body[start:min(start+size, len(body))]); err != nil {
				return
			}
		}
		_ = pw.Close()
	}()

	req, err := http.NewRequest(method, ts.URL+path, pr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", contentType)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestMediaHandler_Create_SlowUploadOutlivesServerTimeouts(t *testing.T) {
	h := NewMediaHandler(nil, nil)
	ts := serveWithTightTimeouts(t, 1, h.Create)

	// A form without a file is fully read and parsed before validation fails,
	// so the error reached shows the slow body arrived intact.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("description", strings.Repeat("a", 512)); err != nil {
		t.Fatalf("write description: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	resp := sendSlowly(t, ts, http.MethodPost, "/api/v2/media", writer.FormDataContentType(), body.Bytes())

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnprocessableEntity)
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Error != "Validation failed: File is required" {
		t.Fatalf("error = %q, want missing file error", payload.Error)
	}
}

func TestAccountsHandler_UpdateCredentials_SlowMultipartOutlivesServerTimeouts(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)
	uploader := &stubProfileMediaUploader{avatarPath: "media_attachments/files/1/avatar.png"}
	h.SetProfileMediaUploader(uploader)
	ts := serveWithTightTimeouts(t, 1, h.UpdateCredentials)

	form := multipartUpdateCredentialsRequest(t, map[string][]byte{"avatar": minimalPNGBytes()}, map[string]string{
		"display_name": "Slow Alice",
	})
	body, err := io.ReadAll(form.Body)
	if err != nil {
		t.Fatalf("read multipart body: %v", err)
	}

	resp := sendSlowly(t, ts, http.MethodPatch, "/api/v1/accounts/update_credentials", form.Header.Get("Content-Type"), body)

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d (body=%s)", resp.StatusCode, http.StatusOK, respBody)
	}
	if !stub.updateCalled || stub.updateParams.DisplayName.String != "Slow Alice" {
		t.Fatalf("update params = %+v, want display name Slow Alice", stub.updateParams)
	}
	if len(uploader.calls) != 1 {
		t.Fatalf("uploader calls = %d, want 1", len(uploader.calls))
	}
}
