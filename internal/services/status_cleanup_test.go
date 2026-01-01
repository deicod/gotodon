package services

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

type stubStatusCleanupStore struct {
	policies     []db.AccountStatusesCleanupPolicy
	listCalls    []db.ListAccountStatusesCleanupPoliciesParams
	cutoffCalls  []db.GetStatusCleanupCutoffIDParams
	cutoffID     int64
	cutoffErr    error
	statusCalls  []db.ListStatusesForCleanupParams
	statuses     []db.Status
	deleteIDs    []int64
	decrementIDs []int64
}

func (s *stubStatusCleanupStore) ListAccountStatusesCleanupPolicies(ctx context.Context, arg db.ListAccountStatusesCleanupPoliciesParams) ([]db.AccountStatusesCleanupPolicy, error) {
	s.listCalls = append(s.listCalls, arg)
	if arg.AfterID == 0 {
		return s.policies, nil
	}
	return nil, nil
}

func (s *stubStatusCleanupStore) GetStatusCleanupCutoffID(ctx context.Context, arg db.GetStatusCleanupCutoffIDParams) (int64, error) {
	s.cutoffCalls = append(s.cutoffCalls, arg)
	if s.cutoffErr != nil {
		return 0, s.cutoffErr
	}
	return s.cutoffID, nil
}

func (s *stubStatusCleanupStore) ListStatusesForCleanup(ctx context.Context, arg db.ListStatusesForCleanupParams) ([]db.Status, error) {
	s.statusCalls = append(s.statusCalls, arg)
	return s.statuses, nil
}

func (s *stubStatusCleanupStore) DeleteStatus(ctx context.Context, id int64) error {
	s.deleteIDs = append(s.deleteIDs, id)
	return nil
}

func (s *stubStatusCleanupStore) DecrementStatusReplies(ctx context.Context, id int64) error {
	s.decrementIDs = append(s.decrementIDs, id)
	return nil
}

func (s *stubStatusCleanupStore) GetMentionsByStatusID(ctx context.Context, statusID int64) ([]db.Mention, error) {
	return nil, nil
}

func (s *stubStatusCleanupStore) GetAccountsByIDs(ctx context.Context, ids []int64) ([]db.Account, error) {
	return nil, nil
}

func (s *stubStatusCleanupStore) CreateConversation(ctx context.Context, arg db.CreateConversationParams) (db.Conversation, error) {
	return db.Conversation{}, nil
}

func (s *stubStatusCleanupStore) GetConversationByParentStatusID(ctx context.Context, parentStatusID pgtype.Int8) (db.Conversation, error) {
	return db.Conversation{}, pgx.ErrNoRows
}

func (s *stubStatusCleanupStore) UpdateStatusConversationID(ctx context.Context, arg db.UpdateStatusConversationIDParams) error {
	return nil
}

func (s *stubStatusCleanupStore) UpdateStatusConversationIDIfNull(ctx context.Context, arg db.UpdateStatusConversationIDIfNullParams) (int64, error) {
	return 0, nil
}

func (s *stubStatusCleanupStore) GetAccountConversationByParticipants(ctx context.Context, arg db.GetAccountConversationByParticipantsParams) (db.AccountConversation, error) {
	return db.AccountConversation{}, pgx.ErrNoRows
}

func (s *stubStatusCleanupStore) CreateAccountConversation(ctx context.Context, arg db.CreateAccountConversationParams) (db.AccountConversation, error) {
	return db.AccountConversation{}, nil
}

func (s *stubStatusCleanupStore) UpdateAccountConversation(ctx context.Context, arg db.UpdateAccountConversationParams) (int64, error) {
	return 0, nil
}

func (s *stubStatusCleanupStore) DeleteAccountConversation(ctx context.Context, arg db.DeleteAccountConversationParams) (int64, error) {
	return 0, nil
}

func TestStatusCleanupServiceRunDeletesStatuses(t *testing.T) {
	policy := db.AccountStatusesCleanupPolicy{
		ID:               10,
		AccountID:        42,
		Enabled:          true,
		MinStatusAge:     60,
		KeepDirect:       true,
		KeepPinned:       true,
		KeepPolls:        false,
		KeepMedia:        true,
		KeepSelfFav:      true,
		KeepSelfBookmark: false,
		MinFavs:          pgtype.Int4{Int32: 2, Valid: true},
		MinReblogs:       pgtype.Int4{Valid: false},
	}
	statuses := []db.Status{
		{ID: 1001, AccountID: 42, InReplyToID: pgtype.Int8{Int64: 555, Valid: true}, Visibility: 0},
		{ID: 1002, AccountID: 42, Visibility: 0},
	}
	store := &stubStatusCleanupStore{
		policies: policySlice(policy),
		cutoffID: 2000,
		statuses: statuses,
	}

	svc := NewStatusCleanupService(store, nil, nil, nil)
	svc.maxBudget = 10
	svc.perPolicyBudget = 10
	svc.now = func() time.Time { return time.Unix(1700000000, 0) }

	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
	}

	if len(store.deleteIDs) != 2 {
		t.Fatalf("deleteIDs = %d, want 2", len(store.deleteIDs))
	}
	if store.deleteIDs[0] != 1001 || store.deleteIDs[1] != 1002 {
		t.Fatalf("deleteIDs = %#v, want [1001 1002]", store.deleteIDs)
	}
	if len(store.decrementIDs) != 1 || store.decrementIDs[0] != 555 {
		t.Fatalf("decrementIDs = %#v, want [555]", store.decrementIDs)
	}
	if len(store.statusCalls) != 1 {
		t.Fatalf("statusCalls = %d, want 1", len(store.statusCalls))
	}
	params := store.statusCalls[0]
	if params.KeepDirect != policy.KeepDirect || params.KeepPinned != policy.KeepPinned || params.KeepMedia != policy.KeepMedia {
		t.Fatalf("params = %#v, want policy booleans", params)
	}
	if params.MinFavs.Valid != policy.MinFavs.Valid || params.MinFavs.Int32 != policy.MinFavs.Int32 {
		t.Fatalf("MinFavs = %#v, want %#v", params.MinFavs, policy.MinFavs)
	}
}

func TestStatusCleanupServiceRunSkipsWhenNoCutoffRows(t *testing.T) {
	policy := db.AccountStatusesCleanupPolicy{
		ID:           11,
		AccountID:    84,
		Enabled:      true,
		MinStatusAge: 60,
	}
	store := &stubStatusCleanupStore{
		policies:  policySlice(policy),
		cutoffErr: pgx.ErrNoRows,
	}

	svc := NewStatusCleanupService(store, nil, nil, nil)
	svc.maxBudget = 10
	svc.perPolicyBudget = 10
	svc.now = func() time.Time { return time.Unix(1700000000, 0) }

	if err := svc.Run(context.Background()); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if len(store.statusCalls) != 0 {
		t.Fatalf("statusCalls = %d, want 0", len(store.statusCalls))
	}
	if len(store.deleteIDs) != 0 {
		t.Fatalf("deleteIDs = %#v, want none", store.deleteIDs)
	}
}

func policySlice(policy db.AccountStatusesCleanupPolicy) []db.AccountStatusesCleanupPolicy {
	return []db.AccountStatusesCleanupPolicy{policy}
}
