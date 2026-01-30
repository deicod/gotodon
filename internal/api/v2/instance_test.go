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

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
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
}
