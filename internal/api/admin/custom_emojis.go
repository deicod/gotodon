package admin

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
	"github.com/deicod/gotodon/internal/web"
)

type CustomEmojisHandler struct {
	queries  *db.Queries
	uploader *media.Uploader
}

func NewCustomEmojisHandler(queries *db.Queries, uploader *media.Uploader) *CustomEmojisHandler {
	return &CustomEmojisHandler{
		queries:  queries,
		uploader: uploader,
	}
}

func mapAdminCustomEmoji(emoji db.CustomEmoji, categoryName string) map[string]any {
	resp := map[string]any{
		"id":                strconv.FormatInt(emoji.ID, 10),
		"shortcode":         emoji.Shortcode,
		"url":               v1.GetEmojiURL(emoji),
		"static_url":        v1.GetEmojiURL(emoji),
		"visible_in_picker": emoji.VisibleInPicker,
		"disabled":          emoji.Disabled,
		"domain":            emoji.Domain.String,
	}
	if categoryName != "" {
		resp["category"] = categoryName
	}
	return resp
}

// List handles GET /api/v1/admin/custom_emojis
func (h *CustomEmojisHandler) List(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	limit := 40 // simple pagination
	offset := 0

	emojis, err := h.queries.AdminListCustomEmojis(r.Context(), db.AdminListCustomEmojisParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to list emojis")
		return
	}

	categoryNames := map[int64]string{}
	if len(emojis) > 0 {
		seen := map[int64]struct{}{}
		categoryIDs := make([]int64, 0, len(emojis))
		for _, emoji := range emojis {
			if !emoji.CategoryID.Valid || emoji.CategoryID.Int64 <= 0 {
				continue
			}
			if _, exists := seen[emoji.CategoryID.Int64]; exists {
				continue
			}
			seen[emoji.CategoryID.Int64] = struct{}{}
			categoryIDs = append(categoryIDs, emoji.CategoryID.Int64)
		}
		if len(categoryIDs) > 0 {
			rows, err := h.queries.GetCustomEmojiCategoriesByIDs(r.Context(), categoryIDs)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to load emoji categories")
				return
			}
			for _, row := range rows {
				if row.Name.Valid {
					categoryNames[row.ID] = row.Name.String
				}
			}
		}
	}

	// Response format matches Admin API
	resp := make([]map[string]any, len(emojis))
	for i, e := range emojis {
		categoryName := ""
		if e.CategoryID.Valid {
			categoryName = categoryNames[e.CategoryID.Int64]
		}
		resp[i] = mapAdminCustomEmoji(e, categoryName)
	}

	web.JSON(w, http.StatusOK, resp)
}

// Create handles POST /api/v1/admin/custom_emojis
func (h *CustomEmojisHandler) Create(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid form data")
		return
	}

	shortcode := r.FormValue("shortcode")
	if shortcode == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Shortcode is required")
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Image is required")
		return
	}
	defer func() { _ = file.Close() }()

	// Read file to bytes
	fileData := make([]byte, header.Size)
	_, err = file.Read(fileData)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to read file")
		return
	}

	// Upload
	res, err := h.uploader.Upload(r.Context(), accountID, header.Filename, fileData)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to upload image")
		return
	}

	// Create DB record
	emoji, err := h.queries.AdminCreateCustomEmoji(r.Context(), db.AdminCreateCustomEmojiParams{
		Shortcode:        shortcode,
		Domain:           pgtype.Text{Valid: false}, // Local
		ImageFileName:    pgtype.Text{String: res.Path, Valid: true},
		ImageContentType: pgtype.Text{String: res.ContentType, Valid: true},
		ImageFileSize:    pgtype.Int4{Int32: int32(res.Size), Valid: true},
		VisibleInPicker:  true,
	})
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to create emoji")
		return
	}

	web.JSON(w, http.StatusOK, map[string]any{
		"id":         strconv.FormatInt(emoji.ID, 10),
		"shortcode":  emoji.Shortcode,
		"url":        v1.GetEmojiURL(emoji),
		"static_url": v1.GetEmojiURL(emoji),
	})
}

// Delete handles DELETE /api/v1/admin/custom_emojis/:id
func (h *CustomEmojisHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	err = h.queries.DeleteCustomEmoji(r.Context(), id)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to delete")
		return
	}

	web.JSON(w, http.StatusOK, map[string]any{
		"id": idStr,
	})
}

func (h *CustomEmojisHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/custom_emojis", h.List)
	mux.HandleFunc("POST /api/v1/admin/custom_emojis", h.Create)
	mux.HandleFunc("DELETE /api/v1/admin/custom_emojis/{id}", h.Delete)
}
