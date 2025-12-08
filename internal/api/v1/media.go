// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/media"
	"github.com/deicod/gotodon/pkg/snowflake"
)

// MediaHandler handles media attachment endpoints
type MediaHandler struct {
	uploader *media.Uploader
	idGen    *snowflake.Generator
}

// NewMediaHandler creates a new media handler
func NewMediaHandler(uploader *media.Uploader) *MediaHandler {
	return &MediaHandler{
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

	// Generate ID
	id := h.idGen.Generate()

	// Add focus point to meta if provided
	if result.Meta != nil && (focusX != 0 || focusY != 0) {
		result.Meta.Focus = &media.MetaFocus{X: focusX, Y: focusY}
	}

	// Build response
	var desc *string
	if description != "" {
		desc = &description
	}

	var blurhash *string
	if result.Blurhash != "" {
		blurhash = &result.Blurhash
	}

	return c.JSON(http.StatusOK, MediaAttachment{
		ID:          strconv.FormatInt(id, 10),
		Type:        string(result.MediaType),
		URL:         result.URL,
		PreviewURL:  result.URL, // TODO: generate actual thumbnail
		RemoteURL:   nil,
		Meta:        result.Meta,
		Description: desc,
		Blurhash:    blurhash,
	})
}

// Get handles GET /api/v1/media/:id
func (h *MediaHandler) Get(c echo.Context) error {
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	// TODO: Fetch from database
	// For now return 404 since we don't persist
	return echo.NewHTTPError(http.StatusNotFound, map[string]string{
		"error": "Record not found",
	})
}

// Update handles PUT /api/v1/media/:id
// Updates the description or focus of a media attachment
func (h *MediaHandler) Update(c echo.Context) error {
	id := c.Param("id")
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	_ = accountID

	description := c.FormValue("description")
	if len(description) > 1500 {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Description is too long (maximum is 1500 characters)",
		})
	}

	// TODO: Update in database
	// For now return 404 since we don't persist
	_ = id
	return echo.NewHTTPError(http.StatusNotFound, map[string]string{
		"error": "Record not found",
	})
}

// RegisterRoutes registers the media routes
func (h *MediaHandler) RegisterRoutes(g *echo.Group) {
	// Authenticated routes only
}

// RegisterAuthenticatedRoutes registers routes that require authentication
func (h *MediaHandler) RegisterAuthenticatedRoutes(g *echo.Group) {
	g.POST("/media", h.Create)
	g.GET("/media/:id", h.Get)
	g.PUT("/media/:id", h.Update)
}
