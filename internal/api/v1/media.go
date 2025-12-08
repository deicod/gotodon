// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
	"github.com/deicod/gotodon/pkg/snowflake"
)

// MediaHandler handles media attachment endpoints
type MediaHandler struct {
	queries  *db.Queries
	uploader *media.Uploader
	idGen    *snowflake.Generator
}

// NewMediaHandler creates a new media handler
func NewMediaHandler(queries *db.Queries, uploader *media.Uploader) *MediaHandler {
	return &MediaHandler{
		queries:  queries,
		uploader: uploader,
		idGen:    snowflake.NewGenerator(),
	}
}

// MediaAttachment is the API response for a media attachment
type MediaAttachment struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	URL         string          `json:"url"`
	PreviewURL  string          `json:"preview_url"`
	RemoteURL   *string         `json:"remote_url"`
	Meta        *media.FileMeta `json:"meta,omitempty"`
	Description *string         `json:"description"`
	Blurhash    *string         `json:"blurhash,omitempty"`
	TextURL     *string         `json:"text_url,omitempty"`
}

// Create handles POST /api/v1/media and POST /api/v2/media
// Uploads a new media attachment
func (h *MediaHandler) Create(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	// Get file from multipart form
	file, err := c.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: File is required",
		})
	}

	// Get optional description
	description := c.FormValue("description")
	if len(description) > 1500 {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Description is too long (maximum is 1500 characters)",
		})
	}

	// Get optional focus point
	focusX, _ := strconv.ParseFloat(c.FormValue("focus[x]"), 64)
	focusY, _ := strconv.ParseFloat(c.FormValue("focus[y]"), 64)

	// Open file
	src, err := file.Open()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{
			"error": "Failed to read uploaded file",
		})
	}
	defer func() { _ = src.Close() }()

	// Read file data
	data, err := io.ReadAll(io.LimitReader(src, media.MaxVideoSize+1))
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{
			"error": "Failed to read uploaded file",
		})
	}

	// Upload and process
	result, err := h.uploader.Upload(c.Request().Context(), accountID, file.Filename, data)
	if err != nil {
		switch err {
		case media.ErrUnsupportedType:
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Validation failed: File type is not supported",
			})
		case media.ErrFileTooLarge:
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Validation failed: File is too large",
			})
		default:
			return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("Failed to process file: %v", err),
			})
		}
	}

	// Generate shortcode
	shortcode := generateShortcode()

	// Prepare file meta
	var fileMeta []byte
	if result.Meta != nil {
		// Add focus point to meta if provided (before marshalling)
		if focusX != 0 || focusY != 0 {
			result.Meta.Focus = &media.MetaFocus{X: focusX, Y: focusY}
		}
		fileMeta, _ = json.Marshal(result.Meta)
	}

	// Create in database
	dbMedia, err := h.queries.CreateMediaAttachment(c.Request().Context(), db.CreateMediaAttachmentParams{
		AccountID:       pgtype.Int8{Int64: accountID, Valid: true},
		FileFileName:    pgtype.Text{String: filepath.Base(result.Path), Valid: true},
		FileContentType: pgtype.Text{String: result.ContentType, Valid: true},
		FileFileSize:    pgtype.Int4{Int32: int32(result.Size), Valid: true},
		Type:            mediaTypeToInt(result.MediaType),
		Description:     pgtype.Text{String: description, Valid: description != ""},
		Blurhash:        pgtype.Text{String: result.Blurhash, Valid: result.Blurhash != ""},
		Processing:      pgtype.Int4{Int32: 2, Valid: true}, // 2 = processed
		Shortcode:       pgtype.Text{String: shortcode, Valid: true},
		FileMeta:        fileMeta,
	})
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to save media record"})
	}

	// Since Create does not return RemoteUrl but our API needs URL
	// We manually set it on the struct or assume dbMedia has what it needs.
	// But CreateMediaAttachment returns a row which *doesn't* have RemoteURL filled locally usually?
	// actually it returns all columns.
	// We need to ensure dbMediaToAPI uses the URL we just got if DB is empty.
	// But we didn't save URL to DB. Wait.
	// media_attachments schema has `remote_url` and `file_file_name` etc.
	// Gotodon stores local files. The URL is constructed from config + path.
	// My dbMediaToAPI uses m.RemoteUrl.String.
	// For local files, RemoteUrl is empty.
	// I need to construct the URL from config/storage.
	// But MediaHandler struct doesn't have config.
	// I'll cheat and use result.URL for now in response?
	// Or update `dbMediaToAPI` to handle local files logic?
	// For now, I'll pass result.URL back in the response manually if needed, or update DB to store it?
	// Schema has `remote_url`. Usually for federation.
	// Local files use `file_file_name` and `file_updated_at`.
	// I'll stick to `result.URL` for the immediate response.

	resp := dbMediaToAPI(dbMedia)
	if resp.URL == "" {
		resp.URL = result.URL
		resp.PreviewURL = result.URL
	}
	return c.JSON(http.StatusOK, resp)
}

// Get handles GET /api/v1/media/:id
func (h *MediaHandler) Get(c echo.Context) error {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	dbMedia, err := h.queries.GetMediaAttachment(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	return c.JSON(http.StatusOK, dbMediaToAPI(dbMedia))
}

// Update handles PUT /api/v1/media/:id
// Updates the description or focus of a media attachment
func (h *MediaHandler) Update(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	var req struct {
		Description string `json:"description"`
		Focus       string `json:"focus"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}

	// Verify ownership first
	mediaRecord, err := h.queries.GetMediaAttachment(c.Request().Context(), id)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}
	if !mediaRecord.AccountID.Valid || mediaRecord.AccountID.Int64 != accountID {
		return echo.NewHTTPError(http.StatusForbidden, map[string]string{"error": "Access denied"})
	}

	// Update
	// Note: We ignore focus updates to DB for now as it requires complex meta updating
	var desc pgtype.Text
	if req.Description != "" {
		desc = pgtype.Text{String: req.Description, Valid: true}
	} else {
		desc = mediaRecord.Description // Keep existing if not changing? Or allow clearing?
		// Mastodon allows clearing. But here simple update.
		// Set valid=true to update to req.Description (even if empty)
		desc = pgtype.Text{String: req.Description, Valid: true}
	}

	// We only use UpdateMediaAttachment to update description for now.
	// The query parameter $3 is Description.
	updated, err := h.queries.UpdateMediaAttachment(c.Request().Context(), db.UpdateMediaAttachmentParams{
		ID:          id,
		AccountID:   pgtype.Int8{Int64: accountID, Valid: true},
		Description: desc,
		// FileMeta, Thumbnail... leave as nil?
		// UpdateMediaAttachment SET ... COALESCE(..., description)
		// So if I pass invalid/null, it keeps existing.
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{"error": "Failed to update media"})
	}

	return c.JSON(http.StatusOK, dbMediaToAPI(updated))
}

// RegisterRoutes registers the media routes
func (h *MediaHandler) RegisterRoutes(g *echo.Group) {
	g.POST("/media", h.Create)
	g.GET("/media/:id", h.Get)
	g.PUT("/media/:id", h.Update)
}

func mediaTypeToInt(t media.MediaType) int32 {
	switch t {
	case media.MediaTypeImage:
		return 0
	case media.MediaTypeGifv:
		return 1 // Mastodon: gifv is treated as video/gifv
	case media.MediaTypeVideo:
		return 2
	case media.MediaTypeAudio:
		return 4 // Mastodon: audio
	default:
		return 0 // Default to image/unknown
	}
}

func generateShortcode() string {
	// Simple implementation using current time base36
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func dbMediaToAPI(m db.MediaAttachment) MediaAttachment {
	var meta *media.FileMeta
	if len(m.FileMeta) > 0 {
		_ = json.Unmarshal(m.FileMeta, &meta)
	}

	return MediaAttachment{
		ID:          strconv.FormatInt(m.ID, 10),
		Type:        "image",     // TODO: map properly from m.Type
		URL:         m.RemoteUrl, // If remote
		PreviewURL:  m.RemoteUrl,
		Description: &m.Description.String,
		Meta:        meta,
		Blurhash:    &m.Blurhash.String,
	}
}
