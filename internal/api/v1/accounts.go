// Package v1 provides the Mastodon-compatible REST API v1 handlers.
package v1

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/auth"
	"github.com/deicod/gotodon/internal/db"
)

// AccountsHandler handles account-related endpoints
type AccountsHandler struct {
	queries *db.Queries
}

// NewAccountsHandler creates a new accounts handler
func NewAccountsHandler(queries *db.Queries) *AccountsHandler {
	return &AccountsHandler{queries: queries}
}

func (h *AccountsHandler) useDB() bool {
	return h.queries != nil
}

// Account represents a Mastodon account in API responses
type Account struct {
	ID             string    `json:"id"`
	Username       string    `json:"username"`
	Acct           string    `json:"acct"`
	DisplayName    string    `json:"display_name"`
	Locked         bool      `json:"locked"`
	Bot            bool      `json:"bot"`
	Discoverable   *bool     `json:"discoverable"`
	Indexable      bool      `json:"indexable"`
	Group          bool      `json:"group"`
	CreatedAt      time.Time `json:"created_at"`
	Note           string    `json:"note"`
	URL            string    `json:"url"`
	URI            string    `json:"uri"`
	Avatar         string    `json:"avatar"`
	AvatarStatic   string    `json:"avatar_static"`
	Header         string    `json:"header"`
	HeaderStatic   string    `json:"header_static"`
	FollowersCount int64     `json:"followers_count"`
	FollowingCount int64     `json:"following_count"`
	StatusesCount  int64     `json:"statuses_count"`
	LastStatusAt   *string   `json:"last_status_at"`
	Emojis         []any     `json:"emojis"`
	Fields         []Field   `json:"fields"`
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
	createdAt := time.Now().Add(-24 * time.Hour)
	if dbAcc.CreatedAt.Valid {
		createdAt = dbAcc.CreatedAt.Time
	}

	acct := dbAcc.Username
	if dbAcc.Domain.Valid && dbAcc.Domain.String != "" {
		acct = dbAcc.Username + "@" + dbAcc.Domain.String
	}

	url := dbAcc.Uri
	if dbAcc.Url.Valid {
		url = dbAcc.Url.String
	}

	discoverable := dbAcc.Discoverable.Valid && dbAcc.Discoverable.Bool
	bot := dbAcc.ActorType.Valid && dbAcc.ActorType.String == "Service"

	avatar := "https://example.com/avatars/original/missing.png"
	header := "https://example.com/headers/original/missing.png"
	if dbAcc.AvatarFileName.Valid && dbAcc.AvatarFileName.String != "" {
		avatar = dbAcc.AvatarFileName.String
	}
	if dbAcc.HeaderFileName.Valid && dbAcc.HeaderFileName.String != "" {
		header = dbAcc.HeaderFileName.String
	}

	return Account{
		ID:             strconv.FormatInt(dbAcc.ID, 10),
		Username:       dbAcc.Username,
		Acct:           acct,
		DisplayName:    dbAcc.DisplayName,
		Locked:         dbAcc.Locked,
		Bot:            bot,
		Discoverable:   &discoverable,
		Indexable:      dbAcc.Indexable,
		Group:          false,
		CreatedAt:      createdAt,
		Note:           dbAcc.Note,
		URL:            url,
		URI:            dbAcc.Uri,
		Avatar:         avatar,
		AvatarStatic:   avatar,
		Header:         header,
		HeaderStatic:   header,
		FollowersCount: 0,
		FollowingCount: 0,
		StatusesCount:  0,
		LastStatusAt:   nil,
		Emojis:         []any{},
		Fields:         []Field{},
	}
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
func (h *AccountsHandler) VerifyCredentials(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	ctx := context.Background()

	if h.useDB() {
		dbAcc, err := h.queries.GetAccountByID(ctx, accountID)
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{
				"error": "Record not found",
			})
		}
		acc := h.dbAccountWithStats(ctx, dbAcc)
		return c.JSON(http.StatusOK, CredentialAccount{
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
	}

	// Fallback mock
	discoverable := true
	return c.JSON(http.StatusOK, CredentialAccount{
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
			Emojis:         []any{},
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
func (h *AccountsHandler) UpdateCredentials(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{
			"error": "The access token is invalid",
		})
	}

	var req UpdateCredentialsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	ctx := context.Background()

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
			return echo.NewHTTPError(http.StatusInternalServerError, map[string]string{
				"error": "Failed to update account",
			})
		}
		acc := h.dbAccountWithStats(ctx, dbAcc)
		return c.JSON(http.StatusOK, CredentialAccount{
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
	}

	return h.VerifyCredentials(c)
}

// Get handles GET /api/v1/accounts/:id
func (h *AccountsHandler) Get(c echo.Context) error {
	idStr := c.Param("id")
	if idStr == "" {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{
			"error": "Record not found",
		})
	}

	ctx := context.Background()

	if h.useDB() {
		dbAcc, err := h.queries.GetAccountByID(ctx, id)
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{
				"error": "Record not found",
			})
		}
		return c.JSON(http.StatusOK, h.dbAccountWithStats(ctx, dbAcc))
	}

	// Fallback mock
	discoverable := true
	return c.JSON(http.StatusOK, Account{
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
		Emojis:         []any{},
		Fields:         []Field{},
	})
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
func (h *AccountsHandler) Register(c echo.Context) error {
	var req RegisterAccountRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, map[string]string{
			"error": "Invalid request body",
		})
	}

	if req.Username == "" {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Username can't be blank",
		})
	}
	if req.Email == "" || !auth.ValidateEmail(req.Email) {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Email is invalid",
		})
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Password " + err.Error(),
		})
	}
	if !req.Agreement {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
			"error": "Validation failed: Agreement must be accepted",
		})
	}

	ctx := context.Background()

	if h.useDB() {
		dbAcc, err := h.queries.CreateAccount(ctx, db.CreateAccountParams{
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
			return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{
				"error": "Validation failed: Username has already been taken",
			})
		}

		// Create account stats
		_, _ = h.queries.CreateAccountStats(ctx, dbAcc.ID)

		return c.JSON(http.StatusOK, map[string]interface{}{
			"access_token": "placeholder_token",
			"token_type":   "Bearer",
			"scope":        "read write follow push",
			"created_at":   dbAcc.CreatedAt.Time.Unix(),
		})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"access_token": "placeholder_token",
		"token_type":   "Bearer",
		"scope":        "read write follow push",
		"created_at":   time.Now().Unix(),
	})
}

// Search handles GET /api/v1/accounts/search
func (h *AccountsHandler) Search(c echo.Context) error {
	query := c.QueryParam("q")
	if query == "" {
		return c.JSON(http.StatusOK, []Account{})
	}

	limit := 40
	if l := c.QueryParam("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 80 {
			limit = parsed
		}
	}

	ctx := context.Background()

	if h.useDB() {
		dbAccounts, err := h.queries.SearchAccounts(ctx, db.SearchAccountsParams{
			Query:  query,
			Limit:  int32(limit),
			Offset: 0,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []Account{})
		}

		accounts := make([]Account, len(dbAccounts))
		for i, dbAcc := range dbAccounts {
			accounts[i] = h.dbAccountWithStats(ctx, dbAcc)
		}
		return c.JSON(http.StatusOK, accounts)
	}

	return c.JSON(http.StatusOK, []Account{})
}

// Followers handles GET /api/v1/accounts/:id/followers
func (h *AccountsHandler) Followers(c echo.Context) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	limit := parseAccountLimit(c.QueryParam("limit"), 40, 80)

	if h.useDB() {
		followers, err := h.queries.GetFollowers(c.Request().Context(), db.GetFollowersParams{
			TargetAccountID: id,
			Limit:           int32(limit),
			Offset:          0,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []Account{})
		}

		accounts := make([]Account, len(followers))
		for i, acc := range followers {
			accounts[i] = h.dbAccountToAPI(acc)
		}
		return c.JSON(http.StatusOK, accounts)
	}

	return c.JSON(http.StatusOK, []Account{})
}

// Following handles GET /api/v1/accounts/:id/following
func (h *AccountsHandler) Following(c echo.Context) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	limit := parseAccountLimit(c.QueryParam("limit"), 40, 80)

	if h.useDB() {
		following, err := h.queries.GetFollowing(c.Request().Context(), db.GetFollowingParams{
			AccountID: id,
			Limit:     int32(limit),
			Offset:    0,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []Account{})
		}

		accounts := make([]Account, len(following))
		for i, acc := range following {
			accounts[i] = h.dbAccountToAPI(acc)
		}
		return c.JSON(http.StatusOK, accounts)
	}

	return c.JSON(http.StatusOK, []Account{})
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
func (h *AccountsHandler) Follow(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	targetID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	if accountID == targetID {
		return echo.NewHTTPError(http.StatusUnprocessableEntity, map[string]string{"error": "Cannot follow yourself"})
	}

	if h.useDB() {
		ctx := c.Request().Context()

		// Check if target account exists
		targetAcc, err := h.queries.GetAccountByID(ctx, targetID)
		if err != nil {
			return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
		}

		// Create follow (or follow request if locked)
		if targetAcc.Locked {
			_, _ = h.queries.CreateFollowRequest(ctx, db.CreateFollowRequestParams{
				AccountID:       accountID,
				TargetAccountID: targetID,
			})
		} else {
			_, _ = h.queries.CreateFollow(ctx, db.CreateFollowParams{
				AccountID:       accountID,
				TargetAccountID: targetID,
			})
		}

		// Return relationship
		rel := h.getRelationship(ctx, accountID, targetID)
		return c.JSON(http.StatusOK, rel)
	}

	return c.JSON(http.StatusOK, Relationship{
		ID:        c.Param("id"),
		Following: true,
		Languages: []any{},
	})
}

// Unfollow handles POST /api/v1/accounts/:id/unfollow
func (h *AccountsHandler) Unfollow(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	targetID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	if h.useDB() {
		ctx := c.Request().Context()
		_ = h.queries.DeleteFollow(ctx, db.DeleteFollowParams{
			AccountID:       accountID,
			TargetAccountID: targetID,
		})

		rel := h.getRelationship(ctx, accountID, targetID)
		return c.JSON(http.StatusOK, rel)
	}

	return c.JSON(http.StatusOK, Relationship{
		ID:        c.Param("id"),
		Following: false,
		Languages: []any{},
	})
}

// Relationships handles GET /api/v1/accounts/relationships
func (h *AccountsHandler) Relationships(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	ids := c.QueryParams()["id[]"]
	if len(ids) == 0 {
		return c.JSON(http.StatusOK, []Relationship{})
	}

	ctx := c.Request().Context()
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

	return c.JSON(http.StatusOK, relationships)
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

// Statuses handles GET /api/v1/accounts/:id/statuses
func (h *AccountsHandler) Statuses(c echo.Context) error {
	targetID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	limit := parseAccountLimit(c.QueryParam("limit"), 20, 40)

	if h.useDB() {
		statuses, err := h.queries.GetAccountStatuses(c.Request().Context(), db.GetAccountStatusesParams{
			AccountID: targetID,
			Limit:     int32(limit),
			Offset:    0,
		})
		if err != nil {
			return c.JSON(http.StatusOK, []any{})
		}
		return c.JSON(http.StatusOK, statuses)
	}

	return c.JSON(http.StatusOK, []any{})
}

// Block handles POST /api/v1/accounts/:id/block
func (h *AccountsHandler) Block(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	targetID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	if h.useDB() {
		_, _ = h.queries.CreateBlock(c.Request().Context(), db.CreateBlockParams{
			AccountID:       accountID,
			TargetAccountID: targetID,
		})
	}

	rel := h.getRelationship(c.Request().Context(), accountID, targetID)
	return c.JSON(http.StatusOK, rel)
}

// Unblock handles POST /api/v1/accounts/:id/unblock
func (h *AccountsHandler) Unblock(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	targetID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	if h.useDB() {
		_ = h.queries.DeleteBlock(c.Request().Context(), db.DeleteBlockParams{
			AccountID:       accountID,
			TargetAccountID: targetID,
		})
	}

	rel := h.getRelationship(c.Request().Context(), accountID, targetID)
	return c.JSON(http.StatusOK, rel)
}

// Mute handles POST /api/v1/accounts/:id/mute
func (h *AccountsHandler) Mute(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	targetID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	if h.useDB() {
		_, _ = h.queries.CreateMute(c.Request().Context(), db.CreateMuteParams{
			AccountID:       accountID,
			TargetAccountID: targetID,
		})
	}

	rel := h.getRelationship(c.Request().Context(), accountID, targetID)
	return c.JSON(http.StatusOK, rel)
}

// Unmute handles POST /api/v1/accounts/:id/unmute
func (h *AccountsHandler) Unmute(c echo.Context) error {
	accountID, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	targetID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, map[string]string{"error": "Record not found"})
	}

	if h.useDB() {
		_ = h.queries.DeleteMute(c.Request().Context(), db.DeleteMuteParams{
			AccountID:       accountID,
			TargetAccountID: targetID,
		})
	}

	rel := h.getRelationship(c.Request().Context(), accountID, targetID)
	return c.JSON(http.StatusOK, rel)
}

// FamiliarFollowers handles GET /api/v1/accounts/familiar_followers
func (h *AccountsHandler) FamiliarFollowers(c echo.Context) error {
	_, ok := middleware.GetAccountID(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, map[string]string{"error": "The access token is invalid"})
	}

	// Return empty familiar followers for now
	return c.JSON(http.StatusOK, []map[string]any{})
}

// RegisterRoutes registers the accounts routes
func (h *AccountsHandler) RegisterRoutes(g *echo.Group) {
	accounts := g.Group("/accounts")
	accounts.GET("/verify_credentials", h.VerifyCredentials)
	accounts.PATCH("/update_credentials", h.UpdateCredentials)
	accounts.GET("/search", h.Search)
	accounts.GET("/relationships", h.Relationships)
	accounts.GET("/familiar_followers", h.FamiliarFollowers)
	accounts.POST("", h.Register)
	accounts.GET("/:id", h.Get)
	accounts.GET("/:id/statuses", h.Statuses)
	accounts.GET("/:id/followers", h.Followers)
	accounts.GET("/:id/following", h.Following)
	accounts.POST("/:id/follow", h.Follow)
	accounts.POST("/:id/unfollow", h.Unfollow)
	accounts.POST("/:id/block", h.Block)
	accounts.POST("/:id/unblock", h.Unblock)
	accounts.POST("/:id/mute", h.Mute)
	accounts.POST("/:id/unmute", h.Unmute)
}
