package nodeinfo

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

const (
	nodeInfoVersion                  = "2.0"
	nodeInfoSchemaRel                = "http://nodeinfo.diaspora.software/ns/schema/2.0"
	defaultNodeInfoSoftwareVersion   = "4.5.3"
	defaultNodeInfoMetadataName      = "Gotodon"
	defaultNodeInfoMetadataShortDesc = "A Mastodon-compatible server"
	settingSiteTitle                 = "site_title"
	settingSiteDescription           = "site_short_description"
	settingRegistrationsEnabled      = "registrations_enabled"
	nodeInfoNoStoreCacheControl      = "private, no-store"
)

var nodeInfoSettingKeys = []string{
	settingSiteTitle,
	settingSiteDescription,
	settingRegistrationsEnabled,
}

type querier interface {
	CountInstanceUsers(ctx context.Context) (int64, error)
	CountNodeInfoActiveUsersMonth(ctx context.Context) (int64, error)
	CountNodeInfoActiveUsersHalfyear(ctx context.Context) (int64, error)
	CountInstanceStatuses(ctx context.Context) (int64, error)
	GetSettingsByVars(ctx context.Context, vars []string) ([]db.GetSettingsByVarsRow, error)
}

// Handler serves NodeInfo discovery and schema endpoints.
type Handler struct {
	queries  querier
	cfg      *config.Config
	cache    nodeInfoCache
	cacheTTL time.Duration
}

// NewHandler creates a NodeInfo handler.
func NewHandler(queries querier, cfg *config.Config) *Handler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &Handler{
		queries:  queries,
		cfg:      cfg,
		cacheTTL: 30 * time.Minute,
	}
}

// RegisterRoutes registers NodeInfo routes.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/nodeinfo", h.Discovery)
	mux.HandleFunc("GET /nodeinfo/2.0", h.Show)
}

// Discovery handles GET /.well-known/nodeinfo.
func (h *Handler) Discovery(w http.ResponseWriter, _ *http.Request) {
	web.JSON(w, http.StatusOK, map[string]any{
		"links": []map[string]string{
			{
				"rel":  nodeInfoSchemaRel,
				"href": "https://" + h.publicDomain() + "/nodeinfo/2.0",
			},
		},
	})
}

// Show handles GET /nodeinfo/2.0.
func (h *Handler) Show(w http.ResponseWriter, r *http.Request) {
	if payload, maxAge, ok := h.loadCachedPayload(); ok {
		w.Header().Set("Cache-Control", nodeInfoPublicCacheControl(maxAge))
		writeJSONPayload(w, payload)
		return
	}

	settings, settingsLoaded := h.loadSettings(r.Context())
	completeData := settingsLoaded

	totalUsers := int64(0)
	activeMonth := int64(0)
	activeHalfyear := int64(0)
	localPosts := int64(0)
	if h != nil && h.queries != nil {
		if count, err := h.queries.CountInstanceUsers(r.Context()); err == nil {
			totalUsers = count
		} else {
			completeData = false
		}
		if count, err := h.queries.CountNodeInfoActiveUsersMonth(r.Context()); err == nil {
			activeMonth = count
		} else {
			completeData = false
		}
		if count, err := h.queries.CountNodeInfoActiveUsersHalfyear(r.Context()); err == nil {
			activeHalfyear = count
		} else {
			completeData = false
		}
		if count, err := h.queries.CountInstanceStatuses(r.Context()); err == nil {
			localPosts = count
		} else {
			completeData = false
		}
	}

	resp := map[string]any{
		"version": nodeInfoVersion,
		"software": map[string]string{
			"name":    "mastodon",
			"version": defaultNodeInfoSoftwareVersion,
		},
		"protocols": []string{"activitypub"},
		"services": map[string][]string{
			"outbound": []string{},
			"inbound":  []string{},
		},
		"usage": map[string]any{
			"users": map[string]int64{
				"total":          totalUsers,
				"activeMonth":    activeMonth,
				"activeHalfyear": activeHalfyear,
			},
			"localPosts": localPosts,
		},
		"openRegistrations": h.openRegistrations(settings.registrationsEnabled),
		"metadata": map[string]string{
			"nodeName":        settings.nodeName,
			"nodeDescription": settings.nodeDescription,
		},
	}

	payload, err := json.Marshal(resp)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "NodeInfo unavailable")
		return
	}
	if completeData {
		w.Header().Set("Cache-Control", nodeInfoPublicCacheControl(h.cacheMaxAgeSeconds()))
		h.storeCachedPayload(payload)
	} else {
		w.Header().Set("Cache-Control", nodeInfoNoStoreCacheControl)
	}
	writeJSONPayload(w, payload)
}

type nodeInfoSettings struct {
	nodeName        string
	nodeDescription string
	// nil means setting is unavailable or unparsable and config fallback should be used.
	registrationsEnabled *bool
}

func (h *Handler) loadSettings(ctx context.Context) (nodeInfoSettings, bool) {
	settings := nodeInfoSettings{
		nodeName:        defaultNodeInfoMetadataName,
		nodeDescription: defaultNodeInfoMetadataShortDesc,
	}
	if h == nil || h.queries == nil {
		return settings, true
	}

	rows, err := h.queries.GetSettingsByVars(ctx, nodeInfoSettingKeys)
	if err != nil {
		return settings, false
	}

	for _, row := range rows {
		if !row.Value.Valid {
			continue
		}
		switch row.Var {
		case settingSiteTitle:
			settings.nodeName = row.Value.String
		case settingSiteDescription:
			settings.nodeDescription = row.Value.String
		case settingRegistrationsEnabled:
			if parsed, err := strconv.ParseBool(row.Value.String); err == nil {
				settings.registrationsEnabled = &parsed
			}
		}
	}
	return settings, true
}

func (h *Handler) openRegistrations(registrationsSetting *bool) bool {
	if h == nil || h.cfg == nil {
		return true
	}
	if h.cfg.Instance.SingleUserMode {
		return false
	}
	if registrationsSetting != nil {
		return *registrationsSetting
	}
	return strings.TrimSpace(strings.ToLower(h.cfg.Instance.RegistrationsMode)) != "none"
}

func (h *Handler) publicDomain() string {
	if h == nil || h.cfg == nil {
		return "example.com"
	}
	if domain := strings.TrimSpace(h.cfg.Instance.WebDomain); domain != "" {
		return domain
	}
	if domain := strings.TrimSpace(h.cfg.Instance.LocalDomain); domain != "" {
		return domain
	}
	return "example.com"
}

type nodeInfoCache struct {
	mu        sync.RWMutex
	payload   []byte
	expiresAt time.Time
}

func (h *Handler) loadCachedPayload() ([]byte, int64, bool) {
	if h == nil || h.cacheTTL <= 0 {
		return nil, 0, false
	}
	now := time.Now()
	h.cache.mu.RLock()
	if len(h.cache.payload) > 0 && now.Before(h.cache.expiresAt) {
		payload := append([]byte(nil), h.cache.payload...)
		remaining := durationToMaxAge(h.cache.expiresAt.Sub(now))
		h.cache.mu.RUnlock()
		return payload, remaining, true
	}
	h.cache.mu.RUnlock()
	return nil, 0, false
}

func (h *Handler) storeCachedPayload(payload []byte) {
	if h == nil || h.cacheTTL <= 0 {
		return
	}
	h.cache.mu.Lock()
	h.cache.payload = append(h.cache.payload[:0], payload...)
	h.cache.expiresAt = time.Now().Add(h.cacheTTL)
	h.cache.mu.Unlock()
}

func writeJSONPayload(w http.ResponseWriter, payload []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(payload); err != nil {
		return
	}
}

func (h *Handler) cacheMaxAgeSeconds() int64 {
	if h == nil {
		return 0
	}
	return durationToMaxAge(h.cacheTTL)
}

func durationToMaxAge(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64((d + time.Second - 1) / time.Second)
}

func nodeInfoPublicCacheControl(maxAge int64) string {
	return "public, max-age=" + strconv.FormatInt(maxAge, 10)
}
