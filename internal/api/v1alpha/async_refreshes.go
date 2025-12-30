// Package v1alpha provides the Mastodon-compatible REST API v1 alpha handlers.
package v1alpha

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/web"
	"github.com/deicod/gotodon/pkg/oauth"
)

const asyncRefreshTokenVersion = "v1"

var errInvalidAsyncRefreshToken = errors.New("invalid async refresh token")

type asyncRefreshSigner struct {
	secret []byte
}

func newAsyncRefreshSigner(secret string) *asyncRefreshSigner {
	trimmed := strings.TrimSpace(secret)
	if trimmed == "" {
		return nil
	}
	return &asyncRefreshSigner{secret: []byte(trimmed)}
}

func (s *asyncRefreshSigner) Sign(key string) (string, error) {
	if s == nil {
		return "", errors.New("async refresh signer not configured")
	}
	if key == "" {
		return "", errors.New("async refresh key is required")
	}
	encodedKey := base64.RawURLEncoding.EncodeToString([]byte(key))
	signature := base64.RawURLEncoding.EncodeToString(s.signature(encodedKey))
	return asyncRefreshTokenVersion + "." + encodedKey + "." + signature, nil
}

func (s *asyncRefreshSigner) Verify(token string) (string, error) {
	if s == nil {
		return "", errInvalidAsyncRefreshToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errInvalidAsyncRefreshToken
	}
	if parts[0] != asyncRefreshTokenVersion {
		return "", errInvalidAsyncRefreshToken
	}
	encodedKey := parts[1]
	decodedKey, err := base64.RawURLEncoding.DecodeString(encodedKey)
	if err != nil {
		return "", errInvalidAsyncRefreshToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errInvalidAsyncRefreshToken
	}
	expected := s.signature(encodedKey)
	if !hmac.Equal(expected, signature) {
		return "", errInvalidAsyncRefreshToken
	}
	return string(decodedKey), nil
}

func (s *asyncRefreshSigner) signature(encodedKey string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(asyncRefreshTokenVersion))
	mac.Write([]byte("."))
	mac.Write([]byte(encodedKey))
	return mac.Sum(nil)
}

// AsyncRefreshData represents the async refresh state loaded from storage.
type AsyncRefreshData struct {
	Status      string
	ResultCount *int64
}

// AsyncRefreshStore loads async refresh state by redis key.
type AsyncRefreshStore interface {
	Get(ctx context.Context, key string) (AsyncRefreshData, bool, error)
}

// RedisAsyncRefreshStore loads async refresh state from Redis.
type RedisAsyncRefreshStore struct {
	client *redis.Client
}

// NewRedisAsyncRefreshStore creates a Redis-backed store.
func NewRedisAsyncRefreshStore(client *redis.Client) *RedisAsyncRefreshStore {
	return &RedisAsyncRefreshStore{client: client}
}

// Get loads async refresh state from Redis.
func (s *RedisAsyncRefreshStore) Get(ctx context.Context, key string) (AsyncRefreshData, bool, error) {
	if s == nil || s.client == nil {
		return AsyncRefreshData{}, false, nil
	}
	exists, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		return AsyncRefreshData{}, false, err
	}
	if exists == 0 {
		return AsyncRefreshData{}, false, nil
	}
	values, err := s.client.HGetAll(ctx, key).Result()
	if err != nil {
		return AsyncRefreshData{}, false, err
	}
	data := AsyncRefreshData{Status: values["status"]}
	if raw, ok := values["result_count"]; ok && strings.TrimSpace(raw) != "" {
		parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return AsyncRefreshData{}, false, err
		}
		data.ResultCount = &parsed
	}
	return data, true, nil
}

// AsyncRefreshHandler handles v1 alpha async refresh endpoints.
type AsyncRefreshHandler struct {
	store  AsyncRefreshStore
	signer *asyncRefreshSigner
}

// NewAsyncRefreshHandler creates a new async refresh handler.
func NewAsyncRefreshHandler(cfg *config.Config, store AsyncRefreshStore) *AsyncRefreshHandler {
	secret := ""
	if cfg != nil {
		secret = cfg.Secrets.SecretKeyBase
	}
	return &AsyncRefreshHandler{
		store:  store,
		signer: newAsyncRefreshSigner(secret),
	}
}

// AsyncRefresh represents the API response payload.
type AsyncRefresh struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	ResultCount *int64 `json:"result_count"`
}

// AsyncRefreshResponse wraps the async refresh payload.
type AsyncRefreshResponse struct {
	AsyncRefresh AsyncRefresh `json:"async_refresh"`
}

// Show handles GET /api/v1_alpha/async_refreshes/{id}.
func (h *AsyncRefreshHandler) Show(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.GetAccountID(r.Context()); !ok {
		web.JSONError(w, http.StatusUnauthorized, "The access token is invalid")
		return
	}

	scopes := middleware.GetScopes(r.Context())
	if scopes != "" && !oauth.HasScope(scopes, "read") {
		web.JSONError(w, http.StatusForbidden, "This action is outside the authorized scopes")
		return
	}

	if h.signer == nil {
		web.JSONError(w, http.StatusInternalServerError, "Async refresh unavailable")
		return
	}

	token := web.Param(r, "id")
	if token == "" {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	key, err := h.signer.Verify(token)
	if err != nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.store == nil {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	data, found, err := h.store.Get(r.Context(), key)
	if err != nil {
		web.JSONError(w, http.StatusInternalServerError, "Failed to load async refresh")
		return
	}
	if !found {
		web.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	web.JSON(w, http.StatusOK, AsyncRefreshResponse{
		AsyncRefresh: AsyncRefresh{
			ID:          token,
			Status:      data.Status,
			ResultCount: data.ResultCount,
		},
	})
}

// RegisterRoutes registers v1 alpha async refresh routes.
func (h *AsyncRefreshHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1_alpha/async_refreshes/{id}", h.Show)
}
