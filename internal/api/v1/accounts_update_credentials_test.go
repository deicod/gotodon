package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
)

type stubAccountUpdateQueries struct {
	db.Querier
	updateCalled         bool
	updateParams         db.UpdateAccountParams
	updateErr            error
	updateSettingsCalled bool
	updateSettingsParams db.UpdateUserSettingsParams
	account              db.Account
	getAccountErr        error
	user                 db.User
}

type stubProfileMediaUploader struct {
	avatarPath       string
	headerPath       string
	avatarErr        error
	headerErr        error
	calls            []stubProfileUploadCall
	deletedPaths     []string
	deleteErr        error
	forceGenericPath string
}

type stubProfileUploadCall struct {
	AccountID int64
	Filename  string
	Data      []byte
}

func (s *stubProfileMediaUploader) Upload(ctx context.Context, accountID int64, filename string, data []byte) (*media.UploadResult, error) {
	call := stubProfileUploadCall{
		AccountID: accountID,
		Filename:  filename,
		Data:      append([]byte(nil), data...),
	}
	s.calls = append(s.calls, call)

	switch filename {
	case "avatar.png":
		if s.avatarErr != nil {
			return nil, s.avatarErr
		}
		path := s.avatarPath
		if path == "" {
			path = "media_attachments/files/1/avatar.png"
		}
		return &media.UploadResult{Path: path, ContentType: media.DetectContentType(data), Size: int64(len(data))}, nil
	case "header.png":
		if s.headerErr != nil {
			return nil, s.headerErr
		}
		path := s.headerPath
		if path == "" {
			path = "media_attachments/files/1/header.png"
		}
		return &media.UploadResult{Path: path, ContentType: media.DetectContentType(data), Size: int64(len(data))}, nil
	default:
		if s.forceGenericPath != "" {
			return &media.UploadResult{Path: s.forceGenericPath, ContentType: media.DetectContentType(data), Size: int64(len(data))}, nil
		}
		return &media.UploadResult{Path: "media_attachments/files/1/" + filename, ContentType: media.DetectContentType(data), Size: int64(len(data))}, nil
	}
}

func (s *stubProfileMediaUploader) Delete(ctx context.Context, path string) error {
	s.deletedPaths = append(s.deletedPaths, path)
	return s.deleteErr
}

func (s *stubAccountUpdateQueries) UpdateAccount(ctx context.Context, arg db.UpdateAccountParams) (db.Account, error) {
	s.updateCalled = true
	s.updateParams = arg
	if s.updateErr != nil {
		return db.Account{}, s.updateErr
	}
	return db.Account{
		ID:                arg.ID,
		Username:          "alice",
		DisplayName:       arg.DisplayName.String,
		Fields:            arg.Fields,
		AvatarFileName:    arg.AvatarFileName,
		AvatarContentType: arg.AvatarContentType,
		AvatarFileSize:    arg.AvatarFileSize,
		AvatarUpdatedAt:   arg.AvatarUpdatedAt,
		HeaderFileName:    arg.HeaderFileName,
		HeaderContentType: arg.HeaderContentType,
		HeaderFileSize:    arg.HeaderFileSize,
		HeaderUpdatedAt:   arg.HeaderUpdatedAt,
		AvatarRemoteUrl:   arg.AvatarRemoteUrl,
		HeaderRemoteUrl:   arg.HeaderRemoteUrl.String,
	}, nil
}

func (s *stubAccountUpdateQueries) GetUserByAccountID(ctx context.Context, accountID int64) (db.User, error) {
	if s.user.ID != 0 {
		return s.user, nil
	}
	return db.User{}, pgx.ErrNoRows
}

func (s *stubAccountUpdateQueries) GetAccountByID(ctx context.Context, id int64) (db.Account, error) {
	if s.getAccountErr != nil {
		return db.Account{}, s.getAccountErr
	}
	if s.account.ID != 0 {
		return s.account, nil
	}
	return db.Account{}, pgx.ErrNoRows
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

func TestAccountsHandler_UpdateCredentials_PreservesVerifiedAtWhenFieldValueUnchanged(t *testing.T) {
	verifiedAt := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	existingFields, err := json.Marshal([]Field{
		{
			Name:       "Website",
			Value:      "https://example.com",
			VerifiedAt: &verifiedAt,
		},
	})
	if err != nil {
		t.Fatalf("marshal existing fields: %v", err)
	}
	stub := &stubAccountUpdateQueries{
		account: db.Account{
			ID:     1,
			Fields: existingFields,
		},
	}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"fields_attributes":[{"name":"Website","value":"https://example.com"}]}`
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
	fields := decodeStoredFields(t, stub.updateParams.Fields)
	if len(fields) != 1 {
		t.Fatalf("fields length = %d, want 1", len(fields))
	}
	if fields[0].VerifiedAt == nil {
		t.Fatal("expected verified_at to be preserved")
	}
	if !fields[0].VerifiedAt.Equal(verifiedAt) {
		t.Fatalf("verified_at = %s, want %s", fields[0].VerifiedAt.UTC(), verifiedAt.UTC())
	}
}

func TestAccountsHandler_UpdateCredentials_DropsVerifiedAtWhenFieldValueChanges(t *testing.T) {
	verifiedAt := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	existingFields, err := json.Marshal([]Field{
		{
			Name:       "Website",
			Value:      "https://example.com",
			VerifiedAt: &verifiedAt,
		},
	})
	if err != nil {
		t.Fatalf("marshal existing fields: %v", err)
	}
	stub := &stubAccountUpdateQueries{
		account: db.Account{
			ID:     1,
			Fields: existingFields,
		},
	}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"fields_attributes":[{"name":"Website","value":"https://new.example.com"}]}`
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
	fields := decodeStoredFields(t, stub.updateParams.Fields)
	if len(fields) != 1 {
		t.Fatalf("fields length = %d, want 1", len(fields))
	}
	if fields[0].VerifiedAt != nil {
		t.Fatalf("verified_at = %v, want nil after value change", fields[0].VerifiedAt)
	}
}

func TestAccountsHandler_UpdateCredentials_AccountLookupFailureReturns500AndDoesNotUpdate(t *testing.T) {
	stub := &stubAccountUpdateQueries{
		getAccountErr: errors.New("db unavailable"),
	}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	body := `{"fields_attributes":[{"name":"Website","value":"https://example.com"}]}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "write:accounts")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if stub.updateCalled {
		t.Fatal("expected UpdateAccount to not be called")
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

func TestAccountsHandler_UpdateCredentials_MultipartAvatarHeaderUploads(t *testing.T) {
	stub := &stubAccountUpdateQueries{
		account: db.Account{
			ID:              1,
			Username:        "alice",
			AvatarRemoteUrl: pgtype.Text{String: "https://cdn.example/avatar-remote.png", Valid: true},
			HeaderRemoteUrl: "https://cdn.example/header-remote.png",
		},
	}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)
	uploader := &stubProfileMediaUploader{
		avatarPath: "media_attachments/files/1/2026/03/29/avatar-local.png",
		headerPath: "media_attachments/files/1/2026/03/29/header-local.png",
	}
	h.SetProfileMediaUploader(uploader)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	avatarPart, err := writer.CreateFormFile("avatar", "avatar.png")
	if err != nil {
		t.Fatalf("create avatar form file: %v", err)
	}
	if _, err := avatarPart.Write(minimalPNGBytes()); err != nil {
		t.Fatalf("write avatar content: %v", err)
	}

	headerPart, err := writer.CreateFormFile("header", "header.png")
	if err != nil {
		t.Fatalf("create header form file: %v", err)
	}
	if _, err := headerPart.Write(minimalPNGBytes()); err != nil {
		t.Fatalf("write header content: %v", err)
	}

	if err := writer.WriteField("display_name", "Alice Updated"); err != nil {
		t.Fatalf("write display_name field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "write:accounts")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !stub.updateCalled {
		t.Fatal("expected UpdateAccount to be called")
	}
	if got := stub.updateParams.DisplayName.String; got != "Alice Updated" {
		t.Fatalf("display_name = %q, want %q", got, "Alice Updated")
	}
	if !stub.updateParams.AvatarFileName.Valid || stub.updateParams.AvatarFileName.String != uploader.avatarPath {
		t.Fatalf("avatar_file_name = %+v, want valid path %q", stub.updateParams.AvatarFileName, uploader.avatarPath)
	}
	if !stub.updateParams.AvatarContentType.Valid || stub.updateParams.AvatarContentType.String != "image/png" {
		t.Fatalf("avatar_content_type = %+v, want %q", stub.updateParams.AvatarContentType, "image/png")
	}
	if !stub.updateParams.AvatarFileSize.Valid || stub.updateParams.AvatarFileSize.Int32 != int32(len(minimalPNGBytes())) {
		t.Fatalf("avatar_file_size = %+v, want %d", stub.updateParams.AvatarFileSize, len(minimalPNGBytes()))
	}
	if !stub.updateParams.AvatarUpdatedAt.Valid {
		t.Fatalf("avatar_updated_at = %+v, want valid timestamp", stub.updateParams.AvatarUpdatedAt)
	}
	if !stub.updateParams.HeaderFileName.Valid || stub.updateParams.HeaderFileName.String != uploader.headerPath {
		t.Fatalf("header_file_name = %+v, want valid path %q", stub.updateParams.HeaderFileName, uploader.headerPath)
	}
	if !stub.updateParams.HeaderContentType.Valid || stub.updateParams.HeaderContentType.String != "image/png" {
		t.Fatalf("header_content_type = %+v, want %q", stub.updateParams.HeaderContentType, "image/png")
	}
	if !stub.updateParams.HeaderFileSize.Valid || stub.updateParams.HeaderFileSize.Int32 != int32(len(minimalPNGBytes())) {
		t.Fatalf("header_file_size = %+v, want %d", stub.updateParams.HeaderFileSize, len(minimalPNGBytes()))
	}
	if !stub.updateParams.HeaderUpdatedAt.Valid {
		t.Fatalf("header_updated_at = %+v, want valid timestamp", stub.updateParams.HeaderUpdatedAt)
	}
	if !stub.updateParams.AvatarRemoteUrl.Valid || stub.updateParams.AvatarRemoteUrl.String != "" {
		t.Fatalf("avatar_remote_url = %+v, want explicit empty string", stub.updateParams.AvatarRemoteUrl)
	}
	if !stub.updateParams.HeaderRemoteUrl.Valid || stub.updateParams.HeaderRemoteUrl.String != "" {
		t.Fatalf("header_remote_url = %+v, want explicit empty string", stub.updateParams.HeaderRemoteUrl)
	}
	if len(uploader.calls) != 2 {
		t.Fatalf("uploader calls = %d, want 2", len(uploader.calls))
	}

	var resp CredentialAccount
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if strings.Contains(resp.Avatar, "cdn.example") {
		t.Fatalf("avatar response should not use stale remote URL: %q", resp.Avatar)
	}
	if strings.Contains(resp.Header, "cdn.example") {
		t.Fatalf("header response should not use stale remote URL: %q", resp.Header)
	}
	if !strings.Contains(resp.Avatar, uploader.avatarPath) {
		t.Fatalf("avatar = %q, want to contain %q", resp.Avatar, uploader.avatarPath)
	}
	if !strings.Contains(resp.Header, uploader.headerPath) {
		t.Fatalf("header = %q, want to contain %q", resp.Header, uploader.headerPath)
	}
}

func TestAccountsHandler_UpdateCredentials_MultipartReplacementDeletesPreviousLocalMedia(t *testing.T) {
	stub := &stubAccountUpdateQueries{
		account: db.Account{
			ID:             1,
			Username:       "alice",
			AvatarFileName: pgtype.Text{String: "media_attachments/files/1/old-avatar.png", Valid: true},
			HeaderFileName: pgtype.Text{String: "media_attachments/files/1/old-header.png", Valid: true},
		},
	}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)
	uploader := &stubProfileMediaUploader{
		avatarPath: "media_attachments/files/1/new-avatar.png",
		headerPath: "media_attachments/files/1/new-header.png",
	}
	h.SetProfileMediaUploader(uploader)

	req := multipartUpdateCredentialsRequest(t, map[string][]byte{
		"avatar": minimalPNGBytes(),
		"header": minimalPNGBytes(),
	}, nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if len(uploader.deletedPaths) != 2 ||
		uploader.deletedPaths[0] != stub.account.AvatarFileName.String ||
		uploader.deletedPaths[1] != stub.account.HeaderFileName.String {
		t.Fatalf("deleted paths = %#v, want [%q %q]", uploader.deletedPaths, stub.account.AvatarFileName.String, stub.account.HeaderFileName.String)
	}
}

func TestAccountsHandler_UpdateCredentials_MultipartUploadWithoutUploaderReturns500(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)

	req := multipartUpdateCredentialsRequest(t, map[string][]byte{"avatar": minimalPNGBytes()}, nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if stub.updateCalled {
		t.Fatal("expected UpdateAccount not to be called")
	}
}

func TestAccountsHandler_UpdateCredentials_MultipartUploadValidationErrorsReturn422(t *testing.T) {
	tests := []struct {
		name       string
		uploadErr  error
		wantSubstr string
	}{
		{
			name:       "unsupported type",
			uploadErr:  media.ErrUnsupportedType,
			wantSubstr: "File type is not supported",
		},
		{
			name:       "too large",
			uploadErr:  media.ErrFileTooLarge,
			wantSubstr: "File is too large",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubAccountUpdateQueries{}
			h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)
			uploader := &stubProfileMediaUploader{
				avatarErr: tt.uploadErr,
			}
			h.SetProfileMediaUploader(uploader)

			req := multipartUpdateCredentialsRequest(t, map[string][]byte{"avatar": minimalPNGBytes()}, nil)
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
			if !strings.Contains(rec.Body.String(), tt.wantSubstr) {
				t.Fatalf("body = %q, want to contain %q", rec.Body.String(), tt.wantSubstr)
			}
		})
	}
}

func TestAccountsHandler_UpdateCredentials_MultipartValidationHappensBeforeUpload(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)
	uploader := &stubProfileMediaUploader{}
	h.SetProfileMediaUploader(uploader)

	req := multipartUpdateCredentialsRequest(t, map[string][]byte{"avatar": minimalPNGBytes()}, map[string]string{
		"display_name": strings.Repeat("x", accountDisplayNameMax+1),
	})
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if len(uploader.calls) != 0 {
		t.Fatalf("uploader calls = %d, want 0", len(uploader.calls))
	}
	if stub.updateCalled {
		t.Fatal("expected UpdateAccount not to be called")
	}
}

func TestAccountsHandler_UpdateCredentials_MultipartHeaderFailureCleansUpAvatar(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)
	uploader := &stubProfileMediaUploader{
		avatarPath: "media_attachments/files/1/avatar.png",
		headerErr:  media.ErrUnsupportedType,
	}
	h.SetProfileMediaUploader(uploader)

	req := multipartUpdateCredentialsRequest(t, map[string][]byte{
		"avatar": minimalPNGBytes(),
		"header": minimalPNGBytes(),
	}, nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if len(uploader.deletedPaths) != 1 || uploader.deletedPaths[0] != uploader.avatarPath {
		t.Fatalf("deleted paths = %#v, want [%q]", uploader.deletedPaths, uploader.avatarPath)
	}
	if stub.updateCalled {
		t.Fatal("expected UpdateAccount not to be called")
	}
}

func TestAccountsHandler_UpdateCredentials_MultipartUpdateFailureCleansUpUploads(t *testing.T) {
	stub := &stubAccountUpdateQueries{updateErr: errors.New("update failed")}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)
	uploader := &stubProfileMediaUploader{
		avatarPath: "media_attachments/files/1/avatar.png",
		headerPath: "media_attachments/files/1/header.png",
	}
	h.SetProfileMediaUploader(uploader)

	req := multipartUpdateCredentialsRequest(t, map[string][]byte{
		"avatar": minimalPNGBytes(),
		"header": minimalPNGBytes(),
	}, nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if len(uploader.deletedPaths) != 2 || uploader.deletedPaths[0] != uploader.avatarPath || uploader.deletedPaths[1] != uploader.headerPath {
		t.Fatalf("deleted paths = %#v, want [%q %q]", uploader.deletedPaths, uploader.avatarPath, uploader.headerPath)
	}
}

func TestAccountsHandler_UpdateCredentials_MultipartAvatarOverProfileLimitReturns422(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)
	uploader := &stubProfileMediaUploader{}
	h.SetProfileMediaUploader(uploader)

	req := multipartUpdateCredentialsRequest(t, map[string][]byte{
		"avatar": make([]byte, int(profileMediaFileSizeLimit)+1),
	}, nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if !strings.Contains(rec.Body.String(), "File is too large") {
		t.Fatalf("body = %q, want to contain %q", rec.Body.String(), "File is too large")
	}
	if len(uploader.calls) != 0 {
		t.Fatalf("uploader calls = %d, want 0", len(uploader.calls))
	}
}

func TestAccountsHandler_UpdateCredentials_MultipartInvalidBooleanReturnsBadRequest(t *testing.T) {
	stub := &stubAccountUpdateQueries{}
	h := NewAccountsHandler(stub, nil, &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}, nil)
	uploader := &stubProfileMediaUploader{}
	h.SetProfileMediaUploader(uploader)

	req := multipartUpdateCredentialsRequest(t, nil, map[string]string{"locked": "definitely-not-bool"})
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, int64(1))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.UpdateCredentials(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if stub.updateCalled {
		t.Fatal("expected UpdateAccount not to be called")
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

func decodeStoredFields(t *testing.T, raw []byte) []Field {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var fields []Field
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal stored fields: %v", err)
	}
	return fields
}

func multipartUpdateCredentialsRequest(t *testing.T, files map[string][]byte, fields map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	for fieldName, fileData := range files {
		part, err := writer.CreateFormFile(fieldName, fieldName+".png")
		if err != nil {
			t.Fatalf("create form file %q: %v", fieldName, err)
		}
		if _, err := part.Write(fileData); err != nil {
			t.Fatalf("write form file %q: %v", fieldName, err)
		}
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write form field %q: %v", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func minimalPNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
		0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
		0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
		0x44, 0xae, 0x42, 0x60, 0x82,
	}
}
