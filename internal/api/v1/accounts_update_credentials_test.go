package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type stubAccountUpdateQueries struct {
	db.Querier
	updateCalled         bool
	updateParams         db.UpdateAccountParams
	updateSettingsCalled bool
	updateSettingsParams db.UpdateUserSettingsParams
	user                 db.User
}

func (s *stubAccountUpdateQueries) UpdateAccount(ctx context.Context, arg db.UpdateAccountParams) (db.Account, error) {
	s.updateCalled = true
	s.updateParams = arg
	return db.Account{ID: arg.ID, Username: "alice", Fields: arg.Fields}, nil
}

func (s *stubAccountUpdateQueries) GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error) {
	if s.user.ID != 0 {
		return s.user, nil
	}
	return db.User{}, pgx.ErrNoRows
}

func (s *stubAccountUpdateQueries) UpdateUserSettings(ctx context.Context, arg db.UpdateUserSettingsParams) error {
	s.updateSettingsCalled = true
	s.updateSettingsParams = arg
	if s.user.ID != 0 {
		s.user.Settings = arg.Settings
	}
	return nil
}

func (s *stubAccountUpdateQueries) ListAccountStatsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.AccountStat, error) {
	return nil, nil
}

func (s *stubAccountUpdateQueries) ListUserSettingsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserSettingsByAccountIDsRow, error) {
	return nil, nil
}

func (s *stubAccountUpdateQueries) ListUserRolesByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserRolesByAccountIDsRow, error) {
	return nil, nil
}

func (s *stubAccountUpdateQueries) ListCustomEmojisByShortcodesAndDomain(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainParams) ([]db.CustomEmoji, error) {
	return nil, nil
}

func (s *stubAccountUpdateQueries) GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error) {
	return nil, nil
}

func (s *stubAccountUpdateQueries) GetAccountsByUsernames(ctx context.Context, usernames []string) ([]db.Account, error) {
	return nil, nil
}

func (s *stubAccountUpdateQueries) GetAccountsByUsernamesAndDomains(ctx context.Context, arg db.GetAccountsByUsernamesAndDomainsParams) ([]db.Account, error) {
	return nil, nil
}

func (s *stubAccountUpdateQueries) CountFollowRequestsForAccount(ctx context.Context, targetAccountID int64) (int64, error) {
	return 0, nil
}

func (s *stubAccountUpdateQueries) GetUserRole(ctx context.Context, userID int64) (db.UserRole, error) {
	return db.UserRole{}, pgx.ErrNoRows
}

func TestAccountsHandler_UpdateCredentials_UnauthorizedWithoutAccountContext(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAccountsHandler_UpdateCredentials_ForbiddenWithWrongScope(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read read:accounts")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	var errResp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if got, want := errResp["error"], "This action is outside the authorized scopes"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestAccountsHandler_UpdateCredentials_AllowsWriteScope(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "write")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAccountsHandler_UpdateCredentials_AllowsWriteAccountsScope(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "write:accounts")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAccountsHandler_UpdateCredentials_AllowsEmptyScopesForCompatibility(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAccountsHandler_UpdateCredentials_FieldsAttributesJSON(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"fields_attributes":[{"name":"Site","value":"https://example.com"},{"name":"Chat","value":"hello"}]}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !stub.updateCalled {
		t.Fatal("expected UpdateAccount to be called")
	}
	fields := decodeUpdateFields(t, stub.updateParams.Fields)
	if len(fields) != 2 {
		t.Fatalf("fields length = %d, want 2", len(fields))
	}
	if fields[0].Name != "Site" || fields[0].Value != "https://example.com" {
		t.Fatalf("field[0] = %+v, want Site/https://example.com", fields[0])
	}
	if fields[1].Name != "Chat" || fields[1].Value != "hello" {
		t.Fatalf("field[1] = %+v, want Chat/hello", fields[1])
	}
}

func TestAccountsHandler_UpdateCredentials_FieldsAttributesForm(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	values := url.Values{}
	values.Set("fields_attributes[0][name]", "Site")
	values.Set("fields_attributes[0][value]", "https://example.com")
	values.Set("fields_attributes[1][name]", "Chat")
	values.Set("fields_attributes[1][value]", "hello")

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	fields := decodeUpdateFields(t, stub.updateParams.Fields)
	if len(fields) != 2 {
		t.Fatalf("fields length = %d, want 2", len(fields))
	}
	if fields[0].Name != "Site" || fields[0].Value != "https://example.com" {
		t.Fatalf("field[0] = %+v, want Site/https://example.com", fields[0])
	}
	if fields[1].Name != "Chat" || fields[1].Value != "hello" {
		t.Fatalf("field[1] = %+v, want Chat/hello", fields[1])
	}
}

func TestAccountsHandler_UpdateCredentials_FieldsAttributesLimit(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	values := url.Values{}
	for i := 0; i < maxProfileFields+1; i++ {
		values.Set("fields_attributes["+strconv.Itoa(i)+"][name]", "Field "+strconv.Itoa(i))
		values.Set("fields_attributes["+strconv.Itoa(i)+"][value]", "Value "+strconv.Itoa(i))
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	fields := decodeUpdateFields(t, stub.updateParams.Fields)
	if len(fields) != maxProfileFields {
		t.Fatalf("fields length = %d, want %d", len(fields), maxProfileFields)
	}
}

func TestAccountsHandler_UpdateCredentials_SourceJSON(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"source":{"privacy":"private","sensitive":true,"language":"fr"}}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !stub.updateSettingsCalled {
		t.Fatal("expected UpdateUserSettings to be called")
	}
	settings := settingsFromText(stub.updateSettingsParams.Settings)
	if settings["default_privacy"] != "private" {
		t.Fatalf("default_privacy = %v, want private", settings["default_privacy"])
	}
	if settings["default_sensitive"] != true {
		t.Fatalf("default_sensitive = %v, want true", settings["default_sensitive"])
	}
	if settings["default_language"] != "fr" {
		t.Fatalf("default_language = %v, want fr", settings["default_language"])
	}
}

func TestAccountsHandler_UpdateCredentials_SourceQuotePolicyJSON(t *testing.T) {
	stub := &stubAccountUpdateQueries{
		user: db.User{
			ID:        1,
			AccountID: 1,
		},
	}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"source":{"quote_policy":"followers"}}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !stub.updateSettingsCalled {
		t.Fatal("expected UpdateUserSettings to be called")
	}
	settings := settingsFromText(stub.updateSettingsParams.Settings)
	if settings["default_quote_policy"] != "followers" {
		t.Fatalf("default_quote_policy = %v, want followers", settings["default_quote_policy"])
	}

	var resp CredentialAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Source.QuotePolicy != "followers" {
		t.Fatalf("response source.quote_policy = %q, want followers", resp.Source.QuotePolicy)
	}
}

func TestAccountsHandler_UpdateCredentials_SourceForm(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	values := url.Values{}
	values.Set("source[privacy]", "public")
	values.Set("source[sensitive]", "false")
	values.Set("source[language]", "en")

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !stub.updateSettingsCalled {
		t.Fatal("expected UpdateUserSettings to be called")
	}
	settings := settingsFromText(stub.updateSettingsParams.Settings)
	if settings["default_privacy"] != "public" {
		t.Fatalf("default_privacy = %v, want public", settings["default_privacy"])
	}
	if settings["default_sensitive"] != false {
		t.Fatalf("default_sensitive = %v, want false", settings["default_sensitive"])
	}
	if settings["default_language"] != "en" {
		t.Fatalf("default_language = %v, want en", settings["default_language"])
	}
}

func TestAccountsHandler_UpdateCredentials_SourceQuotePolicyForm(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	values := url.Values{}
	values.Set("source[quote_policy]", "nobody")

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !stub.updateSettingsCalled {
		t.Fatal("expected UpdateUserSettings to be called")
	}
	settings := settingsFromText(stub.updateSettingsParams.Settings)
	if settings["default_quote_policy"] != "nobody" {
		t.Fatalf("default_quote_policy = %v, want nobody", settings["default_quote_policy"])
	}
}

func TestAccountsHandler_UpdateCredentials_SourceQuotePolicyInvalid(t *testing.T) {
	stub := &stubAccountUpdateQueries{
		user: db.User{
			ID:        1,
			AccountID: 1,
			Settings: pgtype.Text{
				String: `{"default_quote_policy":"followers"}`,
				Valid:  true,
			},
		},
	}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"source":{"quote_policy":"invalid"}}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if stub.updateSettingsCalled {
		t.Fatal("expected UpdateUserSettings not to be called")
	}
	if stub.updateCalled {
		t.Fatal("expected UpdateAccount not to be called")
	}
	var errResp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if got, want := errResp["error"], "Validation failed: Quote policy is invalid"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestAccountsHandler_UpdateCredentials_SourceQuotePolicyEmptyClearsSetting(t *testing.T) {
	stub := &stubAccountUpdateQueries{
		user: db.User{
			ID:        1,
			AccountID: 1,
			Settings: pgtype.Text{
				String: `{"default_quote_policy":"followers","default_privacy":"private"}`,
				Valid:  true,
			},
		},
	}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"source":{"quote_policy":""}}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !stub.updateSettingsCalled {
		t.Fatal("expected UpdateUserSettings to be called")
	}
	settings := settingsFromText(stub.updateSettingsParams.Settings)
	if _, ok := settings["default_quote_policy"]; ok {
		t.Fatalf("default_quote_policy still present: %v", settings["default_quote_policy"])
	}
	if settings["default_privacy"] != "private" {
		t.Fatalf("default_privacy = %v, want private", settings["default_privacy"])
	}
}

func TestAccountsHandler_UpdateCredentials_SourceQuotePolicyEmptyFormClearsSetting(t *testing.T) {
	stub := &stubAccountUpdateQueries{
		user: db.User{
			ID:        1,
			AccountID: 1,
			Settings: pgtype.Text{
				String: `{"default_quote_policy":"followers","default_privacy":"private"}`,
				Valid:  true,
			},
		},
	}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	values := url.Values{}
	values.Set("source[quote_policy]", "")

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !stub.updateSettingsCalled {
		t.Fatal("expected UpdateUserSettings to be called")
	}
	settings := settingsFromText(stub.updateSettingsParams.Settings)
	if _, ok := settings["default_quote_policy"]; ok {
		t.Fatalf("default_quote_policy still present: %v", settings["default_quote_policy"])
	}
	if settings["default_privacy"] != "private" {
		t.Fatalf("default_privacy = %v, want private", settings["default_privacy"])
	}
}

func TestAccountsHandler_UpdateCredentials_AttributionDomains(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"attribution_domains":["Example.com","  example.com  ","foo.test",""]}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := stub.updateParams.AttributionDomains; len(got) != 2 || got[0] != "example.com" || got[1] != "foo.test" {
		t.Fatalf("attribution_domains = %v, want [example.com foo.test]", got)
	}
}

func TestAccountsHandler_UpdateCredentials_AttributionDomainsForm(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	values := url.Values{}
	values.Add("attribution_domains[]", "Example.com")
	values.Add("attribution_domains[]", "example.com")
	values.Add("attribution_domains[]", "foo.test")

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := stub.updateParams.AttributionDomains; len(got) != 2 || got[0] != "example.com" || got[1] != "foo.test" {
		t.Fatalf("attribution_domains = %v, want [example.com foo.test]", got)
	}
}

func decodeUpdateFields(t *testing.T, raw []byte) []UpdateCredentialsField {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var fields []UpdateCredentialsField
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal fields: %v", err)
	}
	return fields
}
