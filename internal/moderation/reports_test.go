package moderation

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
)

type reportNotificationStore struct {
	accounts    map[int64]db.Account
	staff       []db.User
	staffErr    error
	permissions []int64
}

func (s *reportNotificationStore) GetAccountByID(ctx context.Context, id int64) (db.Account, error) {
	account, ok := s.accounts[id]
	if !ok {
		return db.Account{}, pgx.ErrNoRows
	}
	return account, nil
}

func (s *reportNotificationStore) ListUsersWithPermission(ctx context.Context, permission int64) ([]db.User, error) {
	s.permissions = append(s.permissions, permission)
	return s.staff, s.staffErr
}

type recordingDeliverer struct {
	mails []mailer.Mail
	err   error
}

func (d *recordingDeliverer) Deliver(ctx context.Context, mail mailer.Mail) error {
	d.mails = append(d.mails, mail)
	return d.err
}

func TestNotifyNewReport(t *testing.T) {
	store := &reportNotificationStore{
		accounts: map[int64]db.Account{
			1: {ID: 1, Username: "remote.example", Domain: pgtype.Text{String: "remote.example", Valid: true}},
			2: {ID: 2, Username: "alice"},
		},
		staff: []db.User{
			{ID: 10, Email: "admin@local.test", Locale: pgtype.Text{String: "de", Valid: true}},
			{ID: 11, Email: "mod@local.test"},
		},
	}
	// A failed delivery doesn't stop the others.
	deliverer := &recordingDeliverer{err: errors.New("queue down")}

	NotifyNewReport(context.Background(), store, deliverer, db.Report{ID: 7, AccountID: 1, TargetAccountID: 2})

	if len(store.permissions) != 1 || store.permissions[0] != auth.PermissionManageReports {
		t.Fatalf("staff permissions = %v, want [%d]", store.permissions, auth.PermissionManageReports)
	}
	if len(deliverer.mails) != 2 {
		t.Fatalf("mails = %d, want 2", len(deliverer.mails))
	}
	for i, want := range []struct{ to, locale string }{{"admin@local.test", "de"}, {"mod@local.test", ""}} {
		mail := deliverer.mails[i]
		if mail.Kind != mailer.KindAdminNewReport || mail.To != want.to || mail.Locale != want.locale {
			t.Fatalf("mail %d = %+v, want to %q in %q", i, mail, want.to, want.locale)
		}
		if mail.Params["ReportID"] != "7" || mail.Params["Reporter"] != "@remote.example@remote.example" || mail.Params["Target"] != "@alice" {
			t.Fatalf("mail %d params = %v", i, mail.Params)
		}
	}
}

func TestNotifyNewReport_UnknownAccounts(t *testing.T) {
	store := &reportNotificationStore{staff: []db.User{{ID: 10, Email: "admin@local.test"}}}
	deliverer := &recordingDeliverer{}

	NotifyNewReport(context.Background(), store, deliverer, db.Report{ID: 7, AccountID: 1, TargetAccountID: 2})

	if len(deliverer.mails) != 1 {
		t.Fatalf("mails = %d, want 1", len(deliverer.mails))
	}
	if params := deliverer.mails[0].Params; params["Reporter"] != "#1" || params["Target"] != "#2" {
		t.Fatalf("params = %v, want #1 and #2", params)
	}
}

func TestNotifyNewReport_SendsNothing(t *testing.T) {
	tests := []struct {
		name      string
		store     *reportNotificationStore
		deliverer *recordingDeliverer
		lookups   int
	}{
		{name: "no deliverer", store: &reportNotificationStore{staff: []db.User{{Email: "admin@local.test"}}}},
		{name: "no staff", store: &reportNotificationStore{}, deliverer: &recordingDeliverer{}, lookups: 1},
		{
			name:      "staff lookup failing",
			store:     &reportNotificationStore{staff: []db.User{{Email: "admin@local.test"}}, staffErr: errors.New("db down")},
			deliverer: &recordingDeliverer{},
			lookups:   1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var deliverer mailer.Deliverer
			if tt.deliverer != nil {
				deliverer = tt.deliverer
			}
			NotifyNewReport(context.Background(), tt.store, deliverer, db.Report{ID: 7, AccountID: 1, TargetAccountID: 2})
			if len(tt.store.permissions) != tt.lookups {
				t.Fatalf("staff lookups = %d, want %d", len(tt.store.permissions), tt.lookups)
			}
			if tt.deliverer != nil && len(tt.deliverer.mails) != 0 {
				t.Fatalf("mails = %+v, want none", tt.deliverer.mails)
			}
		})
	}
}
