package admin

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// ReportsHandler handles admin report endpoints
type ReportsHandler struct {
	queries *db.Queries
}

// NewReportsHandler creates a new admin reports handler
func NewReportsHandler(queries *db.Queries) *ReportsHandler {
	return &ReportsHandler{
		queries: queries,
	}
}

// AdminReport represents a report in the admin view
type AdminReport struct {
	ID              string      `json:"id"`
	ActionTaken     bool        `json:"action_taken"`
	ActionTakenAt   *string     `json:"action_taken_at"`
	Category        string      `json:"category"`
	Comment         string      `json:"comment"`
	Forwarded       bool        `json:"forwarded"`
	StatusIDs       []string    `json:"status_ids"`
	RuleIDs         []string    `json:"rule_ids"`
	TargetAccountID string      `json:"target_account_id"`
	TargetAccount   *v1.Account `json:"target_account"`
	AccountID       string      `json:"account_id"`
	Account         *v1.Account `json:"account"`
	CreatedAt       string      `json:"created_at"`
	UpdatedAt       string      `json:"updated_at"`
}

func mapCategory(c int32) string {
	switch c {
	case 1:
		return "spam"
	case 2:
		return "violation"
	default:
		return "other"
	}
}

// GetAll handles GET /api/v1/admin/reports
func (h *ReportsHandler) GetAll(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	limit := 100
	if l := web.QueryParam(r, "limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	offset := 0
	if o := web.QueryParam(r, "offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	resolved := web.QueryParam(r, "resolved") == "true"
	accountID, _ := strconv.ParseInt(web.QueryParam(r, "account_id"), 10, 64)
	targetAccountID, _ := strconv.ParseInt(web.QueryParam(r, "target_account_id"), 10, 64)

	rows, err := h.queries.AdminListReports(r.Context(), db.AdminListReportsParams{
		Limit:           int32(limit),
		Offset:          int32(offset),
		Resolved:        resolved,
		AccountID:       accountID,
		TargetAccountID: targetAccountID,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	reports := make([]AdminReport, 0, len(rows))
	for _, row := range rows {
		rep := AdminReport{
			ID:              strconv.FormatInt(row.ID, 10),
			ActionTaken:     row.ActionTakenAt.Valid,
			Category:        mapCategory(row.Category),
			Comment:         row.Comment,
			Forwarded:       row.Forwarded.Bool,
			TargetAccountID: strconv.FormatInt(row.TargetAccountID, 10),
			AccountID:       strconv.FormatInt(row.AccountID, 10),
			CreatedAt:       row.CreatedAt.Time.String(), // ISO format preferred
			UpdatedAt:       row.UpdatedAt.Time.String(),
		}

		if row.ActionTakenAt.Valid {
			t := row.ActionTakenAt.Time.String()
			rep.ActionTakenAt = &t
		}

		for _, sid := range row.StatusIds {
			rep.StatusIDs = append(rep.StatusIDs, strconv.FormatInt(sid, 10))
		}

		// Hydrate accounts (simplified, usually minimal user info)
		// We have joined fields: AccountUsername, AccountDomain, TargetAccountUsername...
		// Ideally we return full Account object structure but partial info.
		// For now, construct minimal Account objects.

		rep.Account = &v1.Account{
			ID:       strconv.FormatInt(row.AccountID, 10),
			Username: row.AccountUsername,
			// Acct need domain handling
		}
		if row.AccountDomain.Valid {
			rep.Account.Acct = row.AccountUsername + "@" + row.AccountDomain.String
		} else {
			rep.Account.Acct = row.AccountUsername
		}

		rep.TargetAccount = &v1.Account{
			ID:       strconv.FormatInt(row.TargetAccountID, 10),
			Username: row.TargetAccountUsername,
		}
		if row.TargetAccountDomain.Valid {
			rep.TargetAccount.Acct = row.TargetAccountUsername + "@" + row.TargetAccountDomain.String
		} else {
			rep.TargetAccount.Acct = row.TargetAccountUsername
		}

		reports = append(reports, rep)
	}

	web.JSON(w, http.StatusOK, reports)
}

// Get handles GET /api/v1/admin/reports/:id
func (h *ReportsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Not found")
		return
	}

	row, err := h.queries.AdminGetReport(r.Context(), id)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Not found")
		return
	}

	rep := AdminReport{
		ID:              strconv.FormatInt(row.ID, 10),
		ActionTaken:     row.ActionTakenAt.Valid,
		Category:        mapCategory(row.Category),
		Comment:         row.Comment,
		Forwarded:       row.Forwarded.Bool,
		TargetAccountID: strconv.FormatInt(row.TargetAccountID, 10),
		AccountID:       strconv.FormatInt(row.AccountID, 10),
		CreatedAt:       row.CreatedAt.Time.String(),
		UpdatedAt:       row.UpdatedAt.Time.String(),
	}
	// Note: AdminGetReport does not join accounts, so we miss account details in detail view unless we fetch them or update query.
	// For MVP, we proceed without full account details in Get, or we can fetch them separately if needed.
	// The List query had JOINs. The Get query did not (in generated code inspection).
	// Let's rely on basic ID info for now.

	web.JSON(w, http.StatusOK, rep)
}

// Resolve handles POST /api/v1/admin/reports/:id/resolve
func (h *ReportsHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}

	// Get current user ID from context as actor
	adminID, _ := middleware.GetAccountID(r.Context())

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Not found")
		return
	}

	err = h.queries.AdminResolveReport(r.Context(), db.AdminResolveReportParams{
		ID:                     id,
		ActionTakenByAccountID: pgtype.Int8{Int64: adminID, Valid: true},
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	h.Get(w, r)
}

// RegisterRoutes registers admin report routes
func (h *ReportsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/reports", h.GetAll)
	mux.HandleFunc("GET /api/v1/admin/reports/{id}", h.Get)
	mux.HandleFunc("POST /api/v1/admin/reports/{id}/resolve", h.Resolve)
}
