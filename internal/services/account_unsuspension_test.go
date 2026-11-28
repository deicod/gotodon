package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
)

var (
	_ AccountUnsuspensionStore = (*db.Queries)(nil)
	_ UnsuspensionDeliverer    = (*federation.OutgoingService)(nil)
)

// GetAccountByIDForShare reads the account as GetAccountByIDAllowSuspended
// does: the stub has no concurrent transactions to wait for.
func (s *suspensionStore) GetAccountByIDForShare(ctx context.Context, id int64) (db.Account, error) {
	return s.GetAccountByIDAllowSuspended(ctx, id)
}

// recordingUnsuspensionDeliverer records the accounts whose unsuspension it
// is asked to deliver, and fails each with err.
type recordingUnsuspensionDeliverer struct {
	delivered []int64
	err       error
}

func (d *recordingUnsuspensionDeliverer) DeliverAccountUnsuspension(_ context.Context, account db.Account) error {
	d.delivered = append(d.delivered, account.ID)
	return d.err
}

func TestAccountUnsuspensionService_Unsuspend(t *testing.T) {
	suspendedAt := pgtype.Timestamptz{Time: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC), Valid: true}
	remoteDomain := pgtype.Text{String: "remote.example", Valid: true}
	accounts := map[int64]db.Account{
		1: {ID: 1, Username: "alice"},
		2: {ID: 2, Username: "bob", Domain: remoteDomain},
		3: {ID: 3, Username: "carol", SuspendedAt: suspendedAt},
		4: {ID: 4, Username: "dan", Domain: remoteDomain, SuspendedAt: suspendedAt},
	}

	tests := []struct {
		name          string
		accountID     int64
		wantDelivered []int64
	}{
		{name: "local account delivers its actor", accountID: 1, wantDelivered: []int64{1}},
		{name: "remote account federates nothing", accountID: 2},
		{name: "local account suspended again", accountID: 3},
		{name: "remote account suspended again", accountID: 4},
		{name: "account that doesn't exist", accountID: 99},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deliverer := &recordingUnsuspensionDeliverer{}
			service := NewAccountUnsuspensionService(&suspensionStore{accounts: accounts}, deliverer)
			if err := service.Unsuspend(context.Background(), tt.accountID); err != nil {
				t.Fatalf("Unsuspend: %v", err)
			}
			if !equalInt64s(deliverer.delivered, tt.wantDelivered) {
				t.Fatalf("delivered = %v, want %v", deliverer.delivered, tt.wantDelivered)
			}
		})
	}

	t.Run("repeated", func(t *testing.T) {
		deliverer := &recordingUnsuspensionDeliverer{}
		service := NewAccountUnsuspensionService(&suspensionStore{accounts: accounts}, deliverer)
		for run := 0; run < 2; run++ {
			if err := service.Unsuspend(context.Background(), 1); err != nil {
				t.Fatalf("run %d: %v", run+1, err)
			}
		}
		if !equalInt64s(deliverer.delivered, []int64{1, 1}) {
			t.Fatalf("delivered = %v, want the actor of alice on each run", deliverer.delivered)
		}
	})
}

func TestAccountUnsuspensionService_UnsuspendFailures(t *testing.T) {
	errStore := errors.New("store down")
	errDeliver := errors.New("queue down")

	t.Run("account lookup fails", func(t *testing.T) {
		deliverer := &recordingUnsuspensionDeliverer{}
		service := NewAccountUnsuspensionService(&suspensionStore{accountErr: errStore}, deliverer)
		if err := service.Unsuspend(context.Background(), 1); !errors.Is(err, errStore) {
			t.Fatalf("err = %v, want %v", err, errStore)
		}
		if len(deliverer.delivered) != 0 {
			t.Fatalf("delivered %v after a failed lookup", deliverer.delivered)
		}
	})

	t.Run("delivery fails", func(t *testing.T) {
		deliverer := &recordingUnsuspensionDeliverer{err: errDeliver}
		store := &suspensionStore{accounts: map[int64]db.Account{1: {ID: 1, Username: "alice"}}}
		service := NewAccountUnsuspensionService(store, deliverer)
		if err := service.Unsuspend(context.Background(), 1); !errors.Is(err, errDeliver) {
			t.Fatalf("err = %v, want %v", err, errDeliver)
		}
	})

	t.Run("missing dependencies", func(t *testing.T) {
		var nilService *AccountUnsuspensionService
		for name, service := range map[string]*AccountUnsuspensionService{
			"nil service":   nilService,
			"no store":      NewAccountUnsuspensionService(nil, &recordingUnsuspensionDeliverer{}),
			"no deliverer":  NewAccountUnsuspensionService(&suspensionStore{}, nil),
			"neither given": {},
		} {
			if err := service.Unsuspend(context.Background(), 1); !errors.Is(err, ErrAccountUnsuspensionMissingDeps) {
				t.Errorf("%s: err = %v, want ErrAccountUnsuspensionMissingDeps", name, err)
			}
		}
	})
}

func equalInt64s(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
