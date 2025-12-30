package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/activitypub"
	"github.com/deicod/gotodon/pkg/oauth"
)

// AccountsHandler handles account-related endpoints
type AccountsHandler struct {
	queries  db.Querier
	pool     *pgxpool.Pool
	cfg      *config.Config
	outgoing federation.OutgoingDeliverer
	resolver *federation.RemoteActorResolver
}

const defaultAccountsLimit = 40

// NewAccountsHandler creates a new accounts handler
func NewAccountsHandler(queries db.Querier, pool *pgxpool.Pool, cfg *config.Config, outgoing federation.OutgoingDeliverer) *AccountsHandler {
	var resolver *federation.RemoteActorResolver
	if queries != nil && cfg != nil {
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

func (h *AccountsHandler) useDB() bool {
	return h.queries != nil
}

// Account represents a Mastodon account in API responses
type Account struct {
	ID             string        `json:"id"`
	Username       string        `json:"username"`
	Acct           string        `json:"acct"`
	DisplayName    string        `json:"display_name"`
	Locked         bool          `json:"locked"`
	Bot            bool          `json:"bot"`
	Discoverable   *bool         `json:"discoverable"`
	Indexable      bool          `json:"indexable"`
	Group          bool          `json:"group"`
	CreatedAt      time.Time     `json:"created_at"`
	Note           string        `json:"note"`
	URL            string        `json:"url"`
	URI            string        `json:"uri"`
	Avatar         string        `json:"avatar"`
	AvatarStatic   string        `json:"avatar_static"`
	Header         string        `json:"header"`
	HeaderStatic   string        `json:"header_static"`
	FollowersCount int64         `json:"followers_count"`
	FollowingCount int64         `json:"following_count"`
	StatusesCount  int64         `json:"statuses_count"`
	LastStatusAt   *string       `json:"last_status_at"`
	Emojis         []CustomEmoji `json:"emojis"`
	Fields         []Field       `json:"fields"`
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
	Privacy             string  `json:"privacy"`
	Sensitive           bool    `json:"sensitive"`
	Language            string  `json:"language"`
	Note                string  `json:"note"`
	Fields              []Field `json:"fields"`
	FollowRequestsCount int     `json:"follow_requests_count"`
}

// AccountRole represents a user role
type AccountRole struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Permissions string `json:"permissions"`
	Highlighted bool   `json:"highlighted"`
}

func (h *AccountsHandler) dbAccountToAPI(dbAcc db.Account) Account {
	return SerializeAccount(h.cfg, dbAcc)
}

func (h *AccountsHandler) dbAccountWithStats(ctx context.Context, dbAcc db.Account) Account {
	acc := h.dbAccountToAPI(dbAcc)
	if h.useDB() {
		stats, err := h.queries.GetAccountStats(ctx, dbAcc.ID)
		if err == nil {
			acc.FollowersCount = stats.FollowersCount
			acc.FollowingCount = stats.FollowingCount
			acc.StatusesCount = stats.StatusesCount
			if stats.LastStatusAt.Valid {
				lastStatus := stats.LastStatusAt.Time.Format("2006-01-02")
				acc.LastStatusAt = &lastStatus
			}
		}
	}
	return acc
}

// VerifyCredentials handles GET /api/v1/accounts/verify_credentials
func (h *AccountsHandler) VerifyCredentials(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		dbAcc, err := h.queries.GetAccountByID(ctx, accountID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		acc := h.dbAccountWithStats(ctx, dbAcc)
		web.JSON(w, http.StatusOK, CredentialAccount{
			Account: acc,
			Source: AccountSource{
				Privacy:             "public",
				Sensitive:           false,
				Language:            "en",
				Note:                dbAcc.Note,
				Fields:              []Field{},
				FollowRequestsCount: 0,
			},
		})
		return
	}

	// Fallback mock
	discoverable := true
	web.JSON(w, http.StatusOK, CredentialAccount{
		Account: Account{
			ID:             strconv.FormatInt(accountID, 10),
			Username:       "user",
			Acct:           "user",
			DisplayName:    "Test User",
			Locked:         false,
			Bot:            false,
			Discoverable:   &discoverable,
			Indexable:      true,
			Group:          false,
			CreatedAt:      time.Now().Add(-24 * time.Hour),
			Note:           "",
			URL:            "https://example.com/@user",
			URI:            "https://example.com/users/user",
			Avatar:         "https://example.com/avatars/original/missing.png",
			AvatarStatic:   "https://example.com/avatars/original/missing.png",
			Header:         "https://example.com/headers/original/missing.png",
			HeaderStatic:   "https://example.com/headers/original/missing.png",
			FollowersCount: 0,
			FollowingCount: 0,
			StatusesCount:  0,
			LastStatusAt:   nil,
			Emojis:         []CustomEmoji{},
			Fields:         []Field{},
		},
		Source: AccountSource{
			Privacy:             "public",
			Sensitive:           false,
			Language:            "en",
			Note:                "",
			Fields:              []Field{},
			FollowRequestsCount: 0,
		},
	})
}

// UpdateCredentialsRequest is the request for PATCH /api/v1/accounts/update_credentials
type UpdateCredentialsRequest struct {
	DisplayName     *string `json:"display_name" form:"display_name"`
	Note            *string `json:"note" form:"note"`
	Avatar          *string `json:"avatar" form:"avatar"`
	Header          *string `json:"header" form:"header"`
	Locked          *bool   `json:"locked" form:"locked"`
	Bot             *bool   `json:"bot" form:"bot"`
	Discoverable    *bool   `json:"discoverable" form:"discoverable"`
	HideCollections *bool   `json:"hide_collections" form:"hide_collections"`
	Indexable       *bool   `json:"indexable" form:"indexable"`
	SourcePrivacy   *string `json:"source[privacy]" form:"source[privacy]"`
	SourceSensitive *bool   `json:"source[sensitive]" form:"source[sensitive]"`
	SourceLanguage  *string `json:"source[language]" form:"source[language]"`
}

// UpdateCredentials handles PATCH /api/v1/accounts/update_credentials
func (h *AccountsHandler) UpdateCredentials(w http.ResponseWriter, r *http.Request) {
	accountID, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	var req UpdateCredentialsRequest
	if err := web.Bind(r, &req); err != nil {
		web.JSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	ctx := r.Context()

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

		dbAcc, err := h.queries.UpdateAccount(ctx, params)
		if err != nil {
			web.JSONError(w, http.StatusInternalServerError, "Failed to update account")
			return
		}

		// Update user settings (source params) - mapped to preferences table
		if req.SourcePrivacy != nil || req.SourceSensitive != nil || req.SourceLanguage != nil {
			// Fetch existing preferences to defaults if needed
			currentPrefs, err := h.queries.GetPreferences(ctx, accountID)
			if err != nil {
				if !errors.Is(err, pgx.ErrNoRows) {
					web.JSONError(w, http.StatusInternalServerError, "Failed to fetch preferences")
					return
				}
				// Likely not found, use defaults
				currentPrefs = db.Preference{
					PostingDefaultVisibility: pgtype.Text{String: "public", Valid: true},
					PostingDefaultSensitive:  pgtype.Bool{Bool: false, Valid: true},
					PostingDefaultLanguage:   pgtype.Text{String: "en", Valid: true},
					ReadingExpandMedia:       pgtype.Text{String: "default", Valid: true},
					ReadingExpandSpoilers:    pgtype.Bool{Bool: false, Valid: true},
				}
			}

			upsertParams := db.UpsertPreferencesParams{
				AccountID:                accountID,
				PostingDefaultVisibility: currentPrefs.PostingDefaultVisibility,
				PostingDefaultSensitive:  currentPrefs.PostingDefaultSensitive,
				PostingDefaultLanguage:   currentPrefs.PostingDefaultLanguage,
				ReadingExpandMedia:       currentPrefs.ReadingExpandMedia,
				ReadingExpandSpoilers:    currentPrefs.ReadingExpandSpoilers,
			}

			if req.SourcePrivacy != nil {
				upsertParams.PostingDefaultVisibility = pgtype.Text{String: *req.SourcePrivacy, Valid: true}
			}
			if req.SourceSensitive != nil {
				upsertParams.PostingDefaultSensitive = pgtype.Bool{Bool: *req.SourceSensitive, Valid: true}
			}
			if req.SourceLanguage != nil {
				upsertParams.PostingDefaultLanguage = pgtype.Text{String: *req.SourceLanguage, Valid: true}
			}

			_, err = h.queries.UpsertPreferences(ctx, upsertParams)
			if err != nil {
				web.JSONError(w, http.StatusInternalServerError, "Failed to update preferences")
				return
			}
			// Ignore error for now, non-critical if preference update fails but main account updated
		}

		acc := h.dbAccountWithStats(ctx, dbAcc)
		web.JSON(w, http.StatusOK, CredentialAccount{
			Account: acc,
			Source: AccountSource{
				Privacy:             "public",
				Sensitive:           false,
				Language:            "en",
				Note:                dbAcc.Note,
				Fields:              []Field{},
				FollowRequestsCount: 0,
			},
		})
		return
	}

	h.VerifyCredentials(w, r)
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

	ctx := r.Context()

	if h.useDB() {
		dbAcc, err := h.queries.GetAccountByID(ctx, id)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, h.dbAccountWithStats(ctx, dbAcc))
		return
	}

	// Fallback mock
	discoverable := true
	web.JSON(w, http.StatusOK, Account{
		ID:             idStr,
		Username:       "user",
		Acct:           "user",
		DisplayName:    "Test User",
		Locked:         false,
		Bot:            false,
		Discoverable:   &discoverable,
		Indexable:      true,
		Group:          false,
		CreatedAt:      time.Now().Add(-24 * time.Hour),
		Note:           "",
		URL:            "https://example.com/@user",
		URI:            "https://example.com/users/user",
		Avatar:         "https://example.com/avatars/original/missing.png",
		AvatarStatic:   "https://example.com/avatars/original/missing.png",
		Header:         "https://example.com/headers/original/missing.png",
		HeaderStatic:   "https://example.com/headers/original/missing.png",
		FollowersCount: 0,
		FollowingCount: 0,
		StatusesCount:  0,
		LastStatusAt:   nil,
		Emojis:         []CustomEmoji{},
		Fields:         []Field{},
	})
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

	if !h.useDB() || len(ids) == 0 {
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

	accountsByID := make(map[int64]Account, len(dbAccounts))
	for _, dbAcc := range dbAccounts {
		accountsByID[dbAcc.ID] = h.dbAccountWithStats(ctx, dbAcc)
	}

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

		// Confirm the user immediately for now
		// Confirm the user immediately for now
		now := time.Now()
		dbUser, err := qtx.CreateUser(ctx, db.CreateUserParams{
			Email:             req.Email,
			EncryptedPassword: hashedPassword,
			AccountID:         dbAcc.ID,
			ConfirmedAt:       pgtype.Timestamptz{Time: now, Valid: true},
			InviteID:          pgtype.Int8{Valid: false},
			RoleID:            pgtype.Int8{Valid: false},
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

	web.JSON(w, http.StatusOK, map[string]interface{}{
		"access_token": "placeholder_token",
		"token_type":   "Bearer",
		"scope":        "read write follow push",
		"created_at":   time.Now().Unix(),
	})
}

// Search handles GET /api/v1/accounts/search
func (h *AccountsHandler) Search(w http.ResponseWriter, r *http.Request) {
	query := web.QueryParam(r, "q")
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

	ctx := r.Context()

	if h.useDB() {
		dbAccounts, err := h.queries.SearchAccounts(ctx, db.SearchAccountsParams{
			Query:  query,
			Limit:  int32(limit),
			Offset: 0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Account{})
			return
		}

		accounts := make([]Account, len(dbAccounts))
		for i, dbAcc := range dbAccounts {
			accounts[i] = h.dbAccountWithStats(ctx, dbAcc)
		}
		web.JSON(w, http.StatusOK, accounts)
		return
	}

	web.JSON(w, http.StatusOK, []Account{})
}

// Followers handles GET /api/v1/accounts/:id/followers
func (h *AccountsHandler) Followers(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	limit := parseAccountLimit(web.QueryParam(r, "limit"), 40, 80)

	if h.useDB() {
		followers, err := h.queries.GetFollowers(r.Context(), db.GetFollowersParams{
			TargetAccountID: id,
			Limit:           int32(limit),
			Offset:          0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Account{})
			return
		}

		accounts := make([]Account, len(followers))
		for i, acc := range followers {
			accounts[i] = h.dbAccountToAPI(acc)
		}
		web.JSON(w, http.StatusOK, accounts)
		return
	}

	web.JSON(w, http.StatusOK, []Account{})
}

// Following handles GET /api/v1/accounts/:id/following
func (h *AccountsHandler) Following(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(web.Param(r, "id"), 10, 64)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	limit := parseAccountLimit(web.QueryParam(r, "limit"), 40, 80)

	if h.useDB() {
		following, err := h.queries.GetFollowing(r.Context(), db.GetFollowingParams{
			AccountID: id,
			Limit:     int32(limit),
			Offset:    0,
		})
		if err != nil {
			web.JSON(w, http.StatusOK, []Account{})
			return
		}

		accounts := make([]Account, len(following))
		for i, acc := range following {
			accounts[i] = h.dbAccountToAPI(acc)
		}
		web.JSON(w, http.StatusOK, accounts)
		return
	}

	web.JSON(w, http.StatusOK, []Account{})
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

	web.JSON(w, http.StatusOK, []FeaturedTag{})
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
		web.JSON(w, http.StatusOK, []Account{})
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

	resp := make([]Account, 0, len(filtered))
	for _, pin := range filtered {
		acc, ok := accountsByID[pin.TargetAccountID]
		if !ok {
			continue
		}
		resp = append(resp, SerializeAccount(h.cfg, acc))
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

	if h.useDB() {
		if _, err := h.queries.GetAccountByID(r.Context(), targetID); err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
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

	if h.useDB() {
		// Verify target account exists
		_, err := h.queries.GetAccountByID(ctx, targetID)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
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

	// Mock fallback
	web.JSON(w, http.StatusOK, Relationship{
		ID:        web.Param(r, "id"),
		Note:      req.Comment,
		Languages: []any{},
	})
}

// FamiliarFollowers handles GET /api/v1/accounts/familiar_followers
func (h *AccountsHandler) FamiliarFollowers(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetAccountID(r.Context())
	if !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	// Return empty familiar followers for now
	web.JSON(w, http.StatusOK, []map[string]any{})
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

	// Mock fallback
	web.JSON(w, http.StatusOK, Relationship{
		ID:         web.Param(r, "id"),
		FollowedBy: false,
		Languages:  []any{},
	})
}

// Lookup handles GET /api/v1/accounts/lookup
func (h *AccountsHandler) Lookup(w http.ResponseWriter, r *http.Request) {
	acct := strings.TrimSpace(web.QueryParam(r, "acct"))
	if acct == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	ctx := r.Context()

	if h.useDB() {
		dbAcc, err := h.lookupAccount(ctx, acct)
		if err != nil {
			web.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		web.JSON(w, http.StatusOK, h.dbAccountWithStats(ctx, dbAcc))
		return
	}

	// Mock fallback
	discoverable := true
	web.JSON(w, http.StatusOK, Account{
		ID:           "1",
		Username:     acct,
		Acct:         acct,
		DisplayName:  acct,
		Discoverable: &discoverable,
		Emojis:       []CustomEmoji{},
		Fields:       []Field{},
	})
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
