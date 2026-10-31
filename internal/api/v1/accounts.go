package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rivo/uniseg"
	"golang.org/x/net/idna"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/mailer"
	"github.com/deicod/gotodon/internal/media"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
	"github.com/deicod/gotodon/pkg/oauth"
)

// AccountsHandler handles account-related endpoints
type AccountsHandler struct {
	queries              db.Querier
	pool                 *pgxpool.Pool
	cfg                  *config.Config
	outgoing             federation.OutgoingDeliverer
	resolver             *federation.RemoteActorResolver
	profileMediaUploader profileMediaUploader
	mail                 mailer.Deliverer
}

type profileMediaUploader interface {
	Upload(ctx context.Context, accountID int64, filename string, data []byte) (*media.UploadResult, error)
	Delete(ctx context.Context, path string) error
}

const (
	defaultAccountsLimit      = 40
	maxProfileFields          = 4
	attributionDomainsLimit   = 100
	accountDisplayNameMax     = 30
	accountNoteLengthLimit    = 500
	accountNoteCommentMax     = 2000
	accountNoteURLPlaceholder = "xxxxxxxxxxxxxxxxxxxxxxx"
	profileMediaFileSizeLimit = 8 * 1024 * 1024
	maxProfileMediaBodySize   = 2*profileMediaFileSizeLimit + 1024*1024
)

var (
	accountNoteURLRegex           = regexp.MustCompile(`https?://[^\s<>()]+`)
	accountNoteRemoteMentionRegex = regexp.MustCompile(`@([a-zA-Z0-9_]+)@([a-zA-Z0-9_]+(?:[.-]+[a-zA-Z0-9_]+)*)`)
)

// NewAccountsHandler creates a new accounts handler
func NewAccountsHandler(queries db.Querier, pool *pgxpool.Pool, cfg *config.Config, outgoing federation.OutgoingDeliverer) *AccountsHandler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	var resolver *federation.RemoteActorResolver
	if queries != nil {
		resolver = federation.NewRemoteActorResolver(queries, cfg.Instance.LocalDomain)
	}
	return &AccountsHandler{queries: queries, pool: pool, cfg: cfg, outgoing: outgoing, resolver: resolver}
}

// SetRemoteAccountRefresher enables background refresh for stale remote accounts.
func (h *AccountsHandler) SetRemoteAccountRefresher(refresher federation.RemoteAccountRefresher) {
	if h == nil || h.resolver == nil {
		return
	}
	h.resolver.SetRefresher(refresher)
}

// SetProfileMediaUploader configures profile media uploads (avatar/header).
func (h *AccountsHandler) SetProfileMediaUploader(uploader profileMediaUploader) {
	if h == nil {
		return
	}
	h.profileMediaUploader = uploader
}

// SetMailDeliverer enables confirmation emails for new registrations. When no
// deliverer is configured, registrations are confirmed immediately.
func (h *AccountsHandler) SetMailDeliverer(deliverer mailer.Deliverer) {
	if h == nil {
		return
	}
	h.mail = deliverer
}

func (h *AccountsHandler) useDB() bool {
	return h.queries != nil
}

// Account represents a Mastodon account in API responses
type Account struct {
	ID              string                `json:"id"`
	Username        string                `json:"username"`
	Acct            string                `json:"acct"`
	DisplayName     string                `json:"display_name"`
	Locked          bool                  `json:"locked"`
	Bot             bool                  `json:"bot"`
	Discoverable    *bool                 `json:"discoverable"`
	Indexable       bool                  `json:"indexable"`
	Group           bool                  `json:"group"`
	CreatedAt       time.Time             `json:"created_at"`
	Note            string                `json:"note"`
	URL             string                `json:"url"`
	URI             string                `json:"uri"`
	Avatar          string                `json:"avatar"`
	AvatarStatic    string                `json:"avatar_static"`
	Header          string                `json:"header"`
	HeaderStatic    string                `json:"header_static"`
	FollowersCount  int64                 `json:"followers_count"`
	FollowingCount  int64                 `json:"following_count"`
	StatusesCount   int64                 `json:"statuses_count"`
	LastStatusAt    *string               `json:"last_status_at"`
	HideCollections bool                  `json:"hide_collections"`
	Moved           *Account              `json:"moved,omitempty"`
	Suspended       *bool                 `json:"suspended,omitempty"`
	Limited         *bool                 `json:"limited,omitempty"`
	NoIndex         *bool                 `json:"noindex,omitempty"`
	Memorial        *bool                 `json:"memorial,omitempty"`
	Roles           *[]AccountRoleSummary `json:"roles,omitempty"`
	Emojis          []CustomEmoji         `json:"emojis"`
	Fields          []Field               `json:"fields"`
}

// Field represents a profile field
type Field struct {
	Name       string     `json:"name"`
	Value      string     `json:"value"`
	VerifiedAt *time.Time `json:"verified_at"`
}

// CredentialAccount is the account response for verify_credentials
type CredentialAccount struct {
	Account
	Source AccountSource `json:"source"`
	Role   *AccountRole  `json:"role,omitempty"`
}

// AccountSource contains the profile source data
type AccountSource struct {
	Privacy             string   `json:"privacy"`
	Sensitive           bool     `json:"sensitive"`
	Language            string   `json:"language"`
	Note                string   `json:"note"`
	Fields              []Field  `json:"fields"`
	FollowRequestsCount int      `json:"follow_requests_count"`
	HideCollections     bool     `json:"hide_collections"`
	Discoverable        bool     `json:"discoverable"`
	Indexable           bool     `json:"indexable"`
	AttributionDomains  []string `json:"attribution_domains"`
	QuotePolicy         string   `json:"quote_policy"`
}

// AccountRole represents a user role
type AccountRole struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Permissions string `json:"permissions"`
	Highlighted bool   `json:"highlighted"`
}

// AccountRoleSummary represents a public role entry for local accounts.
type AccountRoleSummary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type familiarFollowersResult struct {
	ID       string    `json:"id"`
	Accounts []Account `json:"accounts"`
}

func (h *AccountsHandler) dbAccountToAPI(dbAcc db.Account) Account {
	return SerializeAccount(h.cfg, dbAcc)
}

func (h *AccountsHandler) dbAccountWithStats(ctx context.Context, dbAcc db.Account) Account {
	if !h.useDB() {
		return h.dbAccountToAPI(dbAcc)
	}

	accounts := serializeAccounts(ctx, h.queries, h.cfg, []db.Account{dbAcc}, true)
	if acc, ok := accounts[dbAcc.ID]; ok {
		return acc
	}
	return h.dbAccountToAPI(dbAcc)
}

// VerifyCredentials handles GET /api/v1/accounts/verify_credentials
func (h *AccountsHandler) VerifyCredentials(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}
	scopes := middleware.GetScopes(r.Context())
	if scopes != "" && !oauth.HasScope(scopes, "read:accounts") && !oauth.HasScope(scopes, "profile") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Account data unavailable")
		return
	}

	ctx := r.Context()
	dbAcc, err := h.queries.GetAccountByID(ctx, accountID)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	web.JSON(w, http.StatusOK, buildCredentialAccount(ctx, h.queries, h.cfg, dbAcc))
}

// UpdateCredentialsRequest is the request for PATCH /api/v1/accounts/update_credentials
type UpdateCredentialsRequest struct {
	DisplayName        *string                  `json:"display_name" form:"display_name"`
	Note               *string                  `json:"note" form:"note"`
	Avatar             *string                  `json:"avatar" form:"avatar"`
	Header             *string                  `json:"header" form:"header"`
	Locked             *bool                    `json:"locked" form:"locked"`
	Bot                *bool                    `json:"bot" form:"bot"`
	Discoverable       *bool                    `json:"discoverable" form:"discoverable"`
	HideCollections    *bool                    `json:"hide_collections" form:"hide_collections"`
	Indexable          *bool                    `json:"indexable" form:"indexable"`
	Source             *UpdateCredentialsSource `json:"source" form:"source"`
	AttributionDomains []string                 `json:"attribution_domains" form:"attribution_domains"`
	FieldsAttributes   []UpdateCredentialsField `json:"fields_attributes" form:"fields_attributes"`
}

type UpdateCredentialsField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type UpdateCredentialsSource struct {
	Privacy     *string `json:"privacy" form:"privacy"`
	Sensitive   *bool   `json:"sensitive" form:"sensitive"`
	Language    *string `json:"language" form:"language"`
	QuotePolicy *string `json:"quote_policy" form:"quote_policy"`
}

type mastodonValidationDetail struct {
	Error       string `json:"error"`
	Description string `json:"description"`
}

type mastodonValidationErrorResponse struct {
	Error   string                                `json:"error"`
	Details map[string][]mastodonValidationDetail `json:"details"`
}

// UpdateCredentials handles PATCH /api/v1/accounts/update_credentials
func (h *AccountsHandler) UpdateCredentials(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}
	scopes := middleware.GetScopes(r.Context())
	if scopes != "" && !oauth.HasScope(scopes, "write") && !oauth.HasScope(scopes, "write:accounts") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	multipartCredentials := strings.HasPrefix(contentType, "multipart/form-data")
	if multipartCredentials {
		// Avatar and header images need longer than the server's ordinary
		// request budget.
		web.ExtendUploadDeadlines(w)
		r.Body = http.MaxBytesReader(w, r.Body, maxProfileMediaBodySize)
	}

	var req UpdateCredentialsRequest
	if err := bindUpdateCredentialsRequest(r, &req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if multipartCredentials && (errors.As(err, &maxBytesErr) || strings.Contains(err.Error(), "request body too large")) {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: File is too large")
			return
		}
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if multipartCredentials && r.MultipartForm != nil {
		defer func() {
			if err := r.MultipartForm.RemoveAll(); err != nil {
				fmt.Printf("Failed to clean up multipart form files: %v\n", err)
			}
		}()
	}

	ctx := r.Context()
	var sourcePrivacy *string
	var sourceSensitive *bool
	var sourceLanguage *string
	var sourceQuotePolicy *string
	if req.Source != nil {
		sourcePrivacy = req.Source.Privacy
		sourceSensitive = req.Source.Sensitive
		sourceLanguage = req.Source.Language
		sourceQuotePolicy = req.Source.QuotePolicy
	}
	if sourceQuotePolicy == nil {
		if vals, ok := r.PostForm["source[quote_policy]"]; ok {
			value := ""
			if len(vals) > 0 {
				value = vals[0]
			}
			sourceQuotePolicy = &value
		}
	}

	if sourceQuotePolicy != nil {
		trimmed := strings.TrimSpace(*sourceQuotePolicy)
		if trimmed != "" && !isValidPreferenceQuotePolicy(trimmed) {
			description := "is not included in the list"
			web.JSON(w, http.StatusUnprocessableEntity, mastodonValidationErrorResponse{
				Error: fmt.Sprintf("Validation failed: Quote policy %s", description),
				Details: map[string][]mastodonValidationDetail{
					"quote_policy": {
						{
							Error:       "ERR_INCLUSION",
							Description: description,
						},
					},
				},
			})
			return
		}
	}

	if req.DisplayName != nil && len([]rune(*req.DisplayName)) > accountDisplayNameMax {
		description := fmt.Sprintf("character limit of %d exceeded", accountDisplayNameMax)
		web.JSON(w, http.StatusUnprocessableEntity, mastodonValidationErrorResponse{
			Error: fmt.Sprintf("Validation failed: Display name %s", description),
			Details: map[string][]mastodonValidationDetail{
				"display_name": {
					{
						Error:       "ERR_TOO_LONG",
						Description: description,
					},
				},
			},
		})
		return
	}

	if req.Note != nil && accountNoteLength(*req.Note) > accountNoteLengthLimit {
		description := fmt.Sprintf("character limit of %d exceeded", accountNoteLengthLimit)
		web.JSON(w, http.StatusUnprocessableEntity, mastodonValidationErrorResponse{
			Error: fmt.Sprintf("Validation failed: Note %s", description),
			Details: map[string][]mastodonValidationDetail{
				"note": {
					{
						Error:       "ERR_TOO_LONG",
						Description: description,
					},
				},
			},
		})
		return
	}

	var normalizedAttributionDomains []string
	if req.AttributionDomains != nil {
		normalizedAttributionDomains = normalizeAttributionDomains(req.AttributionDomains)
		if len(normalizedAttributionDomains) > attributionDomainsLimit {
			description := fmt.Sprintf("attribution domains limit of %d exceeded", attributionDomainsLimit)
			web.JSON(w, http.StatusUnprocessableEntity, mastodonValidationErrorResponse{
				Error: fmt.Sprintf("Validation failed: Attribution domains %s", description),
				Details: map[string][]mastodonValidationDetail{
					"attribution_domains": {
						{
							Error:       "ERR_TOO_LONG",
							Description: description,
						},
					},
				},
			})
			return
		}
		for _, domain := range normalizedAttributionDomains {
			if !isValidAttributionDomain(domain) {
				description := "is invalid"
				web.JSON(w, http.StatusUnprocessableEntity, mastodonValidationErrorResponse{
					Error: fmt.Sprintf("Validation failed: Attribution domains %s", description),
					Details: map[string][]mastodonValidationDetail{
						"attribution_domains": {
							{
								Error:       "ERR_INVALID",
								Description: description,
							},
						},
					},
				})
				return
			}
		}
	}

	var (
		fields          []UpdateCredentialsField
		fieldsProvided  bool
		existingFields  []byte
		existingAccount db.Account
		hasExisting     bool
	)
	if h.useDB() {
		var parseErr error
		fields, fieldsProvided, parseErr = parseUpdateCredentialsFields(r, req)
		if parseErr != nil {
			web.JSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		existingFields = nil
		avatarRequested := multipartFormHasFile(r, "avatar")
		headerRequested := multipartFormHasFile(r, "header")
		if fieldsProvided && hasProfileFieldWithValueMissingLabel(fields) {
			description := "fields with values must have labels"
			web.JSON(w, http.StatusUnprocessableEntity, mastodonValidationErrorResponse{
				Error: fmt.Sprintf("Validation failed: %s", description),
				Details: map[string][]mastodonValidationDetail{
					"fields_with_values_missing_labels": {
						{
							Error:       "ERR_BLANK",
							Description: description,
						},
					},
				},
			})
			return
		}
		if fieldsProvided && len(fields) > maxProfileFields {
			description := fmt.Sprintf("field limit of %d exceeded", maxProfileFields)
			web.JSON(w, http.StatusUnprocessableEntity, mastodonValidationErrorResponse{
				Error: fmt.Sprintf("Validation failed: Fields %s", description),
				Details: map[string][]mastodonValidationDetail{
					"fields": {
						{
							Error:       "ERR_TOO_LONG",
							Description: description,
						},
					},
				},
			})
			return
		}
		if fieldsProvided || avatarRequested || headerRequested {
			dbAcc, err := h.queries.GetAccountByID(ctx, accountID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				web.JSONError(w, http.StatusInternalServerError, "Failed to fetch account")
				return
			}
			if err == nil {
				existingAccount = dbAcc
				hasExisting = true
				existingFields = dbAcc.Fields
			}
		}
	}

	avatarUpload, avatarProvided, err := h.uploadProfileMedia(r.Context(), accountID, r, "avatar")
	if err != nil {
		writeProfileMediaUploadError(w, err, "avatar")
		return
	}

	headerUpload, headerProvided, err := h.uploadProfileMedia(r.Context(), accountID, r, "header")
	if err != nil {
		if cleanupErr := h.cleanupUploadedProfileMedia(ctx, profileUploadPath(avatarUpload)); cleanupErr != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to clean up uploaded media")
			return
		}
		writeProfileMediaUploadError(w, err, "header")
		return
	}

	if h.useDB() {
		params := db.UpdateAccountParams{ID: accountID}
		if req.DisplayName != nil {
			params.DisplayName = pgtype.Text{String: *req.DisplayName, Valid: true}
		}
		if req.Note != nil {
			params.Note = pgtype.Text{String: *req.Note, Valid: true}
		}
		if req.Locked != nil {
			params.Locked = pgtype.Bool{Bool: *req.Locked, Valid: true}
		}
		if req.Discoverable != nil {
			params.Discoverable = pgtype.Bool{Bool: *req.Discoverable, Valid: true}
		}
		if req.Indexable != nil {
			params.Indexable = pgtype.Bool{Bool: *req.Indexable, Valid: true}
		}
		if req.HideCollections != nil {
			params.HideCollections = pgtype.Bool{Bool: *req.HideCollections, Valid: true}
		}
		if req.Bot != nil {
			actorType := ""
			if *req.Bot {
				actorType = "Service"
			}
			params.ActorType = pgtype.Text{String: actorType, Valid: true}
		}
		if req.AttributionDomains != nil {
			params.AttributionDomains = normalizedAttributionDomains
		}
		uploadedAt := time.Now().UTC()
		if avatarProvided {
			params.AvatarFileName = pgtype.Text{String: avatarUpload.Path, Valid: true}
			params.AvatarContentType = pgtype.Text{String: avatarUpload.ContentType, Valid: true}
			params.AvatarFileSize = pgtype.Int4{Int32: int32(avatarUpload.Size), Valid: true}
			params.AvatarUpdatedAt = pgtype.Timestamptz{Time: uploadedAt, Valid: true}
			params.AvatarRemoteUrl = pgtype.Text{String: "", Valid: true}
		}
		if headerProvided {
			params.HeaderFileName = pgtype.Text{String: headerUpload.Path, Valid: true}
			params.HeaderContentType = pgtype.Text{String: headerUpload.ContentType, Valid: true}
			params.HeaderFileSize = pgtype.Int4{Int32: int32(headerUpload.Size), Valid: true}
			params.HeaderUpdatedAt = pgtype.Timestamptz{Time: uploadedAt, Valid: true}
			params.HeaderRemoteUrl = pgtype.Text{String: "", Valid: true}
		}
		if fieldsProvided {
			encoded, err := json.Marshal(preserveVerifiedAtFields(existingFields, fields))
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to encode fields")
				return
			}
			params.Fields = encoded
		}

		dbAcc, err := h.queries.UpdateAccount(ctx, params)
		if err != nil {
			if cleanupErr := h.cleanupUploadedProfileMedia(ctx, profileUploadPath(avatarUpload), profileUploadPath(headerUpload)); cleanupErr != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to clean up uploaded media")
				return
			}
			web.JSONError(w, http.StatusInternalServerError, "Failed to update account")
			return
		}
		if hasExisting {
			var replacedPaths []string
			if avatarProvided {
				oldAvatarPath := strings.TrimSpace(existingAccount.AvatarFileName.String)
				if oldAvatarPath != "" && oldAvatarPath != avatarUpload.Path {
					replacedPaths = append(replacedPaths, oldAvatarPath)
				}
			}
			if headerProvided {
				oldHeaderPath := strings.TrimSpace(existingAccount.HeaderFileName.String)
				if oldHeaderPath != "" && oldHeaderPath != headerUpload.Path {
					replacedPaths = append(replacedPaths, oldHeaderPath)
				}
			}
			if cleanupErr := h.cleanupUploadedProfileMedia(ctx, replacedPaths...); cleanupErr != nil {
				fmt.Printf("Failed to clean up replaced profile media: %v\n", cleanupErr)
			}
		}

		// Update user settings (source params)
		if sourcePrivacy != nil || sourceSensitive != nil || sourceLanguage != nil || sourceQuotePolicy != nil {
			settings := map[string]any{}
			user, err := h.queries.GetUserByAccountID(ctx, accountID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				web.JSONError(w, http.StatusInternalServerError, "Failed to fetch preferences")
				return
			}
			if err == nil {
				settings = settingsFromText(user.Settings)
			}

			applySourceSettings(settings, sourcePrivacy, sourceSensitive, sourceLanguage, sourceQuotePolicy)
			encoded, err := settingsToText(settings)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to encode preferences")
				return
			}
			if err := h.queries.UpdateUserSettings(ctx, db.UpdateUserSettingsParams{
				AccountID: accountID,
				Settings:  encoded,
			}); err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to update preferences")
				return
			}
		}

		web.JSON(w, http.StatusOK, buildCredentialAccount(ctx, h.queries, h.cfg, dbAcc))
		return
	}

	h.VerifyCredentials(w, r)
}

var errProfileMediaUploaderNotConfigured = errors.New("profile media uploader not configured")

func bindUpdateCredentialsRequest(r *http.Request, req *UpdateCredentialsRequest) error {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(media.MaxImageSize + 1024); err != nil {
			return err
		}
		values := r.PostForm
		locked, err := formBoolPtr(values, "locked")
		if err != nil {
			return err
		}
		bot, err := formBoolPtr(values, "bot")
		if err != nil {
			return err
		}
		discoverable, err := formBoolPtr(values, "discoverable")
		if err != nil {
			return err
		}
		hideCollections, err := formBoolPtr(values, "hide_collections")
		if err != nil {
			return err
		}
		indexable, err := formBoolPtr(values, "indexable")
		if err != nil {
			return err
		}
		*req = UpdateCredentialsRequest{
			DisplayName:        formStringPtr(values, "display_name"),
			Note:               formStringPtr(values, "note"),
			Locked:             locked,
			Bot:                bot,
			Discoverable:       discoverable,
			HideCollections:    hideCollections,
			Indexable:          indexable,
			AttributionDomains: formStringSlice(values, "attribution_domains"),
		}
		if len(req.AttributionDomains) == 0 {
			req.AttributionDomains = formStringSlice(values, "attribution_domains[]")
		}
		source, err := formSource(values)
		if err != nil {
			return err
		}
		if source != nil {
			req.Source = source
		}
		return nil
	}

	return web.Bind(r, req)
}

func formStringPtr(values url.Values, key string) *string {
	value, ok := values[key]
	if !ok || len(value) == 0 {
		return nil
	}
	v := value[0]
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}

func formBoolPtr(values url.Values, key string) (*bool, error) {
	value, ok := values[key]
	if !ok || len(value) == 0 {
		return nil, nil
	}
	parsed, err := parseBoolFormValue(value[0])
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func formStringSlice(values url.Values, key string) []string {
	out, ok := values[key]
	if !ok || len(out) == 0 {
		return nil
	}
	copied := make([]string, len(out))
	copy(copied, out)
	return copied
}

func formSource(values url.Values) (*UpdateCredentialsSource, error) {
	sensitive, err := formBoolPtr(values, "source[sensitive]")
	if err != nil {
		return nil, err
	}
	source := &UpdateCredentialsSource{
		Privacy:     formStringPtr(values, "source[privacy]"),
		Sensitive:   sensitive,
		Language:    formStringPtr(values, "source[language]"),
		QuotePolicy: formOptionalStringPtr(values, "source[quote_policy]"),
	}
	if source.Privacy == nil && source.Sensitive == nil && source.Language == nil && source.QuotePolicy == nil {
		return nil, nil
	}
	return source, nil
}

func formOptionalStringPtr(values url.Values, key string) *string {
	value, ok := values[key]
	if !ok || len(value) == 0 {
		return nil
	}
	v := value[0]
	return &v
}

func parseBoolFormValue(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "t", "yes", "y", "on":
		return true, nil
	case "false", "0", "f", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value")
	}
}

func writeProfileMediaUploadError(w http.ResponseWriter, err error, field string) {
	switch {
	case errors.Is(err, errProfileMediaUploaderNotConfigured):
		web.JSONError(w, http.StatusInternalServerError, "Media storage unavailable")
	case errors.Is(err, media.ErrUnsupportedType):
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: File type is not supported")
	case errors.Is(err, media.ErrFileTooLarge):
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: File is too large")
	default:
		web.JSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to upload %s", field))
	}
}

func (h *AccountsHandler) cleanupUploadedProfileMedia(ctx context.Context, paths ...string) error {
	if h == nil || h.profileMediaUploader == nil {
		return nil
	}

	seen := make(map[string]struct{}, len(paths))
	var cleanupErr error
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		if err := h.profileMediaUploader.Delete(ctx, path); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func multipartFormHasFile(r *http.Request, field string) bool {
	if r == nil || r.MultipartForm == nil || r.MultipartForm.File == nil {
		return false
	}
	return len(r.MultipartForm.File[field]) > 0
}

func profileUploadPath(upload *media.UploadResult) string {
	if upload == nil {
		return ""
	}
	return strings.TrimSpace(upload.Path)
}

func profileMediaSizeLimit(field string) int64 {
	switch field {
	case "avatar", "header":
		return profileMediaFileSizeLimit
	default:
		return media.MaxImageSize
	}
}

func (h *AccountsHandler) uploadProfileMedia(ctx context.Context, accountID int64, r *http.Request, field string) (*media.UploadResult, bool, error) {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		return nil, false, nil
	}

	file, header, err := r.FormFile(field)
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer func() { _ = file.Close() }()

	if h.profileMediaUploader == nil {
		return nil, true, errProfileMediaUploaderNotConfigured
	}

	maxSize := profileMediaSizeLimit(field)
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, true, err
	}
	if int64(len(data)) > maxSize {
		return nil, true, media.ErrFileTooLarge
	}

	result, err := h.profileMediaUploader.Upload(ctx, accountID, header.Filename, data)
	if err != nil {
		return nil, true, err
	}
	if result == nil || strings.TrimSpace(result.Path) == "" {
		return nil, true, errors.New("empty upload result")
	}
	result.Path = strings.TrimSpace(result.Path)
	result.ContentType = strings.TrimSpace(result.ContentType)
	return result, true, nil
}

func parseUpdateCredentialsFields(r *http.Request, req UpdateCredentialsRequest) ([]UpdateCredentialsField, bool, error) {
	if req.FieldsAttributes != nil {
		return normalizeFieldsAttributes(req.FieldsAttributes), true, nil
	}
	if err := r.ParseForm(); err != nil {
		return nil, false, err
	}
	fields, ok := fieldsAttributesFromForm(r.PostForm)
	return normalizeFieldsAttributes(fields), ok, nil
}

func fieldsAttributesFromForm(values url.Values) ([]UpdateCredentialsField, bool) {
	if len(values) == 0 {
		return nil, false
	}

	indexed := map[int]*UpdateCredentialsField{}
	maxIndex := -1
	present := false

	for key, vals := range values {
		if !strings.HasPrefix(key, "fields_attributes[") {
			continue
		}
		idx, attr, indexedKey := parseFieldAttributeKey(key)
		if attr == "" {
			continue
		}
		present = true
		if !indexedKey {
			continue
		}
		field := indexed[idx]
		if field == nil {
			field = &UpdateCredentialsField{}
			indexed[idx] = field
		}
		if len(vals) == 0 {
			continue
		}
		switch attr {
		case "name":
			field.Name = vals[0]
		case "value":
			field.Value = vals[0]
		}
		if idx > maxIndex {
			maxIndex = idx
		}
	}

	fields := make([]UpdateCredentialsField, 0)
	for i := 0; i <= maxIndex; i++ {
		if field, ok := indexed[i]; ok {
			fields = append(fields, *field)
		}
	}

	names := values["fields_attributes[][name]"]
	vals := values["fields_attributes[][value]"]
	if len(names) > 0 || len(vals) > 0 {
		present = true
		maxLen := len(names)
		if len(vals) > maxLen {
			maxLen = len(vals)
		}
		for i := 0; i < maxLen; i++ {
			field := UpdateCredentialsField{}
			if i < len(names) {
				field.Name = names[i]
			}
			if i < len(vals) {
				field.Value = vals[i]
			}
			fields = append(fields, field)
		}
	}

	if !present {
		return nil, false
	}
	return fields, true
}

func parseFieldAttributeKey(key string) (int, string, bool) {
	const prefix = "fields_attributes["
	if !strings.HasPrefix(key, prefix) {
		return 0, "", false
	}
	rest := strings.TrimPrefix(key, prefix)
	idxEnd := strings.Index(rest, "]")
	if idxEnd == -1 {
		return 0, "", false
	}
	idxPart := rest[:idxEnd]
	remainder := rest[idxEnd+1:]
	if !strings.HasPrefix(remainder, "[") {
		return 0, "", false
	}
	attrEnd := strings.Index(remainder, "]")
	if attrEnd == -1 {
		return 0, "", false
	}
	attr := remainder[1:attrEnd]
	if attr != "name" && attr != "value" {
		return 0, "", false
	}
	if idxPart == "" {
		return 0, attr, false
	}
	idx, err := strconv.Atoi(idxPart)
	if err != nil || idx < 0 {
		return 0, "", false
	}
	return idx, attr, true
}

func normalizeFieldsAttributes(fields []UpdateCredentialsField) []UpdateCredentialsField {
	normalized := make([]UpdateCredentialsField, 0, len(fields))
	for _, field := range fields {
		if strings.TrimSpace(field.Name) == "" && strings.TrimSpace(field.Value) == "" {
			continue
		}
		normalized = append(normalized, field)
	}
	return normalized
}

func hasProfileFieldWithValueMissingLabel(fields []UpdateCredentialsField) bool {
	for _, field := range fields {
		if strings.TrimSpace(field.Name) == "" && strings.TrimSpace(field.Value) != "" {
			return true
		}
	}
	return false
}

func preserveVerifiedAtFields(existingRaw []byte, fields []UpdateCredentialsField) []Field {
	existing := parseAccountFields(existingRaw)
	verifiedByValue := make(map[string]time.Time, len(existing))
	for _, field := range existing {
		if field.VerifiedAt == nil {
			continue
		}
		if _, exists := verifiedByValue[field.Value]; exists {
			continue
		}
		verifiedByValue[field.Value] = *field.VerifiedAt
	}

	merged := make([]Field, 0, len(fields))
	for _, field := range fields {
		mergedField := Field{
			Name:  field.Name,
			Value: field.Value,
		}
		if verifiedAt, ok := verifiedByValue[field.Value]; ok {
			value := verifiedAt
			mergedField.VerifiedAt = &value
		}
		merged = append(merged, mergedField)
	}
	return merged
}

func normalizeAttributionDomains(domains []string) []string {
	if len(domains) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(domains))
	normalized := make([]string, 0, len(domains))
	for _, domain := range domains {
		trimmed := strings.ToLower(strings.TrimSpace(domain))
		trimmed = strings.TrimPrefix(trimmed, "http://")
		trimmed = strings.TrimPrefix(trimmed, "https://")
		trimmed = strings.TrimPrefix(trimmed, "*.")
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func isValidAttributionDomain(domain string) bool {
	asciiDomain, err := idna.Lookup.ToASCII(domain)
	if err != nil {
		return false
	}
	asciiDomain = strings.TrimSuffix(asciiDomain, ".")
	if asciiDomain == "" || len(asciiDomain) > 255 {
		return false
	}
	if strings.Contains(asciiDomain, "..") {
		return false
	}
	labels := strings.Split(asciiDomain, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func accountNoteLength(note string) int {
	countable := accountNoteURLRegex.ReplaceAllStringFunc(note, normalizeAccountNoteURLForLength)
	countable = accountNoteRemoteMentionRegex.ReplaceAllString(countable, "@$1")

	graphemes := uniseg.NewGraphemes(countable)
	count := 0
	for graphemes.Next() {
		count++
	}
	return count
}

func normalizeAccountNoteURLForLength(match string) string {
	urlPart, suffix := splitAccountNoteURLSuffix(match)
	if urlPart == "" {
		return match
	}
	if shouldCountAccountNoteURLSuffixAsURL(urlPart, suffix) {
		return accountNoteURLPlaceholder
	}
	return accountNoteURLPlaceholder + suffix
}

func splitAccountNoteURLSuffix(match string) (urlPart, suffix string) {
	idx := len(match)
	for idx > 0 {
		switch match[idx-1] {
		case '.', ',', '!', '?', ';', ':':
			idx--
		default:
			return match[:idx], match[idx:]
		}
	}
	return match, ""
}

func shouldCountAccountNoteURLSuffixAsURL(urlPart, suffix string) bool {
	if len(suffix) != 1 {
		return false
	}
	switch suffix[0] {
	case '!', '?', ';', ':':
		return strings.HasSuffix(urlPart, "/")
	default:
		return false
	}
}

// Get handles GET /api/v1/accounts/:id
func (h *AccountsHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := web.Param(r, "id")
	if idStr == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Account data unavailable")
		return
	}

	ctx := r.Context()
	dbAcc, err := h.queries.GetAccountByID(ctx, id)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	web.JSON(w, http.StatusOK, h.dbAccountWithStats(ctx, dbAcc))
}

// Index handles GET /api/v1/accounts
func (h *AccountsHandler) Index(w http.ResponseWriter, r *http.Request) {
	scopes := middleware.GetScopes(r.Context())
	if scopes != "" && !oauth.HasScope(scopes, "read:accounts") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return
	}

	ids := parseAccountIDs(r)
	if len(ids) > defaultAccountsLimit {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed")
		return
	}
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Account data unavailable")
		return
	}
	if len(ids) == 0 {
		web.JSON(w, http.StatusOK, []Account{})
		return
	}

	ctx := r.Context()
	dbAccounts, err := h.queries.GetAccountsByIDs(ctx, ids)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to fetch accounts")
		return
	}
	if len(dbAccounts) == 0 {
		web.JSON(w, http.StatusOK, []Account{})
		return
	}

	accountsByID := serializeAccounts(ctx, h.queries, h.cfg, dbAccounts, true)

	resp := make([]Account, 0, len(dbAccounts))
	for _, id := range ids {
		acc, ok := accountsByID[id]
		if !ok {
			continue
		}
		resp = append(resp, acc)
	}
	web.JSON(w, http.StatusOK, resp)
}

// RegisterAccountRequest is the request for POST /api/v1/accounts
type RegisterAccountRequest struct {
	Username  string  `json:"username" form:"username"`
	Email     string  `json:"email" form:"email"`
	Password  string  `json:"password" form:"password"`
	Agreement bool    `json:"agreement" form:"agreement"`
	Locale    string  `json:"locale" form:"locale"`
	Reason    *string `json:"reason" form:"reason"`
}

// Register handles POST /api/v1/accounts
func (h *AccountsHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterAccountRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	registrationsMode := strings.ToLower(strings.TrimSpace(h.cfg.Instance.RegistrationsMode))
	if h.cfg.Instance.SingleUserMode || registrationsMode == "none" {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return
	}

	if req.Username == "" {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Username can't be blank")
		return
	}
	if req.Email == "" || !auth.ValidateEmail(req.Email) {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Email is invalid")
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Password "+err.Error())
		return
	}
	if !req.Agreement {
		web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Agreement must be accepted")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		if h.pool == nil {
			web.JSONError(w, http.StatusInternalServerError, "Registration unavailable")
			return
		}
		// Start transaction
		tx, err := h.pool.Begin(ctx)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to start transaction")
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()

		qtx := db.New(tx)

		dbAcc, err := qtx.CreateAccount(ctx, db.CreateAccountParams{
			Username:     req.Username,
			DisplayName:  req.Username,
			Note:         "",
			Uri:          "https://example.com/users/" + req.Username,
			PublicKey:    "",
			Discoverable: pgtype.Bool{Bool: true, Valid: true},
			Indexable:    true,
			Protocol:     0,
		})
		if err != nil {
			web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Username has already been taken")
			return
		}

		// Create account stats
		if _, err := qtx.CreateAccountStats(ctx, dbAcc.ID); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to create account stats")
			return
		}

		// Create User record with credential
		hashedPassword, err := auth.HashPassword(req.Password)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to hash password")
			return
		}

		// Approval-mode registrations stay unapproved until staff review.
		approved := registrationsMode != "approved"

		// With a mail deliverer configured the account starts unconfirmed and
		// receives confirmation instructions; otherwise confirm immediately so
		// instances without SMTP stay usable.
		now := time.Now()
		confirmedAt := pgtype.Timestamptz{Time: now, Valid: true}
		confirmationToken := pgtype.Text{Valid: false}
		confirmationSentAt := pgtype.Timestamptz{Valid: false}
		if h.mail != nil {
			token, err := auth.GenerateConfirmationToken()
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to generate confirmation token")
				return
			}
			confirmedAt = pgtype.Timestamptz{Valid: false}
			confirmationToken = pgtype.Text{String: token, Valid: true}
			confirmationSentAt = pgtype.Timestamptz{Time: now, Valid: true}
		}

		// The column is varchar(255); ignore junk that would fail the insert.
		locale := pgtype.Text{Valid: false}
		if trimmed := strings.TrimSpace(req.Locale); trimmed != "" && len(trimmed) <= 255 {
			locale = pgtype.Text{String: trimmed, Valid: true}
		}

		dbUser, err := qtx.CreateUser(ctx, db.CreateUserParams{
			Email:              req.Email,
			EncryptedPassword:  hashedPassword,
			AccountID:          dbAcc.ID,
			ConfirmedAt:        confirmedAt,
			InviteID:           pgtype.Int8{Valid: false},
			RoleID:             pgtype.Int8{Valid: false},
			ConfirmationToken:  confirmationToken,
			ConfirmationSentAt: confirmationSentAt,
			Locale:             locale,
			Approved:           approved,
		})
		if err != nil {
			// Check for unique constraint violation (code 23505)
			if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
				web.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Email has already been taken")
				return
			}
			web.JSONError(w, http.StatusInternalServerError, "Failed to create user record")
			return
		}

		// Generate OAuth access token
		tokenString, err := auth.GenerateConfirmationToken() // reusing secure random generator
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to generate token")
			return
		}

		// Create token record
		_, err = qtx.CreateOAuthAccessToken(ctx, db.CreateOAuthAccessTokenParams{
			Token:           tokenString,
			RefreshToken:    pgtype.Text{Valid: false},
			ExpiresIn:       pgtype.Int4{Valid: false},
			Scopes:          pgtype.Text{String: "read write follow push", Valid: true},
			ApplicationID:   pgtype.Int8{Valid: false}, // No app context for registration yet
			ResourceOwnerID: pgtype.Int8{Int64: dbUser.ID, Valid: true},
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to create access token")
			return
		}

		if err := tx.Commit(ctx); err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to commit transaction")
			return
		}

		if h.mail != nil && confirmationToken.Valid {
			if err := h.mail.Deliver(ctx, confirmationMail(h.cfg, dbUser.Email, locale.String, confirmationToken.String, approved)); err != nil {
				slog.Error("Failed to enqueue confirmation mail", "error", err)
			}
		}

		createdAt := time.Now()
		if dbAcc.CreatedAt.Valid {
			createdAt = dbAcc.CreatedAt.Time
		}

		web.JSON(w, http.StatusOK, map[string]interface{}{
			"access_token": tokenString,
			"token_type":   "Bearer",
			"scope":        "read write follow push",
			"created_at":   createdAt.Unix(),
		})
		return
	}

	web.JSONError(w, http.StatusInternalServerError, "Registration unavailable")
}

// Search handles GET /api/v1/accounts/search
func (h *AccountsHandler) Search(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	query := strings.TrimSpace(web.QueryParam(r, "q"))
	query = strings.TrimPrefix(query, "@")
	if query == "" {
		web.JSON(w, http.StatusOK, []Account{})
		return
	}

	limit := 40
	if l := web.QueryParam(r, "limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 80 {
			limit = parsed
		}
	}
	offset := 0
	if o := web.QueryParam(r, "offset"); o != "" {
		if parsed, err := strconv.Atoi(o); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	resolve := parseBoolParam(web.QueryParam(r, "resolve"))
	following := parseBoolParam(web.QueryParam(r, "following"))

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Search unavailable")
		return
	}

	ctx := r.Context()

	dbAccounts, err := accountSearch(ctx, h.queries, h.resolver, h.cfg, accountID, query, limit, offset, resolve, following)
	if err != nil {
		web.JSON(w, http.StatusOK, []Account{})
		return
	}

	accountsMap := serializeAccounts(ctx, h.queries, h.cfg, dbAccounts, true)
	accounts := make([]Account, 0, len(dbAccounts))
	for _, dbAcc := range dbAccounts {
		if acc, ok := accountsMap[dbAcc.ID]; ok {
			accounts = append(accounts, acc)
		}
	}
	web.JSON(w, http.StatusOK, accounts)
}

type accountSearchQuerier interface {
	SearchAccounts(ctx context.Context, arg db.SearchAccountsParams) ([]db.Account, error)
	SearchAccountsFollowing(ctx context.Context, arg db.SearchAccountsFollowingParams) ([]db.Account, error)
	GetAccountByUsername(ctx context.Context, username string) (db.Account, error)
	GetAccountByUsernameAndDomain(ctx context.Context, arg db.GetAccountByUsernameAndDomainParams) (db.Account, error)
	GetFollow(ctx context.Context, arg db.GetFollowParams) (db.Follow, error)
}

type accountSearchResolver interface {
	ResolveAccount(ctx context.Context, acct string) (db.Account, error)
}

func accountSearch(ctx context.Context, q accountSearchQuerier, resolver accountSearchResolver, cfg *config.Config, accountID int64, query string, limit, offset int, resolve, following bool) ([]db.Account, error) {
	if q == nil {
		return []db.Account{}, nil
	}

	trimmed := strings.TrimSpace(query)
	trimmed = strings.TrimPrefix(trimmed, "@")
	if trimmed == "" || limit < 1 {
		return []db.Account{}, nil
	}

	term := accountSearchTerms(trimmed, cfg)

	results := make([]db.Account, 0, limit)
	seen := make(map[int64]struct{})

	if offset == 0 {
		if exact, ok := accountSearchExactMatch(ctx, q, resolver, cfg, accountID, trimmed, resolve, following); ok {
			results = append(results, exact)
			seen[exact.ID] = struct{}{}
			if limit > 0 {
				limit--
			}
		}
	}

	if limit > 0 {
		var rows []db.Account
		var err error
		if following {
			rows, err = q.SearchAccountsFollowing(ctx, db.SearchAccountsFollowingParams{
				AccountID: accountID,
				Query:     term,
				Limit:     int32(limit),
				Offset:    int32(offset),
			})
		} else {
			rows, err = q.SearchAccounts(ctx, db.SearchAccountsParams{
				Query:  term,
				Limit:  int32(limit),
				Offset: int32(offset),
			})
		}
		if err != nil {
			return results, err
		}
		for _, acc := range rows {
			if _, ok := seen[acc.ID]; ok {
				continue
			}
			seen[acc.ID] = struct{}{}
			results = append(results, acc)
		}
	}

	return results, nil
}

func accountSearchExactMatch(ctx context.Context, q accountSearchQuerier, resolver accountSearchResolver, cfg *config.Config, accountID int64, query string, resolve, following bool) (db.Account, bool) {
	username, domain, err := activitypub.ParseAcct(query)
	if err != nil || username == "" || domain == "" {
		return db.Account{}, false
	}

	var acc db.Account
	if resolve && resolver != nil {
		acc, err = resolver.ResolveAccount(ctx, query)
		if err != nil {
			return db.Account{}, false
		}
	} else {
		if accountSearchDomainIsLocal(domain, cfg) {
			acc, err = q.GetAccountByUsername(ctx, username)
		} else {
			acc, err = q.GetAccountByUsernameAndDomain(ctx, db.GetAccountByUsernameAndDomainParams{
				Username: username,
				Domain:   domain,
			})
		}
		if err != nil {
			return db.Account{}, false
		}
	}

	if following && acc.ID != accountID {
		if _, err := q.GetFollow(ctx, db.GetFollowParams{
			AccountID:       accountID,
			TargetAccountID: acc.ID,
		}); err != nil {
			return db.Account{}, false
		}
	}

	return acc, true
}

func accountSearchTerms(query string, cfg *config.Config) string {
	username, domain, err := activitypub.ParseAcct(query)
	if err != nil {
		return query
	}
	if accountSearchDomainIsLocal(domain, cfg) {
		return username
	}
	return query
}

func accountSearchDomainIsLocal(domain string, cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	localDomain := strings.TrimSpace(cfg.Instance.LocalDomain)
	if localDomain == "" {
		return false
	}
	return strings.EqualFold(domain, localDomain)
}

func parseBoolParam(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "t", "yes", "on":
		return true
	default:
		return false
	}
}

// Followers handles GET /api/v1/accounts/:id/followers
func (h *AccountsHandler) Followers(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	limit := parseAccountLimit(web.QueryParam(r, "limit"), 40, 80)

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Account data unavailable")
		return
	}

	followers, err := h.queries.GetFollowers(r.Context(), db.GetFollowersParams{
		TargetAccountID: id,
		Limit:           int32(limit),
		Offset:          0,
	})
	if err != nil {
		web.JSON(w, http.StatusOK, []Account{})
		return
	}

	accountsMap := serializeAccounts(r.Context(), h.queries, h.cfg, followers, true)
	accounts := make([]Account, 0, len(followers))
	for _, acc := range followers {
		if apiAcc, ok := accountsMap[acc.ID]; ok {
			accounts = append(accounts, apiAcc)
		}
	}
	web.JSON(w, http.StatusOK, accounts)
}

// Following handles GET /api/v1/accounts/:id/following
func (h *AccountsHandler) Following(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	limit := parseAccountLimit(web.QueryParam(r, "limit"), 40, 80)

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Account data unavailable")
		return
	}

	following, err := h.queries.GetFollowing(r.Context(), db.GetFollowingParams{
		AccountID: id,
		Limit:     int32(limit),
		Offset:    0,
	})
	if err != nil {
		web.JSON(w, http.StatusOK, []Account{})
		return
	}

	accountsMap := serializeAccounts(r.Context(), h.queries, h.cfg, following, true)
	accounts := make([]Account, 0, len(following))
	for _, acc := range following {
		if apiAcc, ok := accountsMap[acc.ID]; ok {
			accounts = append(accounts, apiAcc)
		}
	}
	web.JSON(w, http.StatusOK, accounts)
}

// FeaturedTags handles GET /api/v1/accounts/:id/featured_tags
func (h *AccountsHandler) FeaturedTags(w http.ResponseWriter, r *http.Request) {
	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		tags, err := h.queries.ListFeaturedTags(r.Context(), targetID)
		if err != nil {
			web.JSON(w, http.StatusOK, []FeaturedTag{})
			return
		}

		resp := make([]FeaturedTag, len(tags))
		for i, tag := range tags {
			resp[i] = featuredTagFromDB(tag)
		}
		web.JSON(w, http.StatusOK, resp)
		return
	}
	web.JSONError(w, http.StatusInternalServerError, "Account data unavailable")
}

// Endorsements handles GET /api/v1/accounts/:id/endorsements
func (h *AccountsHandler) Endorsements(w http.ResponseWriter, r *http.Request) {
	scopes := middleware.GetScopes(r.Context())
	if scopes != "" && !oauth.HasScope(scopes, "read:accounts") {
		web.JSONError(w, http.StatusForbidden, "This action is not allowed")
		return
	}

	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	limit := parseAccountLimit(web.QueryParam(r, "limit"), 40, 80)
	maxID, maxValid := parseOptionalID(web.QueryParam(r, "max_id"))
	sinceID, sinceValid := parseOptionalID(web.QueryParam(r, "since_id"))
	if !maxValid || !sinceValid {
		web.JSON(w, http.StatusOK, []Account{})
		return
	}
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Account data unavailable")
		return
	}

	if _, err := h.queries.GetAccountByID(r.Context(), targetID); err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	pins, err := h.queries.ListAccountPins(r.Context(), targetID)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to list endorsements")
		return
	}

	filtered := make([]db.ListAccountPinsRow, 0, len(pins))
	for _, pin := range pins {
		if maxID > 0 && pin.ID >= maxID {
			continue
		}
		if sinceID > 0 && pin.ID <= sinceID {
			continue
		}
		filtered = append(filtered, pin)
		if len(filtered) >= limit {
			break
		}
	}
	if len(filtered) == 0 {
		web.JSON(w, http.StatusOK, []Account{})
		return
	}

	accountIDs := make([]int64, 0, len(filtered))
	for _, pin := range filtered {
		accountIDs = append(accountIDs, pin.TargetAccountID)
	}

	dbAccounts, err := h.queries.GetAccountsByIDs(r.Context(), accountIDs)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to list endorsements")
		return
	}

	accountsByID := make(map[int64]db.Account, len(dbAccounts))
	for _, acc := range dbAccounts {
		accountsByID[acc.ID] = acc
	}
	serialized := serializeAccounts(r.Context(), h.queries, h.cfg, dbAccounts, true)

	resp := make([]Account, 0, len(filtered))
	for _, pin := range filtered {
		acc, ok := accountsByID[pin.TargetAccountID]
		if !ok {
			continue
		}
		if apiAcc, ok := serialized[acc.ID]; ok {
			resp = append(resp, apiAcc)
		}
	}

	web.JSON(w, http.StatusOK, resp)
}

// IdentityProofs handles GET /api/v1/accounts/:id/identity_proofs
func (h *AccountsHandler) IdentityProofs(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Account data unavailable")
		return
	}
	if _, err := h.queries.GetAccountByID(r.Context(), targetID); err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	web.JSON(w, http.StatusOK, []any{})
}

// Relationship represents the relationship between accounts
type Relationship struct {
	ID                  string `json:"id"`
	Following           bool   `json:"following"`
	ShowingReblogs      bool   `json:"showing_reblogs"`
	Notifying           bool   `json:"notifying"`
	Languages           []any  `json:"languages"`
	FollowedBy          bool   `json:"followed_by"`
	Blocking            bool   `json:"blocking"`
	BlockedBy           bool   `json:"blocked_by"`
	Muting              bool   `json:"muting"`
	MutingNotifications bool   `json:"muting_notifications"`
	Requested           bool   `json:"requested"`
	RequestedBy         bool   `json:"requested_by"`
	DomainBlocking      bool   `json:"domain_blocking"`
	Endorsed            bool   `json:"endorsed"`
	Note                string `json:"note"`
}

// Follow handles POST /api/v1/accounts/:id/follow
func (h *AccountsHandler) Follow(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if accountID == targetID {
		web.JSONError(w, http.StatusUnprocessableEntity, "Cannot follow yourself")
		return
	}

	if h.useDB() {
		ctx := r.Context()

		// Check if target account exists
		targetAcc, err := h.queries.GetAccountByID(ctx, targetID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		var followID int64
		// Create follow (or follow request if locked)
		if targetAcc.Locked {
			followReq, err := h.queries.CreateFollowRequest(ctx, db.CreateFollowRequestParams{
				AccountID:       accountID,
				TargetAccountID: targetID,
			})
			if err != nil {
				if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
					// Already requested; keep followID empty.
				} else {
					web.JSONError(w, http.StatusInternalServerError, "Failed to create follow request")
					return
				}
			} else {
				followID = followReq.ID
			}
		} else {
			follow, err := h.queries.CreateFollow(ctx, db.CreateFollowParams{
				AccountID:       accountID,
				TargetAccountID: targetID,
			})
			if err != nil {
				if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
					// Already following; keep followID empty.
				} else {
					web.JSONError(w, http.StatusInternalServerError, "Failed to create follow")
					return
				}
			} else {
				followID = follow.ID
			}
		}

		if followID > 0 && h.outgoing != nil {
			if err := h.outgoing.DeliverFollow(ctx, accountID, targetID, followID); err != nil {
				fmt.Printf("Failed to deliver Follow activity: %v\n", err)
			}
		}

		// Return relationship
		rel := h.getRelationship(ctx, accountID, targetID)
		web.JSON(w, http.StatusOK, rel)
		return
	}

	web.JSON(w, http.StatusOK, Relationship{
		ID:        web.Param(r, "id"),
		Following: true,
		Languages: []any{},
	})
}

// Unfollow handles POST /api/v1/accounts/:id/unfollow
func (h *AccountsHandler) Unfollow(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		ctx := r.Context()
		var followID int64
		if h.outgoing != nil {
			follow, err := h.queries.GetFollow(ctx, db.GetFollowParams{
				AccountID:       accountID,
				TargetAccountID: targetID,
			})
			if err == nil {
				followID = follow.ID
			} else {
				req, err := h.queries.GetFollowRequest(ctx, db.GetFollowRequestParams{
					AccountID:       accountID,
					TargetAccountID: targetID,
				})
				if err == nil {
					followID = req.ID
				}
			}
		}

		_ = h.queries.DeleteFollow(ctx, db.DeleteFollowParams{
			AccountID:       accountID,
			TargetAccountID: targetID,
		})
		_ = h.queries.DeleteFollowRequest(ctx, db.DeleteFollowRequestParams{
			AccountID:       accountID,
			TargetAccountID: targetID,
		})

		rel := h.getRelationship(ctx, accountID, targetID)
		web.JSON(w, http.StatusOK, rel)

		if followID > 0 && h.outgoing != nil {
			if err := h.outgoing.DeliverUndoFollow(ctx, accountID, targetID, followID); err != nil {
				fmt.Printf("Failed to deliver Undo Follow activity: %v\n", err)
			}
		}
		return
	}

	web.JSON(w, http.StatusOK, Relationship{
		ID:        web.Param(r, "id"),
		Following: false,
		Languages: []any{},
	})
}

// Relationships handles GET /api/v1/accounts/relationships
func (h *AccountsHandler) Relationships(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ids := r.URL.Query()["id[]"]
	if len(ids) == 0 {
		web.JSON(w, http.StatusOK, []Relationship{})
		return
	}

	ctx := r.Context()
	relationships := make([]Relationship, 0, len(ids))

	for _, idStr := range ids {
		targetID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		if h.useDB() {
			rel := h.getRelationship(ctx, accountID, targetID)
			relationships = append(relationships, rel)
		} else {
			relationships = append(relationships, Relationship{
				ID:        idStr,
				Languages: []any{},
			})
		}
	}

	web.JSON(w, http.StatusOK, relationships)
}

func (h *AccountsHandler) getRelationship(ctx context.Context, accountID, targetID int64) Relationship {
	rel := Relationship{
		ID:        strconv.FormatInt(targetID, 10),
		Languages: []any{},
	}

	if !h.useDB() {
		return rel
	}

	// Check following
	_, err := h.queries.GetFollow(ctx, db.GetFollowParams{
		AccountID:       accountID,
		TargetAccountID: targetID,
	})
	rel.Following = err == nil
	rel.ShowingReblogs = rel.Following
	rel.Notifying = false

	// Check followed by
	_, err = h.queries.GetFollow(ctx, db.GetFollowParams{
		AccountID:       targetID,
		TargetAccountID: accountID,
	})
	rel.FollowedBy = err == nil

	// Check blocking
	_, err = h.queries.GetBlock(ctx, db.GetBlockParams{
		AccountID:       accountID,
		TargetAccountID: targetID,
	})
	rel.Blocking = err == nil

	// Check blocked by
	_, err = h.queries.GetBlock(ctx, db.GetBlockParams{
		AccountID:       targetID,
		TargetAccountID: accountID,
	})
	rel.BlockedBy = err == nil

	// Check muting
	_, err = h.queries.GetMute(ctx, db.GetMuteParams{
		AccountID:       accountID,
		TargetAccountID: targetID,
	})
	rel.Muting = err == nil

	// Get private note about target account
	note, err := h.queries.GetAccountNote(ctx, db.GetAccountNoteParams{
		AccountID:       accountID,
		TargetAccountID: targetID,
	})
	if err == nil {
		rel.Note = note.Comment
	}

	return rel
}

func parseAccountLimit(s string, defaultVal, maxVal int) int {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return defaultVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func parseAccountIDs(r *http.Request) []int64 {
	query := r.URL.Query()
	rawIDs := append([]string{}, query["id"]...)
	rawIDs = append(rawIDs, query["id[]"]...)
	rawIDs = uniqueStrings(rawIDs)

	ids := make([]int64, 0, len(rawIDs))
	for _, value := range rawIDs {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			id = 0
		}
		ids = append(ids, id)
	}
	return ids
}

func parseOptionalID(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 1 {
		return 0, false
	}
	return v, true
}

// Statuses handles GET /api/v1/accounts/:id/statuses
func (h *AccountsHandler) Statuses(w http.ResponseWriter, r *http.Request) {
	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	limit := parseAccountLimit(web.QueryParam(r, "limit"), 20, 40)

	if h.useDB() {
		statuses, err := h.queries.GetAccountStatuses(r.Context(), db.GetAccountStatusesParams{
			AccountID: targetID,
			Limit:     int32(limit),
			Offset:    0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []any{})
			return
		}
		web.JSON(w, http.StatusOK, SerializeStatuses(r.Context(), h.queries, h.cfg, statuses))
		return
	}

	web.JSON(w, http.StatusOK, []any{})
}

// Block handles POST /api/v1/accounts/:id/block
func (h *AccountsHandler) Block(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		_, _ = h.queries.CreateBlock(r.Context(), db.CreateBlockParams{
			AccountID:       accountID,
			TargetAccountID: targetID,
		})
	}

	rel := h.getRelationship(r.Context(), accountID, targetID)
	web.JSON(w, http.StatusOK, rel)
}

// Unblock handles POST /api/v1/accounts/:id/unblock
func (h *AccountsHandler) Unblock(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		_ = h.queries.DeleteBlock(r.Context(), db.DeleteBlockParams{
			AccountID:       accountID,
			TargetAccountID: targetID,
		})
	}

	rel := h.getRelationship(r.Context(), accountID, targetID)
	web.JSON(w, http.StatusOK, rel)
}

// Mute handles POST /api/v1/accounts/:id/mute
func (h *AccountsHandler) Mute(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		_, _ = h.queries.CreateMute(r.Context(), db.CreateMuteParams{
			AccountID:       accountID,
			TargetAccountID: targetID,
		})
	}

	rel := h.getRelationship(r.Context(), accountID, targetID)
	web.JSON(w, http.StatusOK, rel)
}

// Unmute handles POST /api/v1/accounts/:id/unmute
func (h *AccountsHandler) Unmute(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.useDB() {
		_ = h.queries.DeleteMute(r.Context(), db.DeleteMuteParams{
			AccountID:       accountID,
			TargetAccountID: targetID,
		})
	}

	rel := h.getRelationship(r.Context(), accountID, targetID)
	web.JSON(w, http.StatusOK, rel)
}

// SetNote handles POST /api/v1/accounts/:id/note
func (h *AccountsHandler) SetNote(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req struct {
		Comment string `json:"comment" form:"comment"`
	}
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	ctx := r.Context()
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Account data unavailable")
		return
	}

	if h.useDB() {
		// Verify target account exists
		_, err := h.queries.GetAccountByID(ctx, targetID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}

		if len([]rune(req.Comment)) > accountNoteCommentMax {
			description := fmt.Sprintf("character limit of %d exceeded", accountNoteCommentMax)
			web.JSON(w, http.StatusUnprocessableEntity, mastodonValidationErrorResponse{
				Error: fmt.Sprintf("Validation failed: Comment %s", description),
				Details: map[string][]mastodonValidationDetail{
					"comment": {
						{
							Error:       "ERR_TOO_LONG",
							Description: description,
						},
					},
				},
			})
			return
		}

		if req.Comment == "" {
			// Empty comment means delete the note
			_ = h.queries.DeleteAccountNote(ctx, db.DeleteAccountNoteParams{
				AccountID:       accountID,
				TargetAccountID: targetID,
			})
		} else {
			// Upsert the note
			_, _ = h.queries.UpsertAccountNote(ctx, db.UpsertAccountNoteParams{
				AccountID:       accountID,
				TargetAccountID: targetID,
				Comment:         req.Comment,
			})
		}

		rel := h.getRelationship(ctx, accountID, targetID)
		web.JSON(w, http.StatusOK, rel)
		return
	}
}

// FamiliarFollowers handles GET /api/v1/accounts/familiar_followers
func (h *AccountsHandler) FamiliarFollowers(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Account data unavailable")
		return
	}

	requestedIDs := parseFamiliarFollowerIDs(r)
	if len(requestedIDs) == 0 {
		web.JSON(w, http.StatusOK, []familiarFollowersResult{})
		return
	}

	ctx := r.Context()
	requestedAccounts, err := h.queries.GetAccountsByIDs(ctx, requestedIDs)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load accounts")
		return
	}

	requestedByID := make(map[int64]db.Account, len(requestedAccounts))
	for _, acc := range requestedAccounts {
		if acc.SuspendedAt.Valid {
			continue
		}
		requestedByID[acc.ID] = acc
	}

	results := make([]familiarFollowersResult, 0, len(requestedIDs))
	for _, requestedID := range requestedIDs {
		requestedAcc, ok := requestedByID[requestedID]
		if !ok {
			// Match Mastodon behavior: nonexistent and suspended requested accounts are omitted.
			continue
		}

		result := familiarFollowersResult{
			ID:       strconv.FormatInt(requestedID, 10),
			Accounts: []Account{},
		}
		if requestedAcc.HideCollections.Valid && requestedAcc.HideCollections.Bool {
			results = append(results, result)
			continue
		}

		familiar, err := h.queries.GetFamiliarFollowers(ctx, db.GetFamiliarFollowersParams{
			AccountID:       accountID,
			TargetAccountID: requestedID,
		})
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to load familiar followers")
			return
		}

		serialized := serializeAccounts(ctx, h.queries, h.cfg, familiar, true)
		result.Accounts = make([]Account, 0, len(familiar))
		for _, follower := range familiar {
			if acc, ok := serialized[follower.ID]; ok {
				result.Accounts = append(result.Accounts, acc)
			}
		}

		results = append(results, result)
	}

	web.JSON(w, http.StatusOK, results)
}

func parseFamiliarFollowerIDs(r *http.Request) []int64 {
	rawPairs := strings.Split(r.URL.RawQuery, "&")
	ids := make([]int64, 0, len(rawPairs))
	seen := make(map[int64]struct{}, len(rawPairs))
	for _, pair := range rawPairs {
		if pair == "" {
			continue
		}

		rawKey, rawID, _ := strings.Cut(pair, "=")
		key, err := url.QueryUnescape(rawKey)
		if err != nil {
			continue
		}
		switch key {
		case "id", "id[]", "account_id", "account_id[]":
		default:
			continue
		}

		rawID, err = url.QueryUnescape(rawID)
		if err != nil {
			continue
		}

		id, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil {
			id = 0
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// RemoveFromFollowers handles POST /api/v1/accounts/:id/remove_from_followers
func (h *AccountsHandler) RemoveFromFollowers(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	targetID, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Account data unavailable")
		return
	}

	if h.useDB() {
		// Remove the target's follow on us (they follow us, we remove them)
		_ = h.queries.DeleteFollow(ctx, db.DeleteFollowParams{
			AccountID:       targetID,
			TargetAccountID: accountID,
		})

		rel := h.getRelationship(ctx, accountID, targetID)
		web.JSON(w, http.StatusOK, rel)
		return
	}
}

// Lookup handles GET /api/v1/accounts/lookup
func (h *AccountsHandler) Lookup(w http.ResponseWriter, r *http.Request) {
	acct := strings.TrimSpace(web.QueryParam(r, "acct"))
	if acct == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()
	if !h.useDB() {
		web.JSONError(w, http.StatusInternalServerError, "Account data unavailable")
		return
	}

	if h.useDB() {
		dbAcc, err := h.lookupAccount(ctx, acct)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, h.dbAccountWithStats(ctx, dbAcc))
		return
	}
}

func (h *AccountsHandler) lookupAccount(ctx context.Context, acct string) (db.Account, error) {
	if isRemoteLookup(acct) {
		if h.resolver != nil {
			return h.resolver.ResolveAccount(ctx, acct)
		}
		if strings.HasPrefix(acct, "http://") || strings.HasPrefix(acct, "https://") {
			return h.queries.GetAccountByURI(ctx, acct)
		}
		username, domain, err := activitypub.ParseAcct(acct)
		if err != nil {
			return db.Account{}, err
		}
		return h.queries.GetAccountByUsernameAndDomain(ctx, db.GetAccountByUsernameAndDomainParams{
			Username: username,
			Domain:   domain,
		})
	}
	return h.queries.GetAccountByUsername(ctx, acct)
}

func isRemoteLookup(acct string) bool {
	return strings.Contains(acct, "@") ||
		strings.HasPrefix(acct, "http://") ||
		strings.HasPrefix(acct, "https://")
}

// RegisterRoutes registers the accounts routes
func (h *AccountsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/accounts/verify_credentials", h.VerifyCredentials)
	mux.HandleFunc("PATCH /api/v1/accounts/update_credentials", h.UpdateCredentials)
	mux.HandleFunc("GET /api/v1/accounts/search", h.Search)
	mux.HandleFunc("GET /api/v1/accounts/relationships", h.Relationships)
	mux.HandleFunc("GET /api/v1/accounts/familiar_followers", h.FamiliarFollowers)
	mux.HandleFunc("POST /api/v1/accounts", h.Register)
	mux.HandleFunc("GET /api/v1/accounts", h.Index)
	mux.HandleFunc("GET /api/v1/accounts/{id}", h.Get)
	mux.HandleFunc("GET /api/v1/accounts/{id}/statuses", h.Statuses)
	mux.HandleFunc("GET /api/v1/accounts/{id}/followers", h.Followers)
	mux.HandleFunc("GET /api/v1/accounts/{id}/following", h.Following)
	mux.HandleFunc("GET /api/v1/accounts/{id}/featured_tags", h.FeaturedTags)
	mux.HandleFunc("GET /api/v1/accounts/{id}/endorsements", h.Endorsements)
	mux.HandleFunc("GET /api/v1/accounts/{id}/identity_proofs", h.IdentityProofs)
	mux.HandleFunc("POST /api/v1/accounts/{id}/follow", h.Follow)
	mux.HandleFunc("POST /api/v1/accounts/{id}/unfollow", h.Unfollow)
	mux.HandleFunc("POST /api/v1/accounts/{id}/block", h.Block)
	mux.HandleFunc("POST /api/v1/accounts/{id}/unblock", h.Unblock)
	mux.HandleFunc("POST /api/v1/accounts/{id}/mute", h.Mute)
	mux.HandleFunc("POST /api/v1/accounts/{id}/unmute", h.Unmute)
	mux.HandleFunc("POST /api/v1/accounts/{id}/note", h.SetNote)
	mux.HandleFunc("POST /api/v1/accounts/{id}/remove_from_followers", h.RemoveFromFollowers)
	mux.HandleFunc("GET /api/v1/accounts/lookup", h.Lookup)
}
