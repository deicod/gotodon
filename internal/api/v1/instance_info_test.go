package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
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

func TestInstanceInfoHandler_Activity_Counts(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	queries := &stubInstanceInfoQueries{
		statusCount:       12,
		loginCount:        5,
		registrationCount: 3,
	}
	h := NewInstanceInfoHandler(queries, cfg)

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

	if len(weeks) == 0 {
		t.Fatalf("weeks length = 0, want > 0")
	}

	if weeks[0].Statuses != "12" || weeks[0].Logins != "5" || weeks[0].Registrations != "3" {
		t.Fatalf("week counts = %#v, want statuses=12 logins=5 registrations=3", weeks[0])
	}
}
func TestInstanceInfoHandler_ExtendedDescription(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	updatedAt := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	queries := &stubInstanceInfoQueries{
		settings: []db.GetSettingsByVarsRow{
			{
				Var:       settingSiteExtendedDescription,
				Value:     pgtype.Text{String: "Welcome to **example**.", Valid: true},
				UpdatedAt: pgtype.Timestamptz{Time: updatedAt, Valid: true},
			},
		},
	}
	h := NewInstanceInfoHandler(queries, cfg)

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

	if desc.UpdatedAt == nil || !desc.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated_at = %v, want %v", desc.UpdatedAt, updatedAt)
	}
	if !strings.Contains(desc.Content, "<strong>example</strong>") {
		t.Fatalf("content missing rendered markdown: %q", desc.Content)
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

	var policy PrivacyPolicy
	if err := json.Unmarshal(rec.Body.Bytes(), &policy); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if policy.UpdatedAt == nil || policy.UpdatedAt.Format("2006-01-02") != "2022-10-07" {
		t.Fatalf("updated_at = %v, want 2022-10-07", policy.UpdatedAt)
	}
	if !strings.Contains(policy.Content, "example.com") {
		t.Fatalf("content missing domain substitution: %q", policy.Content)
	}
	if !strings.Contains(policy.Content, "This privacy policy describes how") {
		t.Fatalf("content missing expected text: %q", policy.Content)
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

type stubInstanceInfoQueries struct {
	settings          []db.GetSettingsByVarsRow
	statusCount       int64
	loginCount        int64
	registrationCount int64
	domainBlocks      []db.DomainBlock
}

func (s *stubInstanceInfoQueries) GetSettingsByVars(ctx context.Context, vars []string) ([]db.GetSettingsByVarsRow, error) {
	return s.settings, nil
}

func (s *stubInstanceInfoQueries) ListRules(ctx context.Context) ([]db.Rule, error) {
	return nil, nil
}

func (s *stubInstanceInfoQueries) GetKnownDomains(ctx context.Context) ([]pgtype.Text, error) {
	return nil, nil
}

func (s *stubInstanceInfoQueries) CountStatusesInRange(ctx context.Context, arg db.CountStatusesInRangeParams) (int64, error) {
	return s.statusCount, nil
}

func (s *stubInstanceInfoQueries) CountUserRegistrationsInRange(ctx context.Context, arg db.CountUserRegistrationsInRangeParams) (int64, error) {
	return s.registrationCount, nil
}

func (s *stubInstanceInfoQueries) CountUserLoginsInRange(ctx context.Context, arg db.CountUserLoginsInRangeParams) (int64, error) {
	return s.loginCount, nil
}

func (s *stubInstanceInfoQueries) ListPublicDomainBlocks(ctx context.Context) ([]db.DomainBlock, error) {
	return s.domainBlocks, nil
}

func (s *stubInstanceInfoQueries) GetTermsOfServiceByEffectiveDate(ctx context.Context, effectiveDate pgtype.Date) (db.TermsOfService, error) {
	return db.TermsOfService{}, pgx.ErrNoRows
}

func (s *stubInstanceInfoQueries) GetCurrentTermsOfService(ctx context.Context) (db.TermsOfService, error) {
	return db.TermsOfService{}, pgx.ErrNoRows
}

func (s *stubInstanceInfoQueries) GetUpcomingTermsOfService(ctx context.Context) (db.TermsOfService, error) {
	return db.TermsOfService{}, pgx.ErrNoRows
}

func (s *stubInstanceInfoQueries) GetTermsOfServiceSucceededBy(ctx context.Context, arg db.GetTermsOfServiceSucceededByParams) (db.TermsOfService, error) {
	return db.TermsOfService{}, pgx.ErrNoRows
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

func TestInstanceInfoHandler_DomainBlocks_Disabled(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewInstanceInfoHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/domain_blocks", nil)
	rec := httptest.NewRecorder()

	h.DomainBlocksPublic(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestInstanceInfoHandler_DomainBlocks_All(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	queries := &stubInstanceInfoQueries{
		settings: []db.GetSettingsByVarsRow{
			{
				Var:   settingShowDomainBlocks,
				Value: pgtype.Text{String: "all", Valid: true},
			},
			{
				Var:   settingShowDomainBlocksRationale,
				Value: pgtype.Text{String: "all", Valid: true},
			},
		},
		domainBlocks: []db.DomainBlock{
			{
				Domain:        "example.org",
				Severity:      0,
				Obfuscate:     true,
				PublicComment: pgtype.Text{String: "Bad stuff", Valid: true},
			},
		},
	}
	h := NewInstanceInfoHandler(queries, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/domain_blocks", nil)
	rec := httptest.NewRecorder()

	h.DomainBlocksPublic(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var blocks []InstanceDomainBlock
	if err := json.Unmarshal(rec.Body.Bytes(), &blocks); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks length = %d, want 1", len(blocks))
	}
	if blocks[0].Digest == "" {
		t.Fatalf("digest should not be empty")
	}
	if blocks[0].Domain == "example.org" || !strings.Contains(blocks[0].Domain, "*") {
		t.Fatalf("expected obfuscated domain, got %q", blocks[0].Domain)
	}
	if blocks[0].Severity != "silence" {
		t.Fatalf("severity = %q, want %q", blocks[0].Severity, "silence")
	}
	if blocks[0].Comment != "Bad stuff" {
		t.Fatalf("comment = %q, want %q", blocks[0].Comment, "Bad stuff")
	}
}

func TestInstanceInfoHandler_DomainBlocks_UsersRequiresAuth(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	queries := &stubInstanceInfoQueries{
		settings: []db.GetSettingsByVarsRow{
			{
				Var:   settingShowDomainBlocks,
				Value: pgtype.Text{String: "users", Valid: true},
			},
			{
				Var:   settingShowDomainBlocksRationale,
				Value: pgtype.Text{String: "users", Valid: true},
			},
		},
		domainBlocks: []db.DomainBlock{
			{
				Domain:        "blocked.test",
				Severity:      1,
				PublicComment: pgtype.Text{String: "Hidden", Valid: true},
			},
		},
	}
	h := NewInstanceInfoHandler(queries, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/instance/domain_blocks", nil)
	rec := httptest.NewRecorder()

	h.DomainBlocksPublic(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	reqAuth := httptest.NewRequest(http.MethodGet, "/api/v1/instance/domain_blocks", nil)
	ctx := context.WithValue(reqAuth.Context(), middleware.ContextKeyAccountID, int64(123))
	reqAuth = reqAuth.WithContext(ctx)
	recAuth := httptest.NewRecorder()

	h.DomainBlocksPublic(recAuth, reqAuth)

	if recAuth.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", recAuth.Code, http.StatusOK)
	}

	var blocks []InstanceDomainBlock
	if err := json.Unmarshal(recAuth.Body.Bytes(), &blocks); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Severity != "suspend" {
		t.Fatalf("blocks = %#v, want suspend block", blocks)
	}
	if blocks[0].Comment == "" {
		t.Fatalf("expected comment for authenticated user")
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
