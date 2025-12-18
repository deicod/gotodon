// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strconv"
	"time"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/jackc/pgx/v5/pgtype"
)

// InstanceInfoHandler handles instance information endpoints
type InstanceInfoHandler struct {
	queries *db.Queries
	cfg     *config.Config
}

// NewInstanceInfoHandler creates a new instance info handler
func NewInstanceInfoHandler(queries *db.Queries, cfg *config.Config) *InstanceInfoHandler {
	return &InstanceInfoHandler{queries: queries, cfg: cfg}
}

// RuleResponse represents a server rule in API responses
type RuleResponse struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Hint string `json:"hint,omitempty"`
}

// ActivityWeek represents weekly activity statistics
type ActivityWeek struct {
	Week          string `json:"week"`
	Statuses      string `json:"statuses"`
	Logins        string `json:"logins"`
	Registrations string `json:"registrations"`
}

// ExtendedDescription represents extended instance description
type ExtendedDescription struct {
	UpdatedAt *time.Time `json:"updated_at"`
	Content   string     `json:"content"`
}

// PrivacyPolicy represents the instance privacy policy
type PrivacyPolicy struct {
	UpdatedAt *time.Time `json:"updated_at"`
	Content   string     `json:"content"`
}

// TermsOfService represents the instance terms of service
type TermsOfService struct {
	UpdatedAt *time.Time `json:"updated_at"`
	Content   string     `json:"content"`
}

// Rules handles GET /api/v1/instance/rules
func (h *InstanceInfoHandler) Rules(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		// Mock fallback
		web.JSON(w, http.StatusOK, []RuleResponse{
			{ID: "1", Text: "Be respectful to others."},
			{ID: "2", Text: "No spam or advertising."},
			{ID: "3", Text: "No illegal content."},
		})
		return
	}

	ctx := r.Context()
	rules, err := h.queries.ListRules(ctx)
	if err != nil {
		web.JSON(w, http.StatusOK, []RuleResponse{})
		return
	}

	result := make([]RuleResponse, len(rules))
	for i, rule := range rules {
		result[i] = RuleResponse{
			ID:   strconv.FormatInt(rule.ID, 10),
			Text: rule.Text,
			Hint: rule.Hint,
		}
	}

	web.JSON(w, http.StatusOK, result)
}

// Peers handles GET /api/v1/instance/peers
func (h *InstanceInfoHandler) Peers(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		// Mock fallback - return empty list
		web.JSON(w, http.StatusOK, []string{})
		return
	}

	ctx := r.Context()

	// Get distinct domains from accounts table
	domains, err := h.queries.GetKnownDomains(ctx)
	if err != nil {
		web.JSON(w, http.StatusOK, []string{})
		return
	}

	web.JSON(w, http.StatusOK, domains)
}

// Activity handles GET /api/v1/instance/activity
func (h *InstanceInfoHandler) Activity(w http.ResponseWriter, r *http.Request) {
	// Return last 12 weeks of activity
	// In a production system, this would query aggregated statistics

	now := time.Now()
	weeks := make([]ActivityWeek, 12)

	for i := 0; i < 12; i++ {
		weekStart := now.AddDate(0, 0, -7*(i+1))
		weeks[i] = ActivityWeek{
			Week:          strconv.FormatInt(weekStart.Unix(), 10),
			Statuses:      "0",
			Logins:        "0",
			Registrations: "0",
		}
	}

	if h.queries != nil {
		// Get actual activity stats if database available
		ctx := r.Context()
		for i := 0; i < 12; i++ {
			weekStart := now.AddDate(0, 0, -7*(i+1))
			weekEnd := now.AddDate(0, 0, -7*i)

			// Get status count for this week
			statusCount, err := h.queries.CountStatusesInRange(ctx, db.CountStatusesInRangeParams{
				CreatedAt:   pgtype.Timestamptz{Time: weekStart, Valid: true},
				CreatedAt_2: pgtype.Timestamptz{Time: weekEnd, Valid: true},
			})
			if err == nil {
				weeks[i].Statuses = strconv.FormatInt(statusCount, 10)
			}
		}
	}

	web.JSON(w, http.StatusOK, weeks)
}

// ExtendedDescription handles GET /api/v1/instance/extended_description
func (h *InstanceInfoHandler) ExtendedDescriptionHandler(w http.ResponseWriter, r *http.Request) {
	web.JSON(w, http.StatusOK, ExtendedDescription{
		UpdatedAt: nil,
		Content:   "",
	})
}

// PrivacyPolicy handles GET /api/v1/instance/privacy_policy
func (h *InstanceInfoHandler) PrivacyPolicyHandler(w http.ResponseWriter, r *http.Request) {
	web.JSON(w, http.StatusOK, PrivacyPolicy{
		UpdatedAt: nil,
		Content:   "",
	})
}

// TermsOfService handles GET /api/v1/instance/terms_of_service
func (h *InstanceInfoHandler) TermsOfServiceHandler(w http.ResponseWriter, r *http.Request) {
	web.JSON(w, http.StatusOK, TermsOfService{
		UpdatedAt: nil,
		Content:   "",
	})
}

// TranslationLanguages handles GET /api/v1/instance/translation_languages
func (h *InstanceInfoHandler) TranslationLanguages(w http.ResponseWriter, r *http.Request) {
	// Return empty map since translation is not enabled
	web.JSON(w, http.StatusOK, map[string][]string{})
}

// Languages handles GET /api/v1/instance/languages
func (h *InstanceInfoHandler) Languages(w http.ResponseWriter, r *http.Request) {
	// Return available languages for posting
	web.JSON(w, http.StatusOK, []string{"en"})
}

// DomainBlocksPublic handles GET /api/v1/instance/domain_blocks (public info if enabled)
func (h *InstanceInfoHandler) DomainBlocksPublic(w http.ResponseWriter, r *http.Request) {
	// Return empty list - domain blocks are not publicly exposed by default
	web.JSON(w, http.StatusOK, []string{})
}

// RegisterRoutes registers the instance info routes
func (h *InstanceInfoHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/instance/rules", h.Rules)
	mux.HandleFunc("GET /api/v1/instance/peers", h.Peers)
	mux.HandleFunc("GET /api/v1/instance/activity", h.Activity)
	mux.HandleFunc("GET /api/v1/instance/extended_description", h.ExtendedDescriptionHandler)
	mux.HandleFunc("GET /api/v1/instance/privacy_policy", h.PrivacyPolicyHandler)
	mux.HandleFunc("GET /api/v1/instance/terms_of_service", h.TermsOfServiceHandler)
	mux.HandleFunc("GET /api/v1/instance/translation_languages", h.TranslationLanguages)
	mux.HandleFunc("GET /api/v1/instance/languages", h.Languages)
	mux.HandleFunc("GET /api/v1/instance/domain_blocks", h.DomainBlocksPublic)
}
