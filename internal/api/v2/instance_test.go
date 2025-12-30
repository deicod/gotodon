package v2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/config"
)

func TestInstanceHandler_Get_Defaults(t *testing.T) {
	cfg := &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain:       "local.example",
			WebDomain:         "public.example",
			RegistrationsMode: "approved",
		},
		Secrets: config.SecretsConfig{
			VAPIDPublic: "vapid-public",
		},
	}
	h := NewInstanceHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/instance", nil)
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["domain"] != "public.example" {
		t.Fatalf("domain = %v, want %q", resp["domain"], "public.example")
	}

	if resp["version"] != "4.5.3" {
		t.Fatalf("version = %v, want %q", resp["version"], "4.5.3")
	}

	apiVersions, ok := resp["api_versions"].(map[string]any)
	if !ok {
		t.Fatalf("api_versions missing or invalid: %T", resp["api_versions"])
	}
	if apiVersions["mastodon"] != float64(7) {
		t.Fatalf("api_versions.mastodon = %v, want 7", apiVersions["mastodon"])
	}

	registrations, ok := resp["registrations"].(map[string]any)
	if !ok {
		t.Fatalf("registrations missing or invalid: %T", resp["registrations"])
	}
	if registrations["enabled"] != true {
		t.Fatalf("registrations.enabled = %v, want true", registrations["enabled"])
	}
	if registrations["approval_required"] != true {
		t.Fatalf("registrations.approval_required = %v, want true", registrations["approval_required"])
	}

	configuration, ok := resp["configuration"].(map[string]any)
	if !ok {
		t.Fatalf("configuration missing or invalid: %T", resp["configuration"])
	}
	urls, ok := configuration["urls"].(map[string]any)
	if !ok {
		t.Fatalf("configuration.urls missing or invalid: %T", configuration["urls"])
	}
	if urls["streaming"] != "wss://public.example/api/v1/streaming" {
		t.Fatalf("urls.streaming = %v, want %q", urls["streaming"], "wss://public.example/api/v1/streaming")
	}
	if urls["terms_of_service"] != nil {
		t.Fatalf("urls.terms_of_service = %v, want nil", urls["terms_of_service"])
	}

	vapid, ok := configuration["vapid"].(map[string]any)
	if !ok {
		t.Fatalf("configuration.vapid missing or invalid: %T", configuration["vapid"])
	}
	if vapid["public_key"] != "vapid-public" {
		t.Fatalf("vapid.public_key = %v, want %q", vapid["public_key"], "vapid-public")
	}

	mediaAttachments, ok := configuration["media_attachments"].(map[string]any)
	if !ok {
		t.Fatalf("configuration.media_attachments missing or invalid: %T", configuration["media_attachments"])
	}
	if mediaAttachments["description_limit"] != float64(1500) {
		t.Fatalf("media_attachments.description_limit = %v, want 1500", mediaAttachments["description_limit"])
	}

	icons, ok := resp["icon"].([]any)
	if !ok {
		t.Fatalf("icon missing or invalid: %T", resp["icon"])
	}
	if len(icons) != 9 {
		t.Fatalf("icon len = %d, want 9", len(icons))
	}

	thumbnail, ok := resp["thumbnail"].(map[string]any)
	if !ok {
		t.Fatalf("thumbnail missing or invalid: %T", resp["thumbnail"])
	}
	if thumbnail["url"] != "https://public.example/images/preview.png" {
		t.Fatalf("thumbnail.url = %v, want %q", thumbnail["url"], "https://public.example/images/preview.png")
	}

	contact, ok := resp["contact"].(map[string]any)
	if !ok {
		t.Fatalf("contact missing or invalid: %T", resp["contact"])
	}
	if contact["email"] != "admin@public.example" {
		t.Fatalf("contact.email = %v, want %q", contact["email"], "admin@public.example")
	}

	rules, ok := resp["rules"].([]any)
	if !ok {
		t.Fatalf("rules missing or invalid: %T", resp["rules"])
	}
	if len(rules) != 3 {
		t.Fatalf("rules len = %d, want 3", len(rules))
	}
}
