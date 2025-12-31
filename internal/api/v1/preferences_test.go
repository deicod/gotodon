package v1

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPreferencesHandler_GetDefaults(t *testing.T) {
	h := NewPreferencesHandler(nil)

	req, rec := mockAccountRequest(http.MethodGet, "/api/v1/preferences", "")
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp Preferences
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PostingDefaultVisibility != "public" {
		t.Fatalf("posting:default:visibility = %q, want %q", resp.PostingDefaultVisibility, "public")
	}
	if resp.PostingDefaultSensitive {
		t.Fatalf("posting:default:sensitive = true, want false")
	}
	if resp.ReadingExpandMedia != "default" {
		t.Fatalf("reading:expand:media = %q, want %q", resp.ReadingExpandMedia, "default")
	}
	if resp.ReadingExpandSpoilers {
		t.Fatalf("reading:expand:spoilers = true, want false")
	}
}

func TestPreferencesFromSettings(t *testing.T) {
	settings := map[string]any{
		"default_privacy":             "private",
		"default_sensitive":           true,
		"default_language":            "fr",
		"web.display_media":           "show_all",
		"web.expand_content_warnings": true,
	}

	prefs := preferencesFromSettings(settings, "en")
	if prefs.PostingDefaultVisibility != "private" {
		t.Fatalf("visibility = %q, want %q", prefs.PostingDefaultVisibility, "private")
	}
	if !prefs.PostingDefaultSensitive {
		t.Fatalf("sensitive = false, want true")
	}
	if prefs.PostingDefaultLanguage != "fr" {
		t.Fatalf("language = %q, want %q", prefs.PostingDefaultLanguage, "fr")
	}
	if prefs.ReadingExpandMedia != "show_all" {
		t.Fatalf("expand media = %q, want %q", prefs.ReadingExpandMedia, "show_all")
	}
	if !prefs.ReadingExpandSpoilers {
		t.Fatalf("expand spoilers = false, want true")
	}

	settings = map[string]any{
		"default_privacy":             "invalid",
		"default_sensitive":           "true",
		"web.display_media":           "bad",
		"web.expand_content_warnings": "true",
	}

	prefs = preferencesFromSettings(settings, "en")
	if prefs.PostingDefaultVisibility != "public" {
		t.Fatalf("invalid visibility = %q, want %q", prefs.PostingDefaultVisibility, "public")
	}
	if !prefs.PostingDefaultSensitive {
		t.Fatalf("string sensitive = false, want true")
	}
	if prefs.PostingDefaultLanguage != "en" {
		t.Fatalf("language fallback = %q, want %q", prefs.PostingDefaultLanguage, "en")
	}
	if prefs.ReadingExpandMedia != "default" {
		t.Fatalf("invalid expand media = %q, want %q", prefs.ReadingExpandMedia, "default")
	}
	if !prefs.ReadingExpandSpoilers {
		t.Fatalf("string expand spoilers = false, want true")
	}
}
