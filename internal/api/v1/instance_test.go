package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type stubInstanceQueries struct {
	settings    []db.GetSettingsByVarsRow
	rules       []db.Rule
	userCount   int64
	statusCount int64
	domainCount int64
	account     db.Account
}

func (s *stubInstanceQueries) GetSettingsByVars(_ context.Context, _ []string) ([]db.GetSettingsByVarsRow, error) {
	return s.settings, nil
}

func (s *stubInstanceQueries) ListRules(_ context.Context) ([]db.Rule, error) {
	return s.rules, nil
}

func (s *stubInstanceQueries) CountInstanceUsers(_ context.Context) (int64, error) {
	return s.userCount, nil
}

func (s *stubInstanceQueries) CountInstanceStatuses(_ context.Context) (int64, error) {
	return s.statusCount, nil
}

func (s *stubInstanceQueries) CountInstanceDomains(_ context.Context) (int64, error) {
	return s.domainCount, nil
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
	}

	h := NewInstanceHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil)
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["uri"] != "local.example" {
		t.Fatalf("uri = %v, want %q", resp["uri"], "local.example")
	}

	if resp["version"] != "4.5.3" {
		t.Fatalf("version = %v, want %q", resp["version"], "4.5.3")
	}

	urls, ok := resp["urls"].(map[string]any)
	if !ok {
		t.Fatalf("urls missing or invalid: %T", resp["urls"])
	}
	if urls["streaming_api"] != "wss://public.example" {
		t.Fatalf("urls.streaming_api = %v, want %q", urls["streaming_api"], "wss://public.example")
	}

	thumbnail, ok := resp["thumbnail"].(string)
	if !ok {
		t.Fatalf("thumbnail missing or invalid: %T", resp["thumbnail"])
	}
	if thumbnail != "https://public.example/images/preview.png" {
		t.Fatalf("thumbnail = %v, want %q", thumbnail, "https://public.example/images/preview.png")
	}

	if resp["registrations"] != true {
		t.Fatalf("registrations = %v, want true", resp["registrations"])
	}
	if resp["approval_required"] != true {
		t.Fatalf("approval_required = %v, want true", resp["approval_required"])
	}

	stats, ok := resp["stats"].(map[string]any)
	if !ok {
		t.Fatalf("stats missing or invalid: %T", resp["stats"])
	}
	if stats["user_count"] != float64(0) {
		t.Fatalf("stats.user_count = %v, want 0", stats["user_count"])
	}

	languages, ok := resp["languages"].([]any)
	if !ok || len(languages) != 1 {
		t.Fatalf("languages missing or invalid: %T", resp["languages"])
	}
	if languages[0] != "en" {
		t.Fatalf("languages[0] = %v, want %q", languages[0], "en")
	}
}

func TestInstanceHandler_Get_WithSettings(t *testing.T) {
	settings := []db.GetSettingsByVarsRow{
		{Var: settingSiteTitle, Value: pgtype.Text{String: "Custom", Valid: true}},
		{Var: settingSiteDescription, Value: pgtype.Text{String: "Long desc", Valid: true}},
		{Var: settingSiteShortDescription, Value: pgtype.Text{String: "Short desc", Valid: true}},
		{Var: settingSiteLanguages, Value: pgtype.Text{String: `["es","fr"]`, Valid: true}},
		{Var: settingRegistrationsEnabled, Value: pgtype.Text{String: "false", Valid: true}},
		{Var: settingApprovalRequired, Value: pgtype.Text{String: "false", Valid: true}},
		{Var: settingInvitesEnabled, Value: pgtype.Text{String: "false", Valid: true}},
		{Var: settingMaxTootChars, Value: pgtype.Text{String: "700", Valid: true}},
		{Var: settingMaxMediaAttachments, Value: pgtype.Text{String: "5", Valid: true}},
		{Var: settingMaxPollOptions, Value: pgtype.Text{String: "6", Valid: true}},
		{Var: settingMaxPollOptionChars, Value: pgtype.Text{String: "70", Valid: true}},
		{Var: settingMaxImageSize, Value: pgtype.Text{String: "12345", Valid: true}},
		{Var: settingMaxVideoSize, Value: pgtype.Text{String: "54321", Valid: true}},
		{Var: settingSiteContactEmail, Value: pgtype.Text{String: "staff@example.com", Valid: true}},
		{Var: settingSiteContactUsername, Value: pgtype.Text{String: "admin", Valid: true}},
	}

	account := db.Account{ID: 1, Username: "admin", DisplayName: "Admin", Note: "", Uri: "https://local.example/@admin"}
	queries := &stubInstanceQueries{
		settings:    settings,
		rules:       []db.Rule{{ID: 1, Text: "Rule one", Hint: ""}},
		userCount:   12,
		statusCount: 34,
		domainCount: 5,
		account:     account,
	}

	h := NewInstanceHandler(queries, &config.Config{Instance: config.InstanceConfig{LocalDomain: "local.example"}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance", nil)
	rec := httptest.NewRecorder()

	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["title"] != "Custom" {
		t.Fatalf("title = %v, want %q", resp["title"], "Custom")
	}
	if resp["short_description"] != "Short desc" {
		t.Fatalf("short_description = %v, want %q", resp["short_description"], "Short desc")
	}
	if resp["description"] != "Long desc" {
		t.Fatalf("description = %v, want %q", resp["description"], "Long desc")
	}
	if resp["email"] != "staff@example.com" {
		t.Fatalf("email = %v, want %q", resp["email"], "staff@example.com")
	}
	if resp["registrations"] != false {
		t.Fatalf("registrations = %v, want false", resp["registrations"])
	}
	if resp["approval_required"] != false {
		t.Fatalf("approval_required = %v, want false", resp["approval_required"])
	}
	if resp["invites_enabled"] != false {
		t.Fatalf("invites_enabled = %v, want false", resp["invites_enabled"])
	}

	stats, ok := resp["stats"].(map[string]any)
	if !ok {
		t.Fatalf("stats missing or invalid: %T", resp["stats"])
	}
	if stats["user_count"] != float64(12) {
		t.Fatalf("stats.user_count = %v, want 12", stats["user_count"])
	}
	if stats["status_count"] != float64(34) {
		t.Fatalf("stats.status_count = %v, want 34", stats["status_count"])
	}
	if stats["domain_count"] != float64(5) {
		t.Fatalf("stats.domain_count = %v, want 5", stats["domain_count"])
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

	contactAccount, ok := resp["contact_account"].(map[string]any)
	if !ok {
		t.Fatalf("contact_account missing or invalid: %T", resp["contact_account"])
	}
	if contactAccount["username"] != "admin" {
		t.Fatalf("contact_account.username = %v, want %q", contactAccount["username"], "admin")
	}

	rules, ok := resp["rules"].([]any)
	if !ok || len(rules) != 1 {
		t.Fatalf("rules missing or invalid: %T", resp["rules"])
	}
}
