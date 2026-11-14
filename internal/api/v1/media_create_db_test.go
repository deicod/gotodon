package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
)

// TestMediaCreateURLIsServed uploads through POST /api/v1/media and fetches
// the url the response and GET /api/v1/media/:id return. The server's zone is
// far east of UTC, so the local date differs from the UTC one for much of the
// day.
func TestMediaCreateURLIsServed(t *testing.T) {
	pool := openMigratedTestPool(t)
	previous := time.Local
	time.Local = time.FixedZone("UTC+14", 14*60*60)
	t.Cleanup(func() { time.Local = previous })

	ctx := context.Background()
	var accountID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO accounts (username, uri) VALUES ('alice', 'https://social.example/users/alice') RETURNING id`,
	).Scan(&accountID); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	cfg := fileURLConfig(t)
	h := NewMediaHandler(db.New(pool), media.NewUploader(media.NewStorage(cfg)))

	img := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for x := range 32 {
		img.Set(x, x%16, color.RGBA{R: 200, A: 255})
	}
	var upload bytes.Buffer
	if err := png.Encode(&upload, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "my photo.png")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	_, _ = part.Write(upload.Bytes())
	_ = form.WriteField("description", "a red line")
	_ = form.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, accountID))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	assertNoRelativeFileURLs(t, rec.Body.Bytes())
	var created MediaAttachment
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if !strings.HasPrefix(created.URL, "https://web.example/files/media_attachments/files/") {
		t.Fatalf("url = %q, want an absolute URL below https://web.example/files/", created.URL)
	}
	if created.PreviewURL != created.URL {
		t.Fatalf("preview_url = %q, want the url %q of an attachment without thumbnail", created.PreviewURL, created.URL)
	}
	if created.RemoteURL != nil {
		t.Fatalf("remote_url = %q, want null for a local upload", *created.RemoteURL)
	}
	fetchServedFile(t, cfg, created.URL, upload.Bytes())

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/media/"+created.ID, nil)
	getReq.SetPathValue("id", created.ID)
	getRec := httptest.NewRecorder()
	h.Get(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", getRec.Code, getRec.Body.String())
	}
	var fetched MediaAttachment
	if err := json.Unmarshal(getRec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if fetched.URL != created.URL || fetched.PreviewURL != created.PreviewURL {
		t.Fatalf("GET url = %q, preview_url = %q; want %q from the upload", fetched.URL, fetched.PreviewURL, created.URL)
	}
}
