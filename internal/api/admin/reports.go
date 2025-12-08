// Package admin provides report management for the Admin API.
package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/pkg/snowflake"
)

// ReportsHandler handles admin report endpoints
type ReportsHandler struct {
	idGen   *snowflake.Generator
	reports map[int64]*Report
}

// NewReportsHandler creates a new reports handler
func NewReportsHandler() *ReportsHandler {
	return &ReportsHandler{
		idGen:   snowflake.NewGenerator(),
		reports: make(map[int64]*Report),
	}
}

// Report represents a report
type Report struct {
	ID                string     `json:"id"`
	ActionTaken       bool       `json:"action_taken"`
	ActionTakenAt     *time.Time `json:"action_taken_at,omitempty"`
	Category          string     `json:"category"`
	Comment           string     `json:"comment"`
	Forwarded         bool       `json:"forwarded"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	AccountID         string     `json:"account,omitempty"`
	TargetAccountID   string     `json:"target_account,omitempty"`
	AssignedAccountID *string    `json:"assigned_account,omitempty"`
	ActionTakenByID   *string    `json:"action_taken_by_account,omitempty"`
	StatusIDs         []string   `json:"statuses,omitempty"`
	RuleIDs           []int      `json:"rules,omitempty"`
}

// Valid report categories
var validCategories = map[string]bool{
	"spam":      true,
	"violation": true,
	"legal":     true,
	"other":     true,
}

// GetAll handles GET /api/v1/admin/reports
func (h *ReportsHandler) GetAll(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	// Parse query parameters
	resolved := c.QueryParam("resolved")
	limit := 100
	if l := c.QueryParam("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	reports := make([]Report, 0, limit)
	for _, report := range h.reports {
		// Filter by resolved status
		if resolved == "true" && !report.ActionTaken {
			continue
		}
		if resolved == "false" && report.ActionTaken {
			continue
		}

		reports = append(reports, *report)
		if len(reports) >= limit {
			break
		}
	}

	return c.JSON(http.StatusOK, reports)
}

// Get handles GET /api/v1/admin/reports/:id
func (h *ReportsHandler) Get(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	reportID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	report, exists := h.reports[reportID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	return c.JSON(http.StatusOK, report)
}

// Update handles PUT /api/v1/admin/reports/:id
func (h *ReportsHandler) Update(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	reportID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	report, exists := h.reports[reportID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	var req struct {
		Category string `json:"category" form:"category"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	if req.Category != "" {
		if !validCategories[req.Category] {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Invalid category",
			})
		}
		report.Category = req.Category
	}

	report.UpdatedAt = time.Now()

	return c.JSON(http.StatusOK, report)
}

// Assign handles POST /api/v1/admin/reports/:id/assign_to_self
func (h *ReportsHandler) Assign(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	accountID, _ := middleware.GetAccountID(c)

	reportID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	report, exists := h.reports[reportID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	assignedID := strconv.FormatInt(accountID, 10)
	report.AssignedAccountID = &assignedID
	report.UpdatedAt = time.Now()

	return c.JSON(http.StatusOK, report)
}

// Unassign handles POST /api/v1/admin/reports/:id/unassign
func (h *ReportsHandler) Unassign(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	reportID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	report, exists := h.reports[reportID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	report.AssignedAccountID = nil
	report.UpdatedAt = time.Now()

	return c.JSON(http.StatusOK, report)
}

// Resolve handles POST /api/v1/admin/reports/:id/resolve
func (h *ReportsHandler) Resolve(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	accountID, _ := middleware.GetAccountID(c)

	reportID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	report, exists := h.reports[reportID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	now := time.Now()
	actionBy := strconv.FormatInt(accountID, 10)
	report.ActionTaken = true
	report.ActionTakenAt = &now
	report.ActionTakenByID = &actionBy
	report.UpdatedAt = now

	return c.JSON(http.StatusOK, report)
}

// Reopen handles POST /api/v1/admin/reports/:id/reopen
func (h *ReportsHandler) Reopen(c echo.Context) error {
	if err := requireAdmin(c); err != nil {
		return err
	}

	reportID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	report, exists := h.reports[reportID]
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	report.ActionTaken = false
	report.ActionTakenAt = nil
	report.ActionTakenByID = nil
	report.UpdatedAt = time.Now()

	return c.JSON(http.StatusOK, report)
}

func requireAdmin(c echo.Context) error {
	role, ok := c.Get("role").(string)
	if !ok || (role != "admin" && role != "moderator") {
		return echo.NewHTTPError(http.StatusForbidden, map[string]string{
			"error": "This action is not allowed",
		})
	}
	return nil
}

// RegisterRoutes registers admin report routes
func (h *ReportsHandler) RegisterRoutes(g *echo.Group) {
	reports := g.Group("/reports")
	reports.GET("", h.GetAll)
	reports.GET("/:id", h.Get)
	reports.PUT("/:id", h.Update)
	reports.POST("/:id/assign_to_self", h.Assign)
	reports.POST("/:id/unassign", h.Unassign)
	reports.POST("/:id/resolve", h.Resolve)
	reports.POST("/:id/reopen", h.Reopen)
}
