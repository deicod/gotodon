package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type stubEndorsementsQueries struct {
	accounts      map[int64]db.Account
	pinsByAccount map[int64][]db.ListAccountPinsRow
	nextPinID     int64
}

func newStubEndorsementsQueries() *stubEndorsementsQueries {
	return &stubEndorsementsQueries{
		accounts:      make(map[int64]db.Account),
		pinsByAccount: make(map[int64][]db.ListAccountPinsRow),
		nextPinID:     1,
	}
}

func (s *stubEndorsementsQueries) ListAccountPins(ctx context.Context, accountID int64) ([]db.ListAccountPinsRow, error) {
	return append([]db.ListAccountPinsRow{}, s.pinsByAccount[accountID]...), nil
}

func (s *stubEndorsementsQueries) CreateAccountPin(ctx context.Context, arg db.CreateAccountPinParams) (db.AccountPin, error) {
	pins := s.pinsByAccount[arg.AccountID]
	for _, pin := range pins {
		if pin.TargetAccountID == arg.TargetAccountID {
			return db.AccountPin{}, nil
		}
	}
	acc, ok := s.accounts[arg.TargetAccountID]
	if !ok {
		return db.AccountPin{}, pgx.ErrNoRows
	}
	row := db.ListAccountPinsRow{
		ID:              s.nextPinID,
		AccountID:       arg.AccountID,
		TargetAccountID: arg.TargetAccountID,
		Username:        acc.Username,
		Domain:          acc.Domain,
		DisplayName:     acc.DisplayName,
		AvatarFileName:  acc.AvatarFileName,
		AvatarRemoteUrl: acc.AvatarRemoteUrl,
		HeaderFileName:  acc.HeaderFileName,
		HeaderRemoteUrl: acc.HeaderRemoteUrl,
		Note:            acc.Note,
		Uri:             acc.Uri,
		Url:             acc.Url,
	}
	s.nextPinID++
	s.pinsByAccount[arg.AccountID] = append([]db.ListAccountPinsRow{row}, pins...)
	return db.AccountPin{}, nil
}

func (s *stubEndorsementsQueries) DeleteAccountPin(ctx context.Context, arg db.DeleteAccountPinParams) error {
	pins := s.pinsByAccount[arg.AccountID]
	filtered := make([]db.ListAccountPinsRow, 0, len(pins))
	for _, pin := range pins {
		if pin.TargetAccountID != arg.TargetAccountID {
			filtered = append(filtered, pin)
		}
	}
	s.pinsByAccount[arg.AccountID] = filtered
	return nil
}

func (s *stubEndorsementsQueries) GetAccountByID(ctx context.Context, id int64) (db.Account, error) {
	acc, ok := s.accounts[id]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return acc, nil
}

func (s *stubEndorsementsQueries) ListAccountStatsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.AccountStat, error) {
	return nil, nil
}

func (s *stubEndorsementsQueries) ListUserSettingsByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserSettingsByAccountIDsRow, error) {
	return nil, nil
}

func (s *stubEndorsementsQueries) ListUserRolesByAccountIDs(ctx context.Context, accountIds []int64) ([]db.ListUserRolesByAccountIDsRow, error) {
	return nil, nil
}

func (s *stubEndorsementsQueries) ListCustomEmojisByShortcodesAndDomain(ctx context.Context, arg db.ListCustomEmojisByShortcodesAndDomainParams) ([]db.CustomEmoji, error) {
	return nil, nil
}

func (s *stubEndorsementsQueries) GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error) {
	return nil, nil
}

func (s *stubEndorsementsQueries) GetAccountsByUsernames(ctx context.Context, usernames []string) ([]db.Account, error) {
	return nil, nil
}

func (s *stubEndorsementsQueries) GetAccountsByUsernamesAndDomains(ctx context.Context, arg db.GetAccountsByUsernamesAndDomainsParams) ([]db.Account, error) {
	return nil, nil
}

func TestEndorsementsHandler_EndorseAliases(t *testing.T) {
	stub := newStubEndorsementsQueries()
	stub.accounts[456] = db.Account{
		ID:          456,
		Username:    "user456",
		DisplayName: "User 456",
		Url:         pgtype.Text{String: "https://example.com/@user456", Valid: true},
		Uri:         "https://example.com/users/user456",
	}
	h := NewEndorsementsHandler(stub, &config.Config{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	accountID := int64(123)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/456/endorse", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, accountID))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/endorsements", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, accountID))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var accounts []Account
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(accounts) != 1 || accounts[0].ID != "456" {
		t.Fatalf("unexpected endorsements: %+v", accounts)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/456/unendorse", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, accountID))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/endorsements", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyAccountID, accountID))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accounts); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("expected empty endorsements, got %d", len(accounts))
	}
}

func TestEndorsementsHandler_EndorseAlias_RequiresAuth(t *testing.T) {
	stub := newStubEndorsementsQueries()
	h := NewEndorsementsHandler(stub, &config.Config{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/456/endorse", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
