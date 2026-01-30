// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"net/http"
	"strings"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

// PeersHandler handles peer-related endpoints
type PeersHandler struct {
	queries *db.Queries
}

// NewPeersHandler creates a new peers handler
func NewPeersHandler(queries *db.Queries) *PeersHandler {
	return &PeersHandler{queries: queries}
}

func (h *PeersHandler) useDB() bool {
	return h.queries != nil
}

// Search handles GET /api/v1/peers/search
// Searches for known instance domains matching the query
func (h *PeersHandler) Search(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(web.QueryParam(r, "q"))
	if query == "" {
		web.JSON(w, http.StatusOK, []string{})
		return
	}

	ctx := r.Context()
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Peers unavailable")
		return
	}

	if h.useDB() {
		// Get all known domains
		domains, err := h.queries.GetKnownDomains(ctx)
		if err != nil {
			web.JSON(w, http.StatusOK, []string{})
			return
		}

		// Filter by query
		var matching []string
		for _, domain := range domains {
			if domain.Valid && strings.Contains(strings.ToLower(domain.String), query) {
				matching = append(matching, domain.String)
			}
		}

		// Limit results
		if len(matching) > 50 {
			matching = matching[:50]
		}

		web.JSON(w, http.StatusOK, matching)
		return
	}

}

// RegisterRoutes registers the peers routes
func (h *PeersHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/peers/search", h.Search)
}
