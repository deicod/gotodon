package v1

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
	"github.com/deicod/gotodon/internal/moderation"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

type reportsQuerier interface {
	CreateReport(ctx context.Context, arg db.CreateReportParams) (db.Report, error)
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
	ListUsersWithPermission(ctx context.Context, permission int64) ([]db.User, error)
}

// ReportsHandler handles report endpoints
type ReportsHandler struct {
	queries reportsQuerier
	cfg     *config.Config
	mail    mailer.Deliverer
}

// NewReportsHandler creates a new reports handler
func NewReportsHandler(queries reportsQuerier) *ReportsHandler {
	return &ReportsHandler{
		queries: queries,
	}
}

// SetMailDeliverer enables new-report notification mails to moderators.
func (h *ReportsHandler) SetMailDeliverer(cfg *config.Config, deliverer mailer.Deliverer) {
	if h == nil {
		return
	}
	h.cfg = cfg
	h.mail = deliverer
}

// Report represents a report
type Report struct {
	ID              string   `json:"id"`
	ActionTaken     bool     `json:"action_taken"`
	ActionTakenAt   string   `json:"action_taken_at,omitempty"`
	Category        string   `json:"category"`
	Comment         string   `json:"comment"`
	Forwarded       bool     `json:"forwarded"`
	StatusIDs       []string `json:"status_ids"`
	RuleIDs         []string `json:"rule_ids"`
	TargetAccountID string   `json:"target_account_id"`
	CreatedAt       string   `json:"created_at"`
}

// Create handles POST /api/v1/reports
func (h *ReportsHandler) Create(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var req struct {
		AccountID string   `json:"account_id" form:"account_id"`
		StatusIDs []string `json:"status_ids" form:"status_ids"`
		Comment   string   `json:"comment" form:"comment"`
		Forward   bool     `json:"forward" form:"forward"`
		Category  string   `json:"category" form:"category"`
		RuleIDs   []string `json:"rule_ids" form:"rule_ids"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	targetID, err := strconv.ParseInt(req.AccountID, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid account_id")
		return
	}

	// Verify target account exists (optional but good practice)
	// For now, constraint will fail if not found.

	statusIDs := make([]int64, 0, len(req.StatusIDs))
	for _, sid := range req.StatusIDs {
		if id, err := strconv.ParseInt(sid, 10, 64); err == nil {
			statusIDs = append(statusIDs, id)
		}
	}

	// Map category string to int if needed, or store as int in DB?
	// Schema says `category integer`.
	// Valid categories: spam, violation, other (often mapped 0, 1, 2 etc)
	// Gotodon schema `category integer DEFAULT 0`.
	// Let's assume standard mapping:
	// other=0, spam=1, violation=2
	var categoryInt int32 = 0
	switch req.Category {
	case "spam":
		categoryInt = 1
	case "violation":
		categoryInt = 2
	}

	report, err := h.queries.CreateReport(r.Context(), db.CreateReportParams{
		AccountID:       accountID,
		TargetAccountID: targetID,
		StatusIds:       statusIDs,
		Comment:         req.Comment,
		Category:        categoryInt,
		Forwarded:       pgtype.Bool{Bool: req.Forward, Valid: true},
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	moderation.NotifyNewReport(r.Context(), h.queries, h.mail, report)

	// Return response
	resp := Report{
		ID:              strconv.FormatInt(report.ID, 10),
		Category:        req.Category, // Return what was sent or mapped back
		Comment:         report.Comment,
		Forwarded:       report.Forwarded.Bool,
		TargetAccountID: strconv.FormatInt(report.TargetAccountID, 10),
		CreatedAt:       report.CreatedAt.Time.Format(time.RFC3339),
	}
	// Convert StatusIDs back
	for _, sid := range report.StatusIds {
		resp.StatusIDs = append(resp.StatusIDs, strconv.FormatInt(sid, 10))
	}

	web.JSON(w, http.StatusOK, resp)
}

// RegisterRoutes registers the report route with the scopes of Mastodon's
// Api::V1::ReportsController.
func (h *ReportsHandler) RegisterRoutes(mux middleware.RouteRegistrar) {
	mux.HandleFunc("POST /api/v1/reports", middleware.RequireScope(oauth.ScopeWrite, oauth.ScopeWriteReports)(h.Create))
}
