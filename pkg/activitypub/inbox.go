// Package activitypub provides inbox and outbox handlers.
package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// InboxHandler handles incoming ActivityPub activities
type InboxHandler struct {
	verifier     *HTTPVerifier
	actorFetcher ActorFetcher
	handlers     map[string]ActivityHandler
}

// ActivityHandler processes a specific activity type
type ActivityHandler func(ctx context.Context, activity *Activity) error

// ActorFetcher fetches actor data (for signature verification)
type ActorFetcher interface {
	FetchActor(ctx context.Context, actorURI string) (*Actor, error)
}

// NewInboxHandler creates a new inbox handler
func NewInboxHandler(actorFetcher ActorFetcher) *InboxHandler {
	return &InboxHandler{
		verifier:     NewHTTPVerifier(),
		actorFetcher: actorFetcher,
		handlers:     make(map[string]ActivityHandler),
	}
}

// RegisterHandler registers a handler for an activity type
func (h *InboxHandler) RegisterHandler(activityType string, handler ActivityHandler) {
	h.handlers[activityType] = handler
}

// HandleInbox processes an incoming activity. body must be the exact bytes
// read from req; the request signature must cover its digest and be made
// with a key owned by the activity's actor. HandleInbox does not detect
// replays: a captured request verifies again until its signature expires
// (RequestSignature.ValidUntil), so callers processing state-changing
// activities must remember processed requests at least that long.
func (h *InboxHandler) HandleInbox(ctx context.Context, req *http.Request, body []byte) error {
	// Check signature coverage, freshness and body digest before fetching keys
	sig, err := h.verifier.ParseRequest(req, body)
	if err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	// Parse activity
	var activity Activity
	if err := json.Unmarshal(body, &activity); err != nil {
		return fmt.Errorf("failed to parse activity: %w", err)
	}

	// Get actor URI from activity
	actorURI := h.extractActorURI(&activity)
	if actorURI == "" {
		return fmt.Errorf("missing actor in activity")
	}

	// Fetch actor for public key (for signature verification)
	actor, err := h.actorFetcher.FetchActor(ctx, actorURI)
	if err != nil {
		return fmt.Errorf("failed to fetch actor %s: %w", actorURI, err)
	}

	// Verify HTTP signature
	if err := VerifyKeyOwner(actor, actorURI, sig.KeyID); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	if err := sig.Verify(actor.PublicKey.PublicKeyPem); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	// Dispatch to handler
	handler, exists := h.handlers[activity.Type]
	if !exists {
		// Unknown activity type - log but don't error
		return nil
	}

	return handler(ctx, &activity)
}

func (h *InboxHandler) extractActorURI(activity *Activity) string {
	switch actor := activity.Actor.(type) {
	case string:
		return actor
	case map[string]any:
		if id, ok := actor["id"].(string); ok {
			return id
		}
	}
	return ""
}

// OutboxHandler handles outgoing ActivityPub activities
type OutboxHandler struct {
	client *SignedClient
}

// NewOutboxHandler creates a new outbox handler
func NewOutboxHandler(keyID, privateKeyPEM string) (*OutboxHandler, error) {
	client, err := NewSignedClient(keyID, privateKeyPEM)
	if err != nil {
		return nil, err
	}
	return &OutboxHandler{client: client}, nil
}

// DeliverActivity delivers an activity to a remote inbox
func (h *OutboxHandler) DeliverActivity(ctx context.Context, inboxURL string, activity *Activity) error {
	body, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("failed to marshal activity: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, inboxURL, nil)
	if err != nil {
		return err
	}

	resp, err := h.client.Do(req, body)
	if err != nil {
		return fmt.Errorf("delivery failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("delivery returned status %d", resp.StatusCode)
	}

	return nil
}

// DeliverToFollowers delivers an activity to all followers
func (h *OutboxHandler) DeliverToFollowers(ctx context.Context, activity *Activity, followerInboxes []string) error {
	var lastErr error
	for _, inbox := range followerInboxes {
		if err := h.DeliverActivity(ctx, inbox, activity); err != nil {
			lastErr = err
			// Continue trying other inboxes
		}
	}
	return lastErr
}

// ActivityFactory helpers

// CreateFollowActivity creates a Follow activity
func CreateFollowActivity(actorURI, objectURI string) *Activity {
	return &Activity{
		Context:        DefaultContext(),
		Type:           TypeFollow,
		Actor:          actorURI,
		ActivityObject: objectURI,
	}
}

// CreateAcceptActivity creates an Accept activity for a Follow
func CreateAcceptActivity(actorURI string, follow *Activity) *Activity {
	return &Activity{
		Context:        DefaultContext(),
		Type:           TypeAccept,
		Actor:          actorURI,
		ActivityObject: follow,
	}
}

// CreateUndoActivity creates an Undo activity
func CreateUndoActivity(actorURI string, originalActivity *Activity) *Activity {
	return &Activity{
		Context:        DefaultContext(),
		Type:           TypeUndo,
		Actor:          actorURI,
		ActivityObject: originalActivity,
	}
}

// CreateNoteActivity creates a Create activity for a Note
func CreateNoteActivity(actorURI, noteID, content string, to, cc []string) *Activity {
	return &Activity{
		Context: DefaultContext(),
		Type:    TypeCreate,
		To:      to,
		Cc:      cc,
		Actor:   actorURI,
		ActivityObject: Object{
			Type:         TypeNote,
			ID:           noteID,
			Content:      content,
			AttributedTo: actorURI,
			To:           to,
			Cc:           cc,
		},
	}
}

// CreateLikeActivity creates a Like activity
func CreateLikeActivity(actorURI, objectURI string) *Activity {
	return &Activity{
		Context:        DefaultContext(),
		Type:           TypeLike,
		Actor:          actorURI,
		ActivityObject: objectURI,
	}
}

// CreateAnnounceActivity creates an Announce (reblog) activity
func CreateAnnounceActivity(actorURI, objectURI string, to, cc []string) *Activity {
	return &Activity{
		Context:        DefaultContext(),
		Type:           TypeAnnounce,
		To:             to,
		Cc:             cc,
		Actor:          actorURI,
		ActivityObject: objectURI,
	}
}
