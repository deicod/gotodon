package admin

import (
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

func TestTrendsHandler_LinkPublishers_NoDB(t *testing.T) {
	h := NewTrendsHandler(nil, nil)

	req, rec := mockAdminRequest(http.MethodPost, "/api/v1/admin/trends/links/publishers/1/approve", "")
	req.SetPathValue("id", "1")
	h.ApproveLinkPublisher(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("approve status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	req, rec = mockAdminRequest(http.MethodGet, "/api/v1/admin/trends/links/publishers", "")
	h.GetLinkPublishers(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("list status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	req, rec = mockAdminRequest(http.MethodPost, "/api/v1/admin/trends/links/publishers/1/reject", "")
	req.SetPathValue("id", "1")
	h.RejectLinkPublisher(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("reject status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestMapAdminTrendLinkPublisher(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	reqReview := now.Add(-time.Hour)

	provider := db.PreviewCardProvider{
		ID:     42,
		Domain: "example.com",
		Trendable: pgtype.Bool{
			Valid: false,
		},
		ReviewedAt: pgtype.Timestamptz{
			Time:  now,
			Valid: true,
		},
		RequestedReviewAt: pgtype.Timestamptz{
			Time:  reqReview,
			Valid: true,
		},
	}

	resp := mapAdminTrendLinkPublisher(provider)
	if resp.ID != "42" {
		t.Fatalf("id = %q, want %q", resp.ID, "42")
	}
	if resp.Domain != "example.com" {
		t.Fatalf("domain = %q, want %q", resp.Domain, "example.com")
	}
	if resp.Trendable {
		t.Fatalf("trendable = true, want false")
	}
	if resp.RequiresReview {
		t.Fatalf("requires_review = true, want false")
	}
	if resp.ReviewedAt == nil || *resp.ReviewedAt != now.Format(time.RFC3339) {
		t.Fatalf("reviewed_at = %v, want %q", resp.ReviewedAt, now.Format(time.RFC3339))
	}
	if resp.RequestedReviewAt == nil || *resp.RequestedReviewAt != reqReview.Format(time.RFC3339) {
		t.Fatalf("requested_review_at = %v, want %q", resp.RequestedReviewAt, reqReview.Format(time.RFC3339))
	}
}
