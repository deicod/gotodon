package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/snowflake"
)

const (
	defaultMetricsDays    = 7
	maxMetricsLimit       = 200
	compatMastodonVersion = "4.5.3"
)

type DimensionsHandler struct {
	queries *db.Queries
	cfg     *config.Config
	redis   *redis.Client
}

func NewDimensionsHandler(queries *db.Queries, cfg *config.Config, redisClient *redis.Client) *DimensionsHandler {
	return &DimensionsHandler{queries: queries, cfg: cfg, redis: redisClient}
}

type MeasuresHandler struct {
	queries *db.Queries
}

func NewMeasuresHandler(queries *db.Queries) *MeasuresHandler {
	return &MeasuresHandler{queries: queries}
}

type RetentionHandler struct {
	queries *db.Queries
}

func NewRetentionHandler(queries *db.Queries) *RetentionHandler {
	return &RetentionHandler{queries: queries}
}

type metricsRequest struct {
	Keys      []string `json:"keys"`
	StartAt   *string  `json:"start_at"`
	EndAt     *string  `json:"end_at"`
	Limit     *int     `json:"limit"`
	Frequency string   `json:"frequency"`
}

type metricRange struct {
	Start     time.Time
	End       time.Time
	StartDay  time.Time
	EndDay    time.Time
	PrevStart time.Time
	PrevEnd   time.Time
}

type dimensionResponse struct {
	Key  string          `json:"key"`
	Data []dimensionData `json:"data"`
}

type dimensionData struct {
	Key        string  `json:"key"`
	HumanKey   string  `json:"human_key"`
	Value      string  `json:"value"`
	Unit       *string `json:"unit,omitempty"`
	HumanValue *string `json:"human_value,omitempty"`
}

type measureResponse struct {
	Key           string        `json:"key"`
	Unit          *string       `json:"unit,omitempty"`
	Total         string        `json:"total"`
	HumanValue    *string       `json:"human_value,omitempty"`
	PreviousTotal *string       `json:"previous_total,omitempty"`
	Data          []measureData `json:"data"`
}

type measureData struct {
	Date  string `json:"date"`
	Value string `json:"value"`
}

type retentionCohort struct {
	Period    string          `json:"period"`
	Frequency string          `json:"frequency"`
	Data      []retentionData `json:"data"`
}

type retentionData struct {
	Date  string  `json:"date"`
	Rate  float64 `json:"rate"`
	Value string  `json:"value"`
}

func (h *DimensionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSON(w, http.StatusOK, []dimensionResponse{})
		return
	}

	req, raw, err := decodeMetricsRequest(r)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(req.Keys) == 0 {
		web.JSON(w, http.StatusOK, []dimensionResponse{})
		return
	}

	limit := 10
	if req.Limit != nil && *req.Limit > 0 {
		limit = *req.Limit
		if limit > maxMetricsLimit {
			limit = maxMetricsLimit
		}
	}

	rangeData, err := buildMetricRange(req.StartAt, req.EndAt, defaultMetricsDays)
	if err != nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Invalid date range")
		return
	}

	ctx := r.Context()
	responses := make([]dimensionResponse, 0, len(req.Keys))

	for _, key := range req.Keys {
		switch key {
		case "languages":
			resp, err := h.dimensionLanguages(ctx, rangeData, limit)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "sources":
			resp, err := h.dimensionSources(ctx, rangeData, limit)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "servers":
			resp, err := h.dimensionServers(ctx, rangeData, limit)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "space_usage":
			resp, err := h.dimensionSpaceUsage(ctx)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "software_versions":
			resp, err := h.dimensionSoftwareVersions(ctx)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "tag_servers":
			params, err := decodeKeyParams(raw, key)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid parameters")
				return
			}
			tagID, ok := paramInt64(params, "id")
			if !ok || tagID <= 0 {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid parameters")
				return
			}
			resp, err := h.dimensionTagServers(ctx, rangeData, limit, tagID)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "tag_languages":
			params, err := decodeKeyParams(raw, key)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid parameters")
				return
			}
			tagID, ok := paramInt64(params, "id")
			if !ok || tagID <= 0 {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid parameters")
				return
			}
			resp, err := h.dimensionTagLanguages(ctx, rangeData, limit, tagID)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "instance_accounts":
			params, err := decodeKeyParams(raw, key)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid parameters")
				return
			}
			domain := strings.TrimSpace(paramString(params, "domain"))
			if domain == "" {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid parameters")
				return
			}
			resp, err := h.dimensionInstanceAccounts(ctx, limit, domain)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "instance_languages":
			params, err := decodeKeyParams(raw, key)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid parameters")
				return
			}
			domain := strings.TrimSpace(paramString(params, "domain"))
			if domain == "" {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid parameters")
				return
			}
			resp, err := h.dimensionInstanceLanguages(ctx, rangeData, limit, domain)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		}
	}

	web.JSON(w, http.StatusOK, responses)
}

func (h *MeasuresHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSON(w, http.StatusOK, []measureResponse{})
		return
	}

	req, raw, err := decodeMetricsRequest(r)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(req.Keys) == 0 {
		web.JSON(w, http.StatusOK, []measureResponse{})
		return
	}

	rangeData, err := buildMetricRange(req.StartAt, req.EndAt, defaultMetricsDays)
	if err != nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Invalid date range")
		return
	}

	ctx := r.Context()
	responses := make([]measureResponse, 0, len(req.Keys))

	for _, key := range req.Keys {
		switch key {
		case "active_users":
			resp, err := h.measureActiveUsers(ctx, rangeData)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "new_users":
			resp, err := h.measureNewUsers(ctx, rangeData)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "interactions":
			resp, err := h.measureInteractions(ctx, rangeData)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "opened_reports":
			resp, err := h.measureOpenedReports(ctx, rangeData)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "resolved_reports":
			resp, err := h.measureResolvedReports(ctx, rangeData)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "tag_accounts":
			params, err := decodeKeyParams(raw, key)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid parameters")
				return
			}
			tagID, ok := paramInt64(params, "id")
			if !ok || tagID <= 0 {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid parameters")
				return
			}
			resp, err := h.measureTagAccounts(ctx, rangeData, tagID)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "tag_uses":
			params, err := decodeKeyParams(raw, key)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid parameters")
				return
			}
			tagID, ok := paramInt64(params, "id")
			if !ok || tagID <= 0 {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid parameters")
				return
			}
			resp, err := h.measureTagUses(ctx, rangeData, tagID)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "tag_servers":
			params, err := decodeKeyParams(raw, key)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid parameters")
				return
			}
			tagID, ok := paramInt64(params, "id")
			if !ok || tagID <= 0 {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid parameters")
				return
			}
			resp, err := h.measureTagServers(ctx, rangeData, tagID)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "instance_accounts":
			params, err := decodeKeyParams(raw, key)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid parameters")
				return
			}
			domain, includeSubdomains, ok := parseInstanceParams(params)
			if !ok {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid parameters")
				return
			}
			resp, err := h.measureInstanceAccounts(ctx, rangeData, domain, includeSubdomains)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "instance_follows":
			params, err := decodeKeyParams(raw, key)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid parameters")
				return
			}
			domain, includeSubdomains, ok := parseInstanceParams(params)
			if !ok {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid parameters")
				return
			}
			resp, err := h.measureInstanceFollows(ctx, rangeData, domain, includeSubdomains)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "instance_followers":
			params, err := decodeKeyParams(raw, key)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid parameters")
				return
			}
			domain, includeSubdomains, ok := parseInstanceParams(params)
			if !ok {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid parameters")
				return
			}
			resp, err := h.measureInstanceFollowers(ctx, rangeData, domain, includeSubdomains)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "instance_media_attachments":
			params, err := decodeKeyParams(raw, key)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid parameters")
				return
			}
			domain, includeSubdomains, ok := parseInstanceParams(params)
			if !ok {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid parameters")
				return
			}
			resp, err := h.measureInstanceMedia(ctx, rangeData, domain, includeSubdomains)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "instance_reports":
			params, err := decodeKeyParams(raw, key)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid parameters")
				return
			}
			domain, includeSubdomains, ok := parseInstanceParams(params)
			if !ok {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid parameters")
				return
			}
			resp, err := h.measureInstanceReports(ctx, rangeData, domain, includeSubdomains)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		case "instance_statuses":
			params, err := decodeKeyParams(raw, key)
			if err != nil {
				web.JSONError(w, http.StatusBadRequest, "Invalid parameters")
				return
			}
			domain, includeSubdomains, ok := parseInstanceParams(params)
			if !ok {
				web.JSONError(w, http.StatusUnprocessableEntity, "Invalid parameters")
				return
			}
			resp, err := h.measureInstanceStatuses(ctx, rangeData, domain, includeSubdomains)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			responses = append(responses, resp)
		}
	}

	web.JSON(w, http.StatusOK, responses)
}

func (h *RetentionHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(w, r, h.queries); err != nil {
		return
	}
	if h.queries == nil {
		web.JSON(w, http.StatusOK, []retentionCohort{})
		return
	}

	req, _, err := decodeMetricsRequest(r)
	if err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	frequency := strings.ToLower(strings.TrimSpace(req.Frequency))
	if frequency != "month" {
		frequency = "day"
	}

	rangeData, err := buildRetentionRange(req.StartAt, req.EndAt, frequency)
	if err != nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Invalid date range")
		return
	}

	rows, err := h.queries.AdminMetricsRetention(r.Context(), db.AdminMetricsRetentionParams{
		StartAt:   pgTimestamp(rangeData.Start),
		EndAt:     pgTimestamp(rangeData.End),
		Frequency: frequency,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	cohorts := make([]retentionCohort, 0)
	var current *retentionCohort
	for _, row := range rows {
		if !row.CohortPeriod.Valid || !row.RetentionPeriod.Valid {
			continue
		}
		cohortDate := dateOnly(row.CohortPeriod.Time)
		retentionDate := dateOnly(row.RetentionPeriod.Time)
		if current == nil || current.Period != formatDate(cohortDate) {
			cohort := retentionCohort{
				Period:    formatDate(cohortDate),
				Frequency: frequency,
				Data:      []retentionData{},
			}
			cohorts = append(cohorts, cohort)
			current = &cohorts[len(cohorts)-1]
		}
		value := strconv.FormatInt(row.Value, 10)
		current.Data = append(current.Data, retentionData{
			Date:  formatDate(retentionDate),
			Rate:  row.Rate,
			Value: value,
		})
	}

	web.JSON(w, http.StatusOK, cohorts)
}

func (h *DimensionsHandler) dimensionLanguages(ctx context.Context, rangeData metricRange, limit int) (dimensionResponse, error) {
	rows, err := h.queries.AdminMetricsLanguages(ctx, db.AdminMetricsLanguagesParams{
		StartAt: pgTime(rangeData.Start),
		EndAt:   pgTime(rangeData.End),
		Limit:   int32(limit),
	})
	if err != nil {
		return dimensionResponse{}, err
	}

	data := make([]dimensionData, 0, len(rows))
	for _, row := range rows {
		locale := strings.TrimSpace(row.Locale.String)
		if !row.Locale.Valid || locale == "" {
			continue
		}
		data = append(data, dimensionData{
			Key:      locale,
			HumanKey: humanLocaleName(locale),
			Value:    strconv.FormatInt(row.Value, 10),
		})
	}

	return dimensionResponse{Key: "languages", Data: data}, nil
}

func (h *DimensionsHandler) dimensionSources(ctx context.Context, rangeData metricRange, limit int) (dimensionResponse, error) {
	rows, err := h.queries.AdminMetricsSources(ctx, db.AdminMetricsSourcesParams{
		StartAt: pgTime(rangeData.Start),
		EndAt:   pgTime(rangeData.End),
		Limit:   int32(limit),
	})
	if err != nil {
		return dimensionResponse{}, err
	}

	data := make([]dimensionData, 0, len(rows))
	for _, row := range rows {
		key := "web"
		humanKey := "Web"
		if row.Name.Valid && strings.TrimSpace(row.Name.String) != "" {
			key = row.Name.String
			humanKey = row.Name.String
		}
		data = append(data, dimensionData{
			Key:      key,
			HumanKey: humanKey,
			Value:    strconv.FormatInt(row.Value, 10),
		})
	}

	return dimensionResponse{Key: "sources", Data: data}, nil
}

func (h *DimensionsHandler) dimensionServers(ctx context.Context, rangeData metricRange, limit int) (dimensionResponse, error) {
	earliest, latest := statusIDRange(rangeData)
	rows, err := h.queries.AdminMetricsServers(ctx, db.AdminMetricsServersParams{
		EarliestStatusID: earliest,
		LatestStatusID:   latest,
		Limit:            int32(limit),
	})
	if err != nil {
		return dimensionResponse{}, err
	}

	localDomain := ""
	if h.cfg != nil {
		localDomain = h.cfg.Instance.LocalDomain
	}

	data := make([]dimensionData, 0, len(rows))
	for _, row := range rows {
		domain := strings.TrimSpace(row.Domain.String)
		if !row.Domain.Valid || domain == "" {
			domain = localDomain
		}
		data = append(data, dimensionData{
			Key:      domain,
			HumanKey: domain,
			Value:    strconv.FormatInt(row.Value, 10),
		})
	}

	return dimensionResponse{Key: "servers", Data: data}, nil
}

func (h *DimensionsHandler) dimensionTagServers(ctx context.Context, rangeData metricRange, limit int, tagID int64) (dimensionResponse, error) {
	earliest, latest := statusIDRange(rangeData)
	rows, err := h.queries.AdminMetricsTagServers(ctx, db.AdminMetricsTagServersParams{
		TagID:            tagID,
		EarliestStatusID: earliest,
		LatestStatusID:   latest,
		Limit:            int32(limit),
	})
	if err != nil {
		return dimensionResponse{}, err
	}

	localDomain := ""
	if h.cfg != nil {
		localDomain = h.cfg.Instance.LocalDomain
	}

	data := make([]dimensionData, 0, len(rows))
	for _, row := range rows {
		domain := strings.TrimSpace(row.Domain.String)
		if !row.Domain.Valid || domain == "" {
			domain = localDomain
		}
		data = append(data, dimensionData{
			Key:      domain,
			HumanKey: domain,
			Value:    strconv.FormatInt(row.Value, 10),
		})
	}

	return dimensionResponse{Key: "tag_servers", Data: data}, nil
}

func (h *DimensionsHandler) dimensionTagLanguages(ctx context.Context, rangeData metricRange, limit int, tagID int64) (dimensionResponse, error) {
	earliest, latest := statusIDRange(rangeData)
	rows, err := h.queries.AdminMetricsTagLanguages(ctx, db.AdminMetricsTagLanguagesParams{
		TagID:            tagID,
		EarliestStatusID: earliest,
		LatestStatusID:   latest,
		Limit:            int32(limit),
	})
	if err != nil {
		return dimensionResponse{}, err
	}

	data := make([]dimensionData, 0, len(rows))
	for _, row := range rows {
		lang := strings.TrimSpace(row.Language)
		if lang == "" {
			lang = "und"
		}
		data = append(data, dimensionData{
			Key:      lang,
			HumanKey: humanLocaleName(lang),
			Value:    strconv.FormatInt(row.Value, 10),
		})
	}

	return dimensionResponse{Key: "tag_languages", Data: data}, nil
}

func (h *DimensionsHandler) dimensionInstanceAccounts(ctx context.Context, limit int, domain string) (dimensionResponse, error) {
	rows, err := h.queries.AdminMetricsInstanceAccountsDimension(ctx, db.AdminMetricsInstanceAccountsDimensionParams{
		Domain: textParam(domain),
		Limit:  int32(limit),
	})
	if err != nil {
		return dimensionResponse{}, err
	}

	data := make([]dimensionData, 0, len(rows))
	for _, row := range rows {
		username := strings.TrimSpace(row.Username)
		if username == "" {
			continue
		}
		data = append(data, dimensionData{
			Key:      username,
			HumanKey: username,
			Value:    strconv.FormatInt(row.Value, 10),
		})
	}

	return dimensionResponse{Key: "instance_accounts", Data: data}, nil
}

func (h *DimensionsHandler) dimensionInstanceLanguages(ctx context.Context, rangeData metricRange, limit int, domain string) (dimensionResponse, error) {
	earliest, latest := statusIDRange(rangeData)
	rows, err := h.queries.AdminMetricsInstanceLanguages(ctx, db.AdminMetricsInstanceLanguagesParams{
		Domain:           textParam(domain),
		EarliestStatusID: earliest,
		LatestStatusID:   latest,
		Limit:            int32(limit),
	})
	if err != nil {
		return dimensionResponse{}, err
	}

	data := make([]dimensionData, 0, len(rows))
	for _, row := range rows {
		lang := strings.TrimSpace(row.Language)
		if lang == "" {
			lang = "und"
		}
		data = append(data, dimensionData{
			Key:      lang,
			HumanKey: humanLocaleName(lang),
			Value:    strconv.FormatInt(row.Value, 10),
		})
	}

	return dimensionResponse{Key: "instance_languages", Data: data}, nil
}

func (h *DimensionsHandler) dimensionSpaceUsage(ctx context.Context) (dimensionResponse, error) {
	postgresSize, err := h.queries.AdminMetricsPostgresSize(ctx)
	if err != nil {
		return dimensionResponse{}, err
	}
	mediaSize, err := h.queries.AdminMetricsMediaSizeComponents(ctx)
	if err != nil {
		return dimensionResponse{}, err
	}

	data := []dimensionData{}
	data = append(data, sizeDimension("postgresql", "PostgreSQL", postgresSize))

	if h.redis != nil {
		if redisSize, ok := redisMemoryUsage(ctx, h.redis); ok {
			data = append(data, sizeDimension("redis", "Redis", redisSize))
		}
	}

	totalMedia := mediaSize.MediaAttachmentsSize + mediaSize.CustomEmojisSize + mediaSize.PreviewCardsSize + mediaSize.AccountMediaSize + mediaSize.BackupsSize + mediaSize.SiteUploadsSize
	data = append(data, sizeDimension("media", "Media", totalMedia))

	return dimensionResponse{Key: "space_usage", Data: data}, nil
}

func (h *DimensionsHandler) dimensionSoftwareVersions(ctx context.Context) (dimensionResponse, error) {
	versionValue, err := h.queries.AdminMetricsPostgresVersion(ctx)
	if err != nil {
		return dimensionResponse{}, err
	}

	data := []dimensionData{
		{
			Key:        "mastodon",
			HumanKey:   "Mastodon",
			Value:      compatMastodonVersion,
			HumanValue: stringPointer(compatMastodonVersion),
		},
		{
			Key:        "go",
			HumanKey:   "Go",
			Value:      runtime.Version(),
			HumanValue: stringPointer(runtime.Version()),
		},
	}

	if version := parsePostgresVersion(versionValue); version != "" {
		data = append(data, dimensionData{
			Key:        "postgresql",
			HumanKey:   "PostgreSQL",
			Value:      version,
			HumanValue: stringPointer(version),
		})
	}

	if h.redis != nil {
		if redisVersion, ok := redisServerVersion(ctx, h.redis); ok {
			data = append(data, dimensionData{
				Key:        "redis",
				HumanKey:   "Redis",
				Value:      redisVersion,
				HumanValue: stringPointer(redisVersion),
			})
		}
	}

	return dimensionResponse{Key: "software_versions", Data: data}, nil
}

func (h *MeasuresHandler) measureActiveUsers(ctx context.Context, rangeData metricRange) (measureResponse, error) {
	total, err := h.queries.AdminMetricsActiveUsersTotal(ctx, db.AdminMetricsActiveUsersTotalParams{
		StartAt: pgTime(rangeData.Start),
		EndAt:   pgTime(rangeData.End),
	})
	if err != nil {
		return measureResponse{}, err
	}
	prevTotal, err := h.queries.AdminMetricsActiveUsersTotal(ctx, db.AdminMetricsActiveUsersTotalParams{
		StartAt: pgTime(rangeData.PrevStart),
		EndAt:   pgTime(rangeData.PrevEnd),
	})
	if err != nil {
		return measureResponse{}, err
	}
	rows, err := h.queries.AdminMetricsActiveUsersByDay(ctx, db.AdminMetricsActiveUsersByDayParams{
		StartAt: pgTime(dayStart(rangeData.StartDay)),
		EndAt:   pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	data := buildSeriesFromRows(rows, rangeData.StartDay, rangeData.EndDay, func(row db.AdminMetricsActiveUsersByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})

	return measureResponse{
		Key:           "active_users",
		Total:         strconv.FormatInt(total, 10),
		PreviousTotal: stringPointer(strconv.FormatInt(prevTotal, 10)),
		Data:          data,
	}, nil
}

func (h *MeasuresHandler) measureNewUsers(ctx context.Context, rangeData metricRange) (measureResponse, error) {
	total, err := h.queries.AdminMetricsNewUsersTotal(ctx, db.AdminMetricsNewUsersTotalParams{
		StartAt: pgTime(rangeData.Start),
		EndAt:   pgTime(rangeData.End),
	})
	if err != nil {
		return measureResponse{}, err
	}
	prevTotal, err := h.queries.AdminMetricsNewUsersTotal(ctx, db.AdminMetricsNewUsersTotalParams{
		StartAt: pgTime(rangeData.PrevStart),
		EndAt:   pgTime(rangeData.PrevEnd),
	})
	if err != nil {
		return measureResponse{}, err
	}
	rows, err := h.queries.AdminMetricsNewUsersByDay(ctx, db.AdminMetricsNewUsersByDayParams{
		StartAt: pgTime(dayStart(rangeData.StartDay)),
		EndAt:   pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	data := buildSeriesFromRows(rows, rangeData.StartDay, rangeData.EndDay, func(row db.AdminMetricsNewUsersByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})

	return measureResponse{
		Key:           "new_users",
		Total:         strconv.FormatInt(total, 10),
		PreviousTotal: stringPointer(strconv.FormatInt(prevTotal, 10)),
		Data:          data,
	}, nil
}

func (h *MeasuresHandler) measureInteractions(ctx context.Context, rangeData metricRange) (measureResponse, error) {
	totalStatuses, err := h.queries.AdminMetricsStatusesTotal(ctx, db.AdminMetricsStatusesTotalParams{
		StartAt: pgTime(rangeData.Start),
		EndAt:   pgTime(rangeData.End),
	})
	if err != nil {
		return measureResponse{}, err
	}
	totalFavourites, err := h.queries.AdminMetricsFavouritesTotal(ctx, db.AdminMetricsFavouritesTotalParams{
		StartAt: pgTime(rangeData.Start),
		EndAt:   pgTime(rangeData.End),
	})
	if err != nil {
		return measureResponse{}, err
	}
	totalFollows, err := h.queries.AdminMetricsFollowsTotal(ctx, db.AdminMetricsFollowsTotalParams{
		StartAt: pgTime(rangeData.Start),
		EndAt:   pgTime(rangeData.End),
	})
	if err != nil {
		return measureResponse{}, err
	}
	totalPollVotes, err := h.queries.AdminMetricsPollVotesTotal(ctx, db.AdminMetricsPollVotesTotalParams{
		StartAt: pgTime(rangeData.Start),
		EndAt:   pgTime(rangeData.End),
	})
	if err != nil {
		return measureResponse{}, err
	}

	prevStatuses, err := h.queries.AdminMetricsStatusesTotal(ctx, db.AdminMetricsStatusesTotalParams{
		StartAt: pgTime(rangeData.PrevStart),
		EndAt:   pgTime(rangeData.PrevEnd),
	})
	if err != nil {
		return measureResponse{}, err
	}
	prevFavourites, err := h.queries.AdminMetricsFavouritesTotal(ctx, db.AdminMetricsFavouritesTotalParams{
		StartAt: pgTime(rangeData.PrevStart),
		EndAt:   pgTime(rangeData.PrevEnd),
	})
	if err != nil {
		return measureResponse{}, err
	}
	prevFollows, err := h.queries.AdminMetricsFollowsTotal(ctx, db.AdminMetricsFollowsTotalParams{
		StartAt: pgTime(rangeData.PrevStart),
		EndAt:   pgTime(rangeData.PrevEnd),
	})
	if err != nil {
		return measureResponse{}, err
	}
	prevPollVotes, err := h.queries.AdminMetricsPollVotesTotal(ctx, db.AdminMetricsPollVotesTotalParams{
		StartAt: pgTime(rangeData.PrevStart),
		EndAt:   pgTime(rangeData.PrevEnd),
	})
	if err != nil {
		return measureResponse{}, err
	}

	rowsStatuses, err := h.queries.AdminMetricsStatusesByDay(ctx, db.AdminMetricsStatusesByDayParams{
		StartAt: pgTime(dayStart(rangeData.StartDay)),
		EndAt:   pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}
	rowsFavourites, err := h.queries.AdminMetricsFavouritesByDay(ctx, db.AdminMetricsFavouritesByDayParams{
		StartAt: pgTime(dayStart(rangeData.StartDay)),
		EndAt:   pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}
	rowsFollows, err := h.queries.AdminMetricsFollowsByDay(ctx, db.AdminMetricsFollowsByDayParams{
		StartAt: pgTime(dayStart(rangeData.StartDay)),
		EndAt:   pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}
	rowsPollVotes, err := h.queries.AdminMetricsPollVotesByDay(ctx, db.AdminMetricsPollVotesByDayParams{
		StartAt: pgTime(dayStart(rangeData.StartDay)),
		EndAt:   pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	values := map[time.Time]int64{}
	mergeCounts(values, rowsStatuses, func(row db.AdminMetricsStatusesByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})
	mergeCounts(values, rowsFavourites, func(row db.AdminMetricsFavouritesByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})
	mergeCounts(values, rowsFollows, func(row db.AdminMetricsFollowsByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})
	mergeCounts(values, rowsPollVotes, func(row db.AdminMetricsPollVotesByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})

	data := buildSeries(rangeData.StartDay, rangeData.EndDay, values)
	total := totalStatuses + totalFavourites + totalFollows + totalPollVotes
	prevTotal := prevStatuses + prevFavourites + prevFollows + prevPollVotes

	return measureResponse{
		Key:           "interactions",
		Total:         strconv.FormatInt(total, 10),
		PreviousTotal: stringPointer(strconv.FormatInt(prevTotal, 10)),
		Data:          data,
	}, nil
}

func (h *MeasuresHandler) measureOpenedReports(ctx context.Context, rangeData metricRange) (measureResponse, error) {
	total, err := h.queries.AdminMetricsOpenedReportsTotal(ctx, db.AdminMetricsOpenedReportsTotalParams{
		StartAt: pgTime(rangeData.Start),
		EndAt:   pgTime(rangeData.End),
	})
	if err != nil {
		return measureResponse{}, err
	}
	prevTotal, err := h.queries.AdminMetricsOpenedReportsTotal(ctx, db.AdminMetricsOpenedReportsTotalParams{
		StartAt: pgTime(rangeData.PrevStart),
		EndAt:   pgTime(rangeData.PrevEnd),
	})
	if err != nil {
		return measureResponse{}, err
	}
	rows, err := h.queries.AdminMetricsOpenedReportsByDay(ctx, db.AdminMetricsOpenedReportsByDayParams{
		StartAt: pgTime(dayStart(rangeData.StartDay)),
		EndAt:   pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	data := buildSeriesFromRows(rows, rangeData.StartDay, rangeData.EndDay, func(row db.AdminMetricsOpenedReportsByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})

	return measureResponse{
		Key:           "opened_reports",
		Total:         strconv.FormatInt(total, 10),
		PreviousTotal: stringPointer(strconv.FormatInt(prevTotal, 10)),
		Data:          data,
	}, nil
}

func (h *MeasuresHandler) measureResolvedReports(ctx context.Context, rangeData metricRange) (measureResponse, error) {
	total, err := h.queries.AdminMetricsResolvedReportsTotal(ctx, db.AdminMetricsResolvedReportsTotalParams{
		StartAt: pgTime(rangeData.Start),
		EndAt:   pgTime(rangeData.End),
	})
	if err != nil {
		return measureResponse{}, err
	}
	prevTotal, err := h.queries.AdminMetricsResolvedReportsTotal(ctx, db.AdminMetricsResolvedReportsTotalParams{
		StartAt: pgTime(rangeData.PrevStart),
		EndAt:   pgTime(rangeData.PrevEnd),
	})
	if err != nil {
		return measureResponse{}, err
	}
	rows, err := h.queries.AdminMetricsResolvedReportsByDay(ctx, db.AdminMetricsResolvedReportsByDayParams{
		StartAt: pgTime(dayStart(rangeData.StartDay)),
		EndAt:   pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	data := buildSeriesFromRows(rows, rangeData.StartDay, rangeData.EndDay, func(row db.AdminMetricsResolvedReportsByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})

	return measureResponse{
		Key:           "resolved_reports",
		Total:         strconv.FormatInt(total, 10),
		PreviousTotal: stringPointer(strconv.FormatInt(prevTotal, 10)),
		Data:          data,
	}, nil
}

func (h *MeasuresHandler) measureTagUses(ctx context.Context, rangeData metricRange, tagID int64) (measureResponse, error) {
	total, err := h.queries.AdminMetricsTagUsesTotal(ctx, db.AdminMetricsTagUsesTotalParams{
		TagID:   tagID,
		StartAt: pgTime(rangeData.Start),
		EndAt:   pgTime(rangeData.End),
	})
	if err != nil {
		return measureResponse{}, err
	}
	prevTotal, err := h.queries.AdminMetricsTagUsesTotal(ctx, db.AdminMetricsTagUsesTotalParams{
		TagID:   tagID,
		StartAt: pgTime(rangeData.PrevStart),
		EndAt:   pgTime(rangeData.PrevEnd),
	})
	if err != nil {
		return measureResponse{}, err
	}
	rows, err := h.queries.AdminMetricsTagUsesByDay(ctx, db.AdminMetricsTagUsesByDayParams{
		TagID:   tagID,
		StartAt: pgTime(dayStart(rangeData.StartDay)),
		EndAt:   pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	data := buildSeriesFromRows(rows, rangeData.StartDay, rangeData.EndDay, func(row db.AdminMetricsTagUsesByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})

	return measureResponse{
		Key:           "tag_uses",
		Total:         strconv.FormatInt(total, 10),
		PreviousTotal: stringPointer(strconv.FormatInt(prevTotal, 10)),
		Data:          data,
	}, nil
}

func (h *MeasuresHandler) measureTagAccounts(ctx context.Context, rangeData metricRange, tagID int64) (measureResponse, error) {
	total, err := h.queries.AdminMetricsTagAccountsTotal(ctx, db.AdminMetricsTagAccountsTotalParams{
		TagID:   tagID,
		StartAt: pgTime(rangeData.Start),
		EndAt:   pgTime(rangeData.End),
	})
	if err != nil {
		return measureResponse{}, err
	}
	prevTotal, err := h.queries.AdminMetricsTagAccountsTotal(ctx, db.AdminMetricsTagAccountsTotalParams{
		TagID:   tagID,
		StartAt: pgTime(rangeData.PrevStart),
		EndAt:   pgTime(rangeData.PrevEnd),
	})
	if err != nil {
		return measureResponse{}, err
	}
	rows, err := h.queries.AdminMetricsTagAccountsByDay(ctx, db.AdminMetricsTagAccountsByDayParams{
		TagID:   tagID,
		StartAt: pgTime(dayStart(rangeData.StartDay)),
		EndAt:   pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	data := buildSeriesFromRows(rows, rangeData.StartDay, rangeData.EndDay, func(row db.AdminMetricsTagAccountsByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})

	return measureResponse{
		Key:           "tag_accounts",
		Total:         strconv.FormatInt(total, 10),
		PreviousTotal: stringPointer(strconv.FormatInt(prevTotal, 10)),
		Data:          data,
	}, nil
}

func (h *MeasuresHandler) measureTagServers(ctx context.Context, rangeData metricRange, tagID int64) (measureResponse, error) {
	total, err := h.queries.AdminMetricsTagServersTotal(ctx, db.AdminMetricsTagServersTotalParams{
		TagID:   tagID,
		StartAt: pgTime(rangeData.Start),
		EndAt:   pgTime(rangeData.End),
	})
	if err != nil {
		return measureResponse{}, err
	}
	prevTotal, err := h.queries.AdminMetricsTagServersTotal(ctx, db.AdminMetricsTagServersTotalParams{
		TagID:   tagID,
		StartAt: pgTime(rangeData.PrevStart),
		EndAt:   pgTime(rangeData.PrevEnd),
	})
	if err != nil {
		return measureResponse{}, err
	}
	rows, err := h.queries.AdminMetricsTagServersByDay(ctx, db.AdminMetricsTagServersByDayParams{
		TagID:   tagID,
		StartAt: pgTime(dayStart(rangeData.StartDay)),
		EndAt:   pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	data := buildSeriesFromRows(rows, rangeData.StartDay, rangeData.EndDay, func(row db.AdminMetricsTagServersByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})

	return measureResponse{
		Key:           "tag_servers",
		Total:         strconv.FormatInt(total, 10),
		PreviousTotal: stringPointer(strconv.FormatInt(prevTotal, 10)),
		Data:          data,
	}, nil
}

func (h *MeasuresHandler) measureInstanceAccounts(ctx context.Context, rangeData metricRange, domain string, includeSubdomains bool) (measureResponse, error) {
	total, err := h.queries.AdminMetricsInstanceAccountsTotal(ctx, db.AdminMetricsInstanceAccountsTotalParams{
		Domain:            textParam(domain),
		IncludeSubdomains: includeSubdomains,
	})
	if err != nil {
		return measureResponse{}, err
	}
	rows, err := h.queries.AdminMetricsInstanceAccountsByDay(ctx, db.AdminMetricsInstanceAccountsByDayParams{
		Domain:            textParam(domain),
		IncludeSubdomains: includeSubdomains,
		StartAt:           pgTime(dayStart(rangeData.StartDay)),
		EndAt:             pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	data := buildSeriesFromRows(rows, rangeData.StartDay, rangeData.EndDay, func(row db.AdminMetricsInstanceAccountsByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})

	return measureResponse{
		Key:   "instance_accounts",
		Total: strconv.FormatInt(total, 10),
		Data:  data,
	}, nil
}

func (h *MeasuresHandler) measureInstanceFollows(ctx context.Context, rangeData metricRange, domain string, includeSubdomains bool) (measureResponse, error) {
	total, err := h.queries.AdminMetricsInstanceFollowsTotal(ctx, db.AdminMetricsInstanceFollowsTotalParams{
		Domain:            textParam(domain),
		IncludeSubdomains: includeSubdomains,
	})
	if err != nil {
		return measureResponse{}, err
	}
	rows, err := h.queries.AdminMetricsInstanceFollowsByDay(ctx, db.AdminMetricsInstanceFollowsByDayParams{
		Domain:            textParam(domain),
		IncludeSubdomains: includeSubdomains,
		StartAt:           pgTime(dayStart(rangeData.StartDay)),
		EndAt:             pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	data := buildSeriesFromRows(rows, rangeData.StartDay, rangeData.EndDay, func(row db.AdminMetricsInstanceFollowsByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})

	return measureResponse{
		Key:   "instance_follows",
		Total: strconv.FormatInt(total, 10),
		Data:  data,
	}, nil
}

func (h *MeasuresHandler) measureInstanceFollowers(ctx context.Context, rangeData metricRange, domain string, includeSubdomains bool) (measureResponse, error) {
	total, err := h.queries.AdminMetricsInstanceFollowersTotal(ctx, db.AdminMetricsInstanceFollowersTotalParams{
		Domain:            textParam(domain),
		IncludeSubdomains: includeSubdomains,
	})
	if err != nil {
		return measureResponse{}, err
	}
	rows, err := h.queries.AdminMetricsInstanceFollowersByDay(ctx, db.AdminMetricsInstanceFollowersByDayParams{
		Domain:            textParam(domain),
		IncludeSubdomains: includeSubdomains,
		StartAt:           pgTime(dayStart(rangeData.StartDay)),
		EndAt:             pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	data := buildSeriesFromRows(rows, rangeData.StartDay, rangeData.EndDay, func(row db.AdminMetricsInstanceFollowersByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})

	return measureResponse{
		Key:   "instance_followers",
		Total: strconv.FormatInt(total, 10),
		Data:  data,
	}, nil
}

func (h *MeasuresHandler) measureInstanceMedia(ctx context.Context, rangeData metricRange, domain string, includeSubdomains bool) (measureResponse, error) {
	total, err := h.queries.AdminMetricsInstanceMediaTotal(ctx, db.AdminMetricsInstanceMediaTotalParams{
		Domain:            textParam(domain),
		IncludeSubdomains: includeSubdomains,
	})
	if err != nil {
		return measureResponse{}, err
	}
	rows, err := h.queries.AdminMetricsInstanceMediaByDay(ctx, db.AdminMetricsInstanceMediaByDayParams{
		Domain:            textParam(domain),
		IncludeSubdomains: includeSubdomains,
		StartAt:           pgTime(dayStart(rangeData.StartDay)),
		EndAt:             pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	data := buildSeriesFromRows(rows, rangeData.StartDay, rangeData.EndDay, func(row db.AdminMetricsInstanceMediaByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})
	unit := "bytes"
	human := humanBytes(total)

	return measureResponse{
		Key:        "instance_media_attachments",
		Unit:       &unit,
		Total:      strconv.FormatInt(total, 10),
		HumanValue: &human,
		Data:       data,
	}, nil
}

func (h *MeasuresHandler) measureInstanceReports(ctx context.Context, rangeData metricRange, domain string, includeSubdomains bool) (measureResponse, error) {
	total, err := h.queries.AdminMetricsInstanceReportsTotal(ctx, db.AdminMetricsInstanceReportsTotalParams{
		Domain:            textParam(domain),
		IncludeSubdomains: includeSubdomains,
	})
	if err != nil {
		return measureResponse{}, err
	}
	rows, err := h.queries.AdminMetricsInstanceReportsByDay(ctx, db.AdminMetricsInstanceReportsByDayParams{
		Domain:            textParam(domain),
		IncludeSubdomains: includeSubdomains,
		StartAt:           pgTime(dayStart(rangeData.StartDay)),
		EndAt:             pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	data := buildSeriesFromRows(rows, rangeData.StartDay, rangeData.EndDay, func(row db.AdminMetricsInstanceReportsByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})

	return measureResponse{
		Key:   "instance_reports",
		Total: strconv.FormatInt(total, 10),
		Data:  data,
	}, nil
}

func (h *MeasuresHandler) measureInstanceStatuses(ctx context.Context, rangeData metricRange, domain string, includeSubdomains bool) (measureResponse, error) {
	total, err := h.queries.AdminMetricsInstanceStatusesTotal(ctx, db.AdminMetricsInstanceStatusesTotalParams{
		Domain:            textParam(domain),
		IncludeSubdomains: includeSubdomains,
	})
	if err != nil {
		return measureResponse{}, err
	}
	rows, err := h.queries.AdminMetricsInstanceStatusesByDay(ctx, db.AdminMetricsInstanceStatusesByDayParams{
		Domain:            textParam(domain),
		IncludeSubdomains: includeSubdomains,
		StartAt:           pgTime(dayStart(rangeData.StartDay)),
		EndAt:             pgTime(dayEnd(rangeData.EndDay)),
	})
	if err != nil {
		return measureResponse{}, err
	}

	data := buildSeriesFromRows(rows, rangeData.StartDay, rangeData.EndDay, func(row db.AdminMetricsInstanceStatusesByDayRow) (time.Time, int64) {
		return row.Period.Time, row.Value
	})

	return measureResponse{
		Key:   "instance_statuses",
		Total: strconv.FormatInt(total, 10),
		Data:  data,
	}, nil
}

func buildMetricRange(startAt, endAt *string, defaultDays int) (metricRange, error) {
	now := time.Now().UTC()
	startTime, startOK, err := parseTimeValue(startAt)
	if err != nil {
		return metricRange{}, err
	}
	endTime, endOK, err := parseTimeValue(endAt)
	if err != nil {
		return metricRange{}, err
	}
	if !endOK {
		endTime = now
	}
	if !startOK {
		startTime = endTime.AddDate(0, 0, -(defaultDays - 1))
	}
	startTime = startTime.UTC()
	endTime = endTime.UTC()
	if endTime.Before(startTime) {
		return metricRange{}, errors.New("invalid range")
	}

	duration := endTime.Sub(startTime)
	prevStart := startTime.Add(-duration)
	prevEnd := endTime.Add(-duration)

	startDay := dateOnly(startTime)
	endDay := dateOnly(endTime)

	return metricRange{
		Start:     startTime,
		End:       endTime,
		StartDay:  startDay,
		EndDay:    endDay,
		PrevStart: prevStart,
		PrevEnd:   prevEnd,
	}, nil
}

func buildRetentionRange(startAt, endAt *string, frequency string) (metricRange, error) {
	now := time.Now().UTC()
	startTime, startOK, err := parseTimeValue(startAt)
	if err != nil {
		return metricRange{}, err
	}
	endTime, endOK, err := parseTimeValue(endAt)
	if err != nil {
		return metricRange{}, err
	}

	if !endOK {
		endTime = now
	}
	if !startOK {
		if frequency == "month" {
			startTime = endTime.AddDate(-1, 0, 0)
		} else {
			startTime = endTime.AddDate(0, 0, -30)
		}
	}

	if endTime.Before(startTime) {
		return metricRange{}, errors.New("invalid range")
	}

	startTime = dateOnly(startTime)
	endTime = dateOnly(endTime)

	return metricRange{
		Start: startTime,
		End:   endTime,
	}, nil
}

func parseTimeValue(value *string) (time.Time, bool, error) {
	if value == nil {
		return time.Time{}, false, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" || strings.EqualFold(trimmed, "null") {
		return time.Time{}, false, nil
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			return parsed, true, nil
		}
	}
	return time.Time{}, false, fmt.Errorf("invalid time")
}

func decodeMetricsRequest(r *http.Request) (metricsRequest, map[string]json.RawMessage, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return metricsRequest{}, nil, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		body = []byte("{}")
	}
	var req metricsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return metricsRequest{}, nil, err
	}
	raw := map[string]json.RawMessage{}
	_ = json.Unmarshal(body, &raw)
	return req, raw, nil
}

func decodeKeyParams(raw map[string]json.RawMessage, key string) (map[string]any, error) {
	if raw == nil {
		return nil, errors.New("missing")
	}
	payload, ok := raw[key]
	if !ok || len(payload) == 0 || string(payload) == "null" {
		return nil, errors.New("missing")
	}
	var params map[string]any
	if err := json.Unmarshal(payload, &params); err != nil {
		return nil, err
	}
	return params, nil
}

func parseInstanceParams(params map[string]any) (string, bool, bool) {
	domain := strings.TrimSpace(paramString(params, "domain"))
	if domain == "" {
		return "", false, false
	}
	includeSubdomains := paramBool(params, "include_subdomains")
	return domain, includeSubdomains, true
}

func paramString(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	value, ok := params[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func paramBool(params map[string]any, key string) bool {
	if params == nil {
		return false
	}
	value, ok := params[key]
	if !ok || value == nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(v)
		return err == nil && parsed
	default:
		return false
	}
}

func paramInt64(params map[string]any, key string) (int64, bool) {
	if params == nil {
		return 0, false
	}
	value, ok := params[key]
	if !ok || value == nil {
		return 0, false
	}
	switch v := value.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	case json.Number:
		parsed, err := v.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func buildSeries(startDay, endDay time.Time, values map[time.Time]int64) []measureData {
	if endDay.Before(startDay) {
		return []measureData{}
	}
	data := []measureData{}
	for day := startDay; !day.After(endDay); day = day.AddDate(0, 0, 1) {
		value := values[day]
		data = append(data, measureData{
			Date:  formatDate(day),
			Value: strconv.FormatInt(value, 10),
		})
	}
	return data
}

func buildSeriesFromRows[T any](rows []T, startDay, endDay time.Time, extractor func(T) (time.Time, int64)) []measureData {
	values := map[time.Time]int64{}
	mergeCounts(values, rows, extractor)
	return buildSeries(startDay, endDay, values)
}

func mergeCounts[T any](values map[time.Time]int64, rows []T, extractor func(T) (time.Time, int64)) {
	for _, row := range rows {
		period, value := extractor(row)
		period = dateOnly(period)
		values[period] += value
	}
}

func formatDate(value time.Time) string {
	return value.UTC().Format("2006-01-02")
}

func dateOnly(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func dayStart(day time.Time) time.Time {
	return dateOnly(day)
}

func dayEnd(day time.Time) time.Time {
	return dateOnly(day).Add(24*time.Hour - time.Nanosecond)
}

func pgTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func pgTimestamp(value time.Time) pgtype.Timestamp {
	return pgtype.Timestamp{Time: value, Valid: true}
}

func textParam(value string) pgtype.Text {
	trimmed := strings.TrimSpace(value)
	return pgtype.Text{String: trimmed, Valid: trimmed != ""}
}

func statusIDRange(rangeData metricRange) (int64, int64) {
	start := dayStart(rangeData.StartDay)
	end := dayEnd(rangeData.EndDay)
	return snowflakeIDAt(start), snowflakeIDAt(end)
}

func snowflakeIDAt(value time.Time) int64 {
	ms := value.UTC().UnixMilli() - snowflake.MastodonEpoch
	if ms < 0 {
		return 0
	}
	return ms << snowflake.SequenceBits
}

func sizeDimension(key, humanKey string, size int64) dimensionData {
	unit := "bytes"
	human := humanBytes(size)
	return dimensionData{
		Key:        key,
		HumanKey:   humanKey,
		Value:      strconv.FormatInt(size, 10),
		Unit:       &unit,
		HumanValue: &human,
	}
}

func humanBytes(value int64) string {
	if value < 0 {
		value = 0
	}
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB", "EB"}
	result := float64(value) / float64(div)
	return fmt.Sprintf("%.1f %s", result, units[exp])
}

func humanLocaleName(code string) string {
	tag, err := language.Parse(code)
	if err != nil {
		return code
	}
	name := display.English.Tags().Name(tag)
	if strings.TrimSpace(name) == "" {
		return code
	}
	return name
}

func parsePostgresVersion(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return ""
	}
	if parts[0] == "PostgreSQL" && len(parts) > 1 {
		return parts[1]
	}
	return parts[0]
}

func redisMemoryUsage(ctx context.Context, client *redis.Client) (int64, bool) {
	if client == nil {
		return 0, false
	}
	info, err := client.Info(ctx, "memory").Result()
	if err != nil {
		return 0, false
	}
	value, ok := parseRedisInfo(info, "used_memory")
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func redisServerVersion(ctx context.Context, client *redis.Client) (string, bool) {
	if client == nil {
		return "", false
	}
	info, err := client.Info(ctx, "server").Result()
	if err != nil {
		return "", false
	}
	value, ok := parseRedisInfo(info, "redis_version")
	if !ok {
		return "", false
	}
	return value, true
}

func parseRedisInfo(info, key string) (string, bool) {
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		if parts[0] == key {
			return strings.TrimSpace(parts[1]), true
		}
	}
	return "", false
}

func stringPointer(value string) *string {
	return &value
}

// RegisterDimensionsRoutes registers dimensions route.
func (h *DimensionsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/admin/dimensions", h.Get)
}

// RegisterMeasuresRoutes registers measures route.
func (h *MeasuresHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/admin/measures", h.Get)
}

// RegisterRetentionRoutes registers retention route.
func (h *RetentionHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/admin/retention", h.Get)
}
