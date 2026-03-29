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

func TestAccountsHandler_UpdateCredentials_FieldsAttributesOverLimitReturnsMastodonValidationPayload(t *testing.T) {
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

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if stub.updateCalled {
		t.Fatal("expected UpdateAccount not to be called")
	}
	if stub.updateSettingsCalled {
		t.Fatal("expected UpdateUserSettings not to be called")
	}

	var got struct {
		Error   string `json:"error"`
		Details struct {
			Fields []struct {
				Error       string `json:"error"`
				Description string `json:"description"`
			} `json:"fields"`
		} `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !strings.HasPrefix(got.Error, "Validation failed") {
		t.Fatalf("error = %q, want prefix %q", got.Error, "Validation failed")
	}
	if len(got.Details.Fields) != 1 {
		t.Fatalf("details.fields length = %d, want 1", len(got.Details.Fields))
	}
	if got.Details.Fields[0].Error != "ERR_TOO_LONG" {
		t.Fatalf("details.fields[0].error = %q, want %q", got.Details.Fields[0].Error, "ERR_TOO_LONG")
	}
	if !strings.Contains(got.Details.Fields[0].Description, "field limit of 4 exceeded") {
		t.Fatalf("details.fields[0].description = %q, want to mention %q", got.Details.Fields[0].Description, "field limit of 4 exceeded")
	}
}

func TestAccountsHandler_UpdateCredentials_FieldsAttributesAtLimitPersistsAllFields(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	values := url.Values{}
	for i := 0; i < maxProfileFields; i++ {
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
	if !stub.updateCalled {
		t.Fatal("expected UpdateAccount to be called")
	}
	fields := decodeUpdateFields(t, stub.updateParams.Fields)
	if len(fields) != maxProfileFields {
		t.Fatalf("fields length = %d, want %d", len(fields), maxProfileFields)
	}
	for i := 0; i < maxProfileFields; i++ {
		if fields[i].Name != "Field "+strconv.Itoa(i) || fields[i].Value != "Value "+strconv.Itoa(i) {
			t.Fatalf("field[%d] = %+v, want Field %d/Value %d", i, fields[i], i, i)
		}
	}
}

func TestAccountsHandler_UpdateCredentials_FieldValueWithoutNameReturnsMastodonValidationPayload(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"fields_attributes":[{"name":"   ","value":"https://example.com"}]}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if stub.updateCalled {
		t.Fatal("expected UpdateAccount not to be called")
	}
	if stub.updateSettingsCalled {
		t.Fatal("expected UpdateUserSettings not to be called")
	}

	var got struct {
		Error   string `json:"error"`
		Details struct {
			FieldsWithValuesMissingLabels []struct {
				Error       string `json:"error"`
				Description string `json:"description"`
			} `json:"fields_with_values_missing_labels"`
		} `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !strings.HasPrefix(got.Error, "Validation failed") {
		t.Fatalf("error = %q, want prefix %q", got.Error, "Validation failed")
	}
	if len(got.Details.FieldsWithValuesMissingLabels) != 1 {
		t.Fatalf("details.fields_with_values_missing_labels length = %d, want 1", len(got.Details.FieldsWithValuesMissingLabels))
	}
	if got.Details.FieldsWithValuesMissingLabels[0].Error != "ERR_BLANK" {
		t.Fatalf("details.fields_with_values_missing_labels[0].error = %q, want %q", got.Details.FieldsWithValuesMissingLabels[0].Error, "ERR_BLANK")
	}
}

func TestAccountsHandler_UpdateCredentials_FullyBlankFieldRowIgnored(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"fields_attributes":[{"name":" ","value":" "},{"name":"Site","value":"https://example.com"}]}`
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
	if len(fields) != 1 {
		t.Fatalf("fields length = %d, want 1", len(fields))
	}
	if fields[0].Name != "Site" || fields[0].Value != "https://example.com" {
		t.Fatalf("field[0] = %+v, want Site/https://example.com", fields[0])
	}
}

func TestAccountsHandler_UpdateCredentials_ValidNamedFieldStillSucceeds(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"fields_attributes":[{"name":"Site","value":"https://example.com"}]}`
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
	if len(fields) != 1 {
		t.Fatalf("fields length = %d, want 1", len(fields))
	}
	if fields[0].Name != "Site" || fields[0].Value != "https://example.com" {
		t.Fatalf("field[0] = %+v, want Site/https://example.com", fields[0])
	}
}

func TestAccountsHandler_UpdateCredentials_NoteTooLongReturnsMastodonValidationPayload(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"note":"` + strings.Repeat("a", 2*accountNoteLengthLimit) + `"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if stub.updateCalled {
		t.Fatal("expected UpdateAccount not to be called")
	}
	if stub.updateSettingsCalled {
		t.Fatal("expected UpdateUserSettings not to be called")
	}

	var got struct {
		Error   string `json:"error"`
		Details struct {
			Note []struct {
				Error       string `json:"error"`
				Description string `json:"description"`
			} `json:"note"`
		} `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !strings.HasPrefix(got.Error, "Validation failed") {
		t.Fatalf("error = %q, want prefix %q", got.Error, "Validation failed")
	}
	if len(got.Details.Note) != 1 {
		t.Fatalf("details.note length = %d, want 1", len(got.Details.Note))
	}
	if got.Details.Note[0].Error != "ERR_TOO_LONG" {
		t.Fatalf("details.note[0].error = %q, want %q", got.Details.Note[0].Error, "ERR_TOO_LONG")
	}
	if !strings.Contains(got.Details.Note[0].Description, "character limit of 500 exceeded") {
		t.Fatalf("details.note[0].description = %q, want to mention %q", got.Details.Note[0].Description, "character limit of 500 exceeded")
	}
}

func TestAccountsHandler_UpdateCredentials_DisplayNameTooLongReturnsMastodonValidationPayload(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"display_name":"` + strings.Repeat("a", accountDisplayNameMax+1) + `"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if stub.updateCalled {
		t.Fatal("expected UpdateAccount not to be called")
	}
	if stub.updateSettingsCalled {
		t.Fatal("expected UpdateUserSettings not to be called")
	}

	var got struct {
		Error   string `json:"error"`
		Details struct {
			DisplayName []struct {
				Error       string `json:"error"`
				Description string `json:"description"`
			} `json:"display_name"`
		} `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !strings.HasPrefix(got.Error, "Validation failed") {
		t.Fatalf("error = %q, want prefix %q", got.Error, "Validation failed")
	}
	if len(got.Details.DisplayName) != 1 {
		t.Fatalf("details.display_name length = %d, want 1", len(got.Details.DisplayName))
	}
	if got.Details.DisplayName[0].Error != "ERR_TOO_LONG" {
		t.Fatalf("details.display_name[0].error = %q, want %q", got.Details.DisplayName[0].Error, "ERR_TOO_LONG")
	}
	if !strings.Contains(got.Details.DisplayName[0].Description, "character limit of 30 exceeded") {
		t.Fatalf("details.display_name[0].description = %q, want to mention %q", got.Details.DisplayName[0].Description, "character limit of 30 exceeded")
	}
}

func TestAccountsHandler_UpdateCredentials_NoteLengthBoundaryValidUpdatesAccount(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	note := strings.Repeat("a", accountNoteLengthLimit)
	body := `{"note":"` + note + `"}`
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
	if got := stub.updateParams.Note.String; got != note {
		t.Fatalf("updated note = %q, want %q", got, note)
	}
}

func TestAccountsHandler_UpdateCredentials_DisplayNameLengthBoundaryValidUpdatesAccount(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	displayName := strings.Repeat("a", accountDisplayNameMax)
	body := `{"display_name":"` + displayName + `"}`
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
	if got := stub.updateParams.DisplayName.String; got != displayName {
		t.Fatalf("updated display_name = %q, want %q", got, displayName)
	}
}

func TestAccountNoteLength_URLTrailingPunctuationIsPreserved(t *testing.T) {
	got := accountNoteLength("https://example.com.")
	want := len(accountNoteURLPlaceholder) + 1
	if got != want {
		t.Fatalf("accountNoteLength = %d, want %d", got, want)
	}
}

func TestAccountNoteLength_URLTrailingCommaIsPreserved(t *testing.T) {
	got := accountNoteLength("https://example.com,")
	want := len(accountNoteURLPlaceholder) + 1
	if got != want {
		t.Fatalf("accountNoteLength = %d, want %d", got, want)
	}
}

func TestAccountNoteLength_URLPathExclamationRemainsWeightedAsURL(t *testing.T) {
	got := accountNoteLength("https://example.com/!")
	want := len(accountNoteURLPlaceholder)
	if got != want {
		t.Fatalf("accountNoteLength = %d, want %d", got, want)
	}
}

func TestAccountNoteLength_URLPathTrailingDotIsPreserved(t *testing.T) {
	got := accountNoteLength("https://example.com/path.")
	want := len(accountNoteURLPlaceholder) + 1
	if got != want {
		t.Fatalf("accountNoteLength = %d, want %d", got, want)
	}
}

func TestAccountNoteLength_RemoteMentionTrailingPunctuationIsPreserved(t *testing.T) {
	got := accountNoteLength("@alice@example.com.")
	want := len("@alice.")
	if got != want {
		t.Fatalf("accountNoteLength = %d, want %d", got, want)
	}
}

func TestAccountNoteLength_RemoteMentionSingleLabelDomainIsNormalized(t *testing.T) {
	got := accountNoteLength("@alice@example")
	want := len("@alice")
	if got != want {
		t.Fatalf("accountNoteLength = %d, want %d", got, want)
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
	var got struct {
		Error   string `json:"error"`
		Details struct {
			QuotePolicy []struct {
				Error       string `json:"error"`
				Description string `json:"description"`
			} `json:"quote_policy"`
		} `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !strings.HasPrefix(got.Error, "Validation failed") {
		t.Fatalf("error = %q, want prefix %q", got.Error, "Validation failed")
	}
	if len(got.Details.QuotePolicy) != 1 {
		t.Fatalf("details.quote_policy length = %d, want 1", len(got.Details.QuotePolicy))
	}
	if got.Details.QuotePolicy[0].Error != "ERR_INCLUSION" {
		t.Fatalf("details.quote_policy[0].error = %q, want %q", got.Details.QuotePolicy[0].Error, "ERR_INCLUSION")
	}
	if got.Details.QuotePolicy[0].Description != "is not included in the list" {
		t.Fatalf("details.quote_policy[0].description = %q, want %q", got.Details.QuotePolicy[0].Description, "is not included in the list")
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

	body := `{"attribution_domains":["https://Example.com","http://foo.example","*.bar.example","  example.com  ",""]}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := stub.updateParams.AttributionDomains; len(got) != 3 || got[0] != "example.com" || got[1] != "foo.example" || got[2] != "bar.example" {
		t.Fatalf("attribution_domains = %v, want [example.com foo.example bar.example]", got)
	}
}

func TestAccountsHandler_UpdateCredentials_AttributionDomainsForm(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	values := url.Values{}
	values.Add("attribution_domains[]", "https://Example.com")
	values.Add("attribution_domains[]", "example.com")
	values.Add("attribution_domains[]", "http://foo.test")
	values.Add("attribution_domains[]", "*.bar.test")

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := stub.updateParams.AttributionDomains; len(got) != 3 || got[0] != "example.com" || got[1] != "foo.test" || got[2] != "bar.test" {
		t.Fatalf("attribution_domains = %v, want [example.com foo.test bar.test]", got)
	}
}

func TestAccountsHandler_UpdateCredentials_AttributionDomainsAcceptsIDN(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"attribution_domains":["https://bücher.example"]}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := stub.updateParams.AttributionDomains; len(got) != 1 || got[0] != "bücher.example" {
		t.Fatalf("attribution_domains = %v, want [bücher.example]", got)
	}
}

func TestAccountsHandler_UpdateCredentials_AttributionDomainsAcceptsTrailingDot(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"attribution_domains":["example.com."]}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := stub.updateParams.AttributionDomains; len(got) != 1 || got[0] != "example.com." {
		t.Fatalf("attribution_domains = %v, want [example.com.]", got)
	}
}

func TestAccountsHandler_UpdateCredentials_AttributionDomainsMaxLengthAccepted(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	label := strings.Repeat("a", 63)
	domain := strings.Join([]string{label, label, label, label}, ".")
	body := `{"attribution_domains":["` + domain + `"]}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := stub.updateParams.AttributionDomains; len(got) != 1 || got[0] != domain {
		t.Fatalf("attribution_domains = %v, want [%s]", got, domain)
	}
}

func TestAccountsHandler_UpdateCredentials_AttributionDomainsInvalidReturnsMastodonValidationPayload(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "contains spaces",
			body: `{"attribution_domains":["example com"]}`,
		},
		{
			name: "invalid symbol",
			body: `{"attribution_domains":["@"]}`,
		},
		{
			name: "consecutive dots",
			body: `{"attribution_domains":["example..com"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubAccountUpdateQueries{}
			h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

			req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()

			h.UpdateCredentials(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
			}
			if stub.updateCalled {
				t.Fatal("expected UpdateAccount not to be called")
			}
			if stub.updateSettingsCalled {
				t.Fatal("expected UpdateUserSettings not to be called")
			}

			var got struct {
				Error   string `json:"error"`
				Details struct {
					AttributionDomains []struct {
						Error       string `json:"error"`
						Description string `json:"description"`
					} `json:"attribution_domains"`
				} `json:"details"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if !strings.HasPrefix(got.Error, "Validation failed") {
				t.Fatalf("error = %q, want prefix %q", got.Error, "Validation failed")
			}
			if len(got.Details.AttributionDomains) != 1 {
				t.Fatalf("details.attribution_domains length = %d, want 1", len(got.Details.AttributionDomains))
			}
			if got.Details.AttributionDomains[0].Error != "ERR_INVALID" {
				t.Fatalf("details.attribution_domains[0].error = %q, want %q", got.Details.AttributionDomains[0].Error, "ERR_INVALID")
			}
			if got.Details.AttributionDomains[0].Description != "is invalid" {
				t.Fatalf("details.attribution_domains[0].description = %q, want %q", got.Details.AttributionDomains[0].Description, "is invalid")
			}
		})
	}
}

func TestAccountsHandler_UpdateCredentials_AttributionDomainsOverLimitReturnsMastodonValidationPayload(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	domains := make([]string, 0, attributionDomainsLimit+1)
	for i := 0; i < attributionDomainsLimit+1; i++ {
		domains = append(domains, "domain"+strconv.Itoa(i)+".example")
	}
	bodyBytes, err := json.Marshal(map[string]any{
		"attribution_domains": domains,
	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if stub.updateCalled {
		t.Fatal("expected UpdateAccount not to be called")
	}
	if stub.updateSettingsCalled {
		t.Fatal("expected UpdateUserSettings not to be called")
	}

	var got struct {
		Error   string `json:"error"`
		Details struct {
			AttributionDomains []struct {
				Error       string `json:"error"`
				Description string `json:"description"`
			} `json:"attribution_domains"`
		} `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !strings.HasPrefix(got.Error, "Validation failed") {
		t.Fatalf("error = %q, want prefix %q", got.Error, "Validation failed")
	}
	if len(got.Details.AttributionDomains) != 1 {
		t.Fatalf("details.attribution_domains length = %d, want 1", len(got.Details.AttributionDomains))
	}
	if got.Details.AttributionDomains[0].Error != "ERR_TOO_LONG" {
		t.Fatalf("details.attribution_domains[0].error = %q, want %q", got.Details.AttributionDomains[0].Error, "ERR_TOO_LONG")
	}
	if !strings.Contains(got.Details.AttributionDomains[0].Description, "attribution domains limit of 100 exceeded") {
		t.Fatalf("details.attribution_domains[0].description = %q, want to mention %q", got.Details.AttributionDomains[0].Description, "attribution domains limit of 100 exceeded")
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
