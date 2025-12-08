package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
)

// ReportsHandler handles report endpoints
type ReportsHandler struct {
	queries *db.Queries
}

// NewReportsHandler creates a new reports handler
func NewReportsHandler(queries *db.Queries) *ReportsHandler {
	return &ReportsHandler{
		queries: queries,
	}
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
func (h *ReportsHandler) Create(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}

	var req struct {
		AccountID string   `json:"account_id" form:"account_id"`
		StatusIDs []string `json:"status_ids" form:"status_ids"`
		Comment   string   `json:"comment" form:"comment"`
		Forward   bool     `json:"forward" form:"forward"`
		Category  string   `json:"category" form:"category"`
		RuleIDs   []string `json:"rule_ids" form:"rule_ids"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
	}

	targetID, err := strconv.ParseInt(req.AccountID, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid account_id")
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

	report, err := h.queries.CreateReport(c.Request().Context(), db.CreateReportParams{
		AccountID:       accountID,
		TargetAccountID: targetID,
		StatusIds:       statusIDs,
		Comment:         req.Comment,
		Category:        categoryInt,
		Forwarded:       pgtype.Bool{Bool: req.Forward, Valid: true},
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Database error").SetInternal(err)
	}

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

	return c.JSON(http.StatusOK, resp)
}
