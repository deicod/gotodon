package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/deicod/gotodon/internal/db"
)

type scheduledStatusStoreStub struct {
	createdParams db.CreateScheduledStatusParams
	created       db.ScheduledStatus
	attached      []db.AttachMediaToScheduledStatusParams
	detached      []db.DetachMediaFromScheduledStatusParams
	deleted       []db.DeleteScheduledStatusParams
	attachErr     error
}

func (s *scheduledStatusStoreStub) CreateScheduledStatus(ctx context.Context, arg db.CreateScheduledStatusParams) (db.ScheduledStatus, error) {
	s.createdParams = arg
	if s.created.ID == 0 {
		s.created = db.ScheduledStatus{
			ID:          99,
			AccountID:   arg.AccountID,
			ScheduledAt: arg.ScheduledAt,
			Params:      arg.Params,
		}
	}
	return s.created, nil
}

func (s *scheduledStatusStoreStub) AttachMediaToScheduledStatus(ctx context.Context, arg db.AttachMediaToScheduledStatusParams) (int64, error) {
	s.attached = append(s.attached, arg)
	if s.attachErr != nil {
		return 0, s.attachErr
	}
	return 1, nil
}

func (s *scheduledStatusStoreStub) DeleteScheduledStatus(ctx context.Context, arg db.DeleteScheduledStatusParams) error {
	s.deleted = append(s.deleted, arg)
	return nil
}

func (s *scheduledStatusStoreStub) DetachMediaFromScheduledStatus(ctx context.Context, arg db.DetachMediaFromScheduledStatusParams) (int64, error) {
	s.detached = append(s.detached, arg)
	return 1, nil
}

func TestCreateScheduledStatusTxCreatesScheduledStatus(t *testing.T) {
	h := &StatusesHandler{}
	store := &scheduledStatusStoreStub{}
	scheduledAt := time.Date(2024, 7, 18, 12, 0, 0, 0, time.UTC)
	req := CreateStatusRequest{
		Status:      "hello future",
		InReplyToID: ptrStr("123"),
		MediaIDs:    []string{"10", "nope", "20"},
		Sensitive:   true,
		SpoilerText: "cw",
		Visibility:  "private",
		Language:    ptrStr("en"),
	}

	scheduled, err := h.createScheduledStatusTx(context.Background(), store, 42, req, scheduledAt)
	if err != nil {
		t.Fatalf("createScheduledStatusTx error = %v", err)
	}
	if scheduled.ID == 0 {
		t.Fatalf("scheduled ID = 0, want non-zero")
	}
	if store.createdParams.AccountID != 42 {
		t.Fatalf("account_id = %d, want 42", store.createdParams.AccountID)
	}
	if !store.createdParams.ScheduledAt.Time.Equal(scheduledAt) {
		t.Fatalf("scheduled_at = %v, want %v", store.createdParams.ScheduledAt.Time, scheduledAt)
	}

	var payload scheduledStatusParamsPayload
	if err := json.Unmarshal(store.createdParams.Params, &payload); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if payload.Text != req.Status {
		t.Fatalf("params.text = %q, want %q", payload.Text, req.Status)
	}
	if payload.InReplyToID == nil || *payload.InReplyToID != *req.InReplyToID {
		t.Fatalf("params.in_reply_to_id = %v, want %v", payload.InReplyToID, req.InReplyToID)
	}
	if !reflect.DeepEqual(payload.MediaIDs, req.MediaIDs) {
		t.Fatalf("params.media_ids = %#v, want %#v", payload.MediaIDs, req.MediaIDs)
	}
	if payload.Sensitive != req.Sensitive {
		t.Fatalf("params.sensitive = %v, want %v", payload.Sensitive, req.Sensitive)
	}
	if payload.SpoilerText != req.SpoilerText {
		t.Fatalf("params.spoiler_text = %q, want %q", payload.SpoilerText, req.SpoilerText)
	}
	if payload.Visibility != req.Visibility {
		t.Fatalf("params.visibility = %q, want %q", payload.Visibility, req.Visibility)
	}
	if payload.Language == nil || *payload.Language != *req.Language {
		t.Fatalf("params.language = %v, want %v", payload.Language, req.Language)
	}

	if len(store.attached) != 2 {
		t.Fatalf("attached count = %d, want 2", len(store.attached))
	}
	for _, arg := range store.attached {
		if !arg.ScheduledStatusID.Valid || arg.ScheduledStatusID.Int64 != scheduled.ID {
			t.Fatalf("scheduled_status_id = %#v, want %d", arg.ScheduledStatusID, scheduled.ID)
		}
		if !arg.AccountID.Valid || arg.AccountID.Int64 != 42 {
			t.Fatalf("account_id = %#v, want 42", arg.AccountID)
		}
	}
}

func TestCreateScheduledStatusTxAttachmentFailureCleansUp(t *testing.T) {
	h := &StatusesHandler{}
	store := &scheduledStatusStoreStub{attachErr: errors.New("attach failed")}
	scheduledAt := time.Date(2024, 7, 18, 12, 0, 0, 0, time.UTC)
	req := CreateStatusRequest{
		Status:   "hello future",
		MediaIDs: []string{"10"},
	}

	_, err := h.createScheduledStatusTx(context.Background(), store, 42, req, scheduledAt)
	var createErr statusCreateError
	if !errors.As(err, &createErr) || createErr.Status != http.StatusForbidden {
		t.Fatalf("error = %#v, want statusCreateError 403", err)
	}
	if len(store.detached) == 0 {
		t.Fatalf("expected DetachMediaFromScheduledStatus to be called")
	}
	if len(store.deleted) == 0 {
		t.Fatalf("expected DeleteScheduledStatus to be called")
	}
	detach := store.detached[len(store.detached)-1]
	if !detach.ScheduledStatusID.Valid || detach.ScheduledStatusID.Int64 != store.created.ID {
		t.Fatalf("detach scheduled_status_id = %#v, want %d", detach.ScheduledStatusID, store.created.ID)
	}
	if !detach.AccountID.Valid || detach.AccountID.Int64 != 42 {
		t.Fatalf("detach account_id = %#v, want 42", detach.AccountID)
	}
}

func TestParseScheduledAt(t *testing.T) {
	valid := "2024-07-18T12:00:00Z"
	parsed, err := parseScheduledAt(&valid)
	if err != nil {
		t.Fatalf("parseScheduledAt error = %v", err)
	}
	if parsed == nil || !parsed.Equal(time.Date(2024, 7, 18, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("parsed = %v, want 2024-07-18T12:00:00Z", parsed)
	}

	empty := "   "
	parsed, err = parseScheduledAt(&empty)
	if err != nil || parsed != nil {
		t.Fatalf("parseScheduledAt empty = %v, %v, want nil", parsed, err)
	}

	invalid := "not-a-time"
	_, err = parseScheduledAt(&invalid)
	if err == nil {
		t.Fatalf("parseScheduledAt invalid = nil, want error")
	}
}
