// Package oauth provides OAuth 2.0 endpoints for Mastodon-compatible authentication.
package oauth

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/oauth"
)

type tokenValidatorQueries interface {
	GetOAuthAccessTokenByTokenIncludingRevoked(ctx context.Context, token string) (db.OauthAccessToken, error)
	UpdateOAuthTokenLastUsed(ctx context.Context, arg db.UpdateOAuthTokenLastUsedParams) error
	GetUserByID(ctx context.Context, id int64) (db.User, error)
}

// TokenValidator validates OAuth access tokens and records usage.
type TokenValidator struct {
	queries tokenValidatorQueries
	now     func() time.Time
}

// NewTokenValidator creates a validator backed by database queries.
func NewTokenValidator(queries tokenValidatorQueries) *TokenValidator {
	return &TokenValidator{
		queries: queries,
		now:     time.Now,
	}
}

// ValidateToken verifies a token and returns identity context details.
func (v *TokenValidator) ValidateToken(ctx context.Context, token string) (*middleware.TokenInfo, error) {
	if v.queries == nil {
		return nil, oauth.ErrInvalidToken
	}

	dbToken, err := v.queries.GetOAuthAccessTokenByTokenIncludingRevoked(ctx, token)
	if err != nil {
		return nil, oauth.ErrInvalidToken
	}
	if dbToken.RevokedAt.Valid {
		return nil, oauth.ErrRevokedToken
	}

	if dbToken.ExpiresIn.Valid && dbToken.CreatedAt.Valid {
		expiresIn := int(dbToken.ExpiresIn.Int32)
		if oauth.IsTokenExpired(dbToken.CreatedAt.Time, &expiresIn) {
			return nil, oauth.ErrExpiredToken
		}
	}

	info := &middleware.TokenInfo{
		TokenID: dbToken.ID,
		Scopes:  "",
	}
	if dbToken.ApplicationID.Valid {
		info.AppID = dbToken.ApplicationID.Int64
	}
	if dbToken.Scopes.Valid {
		info.Scopes = strings.TrimSpace(dbToken.Scopes.String)
	}

	if dbToken.ResourceOwnerID.Valid {
		user, err := v.queries.GetUserByID(ctx, dbToken.ResourceOwnerID.Int64)
		if err != nil || user.Disabled || !user.Approved || !user.ConfirmedAt.Valid {
			return nil, oauth.ErrInvalidToken
		}
		userID := user.ID
		accountID := user.AccountID
		info.UserID = &userID
		info.AccountID = &accountID
	}

	v.touchToken(ctx, dbToken)
	return info, nil
}

func (v *TokenValidator) touchToken(ctx context.Context, token db.OauthAccessToken) {
	if v.queries == nil {
		return
	}
	if !v.shouldTrackUsage(token.LastUsedAt) {
		return
	}

	ip := ""
	if ctxIP, ok := ctx.Value(middleware.ContextKeyClientIP).(string); ok {
		ip = strings.TrimSpace(ctxIP)
	}

	var ipAddr *netip.Addr
	if ip != "" {
		if addr, err := netip.ParseAddr(ip); err == nil {
			ipAddr = &addr
		}
	}

	_ = v.queries.UpdateOAuthTokenLastUsed(ctx, db.UpdateOAuthTokenLastUsedParams{
		ID:         token.ID,
		LastUsedIp: ipAddr,
	})
}

func (v *TokenValidator) shouldTrackUsage(lastUsedAt pgtype.Timestamptz) bool {
	if !lastUsedAt.Valid {
		return true
	}
	now := time.Now
	if v.now != nil {
		now = v.now
	}
	return now().After(lastUsedAt.Time.Add(24 * time.Hour))
}
