package manifest

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type stubQueries struct {
	settings []db.GetSettingsByVarsRow
}

func (s *stubQueries) GetSettingsByVars(_ context.Context, _ []string) ([]db.GetSettingsByVarsRow, error) {
	return s.settings, nil
}

func TestHandler_ManifestSuccessAndCompatibilityFields(t *testing.T) {
	cfg := &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "social.example",
			WebDomain:   "public.example",
		},
	}
	h := NewHandler(nil, cfg)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/manifest", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Cache-Control"); got != manifestCacheControl {
		t.Fatalf("cache-control = %q, want %q", got, manifestCacheControl)
	}
	if got := rec.Header().Get("Content-Type"); got != manifestJSONType {
		t.Fatalf("content-type = %q, want %q", got, manifestJSONType)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := body["id"]; got != "/home" {
		t.Fatalf("id = %v, want /home", got)
	}
	if got := body["name"]; got != defaultManifestTitle {
		t.Fatalf("name = %v, want %q", got, defaultManifestTitle)
	}
	if got := body["short_name"]; got != defaultManifestTitle {
		t.Fatalf("short_name = %v, want %q", got, defaultManifestTitle)
	}
	if got := body["start_url"]; got != "/" {
		t.Fatalf("start_url = %v, want /", got)
	}
	if got := body["scope"]; got != "/" {
		t.Fatalf("scope = %v, want /", got)
	}
	if got := body["display"]; got != "standalone" {
		t.Fatalf("display = %v, want standalone", got)
	}
	if got := body["prefer_related_applications"]; got != true {
		t.Fatalf("prefer_related_applications = %v, want true", got)
	}
	if _, ok := body["share_target"].(map[string]any); !ok {
		t.Fatalf("share_target missing or invalid shape: %#v", body["share_target"])
	}
	if shortcuts, ok := body["shortcuts"].([]any); !ok || len(shortcuts) < 3 {
		t.Fatalf("shortcuts shape = %#v, want at least 3 entries", body["shortcuts"])
	}
	if related, ok := body["related_applications"].([]any); !ok || len(related) < 3 {
		t.Fatalf("related_applications shape = %#v, want at least 3 entries", body["related_applications"])
	}
}

func TestHandler_UsesSiteTitleFromSettings(t *testing.T) {
	queries := &stubQueries{
		settings: []db.GetSettingsByVarsRow{
			{
				Var:   settingSiteTitle,
				Value: pgtype.Text{String: "Custom Instance", Valid: true},
			},
		},
	}
	cfg := &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "social.example",
		},
	}
	h := NewHandler(queries, cfg)

	req := httptest.NewRequest(http.MethodGet, "/manifest", nil)
	rec := httptest.NewRecorder()

	h.Show(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := body["name"]; got != "Custom Instance" {
		t.Fatalf("name = %v, want %q", got, "Custom Instance")
	}
	if got := body["short_name"]; got != "Custom Instance" {
		t.Fatalf("short_name = %v, want %q", got, "Custom Instance")
	}
}

func TestHandler_IconURLsUsePublicDomain(t *testing.T) {
	cfg := &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "local.example",
			WebDomain:   "public.example",
		},
	}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/manifest", nil)
	rec := httptest.NewRecorder()
	h.Show(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	icons, ok := body["icons"].([]any)
	if !ok || len(icons) == 0 {
		t.Fatalf("icons shape = %#v", body["icons"])
	}
	firstIcon, ok := icons[0].(map[string]any)
	if !ok {
		t.Fatalf("first icon shape = %#v", icons[0])
	}
	if got := firstIcon["src"]; got != "https://public.example/icons/android-chrome-36x36.png" {
		t.Fatalf("icon src = %v, want web-domain based URL", got)
	}
}

func TestHandler_IconURLsFallbackToLocalDomain(t *testing.T) {
	cfg := &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "local.example",
		},
	}
	h := NewHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/manifest", nil)
	rec := httptest.NewRecorder()
	h.Show(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	icons, ok := body["icons"].([]any)
	if !ok || len(icons) == 0 {
		t.Fatalf("icons shape = %#v", body["icons"])
	}
	firstIcon, ok := icons[0].(map[string]any)
	if !ok {
		t.Fatalf("first icon shape = %#v", icons[0])
	}
	if got := firstIcon["src"]; got != "https://local.example/icons/android-chrome-36x36.png" {
		t.Fatalf("icon src = %v, want local-domain based URL", got)
	}
}

func TestHandler_RegistersManifestAdvertisedRoutes(t *testing.T) {
	cfg := &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain: "social.example",
		},
	}
	h := NewHandler(nil, cfg)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	testCases := []struct {
		name         string
		target       string
		contentType  string
		containsBody string
	}{
		{
			name:         "home",
			target:       "/",
			contentType:  "text/html; charset=utf-8",
			containsBody: "<h1>Home</h1>",
		},
		{
			name:         "share",
			target:       "/share?title=hello&text=world&url=https://example.com",
			contentType:  "text/html; charset=utf-8",
			containsBody: "<h1>Share</h1>",
		},
		{
			name:         "publish",
			target:       "/publish",
			contentType:  "text/html; charset=utf-8",
			containsBody: "<h1>Compose</h1>",
		},
		{
			name:         "notifications",
			target:       "/notifications",
			contentType:  "text/html; charset=utf-8",
			containsBody: "<h1>Notifications</h1>",
		},
		{
			name:         "explore",
			target:       "/explore",
			contentType:  "text/html; charset=utf-8",
			containsBody: "<h1>Explore</h1>",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got := rec.Header().Get("Content-Type"); got != tc.contentType {
				t.Fatalf("content-type = %q, want %q", got, tc.contentType)
			}
			if !strings.Contains(rec.Body.String(), tc.containsBody) {
				t.Fatalf("body = %q, want to contain %q", rec.Body.String(), tc.containsBody)
			}
		})
	}
}

func TestHandler_IconEndpointServesGeneratedPNG(t *testing.T) {
	h := NewHandler(nil, &config.Config{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/icons/android-chrome-192x192.png", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content-type = %q, want %q", got, "image/png")
	}
	if got := rec.Header().Get("Cache-Control"); got != manifestCacheControl {
		t.Fatalf("cache-control = %q, want %q", got, manifestCacheControl)
	}

	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 192 || bounds.Dy() != 192 {
		t.Fatalf("icon size = %dx%d, want 192x192", bounds.Dx(), bounds.Dy())
	}
}

func TestHandler_IconEndpointRejectsUnknownSize(t *testing.T) {
	h := NewHandler(nil, &config.Config{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/icons/android-chrome-200x200.png", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
