package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

func TestInstanceInfoHandler_Rules_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/rules", nil)
	rec := httptest.NewRecorder()

	h.Rules(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var rules []RuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rules); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(rules) != 3 {
		t.Errorf("rules length = %d, want 3", len(rules))
	}
}

func TestInstanceInfoHandler_Peers_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/peers", nil)
	rec := httptest.NewRecorder()

	h.Peers(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var peers []string
	if err := json.Unmarshal(rec.Body.Bytes(), &peers); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Mock mode returns empty list
	if len(peers) != 0 {
		t.Errorf("peers length = %d, want 0", len(peers))
	}
}

func TestInstanceInfoHandler_Activity_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/activity", nil)
	rec := httptest.NewRecorder()

	h.Activity(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var weeks []ActivityWeek
	if err := json.Unmarshal(rec.Body.Bytes(), &weeks); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(weeks) != 12 {
		t.Errorf("weeks length = %d, want 12", len(weeks))
	}

	// Check that weeks have proper structure
	for _, week := range weeks {
		if week.Week == "" {
			t.Error("week timestamp should not be empty")
		}
	}
}

func TestInstanceInfoHandler_ExtendedDescription(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/extended_description", nil)
	rec := httptest.NewRecorder()

	h.ExtendedDescriptionHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var desc ExtendedDescription
	if err := json.Unmarshal(rec.Body.Bytes(), &desc); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
}

func TestInstanceInfoHandler_PrivacyPolicy(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/privacy_policy", nil)
	rec := httptest.NewRecorder()

	h.PrivacyPolicyHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestInstanceInfoHandler_TermsOfService_NotFound_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/terms_of_service", nil)
	rec := httptest.NewRecorder()

	h.TermsOfServiceHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestInstanceInfoHandler_TermsOfServiceByDate_InvalidDate(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/terms_of_service/bad-date", nil)
	req.SetPathValue("date", "bad-date")
	rec := httptest.NewRecorder()

	h.TermsOfServiceByDate(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestInstanceInfoHandler_TermsOfServiceByDate_NotFound_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/terms_of_service/2025-01-01", nil)
	req.SetPathValue("date", "2025-01-01")
	rec := httptest.NewRecorder()

	h.TermsOfServiceByDate(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestInstanceInfoHandler_TranslationLanguages(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/translation_languages", nil)
	rec := httptest.NewRecorder()

	h.TranslationLanguages(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var langs map[string][]string
	if err := json.Unmarshal(rec.Body.Bytes(), &langs); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
}

func TestInstanceInfoHandler_Languages(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/languages", nil)
	rec := httptest.NewRecorder()

	h.Languages(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var langs []string
	if err := json.Unmarshal(rec.Body.Bytes(), &langs); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(langs) == 0 {
		t.Error("expected at least one language")
	}
}

func TestInstanceInfoHandler_DomainBlocks(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/domain_blocks", nil)
	rec := httptest.NewRecorder()

	h.DomainBlocksPublic(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestInstanceInfoHandler_SerializeTermsOfService_EffectiveToday(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	now := time.Now().UTC()
	tos := db.TermsOfService{
		ID:            1,
		Text:          "Terms",
		EffectiveDate: pgtype.Date{Time: now, Valid: true},
	}

	resp := h.serializeTermsOfService(context.Background(), tos)
	if !resp.Effective {
		t.Fatalf("effective = false, want true")
	}

	expectedDate := now.Format("2006-01-02")
	if resp.EffectiveDate != expectedDate {
		t.Fatalf("effective_date = %q, want %q", resp.EffectiveDate, expectedDate)
	}
}

func TestInstanceInfoHandler_SerializeTermsOfService_PublishedNoEffectiveDate(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	now := time.Now().UTC()
	tos := db.TermsOfService{
		ID:          2,
		Text:        "Terms",
		PublishedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}

	resp := h.serializeTermsOfService(context.Background(), tos)
	if !resp.Effective {
		t.Fatalf("effective = false, want true")
	}

	expectedDate := now.Format(time.RFC3339)
	if resp.EffectiveDate != expectedDate {
		t.Fatalf("effective_date = %q, want %q", resp.EffectiveDate, expectedDate)
	}
}

func TestRuleResponseStruct(t *testing.T) {
	rule := RuleResponse{
		ID:   "1",
		Text: "Be respectful",
		Hint: "This includes all interactions",
	}

	data, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("Failed to marshal RuleResponse: %v", err)
	}

	var unmarshaled RuleResponse
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("Failed to unmarshal RuleResponse: %v", err)
	}

	if unmarshaled.ID != rule.ID {
		t.Errorf("id = %q, want %q", unmarshaled.ID, rule.ID)
	}
	if unmarshaled.Text != rule.Text {
		t.Errorf("text = %q, want %q", unmarshaled.Text, rule.Text)
	}
	if unmarshaled.Hint != rule.Hint {
		t.Errorf("hint = %q, want %q", unmarshaled.Hint, rule.Hint)
	}
}
