// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// AnnualReportsHandler handles annual reports endpoints
type AnnualReportsHandler struct {
	queries *db.Queries
}

// NewAnnualReportsHandler creates a new annual reports handler
func NewAnnualReportsHandler(queries *db.Queries) *AnnualReportsHandler {
	return &AnnualReportsHandler{queries: queries}
}

func (h *AnnualReportsHandler) useDB() bool {
	return h.queries != nil
}

// AnnualReport represents an annual report in API responses
type AnnualReport struct {
	Year     int         `json:"year"`
	Data     interface{} `json:"data"`
	ViewedAt *time.Time  `json:"viewed_at,omitempty"`
}

// List handles GET /api/v1/annual_reports
func (h *AnnualReportsHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		dbReports, err := h.queries.ListAnnualReportsByAccount(ctx, accountID)
		if err != nil {
			web.JSON(w, http.StatusOK, []AnnualReport{})
			return
		}

		reports := make([]AnnualReport, len(dbReports))
		for i, r := range dbReports {
			reports[i] = h.dbReportToAPI(r)
		}
		web.JSON(w, http.StatusOK, reports)
		return
	}

	// Mock fallback - return current year placeholder
	currentYear := time.Now().Year()
	web.JSON(w, http.StatusOK, []AnnualReport{
		{Year: currentYear - 1, Data: map[string]interface{}{}},
	})
}

// Get handles GET /api/v1/annual_reports/:id
func (h *AnnualReportsHandler) Get(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	// ID is the year
	yearStr := web.Param(r, "id")
	year, err := strconv.Atoi(yearStr)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		dbReport, err := h.queries.GetAnnualReportByAccountAndYear(ctx, db.GetAnnualReportByAccountAndYearParams{
			AccountID: accountID,
			Year:      int32(year),
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, h.dbReportToAPI(dbReport))
		return
	}

	// Mock fallback
	web.JSON(w, http.StatusOK, AnnualReport{
		Year: year,
		Data: map[string]interface{}{
			"statuses_count":   0,
			"followers_gained": 0,
			"following_gained": 0,
			"top_hashtags":     []string{},
		},
	})
}

// MarkAsRead handles POST /api/v1/annual_reports/:id/read
func (h *AnnualReportsHandler) MarkAsRead(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	// ID is the year
	yearStr := web.Param(r, "id")
	year, err := strconv.Atoi(yearStr)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		// Get report by account and year to get the ID
		dbReport, err := h.queries.GetAnnualReportByAccountAndYear(ctx, db.GetAnnualReportByAccountAndYearParams{
			AccountID: accountID,
			Year:      int32(year),
		})
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		// Mark as viewed
		updated, err := h.queries.MarkAnnualReportViewed(ctx, dbReport.ID)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to mark as read")
			return
		}

		web.JSON(w, http.StatusOK, h.dbReportToAPI(updated))
		return
	}

	// Mock fallback
	now := time.Now()
	web.JSON(w, http.StatusOK, AnnualReport{
		Year:     year,
		Data:     map[string]interface{}{},
		ViewedAt: &now,
	})
}

func (h *AnnualReportsHandler) dbReportToAPI(r db.GeneratedAnnualReport) AnnualReport {
	report := AnnualReport{
		Year: int(r.Year),
	}

	// Parse JSON data if valid
	if len(r.Data) > 0 {
		var data interface{}
		if err := json.Unmarshal(r.Data, &data); err == nil {
			report.Data = data
		} else {
			report.Data = map[string]interface{}{}
		}
	} else {
		report.Data = map[string]interface{}{}
	}

	if r.ViewedAt.Valid {
		report.ViewedAt = &r.ViewedAt.Time
	}

	return report
}

// RegisterRoutes registers the annual reports routes
func (h *AnnualReportsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/annual_reports", h.List)
	mux.HandleFunc("GET /api/v1/annual_reports/{id}", h.Get)
	mux.HandleFunc("POST /api/v1/annual_reports/{id}/read", h.MarkAsRead)
}
