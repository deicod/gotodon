package v1

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type statusCreateStoreStub struct {
	createdStatusID int64
	createdPollID   int64

	createStatusParams     db.CreateStatusParams
	createPollParams       db.CreatePollParams
	updateStatusPollParams db.UpdateStatusPollIDParams

	createPollCalled       bool
	updateStatusPollCalled bool
}

func (s *statusCreateStoreStub) GetAccountByID(ctx context.Context, id int64) (db.Account, error) {
	return db.Account{ID: id, Username: "tester"}, nil
}

func (s *statusCreateStoreStub) GetStatusByID(ctx context.Context, id int64) (db.Status, error) {
	return db.Status{ID: id}, nil
}

func (s *statusCreateStoreStub) CreateStatus(ctx context.Context, arg db.CreateStatusParams) (db.Status, error) {
	s.createStatusParams = arg
	id := s.createdStatusID
	if id == 0 {
		id = 100
	}
	now := time.Now().UTC()
	return db.Status{
		ID:        id,
		Uri:       arg.Uri,
		Text:      arg.Text,
		AccountID: arg.AccountID,
		CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil
}

func (s *statusCreateStoreStub) UpdateStatusURI(ctx context.Context, arg db.UpdateStatusURIParams) error {
	return nil
}

func (s *statusCreateStoreStub) CreateStatusStats(ctx context.Context, statusID int64) (db.StatusStat, error) {
	return db.StatusStat{StatusID: statusID}, nil
}

func (s *statusCreateStoreStub) IncrementStatusReplies(ctx context.Context, statusID int64) error {
	return nil
}

func (s *statusCreateStoreStub) AttachMediaToStatus(ctx context.Context, arg db.AttachMediaToStatusParams) (int64, error) {
	return 1, nil
}

func (s *statusCreateStoreStub) CreatePoll(ctx context.Context, arg db.CreatePollParams) (db.Poll, error) {
	s.createPollCalled = true
	s.createPollParams = arg
	id := s.createdPollID
	if id == 0 {
		id = 200
	}
	return db.Poll{
		ID:       id,
		StatusID: arg.StatusID,
	}, nil
}

func (s *statusCreateStoreStub) UpdateStatusPollID(ctx context.Context, arg db.UpdateStatusPollIDParams) error {
	s.updateStatusPollCalled = true
	s.updateStatusPollParams = arg
	return nil
}

func (s *statusCreateStoreStub) GetAccountsByUsernames(ctx context.Context, usernames []string) ([]db.Account, error) {
	return nil, nil
}

func (s *statusCreateStoreStub) GetAccountsByUsernamesAndDomains(ctx context.Context, arg db.GetAccountsByUsernamesAndDomainsParams) ([]db.Account, error) {
	return nil, nil
}

func (s *statusCreateStoreStub) GetMutesFromAccounts(ctx context.Context, arg db.GetMutesFromAccountsParams) ([]db.Mute, error) {
	return nil, nil
}

func (s *statusCreateStoreStub) GetBlocksFromAccounts(ctx context.Context, arg db.GetBlocksFromAccountsParams) ([]db.Block, error) {
	return nil, nil
}

func (s *statusCreateStoreStub) CreateMentions(ctx context.Context, arg db.CreateMentionsParams) error {
	return nil
}

func (s *statusCreateStoreStub) CreateNotifications(ctx context.Context, arg db.CreateNotificationsParams) ([]db.Notification, error) {
	return nil, nil
}

func (s *statusCreateStoreStub) UpsertTags(ctx context.Context, tags []string) error {
	return nil
}

func (s *statusCreateStoreStub) GetTagsByNames(ctx context.Context, names []string) ([]db.Tag, error) {
	return nil, nil
}

func (s *statusCreateStoreStub) AddStatusTags(ctx context.Context, arg db.AddStatusTagsParams) error {
	return nil
}

func TestCreateStatusTxCreatesPoll(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewStatusesHandler(nil, nil, nil, cfg, nil)
	store := &statusCreateStoreStub{
		createdStatusID: 123,
		createdPollID:   456,
	}

	start := time.Now()
	req := CreateStatusRequest{
		Poll: &PollReq{
			Options:    []string{"A", "B"},
			ExpiresIn:  300,
			Multiple:   true,
			HideTotals: true,
		},
	}

	result, err := h.createStatusTx(context.Background(), store, 42, req)
	if err != nil {
		t.Fatalf("createStatusTx error = %v", err)
	}

	if !store.createPollCalled {
		t.Fatal("expected CreatePoll to be called")
	}
	if !store.updateStatusPollCalled {
		t.Fatal("expected UpdateStatusPollID to be called")
	}

	if !store.createPollParams.AccountID.Valid || store.createPollParams.AccountID.Int64 != 42 {
		t.Fatalf("poll account_id = %#v, want 42", store.createPollParams.AccountID)
	}
	if !store.createPollParams.StatusID.Valid || store.createPollParams.StatusID.Int64 != store.createdStatusID {
		t.Fatalf("poll status_id = %#v, want %d", store.createPollParams.StatusID, store.createdStatusID)
	}
	if store.updateStatusPollParams.ID != store.createdStatusID {
		t.Fatalf("update status id = %d, want %d", store.updateStatusPollParams.ID, store.createdStatusID)
	}
	if !store.updateStatusPollParams.PollID.Valid || store.updateStatusPollParams.PollID.Int64 != store.createdPollID {
		t.Fatalf("update poll id = %#v, want %d", store.updateStatusPollParams.PollID, store.createdPollID)
	}
	if !result.Status.PollID.Valid || result.Status.PollID.Int64 != store.createdPollID {
		t.Fatalf("result poll_id = %#v, want %d", result.Status.PollID, store.createdPollID)
	}

	if len(store.createPollParams.Options) != len(req.Poll.Options) {
		t.Fatalf("poll options length = %d, want %d", len(store.createPollParams.Options), len(req.Poll.Options))
	}
	if len(store.createPollParams.CachedTallies) != len(req.Poll.Options) {
		t.Fatalf("poll cached_tallies length = %d, want %d", len(store.createPollParams.CachedTallies), len(req.Poll.Options))
	}
	for i, v := range store.createPollParams.CachedTallies {
		if v != 0 {
			t.Fatalf("poll cached_tallies[%d] = %d, want 0", i, v)
		}
	}

	minExpiry := start.Add(time.Duration(req.Poll.ExpiresIn) * time.Second)
	maxExpiry := time.Now().Add(time.Duration(req.Poll.ExpiresIn) * time.Second)
	if store.createPollParams.ExpiresAt.Time.Before(minExpiry) || store.createPollParams.ExpiresAt.Time.After(maxExpiry) {
		t.Fatalf("poll expires_at = %s, want between %s and %s", store.createPollParams.ExpiresAt.Time, minExpiry, maxExpiry)
	}
}

func TestCreateStatusTxWithoutPoll(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewStatusesHandler(nil, nil, nil, cfg, nil)
	store := &statusCreateStoreStub{
		createdStatusID: 123,
	}

	req := CreateStatusRequest{
		Status: "hello",
	}

	_, err := h.createStatusTx(context.Background(), store, 42, req)
	if err != nil {
		t.Fatalf("createStatusTx error = %v", err)
	}

	if store.createPollCalled {
		t.Fatal("did not expect CreatePoll to be called")
	}
	if store.updateStatusPollCalled {
		t.Fatal("did not expect UpdateStatusPollID to be called")
	}
}
