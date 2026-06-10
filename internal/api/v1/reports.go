package v1

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/mailer"
	"github.com/deicod/gotodon/internal/web"
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

	h.notifyModerators(r.Context(), report)

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

// notifyModerators emails every user who can manage reports about the new report.
func (h *ReportsHandler) notifyModerators(ctx context.Context, report db.Report) {
	if h.mail == nil {
		return
	}
	staff, err := h.queries.ListUsersWithPermission(ctx, auth.PermissionManageReports)
	if err != nil {
		slog.Error("Failed to load staff for report mail", "error", err)
		return
	}
	reporter := h.accountHandle(ctx, report.AccountID)
	target := h.accountHandle(ctx, report.TargetAccountID)
	for _, member := range staff {
		locale := ""
		if member.Locale.Valid {
			locale = member.Locale.String
		}
		mail := mailer.Mail{
			Kind:   mailer.KindAdminNewReport,
			To:     member.Email,
			Locale: locale,
			Params: map[string]string{
				"ReportID": strconv.FormatInt(report.ID, 10),
				"Reporter": reporter,
				"Target":   target,
			},
		}
		if err := h.mail.Deliver(ctx, mail); err != nil {
			slog.Error("Failed to enqueue report mail", "error", err)
		}
	}
}

func (h *ReportsHandler) accountHandle(ctx context.Context, accountID int64) string {
	account, err := h.queries.GetAccountByID(ctx, accountID)
	if err != nil {
		return "#" + strconv.FormatInt(accountID, 10)
	}
	handle := "@" + account.Username
	if account.Domain.Valid && account.Domain.String != "" {
		handle += "@" + account.Domain.String
	}
	return handle
}

// RegisterRoutes registers report routes
func (h *ReportsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/reports", h.Create)
}
