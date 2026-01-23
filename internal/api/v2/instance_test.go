package v2

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type stubInstanceQueries struct {
	settings    []db.GetSettingsByVarsRow
	rules       []db.Rule
	account     db.Account
	activeUsers int64
	hasTerms    bool
}

func (s *stubInstanceQueries) GetSettingsByVars(_ context.Context, _ []string) ([]db.GetSettingsByVarsRow, error) {
	return s.settings, nil
}

func (s *stubInstanceQueries) ListRules(_ context.Context) ([]db.Rule, error) {
	return s.rules, nil
}

func (s *stubInstanceQueries) GetCurrentTermsOfService(_ context.Context) (db.TermsOfService, error) {
	if s.hasTerms {
		return db.TermsOfService{}, nil
	}
	return db.TermsOfService{}, errors.New("not found")
}

func (s *stubInstanceQueries) CountActiveUsers(_ context.Context) (int64, error) {
	return s.activeUsers, nil
}

func (s *stubInstanceQueries) GetAccountByUsername(_ context.Context, _ string) (db.Account, error) {
	return s.account, nil
}

func (s *stubInstanceQueries) GetAccountByUsernameAndDomain(_ context.Context, _ db.GetAccountByUsernameAndDomainParams) (db.Account, error) {
	return s.account, nil
}

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

	accounts, ok := configuration["accounts"].(map[string]any)
	if !ok {
		t.Fatalf("configuration.accounts missing or invalid: %T", configuration["accounts"])
	}
	if accounts["max_profile_fields"] != float64(4) {
		t.Fatalf("accounts.max_profile_fields = %v, want 4", accounts["max_profile_fields"])
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

func TestInstanceHandler_Get_Settings(t *testing.T) {
	langs, err := json.Marshal([]string{"es", "fr"})
	if err != nil {
		t.Fatalf("marshal languages: %v", err)
	}

	createdAt := time.Date(2024, time.March, 4, 0, 0, 0, 0, time.UTC)
	queries := &stubInstanceQueries{
		settings: []db.GetSettingsByVarsRow{
			{Var: settingSiteTitle, Value: pgtype.Text{String: "Custom Title", Valid: true}},
			{Var: settingSiteDescription, Value: pgtype.Text{String: "Custom description", Valid: true}},
			{Var: settingSiteLanguages, Value: pgtype.Text{String: string(langs), Valid: true}},
			{Var: settingRegistrationsEnabled, Value: pgtype.Text{String: "false", Valid: true}},
			{Var: settingApprovalRequired, Value: pgtype.Text{String: "true", Valid: true}},
			{Var: settingMaxTootChars, Value: pgtype.Text{String: "700", Valid: true}},
			{Var: settingMaxMediaAttachments, Value: pgtype.Text{String: "5", Valid: true}},
			{Var: settingMaxImageSize, Value: pgtype.Text{String: "12345", Valid: true}},
			{Var: settingMaxVideoSize, Value: pgtype.Text{String: "54321", Valid: true}},
			{Var: settingMaxPollOptions, Value: pgtype.Text{String: "6", Valid: true}},
			{Var: settingMaxPollOptionChars, Value: pgtype.Text{String: "70", Valid: true}},
			{Var: settingSiteContactEmail, Value: pgtype.Text{String: "support@example.com", Valid: true}},
			{Var: settingSiteContactUsername, Value: pgtype.Text{String: "admin", Valid: true}},
		},
		rules: []db.Rule{{ID: 1, Text: "Be kind"}},
		account: db.Account{
			ID:          42,
			Username:    "admin",
			DisplayName: "Admin",
			CreatedAt:   pgtype.Timestamptz{Time: createdAt, Valid: true},
			Domain:      pgtype.Text{Valid: false},
		},
		activeUsers: 12,
		hasTerms:    true,
	}

	cfg := &config.Config{
		Instance: config.InstanceConfig{
			LocalDomain:       "local.example",
			WebDomain:         "public.example",
			RegistrationsMode: "open",
		},
		Secrets: config.SecretsConfig{
			VAPIDPublic: "vapid-public",
		},
	}

	h := NewInstanceHandler(queries, cfg)

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

	if resp["title"] != "Custom Title" {
		t.Fatalf("title = %v, want %q", resp["title"], "Custom Title")
	}
	if resp["description"] != "Custom description" {
		t.Fatalf("description = %v, want %q", resp["description"], "Custom description")
	}

	usage, ok := resp["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage missing or invalid: %T", resp["usage"])
	}
	users, ok := usage["users"].(map[string]any)
	if !ok {
		t.Fatalf("usage.users missing or invalid: %T", usage["users"])
	}
	if users["active_month"] != float64(12) {
		t.Fatalf("usage.users.active_month = %v, want 12", users["active_month"])
	}

	languages, ok := resp["languages"].([]any)
	if !ok || len(languages) != 2 {
		t.Fatalf("languages missing or invalid: %T", resp["languages"])
	}
	if languages[0] != "es" {
		t.Fatalf("languages[0] = %v, want %q", languages[0], "es")
	}
	if languages[1] != "fr" {
		t.Fatalf("languages[1] = %v, want %q", languages[1], "fr")
	}

	configuration, ok := resp["configuration"].(map[string]any)
	if !ok {
		t.Fatalf("configuration missing or invalid: %T", resp["configuration"])
	}

	statuses, ok := configuration["statuses"].(map[string]any)
	if !ok {
		t.Fatalf("configuration.statuses missing or invalid: %T", configuration["statuses"])
	}
	if statuses["max_characters"] != float64(700) {
		t.Fatalf("statuses.max_characters = %v, want 700", statuses["max_characters"])
	}
	if statuses["max_media_attachments"] != float64(5) {
		t.Fatalf("statuses.max_media_attachments = %v, want 5", statuses["max_media_attachments"])
	}

	mediaAttachments, ok := configuration["media_attachments"].(map[string]any)
	if !ok {
		t.Fatalf("configuration.media_attachments missing or invalid: %T", configuration["media_attachments"])
	}
	if mediaAttachments["image_size_limit"] != float64(12345) {
		t.Fatalf("media_attachments.image_size_limit = %v, want 12345", mediaAttachments["image_size_limit"])
	}
	if mediaAttachments["video_size_limit"] != float64(54321) {
		t.Fatalf("media_attachments.video_size_limit = %v, want 54321", mediaAttachments["video_size_limit"])
	}

	polls, ok := configuration["polls"].(map[string]any)
	if !ok {
		t.Fatalf("configuration.polls missing or invalid: %T", configuration["polls"])
	}
	if polls["max_options"] != float64(6) {
		t.Fatalf("polls.max_options = %v, want 6", polls["max_options"])
	}
	if polls["max_characters_per_option"] != float64(70) {
		t.Fatalf("polls.max_characters_per_option = %v, want 70", polls["max_characters_per_option"])
	}

	urls, ok := configuration["urls"].(map[string]any)
	if !ok {
		t.Fatalf("configuration.urls missing or invalid: %T", configuration["urls"])
	}
	if urls["terms_of_service"] != "https://public.example/terms-of-service" {
		t.Fatalf("urls.terms_of_service = %v, want %q", urls["terms_of_service"], "https://public.example/terms-of-service")
	}

	contact, ok := resp["contact"].(map[string]any)
	if !ok {
		t.Fatalf("contact missing or invalid: %T", resp["contact"])
	}
	if contact["email"] != "support@example.com" {
		t.Fatalf("contact.email = %v, want %q", contact["email"], "support@example.com")
	}
	contactAccount, ok := contact["account"].(map[string]any)
	if !ok {
		t.Fatalf("contact.account missing or invalid: %T", contact["account"])
	}
	if contactAccount["username"] != "admin" {
		t.Fatalf("contact.account.username = %v, want %q", contactAccount["username"], "admin")
	}

	rules, ok := resp["rules"].([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("rules missing or invalid: %T", resp["rules"])
	}
}
