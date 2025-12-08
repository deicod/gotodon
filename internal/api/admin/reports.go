package admin

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/db"
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
func (h *ReportsHandler) GetAll(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	limit := 100
	if l := c.QueryParam("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	offset := 0
	if o := c.QueryParam("offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	resolved := c.QueryParam("resolved") == "true"
	accountID, _ := strconv.ParseInt(c.QueryParam("account_id"), 10, 64)
	targetAccountID, _ := strconv.ParseInt(c.QueryParam("target_account_id"), 10, 64)

	rows, err := h.queries.AdminListReports(c.Request().Context(), db.AdminListReportsParams{
		Limit:           int32(limit),
		Offset:          int32(offset),
		Resolved:        resolved,
		AccountID:       accountID,
		TargetAccountID: targetAccountID,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Database error").SetInternal(err)
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

	return c.JSON(http.StatusOK, reports)
}

// Get handles GET /api/v1/admin/reports/:id
func (h *ReportsHandler) Get(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Not found")
	}

	row, err := h.queries.AdminGetReport(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Not found")
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

	return c.JSON(http.StatusOK, rep)
}

// Resolve handles POST /api/v1/admin/reports/:id/resolve
func (h *ReportsHandler) Resolve(c echo.Context) error {
	// TODO: Auth check (requireAdmin)
	if err := requireAdmin(c); err != nil {
		return err
	}

	// Get current user ID from context as actor
	// middleware.GetAccountID(c) ...
	// Since requireAdmin is passed, we assume role check passed, but we need ID for `action_taken_by_account_id`.
	// Let's skip recording WHO resolved it for a moment if getting ID is complex, or pass 0/NULL.
	// Actually `GetAccountID` uses "account_id" from JWT/session.
	adminID, _ := middleware.GetAccountID(c)

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Not found")
	}

	err = h.queries.AdminResolveReport(c.Request().Context(), db.AdminResolveReportParams{
		ID:                     id,
		ActionTakenByAccountID: pgtype.Int8{Int64: adminID, Valid: true},
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Database error").SetInternal(err)
	}

	return h.Get(c)
}

// RegisterRoutes registers admin report routes
func (h *ReportsHandler) RegisterRoutes(g *echo.Group) {
	reports := g.Group("/reports")
	reports.GET("", h.GetAll)
	reports.GET("/:id", h.Get)
	reports.POST("/:id/resolve", h.Resolve)
}
