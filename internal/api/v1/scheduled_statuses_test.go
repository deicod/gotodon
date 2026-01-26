package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
)

type scheduledStatusStub struct {
	db.Querier
	countTotal   int64
	countDaily   int64
	scheduled    db.ScheduledStatus
	media        []db.MediaAttachment
	list         []db.ScheduledStatus
	listErr      error
	deleteParams []db.DeleteScheduledStatusParams
	detachParams []db.DetachMediaFromScheduledStatusParams
	getErr       error
	updateErr    error
	updated      bool
	updateParams db.UpdateScheduledStatusParams
}

func (s *scheduledStatusStub) GetScheduledStatus(ctx context.Context, arg db.GetScheduledStatusParams) (db.ScheduledStatus, error) {
	if s.getErr != nil {
		return db.ScheduledStatus{}, s.getErr
	}
	if arg.ID != s.scheduled.ID || arg.AccountID != s.scheduled.AccountID {
		return db.ScheduledStatus{}, pgx.ErrNoRows
	}
	return s.scheduled, nil
}

func (s *scheduledStatusStub) ListScheduledStatuses(ctx context.Context, arg db.ListScheduledStatusesParams) ([]db.ScheduledStatus, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.list, nil
}

func (s *scheduledStatusStub) CountScheduledStatusesByAccount(ctx context.Context, accountID int64) (int64, error) {
	return s.countTotal, nil
}

func (s *scheduledStatusStub) CountScheduledStatusesByAccountAndDate(ctx context.Context, arg db.CountScheduledStatusesByAccountAndDateParams) (int64, error) {
	return s.countDaily, nil
}

func (s *scheduledStatusStub) UpdateScheduledStatus(ctx context.Context, arg db.UpdateScheduledStatusParams) (db.ScheduledStatus, error) {
	s.updateParams = arg
	s.updated = true
	if s.updateErr != nil {
		return db.ScheduledStatus{}, s.updateErr
	}
	if arg.ID != s.scheduled.ID || arg.AccountID != s.scheduled.AccountID {
		return db.ScheduledStatus{}, pgx.ErrNoRows
	}
	updated := s.scheduled
	if arg.ScheduledAt.Valid {
		updated.ScheduledAt = arg.ScheduledAt
	}
	return updated, nil
}

func (s *scheduledStatusStub) DeleteScheduledStatus(ctx context.Context, arg db.DeleteScheduledStatusParams) error {
	s.deleteParams = append(s.deleteParams, arg)
	return nil
}

func (s *scheduledStatusStub) DetachMediaFromScheduledStatus(ctx context.Context, arg db.DetachMediaFromScheduledStatusParams) (int64, error) {
	s.detachParams = append(s.detachParams, arg)
	return 1, nil
}

func (s *scheduledStatusStub) GetMediaAttachmentsByScheduledStatusIDs(ctx context.Context, ids []int64) ([]db.MediaAttachment, error) {
	return s.media, nil
}

func scheduledStatusRequest(method, path, body string, accountID int64, scopes string) (*http.Request, *httptest.ResponseRecorder) {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Content-Type", "application/json")

	ctx := req.Context()
	if accountID > 0 {
		ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, accountID)
	}
	if scopes != "" {
		ctx = context.WithValue(ctx, middleware.ContextKeyScopes, scopes)
	}
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	return req, rec
}

func scheduledStatusRequestWithToken(method, path, body string, accountID int64, scopes string, tokenID int64) (*http.Request, *httptest.ResponseRecorder) {
	req, rec := scheduledStatusRequest(method, path, body, accountID, scopes)
	if tokenID > 0 {
		ctx := context.WithValue(req.Context(), middleware.ContextKeyTokenID, tokenID)
		req = req.WithContext(ctx)
	}
	return req, rec
}

func TestScheduledStatusesHandler_Update_Unauthorized(t *testing.T) {
	h := NewScheduledStatusesHandler(nil)

	req, rec := scheduledStatusRequest(http.MethodPut, "/api/v1/scheduled_statuses/1", `{"scheduled_at":"2024-01-01T00:00:00Z"}`, 0, "")
	req.SetPathValue("id", "1")

	h.Update(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestScheduledStatusesHandler_Update_Forbidden(t *testing.T) {
	h := NewScheduledStatusesHandler(nil)

	req, rec := scheduledStatusRequest(http.MethodPut, "/api/v1/scheduled_statuses/1", `{"scheduled_at":"2024-01-01T00:00:00Z"}`, 10, "read")
	req.SetPathValue("id", "1")

	h.Update(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestScheduledStatusesHandler_Update_InvalidScheduledAt(t *testing.T) {
	now := time.Now().UTC().Add(10 * time.Minute)
	stub := &scheduledStatusStub{
		scheduled: db.ScheduledStatus{
			ID:          10,
			AccountID:   5,
			ScheduledAt: pgtype.Timestamptz{Time: now, Valid: true},
		},
	}
	h := NewScheduledStatusesHandler(stub)

	req, rec := scheduledStatusRequest(http.MethodPut, "/api/v1/scheduled_statuses/10", `{"scheduled_at":"nope"}`, 5, "write:statuses")
	req.SetPathValue("id", "10")

	h.Update(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestScheduledStatusesHandler_Update_TooSoon(t *testing.T) {
	now := time.Now().UTC()
	stub := &scheduledStatusStub{
		scheduled: db.ScheduledStatus{
			ID:          12,
			AccountID:   5,
			ScheduledAt: pgtype.Timestamptz{Time: now.Add(10 * time.Minute), Valid: true},
		},
	}
	h := NewScheduledStatusesHandler(stub)

	tooSoon := now.Add(2 * time.Minute).Format(time.RFC3339Nano)
	req, rec := scheduledStatusRequest(http.MethodPut, "/api/v1/scheduled_statuses/12", fmt.Sprintf(`{"scheduled_at":"%s"}`, tooSoon), 5, "write:statuses")
	req.SetPathValue("id", "12")

	h.Update(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if stub.updated {
		t.Fatalf("expected update to be rejected")
	}
}

func TestScheduledStatusesHandler_Update_TotalLimitExceeded(t *testing.T) {
	now := time.Now().UTC()
	stub := &scheduledStatusStub{
		countTotal: scheduledStatusTotalLimit,
		scheduled: db.ScheduledStatus{
			ID:          20,
			AccountID:   5,
			ScheduledAt: pgtype.Timestamptz{Time: now.Add(10 * time.Minute), Valid: true},
		},
	}
	h := NewScheduledStatusesHandler(stub)

	future := now.Add(10 * time.Minute).Format(time.RFC3339Nano)
	req, rec := scheduledStatusRequest(http.MethodPut, "/api/v1/scheduled_statuses/20", fmt.Sprintf(`{"scheduled_at":"%s"}`, future), 5, "write:statuses")
	req.SetPathValue("id", "20")

	h.Update(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestScheduledStatusesHandler_Update_DailyLimitExceeded(t *testing.T) {
	now := time.Now().UTC()
	stub := &scheduledStatusStub{
		countTotal: 1,
		countDaily: scheduledStatusDailyLimit,
		scheduled: db.ScheduledStatus{
			ID:          21,
			AccountID:   5,
			ScheduledAt: pgtype.Timestamptz{Time: now.Add(20 * time.Minute), Valid: true},
		},
	}
	h := NewScheduledStatusesHandler(stub)

	future := now.Add(20 * time.Minute).Format(time.RFC3339Nano)
	req, rec := scheduledStatusRequest(http.MethodPut, "/api/v1/scheduled_statuses/21", fmt.Sprintf(`{"scheduled_at":"%s"}`, future), 5, "write:statuses")
	req.SetPathValue("id", "21")

	h.Update(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestScheduledStatusesHandler_Update_NotFound(t *testing.T) {
	stub := &scheduledStatusStub{
		getErr: pgx.ErrNoRows,
	}
	h := NewScheduledStatusesHandler(stub)

	req, rec := scheduledStatusRequest(http.MethodPut, "/api/v1/scheduled_statuses/44", `{"scheduled_at":"2024-01-01T00:00:00Z"}`, 5, "write:statuses")
	req.SetPathValue("id", "44")

	h.Update(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestScheduledStatusesHandler_Update_Success(t *testing.T) {
	now := time.Now().UTC()
	updatedAt := now.Add(10 * time.Minute)
	stub := &scheduledStatusStub{
		countTotal: 1,
		countDaily: 1,
		scheduled: db.ScheduledStatus{
			ID:          15,
			AccountID:   7,
			ScheduledAt: pgtype.Timestamptz{Time: now.Add(20 * time.Minute), Valid: true},
			Params:      []byte(`{}`),
		},
	}
	h := NewScheduledStatusesHandler(stub)

	body := fmt.Sprintf(`{"scheduled_at":"%s"}`, updatedAt.Format(time.RFC3339Nano))
	req, rec := scheduledStatusRequest(http.MethodPut, "/api/v1/scheduled_statuses/15", body, 7, "write:statuses")
	req.SetPathValue("id", "15")

	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !stub.updated {
		t.Fatalf("expected update to be recorded")
	}
	if !stub.updateParams.ScheduledAt.Valid {
		t.Fatalf("scheduled_at should be set")
	}
	if !stub.updateParams.ScheduledAt.Time.Equal(updatedAt) {
		t.Fatalf("scheduled_at = %s, want %s", stub.updateParams.ScheduledAt.Time, updatedAt)
	}

	var resp ScheduledStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.ID != "15" {
		t.Fatalf("id = %q, want %q", resp.ID, "15")
	}
	scheduledAt, ok := resp.ScheduledAt.(string)
	if !ok {
		t.Fatalf("scheduled_at type = %T, want string", resp.ScheduledAt)
	}
	parsed, err := time.Parse(time.RFC3339Nano, scheduledAt)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, scheduledAt)
	}
	if err != nil {
		t.Fatalf("failed to parse scheduled_at: %v", err)
	}
	if !parsed.Equal(updatedAt) {
		t.Fatalf("scheduled_at = %s, want %s", parsed, updatedAt)
	}
}

func TestScheduledStatusesHandler_List_AppToken(t *testing.T) {
	h := NewScheduledStatusesHandler(nil)

	req, rec := scheduledStatusRequestWithToken(http.MethodGet, "/api/v1/scheduled_statuses", "", 0, "read:statuses", 99)

	h.List(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestScheduledStatusesHandler_List_Forbidden(t *testing.T) {
	h := NewScheduledStatusesHandler(nil)

	req, rec := scheduledStatusRequest(http.MethodGet, "/api/v1/scheduled_statuses", "", 10, "write:statuses")

	h.List(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestScheduledStatusesHandler_List_Success(t *testing.T) {
	now := time.Now().UTC().Add(10 * time.Minute)
	stub := &scheduledStatusStub{
		list: []db.ScheduledStatus{
			{
				ID:          1,
				AccountID:   5,
				ScheduledAt: pgtype.Timestamptz{Time: now, Valid: true},
				Params:      []byte(`{}`),
			},
		},
	}
	h := NewScheduledStatusesHandler(stub)

	req, rec := scheduledStatusRequest(http.MethodGet, "/api/v1/scheduled_statuses", "", 5, "read:statuses")

	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp []ScheduledStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp) != 1 || resp[0].ID != "1" {
		t.Fatalf("response = %#v, want id 1", resp)
	}
}

func TestScheduledStatusesHandler_Get_AppToken(t *testing.T) {
	h := NewScheduledStatusesHandler(nil)

	req, rec := scheduledStatusRequestWithToken(http.MethodGet, "/api/v1/scheduled_statuses/1", "", 0, "read:statuses", 77)
	req.SetPathValue("id", "1")

	h.Get(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestScheduledStatusesHandler_Get_Forbidden(t *testing.T) {
	h := NewScheduledStatusesHandler(nil)

	req, rec := scheduledStatusRequest(http.MethodGet, "/api/v1/scheduled_statuses/1", "", 5, "write:statuses")
	req.SetPathValue("id", "1")

	h.Get(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestScheduledStatusesHandler_Delete_AppToken(t *testing.T) {
	h := NewScheduledStatusesHandler(nil)

	req, rec := scheduledStatusRequestWithToken(http.MethodDelete, "/api/v1/scheduled_statuses/1", "", 0, "write:statuses", 55)
	req.SetPathValue("id", "1")

	h.Delete(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestScheduledStatusesHandler_Delete_Forbidden(t *testing.T) {
	h := NewScheduledStatusesHandler(nil)

	req, rec := scheduledStatusRequest(http.MethodDelete, "/api/v1/scheduled_statuses/1", "", 5, "read:statuses")
	req.SetPathValue("id", "1")

	h.Delete(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
