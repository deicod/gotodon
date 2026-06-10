package authweb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
)

type stubQuerier struct {
	usersByConfirmationToken map[string]db.User
	usersByEmail             map[string]db.User
	usersByResetToken        map[string]db.User
	accountsByID             map[int64]db.Account
	staff                    []db.User

	confirmedIDs     []int64
	confirmationSets []db.UpdateUserConfirmationTokenParams
	resetTokenSets   []db.UpdateUserResetPasswordTokenParams
	passwordSets     []db.UpdateUserPasswordParams
	revokedTokenIDs  []pgtype.Int8
	staffPermission  int64
}

func newStubQuerier() *stubQuerier {
	return &stubQuerier{
		usersByConfirmationToken: map[string]db.User{},
		usersByEmail:             map[string]db.User{},
		usersByResetToken:        map[string]db.User{},
		accountsByID:             map[int64]db.Account{},
	}
}

func (s *stubQuerier) GetUserByConfirmationToken(_ context.Context, token pgtype.Text) (db.User, error) {
	user, ok := s.usersByConfirmationToken[token.String]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *stubQuerier) ConfirmUser(_ context.Context, id int64) error {
	s.confirmedIDs = append(s.confirmedIDs, id)
	return nil
}

func (s *stubQuerier) GetUserByEmail(_ context.Context, email string) (db.User, error) {
	user, ok := s.usersByEmail[strings.ToLower(email)]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *stubQuerier) UpdateUserConfirmationToken(_ context.Context, arg db.UpdateUserConfirmationTokenParams) error {
	s.confirmationSets = append(s.confirmationSets, arg)
	return nil
}

func (s *stubQuerier) UpdateUserResetPasswordToken(_ context.Context, arg db.UpdateUserResetPasswordTokenParams) error {
	s.resetTokenSets = append(s.resetTokenSets, arg)
	return nil
}

func (s *stubQuerier) GetUserByResetPasswordToken(_ context.Context, token pgtype.Text) (db.User, error) {
	user, ok := s.usersByResetToken[token.String]
	if !ok {
		return db.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *stubQuerier) UpdateUserPassword(_ context.Context, arg db.UpdateUserPasswordParams) error {
	s.passwordSets = append(s.passwordSets, arg)
	return nil
}

func (s *stubQuerier) RevokeOAuthAccessTokensByUser(_ context.Context, resourceOwnerID pgtype.Int8) error {
	s.revokedTokenIDs = append(s.revokedTokenIDs, resourceOwnerID)
	return nil
}

func (s *stubQuerier) ListUsersWithPermission(_ context.Context, permission int64) ([]db.User, error) {
	s.staffPermission = permission
	return s.staff, nil
}

func (s *stubQuerier) GetAccountByID(_ context.Context, id int64) (db.Account, error) {
	account, ok := s.accountsByID[id]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

type captureDeliverer struct {
	mails      []mailer.Mail
	deliverErr error
}

func (c *captureDeliverer) Deliver(_ context.Context, mail mailer.Mail) error {
	c.mails = append(c.mails, mail)
	return c.deliverErr
}

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Instance.LocalDomain = "social.example.com"
	return cfg
}

func newTestHandler(queries Querier, deliverer mailer.Deliverer) *Handler {
	return NewHandler(queries, testConfig(), deliverer)
}

func TestConfirmSuccess(t *testing.T) {
	queries := newStubQuerier()
	queries.usersByConfirmationToken["tok"] = db.User{
		ID:                 7,
		Approved:           true,
		ConfirmationSentAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	}
	h := newTestHandler(queries, nil)

	req := httptest.NewRequest(http.MethodGet, "/auth/confirmation?confirmation_token=tok", nil)
	rec := httptest.NewRecorder()
	h.Confirm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(queries.confirmedIDs) != 1 || queries.confirmedIDs[0] != 7 {
		t.Errorf("confirmed IDs = %v", queries.confirmedIDs)
	}
	if !strings.Contains(rec.Body.String(), "confirmed") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestConfirmPendingNotifiesStaff(t *testing.T) {
	queries := newStubQuerier()
	queries.usersByConfirmationToken["tok"] = db.User{
		ID:                 7,
		Email:              "newbie@example.org",
		AccountID:          70,
		Approved:           false,
		ConfirmationSentAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	}
	queries.accountsByID[70] = db.Account{ID: 70, Username: "newbie"}
	queries.staff = []db.User{
		{ID: 1, Email: "admin@example.org"},
		{ID: 2, Email: "mod@example.org"},
	}
	deliverer := &captureDeliverer{}
	h := newTestHandler(queries, deliverer)

	req := httptest.NewRequest(http.MethodGet, "/auth/confirmation?confirmation_token=tok", nil)
	rec := httptest.NewRecorder()
	h.Confirm(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if queries.staffPermission != auth.PermissionManageUsers {
		t.Errorf("staff permission = %d, want manage_users", queries.staffPermission)
	}
	if len(deliverer.mails) != 2 {
		t.Fatalf("mails = %d, want 2", len(deliverer.mails))
	}
	for _, mail := range deliverer.mails {
		if mail.Kind != mailer.KindAdminNewPendingAccount {
			t.Errorf("mail kind = %s", mail.Kind)
		}
		if mail.Params["Username"] != "newbie" || mail.Params["Email"] != "newbie@example.org" {
			t.Errorf("mail params = %v", mail.Params)
		}
	}
	if !strings.Contains(rec.Body.String(), "waiting for review") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestConfirmInvalidToken(t *testing.T) {
	h := newTestHandler(newStubQuerier(), nil)
	req := httptest.NewRequest(http.MethodGet, "/auth/confirmation?confirmation_token=missing", nil)
	rec := httptest.NewRecorder()
	h.Confirm(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestConfirmExpiredToken(t *testing.T) {
	queries := newStubQuerier()
	queries.usersByConfirmationToken["tok"] = db.User{
		ID:                 7,
		ConfirmationSentAt: pgtype.Timestamptz{Time: time.Now().Add(-72 * time.Hour), Valid: true},
	}
	h := newTestHandler(queries, nil)
	req := httptest.NewRequest(http.MethodGet, "/auth/confirmation?confirmation_token=tok", nil)
	rec := httptest.NewRecorder()
	h.Confirm(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if len(queries.confirmedIDs) != 0 {
		t.Errorf("user confirmed despite expired token")
	}
	if !strings.Contains(rec.Body.String(), "expired") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestConfirmAlreadyConfirmed(t *testing.T) {
	queries := newStubQuerier()
	queries.usersByConfirmationToken["tok"] = db.User{
		ID:          7,
		ConfirmedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	h := newTestHandler(queries, nil)
	req := httptest.NewRequest(http.MethodGet, "/auth/confirmation?confirmation_token=tok", nil)
	rec := httptest.NewRecorder()
	h.Confirm(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if len(queries.confirmedIDs) != 0 {
		t.Errorf("already-confirmed user confirmed again")
	}
}

func TestResendConfirmationSendsMail(t *testing.T) {
	queries := newStubQuerier()
	queries.usersByEmail["user@example.org"] = db.User{ID: 7, Email: "user@example.org"}
	deliverer := &captureDeliverer{}
	h := newTestHandler(queries, deliverer)

	form := url.Values{"email": {"user@example.org"}}
	req := httptest.NewRequest(http.MethodPost, "/auth/confirmation", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ResendConfirmation(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(queries.confirmationSets) != 1 {
		t.Fatalf("confirmation token updates = %d, want 1", len(queries.confirmationSets))
	}
	if len(deliverer.mails) != 1 || deliverer.mails[0].Kind != mailer.KindConfirmationInstructions {
		t.Fatalf("mails = %+v", deliverer.mails)
	}
	if !strings.Contains(deliverer.mails[0].Params["URL"], "confirmation_token=") {
		t.Errorf("mail URL = %s", deliverer.mails[0].Params["URL"])
	}
}

func TestResendConfirmationDoesNotDiscloseAccounts(t *testing.T) {
	deliverer := &captureDeliverer{}
	h := newTestHandler(newStubQuerier(), deliverer)

	form := url.Values{"email": {"nobody@example.org"}}
	req := httptest.NewRequest(http.MethodPost, "/auth/confirmation", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ResendConfirmation(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (no enumeration)", rec.Code)
	}
	if len(deliverer.mails) != 0 {
		t.Errorf("mails = %d, want 0", len(deliverer.mails))
	}
}

func TestRequestPasswordResetSendsMail(t *testing.T) {
	queries := newStubQuerier()
	queries.usersByEmail["user@example.org"] = db.User{ID: 7, Email: "user@example.org"}
	deliverer := &captureDeliverer{}
	h := newTestHandler(queries, deliverer)

	form := url.Values{"email": {"user@example.org"}}
	req := httptest.NewRequest(http.MethodPost, "/auth/password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.PasswordPost(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(queries.resetTokenSets) != 1 {
		t.Fatalf("reset token updates = %d, want 1", len(queries.resetTokenSets))
	}
	if len(deliverer.mails) != 1 || deliverer.mails[0].Kind != mailer.KindResetPasswordInstructions {
		t.Fatalf("mails = %+v", deliverer.mails)
	}
	if !strings.Contains(deliverer.mails[0].Params["URL"], "reset_password_token=") {
		t.Errorf("mail URL = %s", deliverer.mails[0].Params["URL"])
	}
}

func TestRequestPasswordResetUnknownEmailNeutral(t *testing.T) {
	deliverer := &captureDeliverer{}
	h := newTestHandler(newStubQuerier(), deliverer)

	form := url.Values{"email": {"nobody@example.org"}}
	req := httptest.NewRequest(http.MethodPost, "/auth/password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.PasswordPost(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (no enumeration)", rec.Code)
	}
	if len(deliverer.mails) != 0 {
		t.Errorf("mails = %d, want 0", len(deliverer.mails))
	}
}

func resetForm(token, password, confirmation string) *http.Request {
	form := url.Values{
		"_method":               {"put"},
		"reset_password_token":  {token},
		"password":              {password},
		"password_confirmation": {confirmation},
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestUpdatePasswordSuccess(t *testing.T) {
	queries := newStubQuerier()
	queries.usersByResetToken["tok"] = db.User{
		ID:                  7,
		Email:               "user@example.org",
		ResetPasswordSentAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	}
	deliverer := &captureDeliverer{}
	h := newTestHandler(queries, deliverer)

	rec := httptest.NewRecorder()
	h.PasswordPost(rec, resetForm("tok", "new-password-123", "new-password-123"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(queries.passwordSets) != 1 {
		t.Fatalf("password updates = %d, want 1", len(queries.passwordSets))
	}
	if !auth.CheckPassword("new-password-123", queries.passwordSets[0].EncryptedPassword) {
		t.Errorf("stored password hash does not match new password")
	}
	if len(queries.revokedTokenIDs) != 1 || queries.revokedTokenIDs[0].Int64 != 7 {
		t.Errorf("revoked token owners = %+v, want user 7", queries.revokedTokenIDs)
	}
	if len(deliverer.mails) != 1 || deliverer.mails[0].Kind != mailer.KindPasswordChanged {
		t.Errorf("mails = %+v", deliverer.mails)
	}
}

func TestUpdatePasswordMismatch(t *testing.T) {
	queries := newStubQuerier()
	queries.usersByResetToken["tok"] = db.User{
		ID:                  7,
		ResetPasswordSentAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	h := newTestHandler(queries, nil)
	rec := httptest.NewRecorder()
	h.PasswordPost(rec, resetForm("tok", "new-password-123", "different"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if len(queries.passwordSets) != 0 {
		t.Errorf("password updated despite mismatch")
	}
}

func TestUpdatePasswordExpiredToken(t *testing.T) {
	queries := newStubQuerier()
	queries.usersByResetToken["tok"] = db.User{
		ID:                  7,
		ResetPasswordSentAt: pgtype.Timestamptz{Time: time.Now().Add(-7 * time.Hour), Valid: true},
	}
	h := newTestHandler(queries, nil)
	rec := httptest.NewRecorder()
	h.PasswordPost(rec, resetForm("tok", "new-password-123", "new-password-123"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if len(queries.passwordSets) != 0 {
		t.Errorf("password updated despite expired token")
	}
}

func TestUpdatePasswordInvalidToken(t *testing.T) {
	h := newTestHandler(newStubQuerier(), nil)
	rec := httptest.NewRecorder()
	h.PasswordPost(rec, resetForm("missing", "new-password-123", "new-password-123"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestUpdatePasswordTooShort(t *testing.T) {
	queries := newStubQuerier()
	queries.usersByResetToken["tok"] = db.User{
		ID:                  7,
		ResetPasswordSentAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	h := newTestHandler(queries, nil)
	rec := httptest.NewRecorder()
	h.PasswordPost(rec, resetForm("tok", "short", "short"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestFormsRender(t *testing.T) {
	h := newTestHandler(newStubQuerier(), nil)

	rec := httptest.NewRecorder()
	h.NewPassword(rec, httptest.NewRequest(http.MethodGet, "/auth/password/new", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="email"`) {
		t.Errorf("password/new status = %d body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.NewConfirmation(rec, httptest.NewRequest(http.MethodGet, "/auth/confirmation/new", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="email"`) {
		t.Errorf("confirmation/new status = %d body = %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.EditPassword(rec, httptest.NewRequest(http.MethodGet, "/auth/password/edit?reset_password_token=tok", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `name="password"`) || !strings.Contains(body, `value="tok"`) {
		t.Errorf("password/edit status = %d body = %s", rec.Code, body)
	}

	rec = httptest.NewRecorder()
	h.EditPassword(rec, httptest.NewRequest(http.MethodGet, "/auth/password/edit", nil))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("password/edit without token status = %d, want 422", rec.Code)
	}
}

func TestDeliverErrorsAreSwallowed(t *testing.T) {
	queries := newStubQuerier()
	queries.usersByEmail["user@example.org"] = db.User{ID: 7, Email: "user@example.org"}
	deliverer := &captureDeliverer{deliverErr: errors.New("queue down")}
	h := newTestHandler(queries, deliverer)

	form := url.Values{"email": {"user@example.org"}}
	req := httptest.NewRequest(http.MethodPost, "/auth/password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.PasswordPost(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 despite mail error", rec.Code)
	}
}
