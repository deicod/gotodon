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

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
	"github.com/deicod/gotodon/internal/web"
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
func (h *MediaHandler) Create(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	// Limit upload size (e.g. 40MB matches max video size)
	r.Body = http.MaxBytesReader(w, r.Body, media.MaxVideoSize+1024)
	if err := r.ParseMultipartForm(media.MaxVideoSize + 1024); err != nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: File is too large")
		return
	}

	// Get file from multipart form
	file, header, err := r.FormFile("file")
	if err != nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: File is required")
		return
	}
	defer func() { _ = file.Close() }()

	// Get optional description
	description := r.FormValue("description")
	if len(description) > 1500 {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Description is too long (maximum is 1500 characters)")
		return
	}

	// Get optional focus point
	focusX, _ := strconv.ParseFloat(r.FormValue("focus[x]"), 64)
	focusY, _ := strconv.ParseFloat(r.FormValue("focus[y]"), 64)

	// Read file data
	data, err := io.ReadAll(io.LimitReader(file, media.MaxVideoSize+1))
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to read uploaded file")
		return
	}

	// Upload and process
	result, err := h.uploader.Upload(r.Context(), accountID, header.Filename, data)
	if err != nil {
		switch err {
		case media.ErrUnsupportedType:
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: File type is not supported")
			return
		case media.ErrFileTooLarge:
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: File is too large")
			return
		default:
			web.JSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to process file: %v", err))
			return
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
	dbMedia, err := h.queries.CreateMediaAttachment(r.Context(), db.CreateMediaAttachmentParams{
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
		web.JSONError(w, http.StatusInternalServerError, "Failed to save media record")
		return
	}

	resp := dbMediaToAPI(dbMedia)
	if resp.URL == "" {
		resp.URL = result.URL
		resp.PreviewURL = result.URL
	}
	web.JSON(w, http.StatusOK, resp)
}

// Get handles GET /api/v1/media/:id
func (h *MediaHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	dbMedia, err := h.queries.GetMediaAttachment(r.Context(), id)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	web.JSON(w, http.StatusOK, dbMediaToAPI(dbMedia))
}

// Update handles PUT /api/v1/media/:id
// Updates the description or focus of a media attachment
func (h *MediaHandler) Update(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	idStr := web.Param(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req struct {
		Description string `json:"description"`
		Focus       string `json:"focus"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	// Verify ownership first
	mediaRecord, err := h.queries.GetMediaAttachment(r.Context(), id)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	if !mediaRecord.AccountID.Valid || mediaRecord.AccountID.Int64 != accountID {
		web.JSONError(w, http.StatusForbidden, "Access denied")
		return
	}

	// Update
	// Note: We ignore focus updates to DB for now as it requires complex meta updating
	var desc pgtype.Text
	if req.Description != "" {
		desc = pgtype.Text{String: req.Description, Valid: true}
	} else {
		// Mastodon allows clearing. But here simple update.
		// Set valid=true to update to req.Description (even if empty)
		desc = pgtype.Text{String: req.Description, Valid: true}
	}

	// We only use UpdateMediaAttachment to update description for now.
	updated, err := h.queries.UpdateMediaAttachment(r.Context(), db.UpdateMediaAttachmentParams{
		ID:          id,
		AccountID:   pgtype.Int8{Int64: accountID, Valid: true},
		Description: desc,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to update media")
		return
	}

	web.JSON(w, http.StatusOK, dbMediaToAPI(updated))
}

// RegisterRoutes registers the media routes
func (h *MediaHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/media", h.Create)
	mux.HandleFunc("GET /api/v1/media/{id}", h.Get)
	mux.HandleFunc("PUT /api/v1/media/{id}", h.Update)
	// V2 endpoint alias
	mux.HandleFunc("POST /api/v2/media", h.Create)
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
		Type:        mapMediaType(m.Type),
		URL:         m.RemoteUrl, // If remote
		PreviewURL:  m.RemoteUrl,
		Description: &m.Description.String,
		Meta:        meta,
		Blurhash:    &m.Blurhash.String,
	}
}

func mapMediaType(t int32) string {
	switch t {
	case 0:
		return "image"
	case 1:
		return "gifv"
	case 2:
		return "video"
	case 3:
		return "audio"
	default:
		return "unknown"
	}
}
