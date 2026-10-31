package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // decodes GIF headers for image.DecodeConfig
	_ "image/png" // decodes PNG headers for image.DecodeConfig
	"io"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	_ "golang.org/x/image/webp" // decodes WebP headers for image.DecodeConfig

	v1 "github.com/deicod/gotodon/internal/api/v1"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

// Limits from Mastodon's CustomEmoji model and Attachmentable concern.
const (
	// customEmojiImageLimit is CustomEmoji::LIMIT. Paperclip validates the
	// size with less_than, so an image must be smaller than this.
	customEmojiImageLimit = 256 << 10
	// customEmojiRequestOverhead is what a create request's multipart body
	// may carry besides the image: boundaries, part headers and the
	// shortcode.
	customEmojiRequestOverhead = 16 << 10
	// maxCustomEmojiRequestBytes bounds a create request's body. The form is
	// parsed with the same memory limit, so no part is written to disk.
	maxCustomEmojiRequestBytes = customEmojiImageLimit + customEmojiRequestOverhead

	// customEmojiMinShortcode is CustomEmoji::MINIMUM_SHORTCODE_SIZE.
	customEmojiMinShortcode = 2
	// customEmojiMaxShortcode is CustomEmoji::MAX_SHORTCODE_SIZE, which
	// applies to local emojis.
	customEmojiMaxShortcode = 128
	// customEmojiMaxFederatedShortcode is
	// CustomEmoji::MAX_FEDERATED_SHORTCODE_SIZE, which applies to every emoji.
	customEmojiMaxFederatedShortcode = 2048

	// maxImagePixels is Attachmentable::MAX_MATRIX_LIMIT.
	maxImagePixels = 33_177_600
	// maxGIFPixels is Attachmentable::GIF_MATRIX_LIMIT.
	maxGIFPixels = 921_600

	// customEmojiImageDir is where custom emoji images are stored. It mirrors
	// the :class/:attachment/:style tail of Mastodon's Paperclip path, with
	// the original image under the original style.
	customEmojiImageDir = "custom_emojis/images/original/"
)

// customEmojiShortcodeRE is CustomEmoji::SHORTCODE_ONLY_RE.
var customEmojiShortcodeRE = regexp.MustCompile(`\A[a-zA-Z0-9_]{2,}\z`)

// customEmojiImageTypes are CustomEmoji::IMAGE_MIME_TYPES, each with the
// extension Mastodon's Attachmentable gives its files.
var customEmojiImageTypes = map[string]string{
	"image/png":  "png",
	"image/gif":  "gif",
	"image/webp": "webp",
}

// Validation messages, as Rails and Paperclip word them for CustomEmoji.
// Mastodon's locales override none of them.
const (
	msgImageContentTypeInvalid   = "Image content type is invalid"
	msgImageInvalid              = "Image is invalid"
	msgImageBlank                = "Image can't be blank"
	msgImageTooLarge             = "Image must be less than 256 KB"
	msgImageFileSizeTooLarge     = "Image file size must be less than 256 KB"
	msgShortcodeTaken            = "Shortcode has already been taken"
	msgShortcodeInvalid          = "Shortcode is invalid"
	msgShortcodeTooShort         = "Shortcode is too short (minimum is 2 characters)"
	msgShortcodeTooLongFederated = "Shortcode is too long (maximum is 2048 characters)"
	msgShortcodeTooLong          = "Shortcode is too long (maximum is 128 characters)"
)

// customEmojiStore is the part of *db.Queries the custom emoji handler uses.
// Tests use stubs.
type customEmojiStore interface {
	AdminListCustomEmojis(ctx context.Context, arg db.AdminListCustomEmojisParams) ([]db.CustomEmoji, error)
	GetCustomEmojiCategoriesByIDs(ctx context.Context, ids []int64) ([]db.GetCustomEmojiCategoriesByIDsRow, error)
	LocalCustomEmojiShortcodeTaken(ctx context.Context, shortcode string) (bool, error)
	AdminCreateCustomEmoji(ctx context.Context, arg db.AdminCreateCustomEmojiParams) (db.CustomEmoji, error)
	DeleteCustomEmoji(ctx context.Context, id int64) (db.CustomEmoji, error)
}

// CustomEmojisHandler handles the admin custom emoji endpoints.
type CustomEmojisHandler struct {
	store   customEmojiStore
	storage media.Storage
	roles   RoleLookup
}

// NewCustomEmojisHandler creates a new custom emojis handler that stores
// images in storage.
func NewCustomEmojisHandler(queries *db.Queries, storage media.Storage) *CustomEmojisHandler {
	h := &CustomEmojisHandler{
		storage: storage,
		roles:   NewRoleLookup(queries),
	}
	// A nil *db.Queries leaves store nil rather than a non-nil interface
	// holding a nil pointer. The routes are unreachable then: without a
	// role lookup, WithAccess answers 500.
	if queries != nil {
		h.store = queries
	}
	return h
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

// List handles GET /api/v1/admin/custom_emojis. It returns local and
// remote emojis, newest first, paged as the other admin lists are: limit
// takes 1 to 200 and defaults to 100, offset defaults to 0. Values outside
// those ranges fall back to the defaults; an offset beyond the last emoji
// returns an empty page.
func (h *CustomEmojisHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit := 100
	if l := web.QueryParam(r, "limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	offset := int64(0)
	if o := web.QueryParam(r, "offset"); o != "" {
		if parsed, err := strconv.ParseInt(o, 10, 64); err == nil && parsed >= 0 {
			offset = min(parsed, math.MaxInt32)
		} else if errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(o, "-") {
			offset = math.MaxInt32
		}
	}

	emojis, err := h.store.AdminListCustomEmojis(ctx, db.AdminListCustomEmojisParams{
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		slog.ErrorContext(ctx, "Listing custom emojis failed", "error", err)
		web.JSONError(w, http.StatusInternalServerError, "Database error")
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
			rows, err := h.store.GetCustomEmojiCategoriesByIDs(ctx, categoryIDs)
			if err != nil {
				slog.ErrorContext(ctx, "Loading custom emoji categories failed", "error", err)
				web.JSONError(w, http.StatusInternalServerError, "Database error")
				return
			}
			for _, row := range rows {
				if row.Name.Valid {
					categoryNames[row.ID] = row.Name.String
				}
			}
		}
	}

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

// customEmojiUpload holds a create request's parameters.
type customEmojiUpload struct {
	shortcode string
	hasImage  bool
	// image holds at most customEmojiImageLimit bytes of the image; a
	// larger image is cut off there, which is enough to reject it.
	image []byte
}

// readCustomEmojiUpload reads the shortcode and image parameters. The image
// can only arrive in a multipart/form-data body; an
// application/x-www-form-urlencoded body can carry the shortcode, and other
// bodies carry no parameters. r.Body must be limited to
// maxCustomEmojiRequestBytes, so that the whole form fits in memory.
func readCustomEmojiUpload(r *http.Request) (customEmojiUpload, error) {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "multipart/form-data" {
		if err := r.ParseForm(); err != nil {
			return customEmojiUpload{}, err
		}
		return customEmojiUpload{shortcode: r.PostFormValue("shortcode")}, nil
	}

	if err := r.ParseMultipartForm(maxCustomEmojiRequestBytes); err != nil {
		return customEmojiUpload{}, err
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	upload := customEmojiUpload{shortcode: r.PostFormValue("shortcode")}
	files := r.MultipartForm.File["image"]
	if len(files) == 0 {
		return upload, nil
	}
	file, err := files[0].Open()
	if err != nil {
		return customEmojiUpload{}, fmt.Errorf("opening image: %w", err)
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, customEmojiImageLimit))
	if err != nil {
		return customEmojiUpload{}, fmt.Errorf("reading image: %w", err)
	}
	upload.hasImage = true
	upload.image = data
	return upload, nil
}

// customEmojiImage describes an uploaded image as its bytes show it.
type customEmojiImage struct {
	contentType string
	// decoded reports whether the image's header could be read for its
	// dimensions.
	decoded       bool
	width, height int
}

func inspectCustomEmojiImage(data []byte) customEmojiImage {
	img := customEmojiImage{contentType: http.DetectContentType(data)}
	if !strings.HasPrefix(img.contentType, "image/") {
		return img
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return img
	}
	img.decoded = true
	img.width, img.height = config.Width, config.Height
	return img
}

// dimensionsError returns the error Mastodon's Attachmentable raises for an
// image with too many pixels, or "" when the image is within the limits.
func (img customEmojiImage) dimensionsError() string {
	if !img.decoded {
		return ""
	}
	pixels := int64(img.width) * int64(img.height)
	if img.contentType == "image/gif" && pixels > maxGIFPixels {
		return fmt.Sprintf("%dx%d GIF files are not supported", img.width, img.height)
	}
	if pixels > maxImagePixels {
		return fmt.Sprintf("%dx%d images are not supported", img.width, img.height)
	}
	return ""
}

// imageProblems returns the image's validation errors in the order Mastodon
// reports them: content type, presence, then size. An image whose header
// can't be read is refused as having an invalid content type: its bytes
// begin like an accepted type but aren't one. Mastodon refuses it too, but
// with the name of the error its image processing raises.
func (upload customEmojiUpload) imageProblems(img customEmojiImage) []string {
	if !upload.hasImage {
		return []string{msgImageBlank}
	}
	var problems []string
	if _, ok := customEmojiImageTypes[img.contentType]; !ok || !img.decoded {
		problems = append(problems, msgImageContentTypeInvalid, msgImageInvalid)
	}
	if len(upload.image) >= customEmojiImageLimit {
		problems = append(problems, msgImageTooLarge, msgImageFileSizeTooLarge)
	}
	return problems
}

// shortcodeFormatProblems returns the shortcode's validation errors other
// than uniqueness, in the order Mastodon reports them. Lengths count
// characters, as Rails does.
func shortcodeFormatProblems(shortcode string) []string {
	var problems []string
	if !customEmojiShortcodeRE.MatchString(shortcode) {
		problems = append(problems, msgShortcodeInvalid)
	}
	length := utf8.RuneCountInString(shortcode)
	if length < customEmojiMinShortcode {
		problems = append(problems, msgShortcodeTooShort)
	}
	if length > customEmojiMaxFederatedShortcode {
		problems = append(problems, msgShortcodeTooLongFederated)
	}
	if length > customEmojiMaxShortcode {
		problems = append(problems, msgShortcodeTooLong)
	}
	return problems
}

// validationFailed answers 422 with the messages joined as Rails joins a
// record's errors.
func validationFailed(w http.ResponseWriter, messages ...string) {
	web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: "+strings.Join(messages, ", "))
}

// newCustomEmojiImagePath returns a new storage path for an image with the
// given extension. The name is random, as Mastodon's Attachmentable makes
// it.
func newCustomEmojiImagePath(extension string) (string, error) {
	var name [8]byte
	if _, err := rand.Read(name[:]); err != nil {
		return "", fmt.Errorf("generating image name: %w", err)
	}
	return customEmojiImageDir + hex.EncodeToString(name[:]) + "." + extension, nil
}

// Create handles POST /api/v1/admin/custom_emojis. It creates a local emoji
// from a multipart form with shortcode and image, validated as Mastodon's
// CustomEmoji model validates them.
func (h *CustomEmojisHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// A body declared larger than the limit is refused unread; any other is
	// cut off at the limit.
	if r.ContentLength > maxCustomEmojiRequestBytes {
		validationFailed(w, msgImageTooLarge, msgImageFileSizeTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCustomEmojiRequestBytes)

	upload, err := readCustomEmojiUpload(r)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			validationFailed(w, msgImageTooLarge, msgImageFileSizeTooLarge)
			return
		}
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Mastodon checks the dimensions when the image is assigned, before
	// validating the record, and answers with this error alone.
	var img customEmojiImage
	if upload.hasImage {
		img = inspectCustomEmojiImage(upload.image)
		if msg := img.dimensionsError(); msg != "" {
			web.JSONError(w, http.StatusUnprocessableEntity, msg)
			return
		}
	}

	problems := upload.imageProblems(img)
	taken, err := h.store.LocalCustomEmojiShortcodeTaken(ctx, upload.shortcode)
	if err != nil {
		slog.ErrorContext(ctx, "Checking custom emoji shortcode failed", "error", err)
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}
	if taken {
		problems = append(problems, msgShortcodeTaken)
	}
	problems = append(problems, shortcodeFormatProblems(upload.shortcode)...)
	if len(problems) > 0 {
		validationFailed(w, problems...)
		return
	}

	path, err := newCustomEmojiImagePath(customEmojiImageTypes[img.contentType])
	if err != nil {
		slog.ErrorContext(ctx, "Naming custom emoji image failed", "error", err)
		web.JSONError(w, http.StatusInternalServerError, "Failed to store image")
		return
	}
	if err := h.storage.Save(ctx, path, upload.image); err != nil {
		slog.ErrorContext(ctx, "Storing custom emoji image failed", "path", path, "error", err)
		web.JSONError(w, http.StatusInternalServerError, "Failed to store image")
		return
	}

	emoji, err := h.store.AdminCreateCustomEmoji(ctx, db.AdminCreateCustomEmojiParams{
		Shortcode:        upload.shortcode,
		Domain:           pgtype.Text{},
		ImageFileName:    pgtype.Text{String: path, Valid: true},
		ImageContentType: pgtype.Text{String: img.contentType, Valid: true},
		ImageFileSize:    pgtype.Int4{Int32: int32(len(upload.image)), Valid: true},
		VisibleInPicker:  true,
	})
	if err != nil {
		h.removeImage(ctx, path)
		// Another request created the shortcode after the check above;
		// index_custom_emojis_on_shortcode_local refused this one.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			validationFailed(w, msgShortcodeTaken)
			return
		}
		slog.ErrorContext(ctx, "Creating custom emoji failed", "error", err)
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	web.JSON(w, http.StatusOK, mapAdminCustomEmoji(emoji, ""))
}

// removeImage deletes a stored image, even when the request has been
// canceled. A failure is logged: the image is left behind, unreferenced.
func (h *CustomEmojisHandler) removeImage(ctx context.Context, path string) {
	if err := h.storage.Delete(context.WithoutCancel(ctx), path); err != nil {
		slog.ErrorContext(ctx, "Removing custom emoji image failed", "path", path, "error", err)
	}
}

// storedCustomEmojiFiles returns the files stored for an emoji, which
// Delete removes. Create stores the original image under
// customEmojiImageDir; a file name outside it wasn't stored there and is
// left alone, so a stray value can't point Delete at another file.
func storedCustomEmojiFiles(emoji db.CustomEmoji) []string {
	name := emoji.ImageFileName.String
	if !emoji.ImageFileName.Valid || !strings.HasPrefix(name, customEmojiImageDir) || path.Clean(name) != name {
		return nil
	}
	return []string{name}
}

// Delete handles DELETE /api/v1/admin/custom_emojis/:id. Deleting the row
// deletes the reactions made with the emoji through their foreign key; the
// stored files are removed afterwards, and a failure to remove them is
// logged without failing the request.
func (h *CustomEmojisHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	emoji, err := h.store.DeleteCustomEmoji(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "Deleting custom emoji failed", "id", id, "error", err)
		web.JSONError(w, http.StatusInternalServerError, "Database error")
		return
	}

	files := storedCustomEmojiFiles(emoji)
	if len(files) == 0 && emoji.ImageFileName.String != "" {
		slog.WarnContext(ctx, "Not removing custom emoji image outside the custom emoji directory",
			"id", id, "image_file_name", emoji.ImageFileName.String)
	}
	for _, file := range files {
		h.removeImage(ctx, file)
	}

	web.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers the custom emoji routes. Mastodon has no REST API
// for custom emojis; its admin UI authorizes every action through
// CustomEmojiPolicy, which requires manage_custom_emojis. There are no
// per-resource admin scopes for custom emojis, so reads need admin:read and
// writes admin:write.
func (h *CustomEmojisHandler) RegisterRoutes(mux RouteRegistrar) {
	read := WithAccess(h.roles, oauth.ScopeAdminRead, auth.PermissionManageCustomEmojis)
	write := WithAccess(h.roles, oauth.ScopeAdminWrite, auth.PermissionManageCustomEmojis)

	mux.HandleFunc("GET /api/v1/admin/custom_emojis", read(h.List))
	mux.HandleFunc("POST /api/v1/admin/custom_emojis", write(h.Create))
	mux.HandleFunc("DELETE /api/v1/admin/custom_emojis/{id}", write(h.Delete))
}
