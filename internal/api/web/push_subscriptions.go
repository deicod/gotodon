package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/push"
	webutil "github.com/deicod/gotodon/internal/web"
)

const (
	defaultWebPushPolicy = "all"
	webPushTokenTTL      = 48 * time.Hour
)

var webPushPolicies = map[string]struct{}{
	"all":      {},
	"none":     {},
	"followed": {},
	"follower": {},
}

var webPushNotificationTypes = []string{
	"mention",
	"status",
	"reblog",
	"follow",
	"follow_request",
	"favourite",
	"poll",
	"update",
	"severed_relationships",
	"moderation_warning",
	"annual_report",
	"admin.sign_up",
	"admin.report",
	"quote",
	"quoted_update",
}

type webPushStore interface {
	CreatePushSubscription(ctx context.Context, arg db.CreatePushSubscriptionParams) (db.WebPushSubscription, error)
	DeletePushSubscription(ctx context.Context, accessTokenID pgtype.Int8) error
	DeletePushSubscriptionByID(ctx context.Context, id int64) error
	GetPushSubscriptionByID(ctx context.Context, id int64) (db.WebPushSubscription, error)
	UpdatePushSubscriptionByID(ctx context.Context, arg db.UpdatePushSubscriptionByIDParams) (db.WebPushSubscription, error)
}

// PushSubscriptionsHandler handles /api/web/push_subscriptions.
type PushSubscriptionsHandler struct {
	store  webPushStore
	cfg    *config.Config
	nowFn  func() time.Time
	secret string
}

// NewPushSubscriptionsHandler creates a new handler for web push subscriptions.
func NewPushSubscriptionsHandler(store webPushStore, cfg *config.Config) *PushSubscriptionsHandler {
	secret := ""
	if cfg != nil {
		secret = cfg.Secrets.SecretKeyBase
	}
	return &PushSubscriptionsHandler{
		store:  store,
		cfg:    cfg,
		nowFn:  time.Now,
		secret: secret,
	}
}

// Create handles POST /api/web/push_subscriptions.
func (h *PushSubscriptionsHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := requireWebUser(w, r)
	if !ok {
		return
	}
	if h == nil || h.store == nil {
		webutil.JSONError(w, http.StatusInternalServerError, "Push subscriptions unavailable")
		return
	}
	accessTokenID, ok := tokenIDFromContext(w, r)
	if !ok {
		return
	}

	var req struct {
		Subscription struct {
			Endpoint string         `json:"endpoint"`
			Keys     map[string]any `json:"keys"`
			Standard any            `json:"standard"`
		} `json:"subscription"`
		Data map[string]any `json:"data"`
	}
	if err := webutil.Bind(r, &req); err != nil {
		webutil.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	endpoint := strings.TrimSpace(req.Subscription.Endpoint)
	if endpoint == "" {
		webutil.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Endpoint is required")
		return
	}
	if push.ValidateEndpoint(endpoint) != nil {
		webutil.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Endpoint is invalid")
		return
	}
	keys, err := parseSubscriptionKeys(req.Subscription.Keys)
	if err != nil {
		webutil.JSONError(w, http.StatusUnprocessableEntity, "Validation failed: Keys are required")
		return
	}

	data := defaultWebPushData(false)
	if req.Data != nil {
		if data, err = mergeWebPushData(data, req.Data); err != nil {
			webutil.JSONError(w, http.StatusBadRequest, "Invalid request")
			return
		}
	}
	dataBytes, err := json.Marshal(map[string]any{
		"policy": data.Policy,
		"alerts": data.Alerts,
	})
	if err != nil {
		webutil.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	standard := parseStandard(req.Subscription.Standard)

	_ = h.store.DeletePushSubscription(r.Context(), pgtype.Int8{Int64: accessTokenID, Valid: true})
	sub, err := h.store.CreatePushSubscription(r.Context(), db.CreatePushSubscriptionParams{
		Endpoint:      endpoint,
		KeyP256dh:     pgtype.Text{String: keys.P256DH, Valid: true},
		KeyAuth:       pgtype.Text{String: keys.Auth, Valid: true},
		Data:          dataBytes,
		UserID:        pgtype.Int8{Int64: userID, Valid: true},
		AccessTokenID: pgtype.Int8{Int64: accessTokenID, Valid: true},
		Standard:      standard,
	})
	if err != nil {
		webutil.JSONError(w, http.StatusInternalServerError, "Failed to create subscription")
		return
	}

	webutil.JSON(w, http.StatusOK, h.serializeSubscription(sub))
}

// Update handles PUT/PATCH /api/web/push_subscriptions/:id.
func (h *PushSubscriptionsHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := requireWebUser(w, r)
	if !ok {
		return
	}
	if h == nil || h.store == nil {
		webutil.JSONError(w, http.StatusInternalServerError, "Push subscriptions unavailable")
		return
	}
	subscriptionID, err := strconv.ParseInt(webutil.Param(r, "id"), 10, 64)
	if err != nil {
		webutil.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	var req struct {
		Data map[string]any `json:"data"`
	}
	if err := webutil.Bind(r, &req); err != nil || req.Data == nil {
		webutil.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	current, err := h.store.GetPushSubscriptionByID(r.Context(), subscriptionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			webutil.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		webutil.JSONError(w, http.StatusInternalServerError, "Failed to update subscription")
		return
	}
	if !current.UserID.Valid || current.UserID.Int64 != userID {
		webutil.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	data := webPushDataFromBytes(current.Data)
	if data.Policy == "" {
		data.Policy = defaultWebPushPolicy
	}
	if data.Alerts == nil {
		data.Alerts = defaultWebPushAlerts(false)
	}
	if data, err = mergeWebPushData(data, req.Data); err != nil {
		webutil.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	dataBytes, err := json.Marshal(map[string]any{
		"policy": data.Policy,
		"alerts": data.Alerts,
	})
	if err != nil {
		webutil.JSONError(w, http.StatusBadRequest, "Invalid request")
		return
	}

	updated, err := h.store.UpdatePushSubscriptionByID(r.Context(), db.UpdatePushSubscriptionByIDParams{
		ID:   subscriptionID,
		Data: dataBytes,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			webutil.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
		webutil.JSONError(w, http.StatusInternalServerError, "Failed to update subscription")
		return
	}

	webutil.JSON(w, http.StatusOK, h.serializeSubscription(updated))
}

// Delete handles DELETE /api/web/push_subscriptions/:id.
func (h *PushSubscriptionsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil {
		webutil.JSON(w, http.StatusOK, map[string]any{})
		return
	}
	if h.secret == "" {
		webutil.JSON(w, http.StatusOK, map[string]any{})
		return
	}
	value := webutil.Param(r, "id")
	if value == "" {
		webutil.JSON(w, http.StatusOK, map[string]any{})
		return
	}

	id, ok := parseWebPushToken(h.secret, value, h.nowFn(), webPushTokenTTL)
	if ok {
		_ = h.store.DeletePushSubscriptionByID(r.Context(), id)
	}

	webutil.JSON(w, http.StatusOK, map[string]any{})
}

// RegisterRoutes registers web push subscription routes.
func (h *PushSubscriptionsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/web/push_subscriptions", h.Create)
	mux.HandleFunc("PUT /api/web/push_subscriptions/{id}", h.Update)
	mux.HandleFunc("PATCH /api/web/push_subscriptions/{id}", h.Update)
	mux.HandleFunc("DELETE /api/web/push_subscriptions/{id}", h.Delete)
}

type subscriptionKeys struct {
	P256DH string
	Auth   string
}

type webPushData struct {
	Policy string
	Alerts map[string]bool
}

type webPushSubscriptionResponse struct {
	ID        string          `json:"id"`
	Endpoint  string          `json:"endpoint"`
	Standard  bool            `json:"standard"`
	Alerts    map[string]bool `json:"alerts"`
	ServerKey string          `json:"server_key"`
	Policy    string          `json:"policy"`
}

func (h *PushSubscriptionsHandler) serializeSubscription(sub db.WebPushSubscription) webPushSubscriptionResponse {
	data := webPushDataFromBytes(sub.Data)
	policy := strings.TrimSpace(data.Policy)
	if policy == "" {
		policy = defaultWebPushPolicy
	}
	alerts := data.Alerts
	if alerts == nil {
		alerts = map[string]bool{}
	}
	serverKey := ""
	if h != nil && h.cfg != nil {
		serverKey = h.cfg.Secrets.VAPIDPublic
	}
	id := strconv.FormatInt(sub.ID, 10)
	if h != nil && h.secret != "" && sub.ID > 0 {
		nowFn := h.nowFn
		if nowFn == nil {
			nowFn = time.Now
		}
		if token, err := buildWebPushToken(h.secret, sub.ID, nowFn()); err == nil {
			id = token
		}
	}
	return webPushSubscriptionResponse{
		ID:        id,
		Endpoint:  sub.Endpoint,
		Standard:  sub.Standard,
		Alerts:    alerts,
		ServerKey: serverKey,
		Policy:    policy,
	}
}

func defaultWebPushData(alertsEnabled bool) webPushData {
	return webPushData{
		Policy: defaultWebPushPolicy,
		Alerts: defaultWebPushAlerts(alertsEnabled),
	}
}

func defaultWebPushAlerts(enabled bool) map[string]bool {
	alerts := make(map[string]bool, len(webPushNotificationTypes))
	for _, typ := range webPushNotificationTypes {
		alerts[typ] = enabled
	}
	return alerts
}

func mergeWebPushData(base webPushData, update map[string]any) (webPushData, error) {
	if base.Alerts == nil {
		base.Alerts = defaultWebPushAlerts(false)
	}
	if update == nil {
		return base, nil
	}
	if rawPolicy, ok := update["policy"]; ok {
		policy, ok := rawPolicy.(string)
		if !ok {
			return base, errors.New("invalid policy")
		}
		policy = strings.TrimSpace(policy)
		if _, ok := webPushPolicies[policy]; !ok {
			return base, errors.New("invalid policy")
		}
		base.Policy = policy
	}
	if rawAlerts, ok := update["alerts"]; ok {
		alertMap, ok := rawAlerts.(map[string]any)
		if !ok {
			return base, errors.New("invalid alerts")
		}
		allowed := notificationTypeSet()
		for key, val := range alertMap {
			if _, ok := allowed[key]; !ok {
				continue
			}
			parsed, err := parseBool(val)
			if err != nil {
				return base, err
			}
			base.Alerts[key] = parsed
		}
	}
	return base, nil
}

func webPushDataFromBytes(data []byte) webPushData {
	if len(data) == 0 {
		return webPushData{}
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return webPushData{}
	}
	policy, _ := payload["policy"].(string)
	alerts := map[string]bool{}
	if rawAlerts, ok := payload["alerts"].(map[string]any); ok {
		allowed := notificationTypeSet()
		for key, val := range rawAlerts {
			if _, ok := allowed[key]; !ok {
				continue
			}
			parsed, err := parseBool(val)
			if err != nil {
				continue
			}
			alerts[key] = parsed
		}
	}
	return webPushData{Policy: policy, Alerts: alerts}
}

func parseSubscriptionKeys(input map[string]any) (subscriptionKeys, error) {
	if input == nil {
		return subscriptionKeys{}, errors.New("missing keys")
	}
	get := func(key string) string {
		if value, ok := input[key]; ok {
			if s, ok := value.(string); ok {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	keys := subscriptionKeys{
		P256DH: get("p256dh"),
		Auth:   get("auth"),
	}
	if keys.P256DH == "" || keys.Auth == "" {
		return subscriptionKeys{}, errors.New("missing keys")
	}
	return keys, nil
}

func parseStandard(value any) bool {
	if value == nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		parsed, err := parseBool(v)
		if err != nil {
			return false
		}
		return parsed
	case float64:
		return v != 0
	default:
		return false
	}
}

func parseBool(value any) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		return parseBoolString(v)
	case float64:
		return v != 0, nil
	default:
		return false, errors.New("invalid boolean value")
	}
}

func parseBoolString(value string) (bool, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "true", "1", "t", "yes", "y", "on":
		return true, nil
	case "false", "0", "f", "no", "n", "off":
		return false, nil
	default:
		return false, errors.New("invalid boolean value")
	}
}

func notificationTypeSet() map[string]struct{} {
	allowed := make(map[string]struct{}, len(webPushNotificationTypes))
	for _, typ := range webPushNotificationTypes {
		allowed[typ] = struct{}{}
	}
	return allowed
}

func parseWebPushToken(secret, token string, now time.Time, ttl time.Duration) (int64, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return 0, false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, false
	}
	signatureBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payloadBytes)
	expected := mac.Sum(nil)
	if !hmac.Equal(signatureBytes, expected) {
		return 0, false
	}
	payload := string(payloadBytes)
	sections := strings.Split(payload, ":")
	if len(sections) != 2 {
		return 0, false
	}
	id, err := strconv.ParseInt(sections[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	issued, err := strconv.ParseInt(sections[1], 10, 64)
	if err != nil {
		return 0, false
	}
	issuedAt := time.Unix(issued, 0)
	if issuedAt.After(now) {
		return 0, false
	}
	if now.Sub(issuedAt) > ttl {
		return 0, false
	}
	return id, true
}

func buildWebPushToken(secret string, id int64, now time.Time) (string, error) {
	if secret == "" || id <= 0 {
		return "", errors.New("invalid token input")
	}
	payload := strconv.FormatInt(id, 10) + ":" + strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	signature := mac.Sum(nil)
	encodedPayload := base64.RawURLEncoding.EncodeToString([]byte(payload))
	encodedSig := base64.RawURLEncoding.EncodeToString(signature)
	return encodedPayload + "." + encodedSig, nil
}
