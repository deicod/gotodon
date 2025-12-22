package federation

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

const (
	defaultRemoteRepliesRefresh  = 24 * time.Hour
	defaultRemoteRepliesMaxDepth = 3
	defaultRemoteRepliesMaxItems = 200
	defaultRemoteRepliesMaxPages = 5
)

// ThreadFetcher resolves remote thread context for a status.
type ThreadFetcher interface {
	FetchThread(ctx context.Context, status db.Status) error
}

// RemoteStatusStore describes persistence needed for remote status fetching.
type RemoteStatusStore interface {
	CreateStore
	GetStatusByID(ctx context.Context, id int64) (db.Status, error)
	UpdateStatusInReplyTo(ctx context.Context, arg db.UpdateStatusInReplyToParams) (int64, error)
	UpdateStatusFetchedRepliesAt(ctx context.Context, arg db.UpdateStatusFetchedRepliesAtParams) error
}

type remoteFetchStateKey struct{}

// RemoteStatusFetcher fetches remote statuses and thread context.
type RemoteStatusFetcher struct {
	store          RemoteStatusStore
	localDomain    string
	objectFetcher  ObjectFetcher
	helper         *CreateService
	now            func() time.Time
	repliesRefresh time.Duration
	maxReplyDepth  int
	maxReplyItems  int
	maxReplyPages  int
}

// NewRemoteStatusFetcher creates a remote status fetcher with defaults.
func NewRemoteStatusFetcher(store RemoteStatusStore, localDomain string, fetcher activitypub.ActorFetcher) *RemoteStatusFetcher {
	helper := NewCreateService(store, localDomain, fetcher)
	rs := &RemoteStatusFetcher{
		store:          store,
		localDomain:    localDomain,
		objectFetcher:  newHTTPObjectFetcher(),
		helper:         helper,
		now:            time.Now,
		repliesRefresh: defaultRemoteRepliesRefresh,
		maxReplyDepth:  defaultRemoteRepliesMaxDepth,
		maxReplyItems:  defaultRemoteRepliesMaxItems,
		maxReplyPages:  defaultRemoteRepliesMaxPages,
	}
	helper.SetStatusFetcher(rs)
	return rs
}

// FetchStatus retrieves and stores a remote status by URI.
func (r *RemoteStatusFetcher) FetchStatus(ctx context.Context, uri string) (db.Status, error) {
	if r.store == nil {
		return db.Status{}, errors.New("remote status store missing")
	}
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return db.Status{}, errors.New("status uri is required")
	}
	if isLocalURI(uri, r.localDomain) {
		return r.store.GetStatusByURI(ctx, pgtype.Text{String: uri, Valid: true})
	}

	ctx, visited := r.ensureFetchState(ctx)
	if visited[uri] {
		return db.Status{}, errors.New("remote status fetch loop")
	}
	visited[uri] = true

	status, err := r.store.GetStatusByURI(ctx, pgtype.Text{String: uri, Valid: true})
	if err == nil {
		return status, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.Status{}, err
	}
	if r.objectFetcher == nil {
		return db.Status{}, errors.New("object fetcher unavailable")
	}

	payload, err := r.objectFetcher.FetchObject(ctx, uri)
	if err != nil {
		return db.Status{}, err
	}
	return r.ingestStatusPayload(ctx, payload, uri)
}

// FetchThread fetches missing ancestors and replies for a remote status.
func (r *RemoteStatusFetcher) FetchThread(ctx context.Context, status db.Status) error {
	if r.store == nil {
		return errors.New("remote status store missing")
	}
	if !status.Uri.Valid || status.Uri.String == "" {
		return errors.New("status uri missing")
	}
	if isLocalURI(status.Uri.String, r.localDomain) {
		return nil
	}

	needsReplies := r.shouldRefreshReplies(status)
	needsParent := status.Reply && !status.InReplyToID.Valid
	if !needsReplies && !needsParent {
		return nil
	}
	if r.objectFetcher == nil {
		return errors.New("object fetcher unavailable")
	}

	ctx, _ = r.ensureFetchState(ctx)
	payload, err := r.objectFetcher.FetchObject(ctx, status.Uri.String)
	if err != nil {
		return err
	}

	noteValue := unwrapAnnounceObject(payload)
	if noteValue == nil {
		return errors.New("missing note object")
	}
	if noteURI, ok := noteValue.(string); ok && noteURI != "" && noteURI != status.Uri.String {
		payload, err = r.objectFetcher.FetchObject(ctx, noteURI)
		if err != nil {
			return err
		}
		noteValue = unwrapAnnounceObject(payload)
	}
	if noteValue == nil {
		return errors.New("missing note object")
	}

	note, err := parseCreateNote(noteValue)
	if err != nil {
		return err
	}
	if note.Type == "" {
		return errors.New("missing note type")
	}
	if !strings.EqualFold(note.Type, activitypub.TypeNote) {
		return nil
	}

	if needsParent {
		if err := r.resolveParent(ctx, status, note); err != nil {
			return err
		}
	}

	if needsReplies {
		remaining := r.maxReplyItems
		if err := r.fetchReplies(ctx, note.Replies, 0, &remaining); err != nil {
			return err
		}
		_ = r.store.UpdateStatusFetchedRepliesAt(ctx, db.UpdateStatusFetchedRepliesAtParams{
			ID:               status.ID,
			FetchedRepliesAt: pgtype.Timestamptz{Time: r.now().UTC(), Valid: true},
		})
	}

	return nil
}

func (r *RemoteStatusFetcher) ensureFetchState(ctx context.Context) (context.Context, map[string]bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if visited, ok := ctx.Value(remoteFetchStateKey{}).(map[string]bool); ok {
		return ctx, visited
	}
	visited := make(map[string]bool)
	return context.WithValue(ctx, remoteFetchStateKey{}, visited), visited
}

func (r *RemoteStatusFetcher) ingestStatusPayload(ctx context.Context, payload any, fallbackURI string) (db.Status, error) {
	noteValue, actorURI, to, cc := r.extractActivityFields(payload)
	if noteValue == nil {
		return db.Status{}, errors.New("missing note object")
	}

	if noteURI, ok := noteValue.(string); ok {
		if noteURI == "" {
			return db.Status{}, errors.New("missing note id")
		}
		if noteURI == fallbackURI {
			return db.Status{}, errors.New("note object unresolved")
		}
		return r.FetchStatus(ctx, noteURI)
	}

	note, err := parseCreateNote(noteValue)
	if err != nil {
		return db.Status{}, err
	}
	if note.Type == "" {
		return db.Status{}, errors.New("missing note type")
	}
	if !strings.EqualFold(note.Type, activitypub.TypeNote) {
		return db.Status{}, nil
	}

	noteValue, noteURI := ensureNoteID(note, noteValue, fallbackURI)
	if noteURI == "" {
		return db.Status{}, errors.New("missing note id")
	}

	noteActor := extractAttributedActor(note.AttributedTo)
	if noteActor != "" && actorURI != "" && noteActor != actorURI {
		return db.Status{}, errors.New("note actor mismatch")
	}
	if actorURI == "" {
		actorURI = noteActor
	}
	if actorURI == "" {
		return db.Status{}, errors.New("missing note actor")
	}

	status, err := r.store.GetStatusByURI(ctx, pgtype.Text{String: noteURI, Valid: true})
	if err == nil {
		r.linkReplyIfMissing(ctx, status, note)
		return status, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return db.Status{}, err
	}
	if r.helper == nil {
		return db.Status{}, errors.New("create helper unavailable")
	}

	activity := &activitypub.Activity{
		Type:           activitypub.TypeCreate,
		Actor:          actorURI,
		To:             to,
		Cc:             cc,
		ActivityObject: noteValue,
	}
	if err := r.helper.handleCreate(ctx, activity, createOptions{skipMentions: true}); err != nil {
		return db.Status{}, err
	}

	status, err = r.store.GetStatusByURI(ctx, pgtype.Text{String: noteURI, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Status{}, nil
		}
		return db.Status{}, err
	}
	r.linkReplyIfMissing(ctx, status, note)
	return status, nil
}

func (r *RemoteStatusFetcher) extractActivityFields(payload any) (any, string, []string, []string) {
	noteValue := payload
	actorURI := ""
	var to []string
	var cc []string

	switch v := payload.(type) {
	case activitypub.Activity:
		actorURI = extractActorURI(v.Actor)
		to = v.To
		cc = v.Cc
		noteValue = v.ActivityObject
	case *activitypub.Activity:
		if v != nil {
			actorURI = extractActorURI(v.Actor)
			to = v.To
			cc = v.Cc
			noteValue = v.ActivityObject
		}
	case map[string]any:
		typ := coerceActivityType(v["type"])
		if typ != "" && !strings.EqualFold(typ, activitypub.TypeNote) {
			actorURI = extractActorURI(v["actor"])
			to = stringSlice(v["to"])
			cc = stringSlice(v["cc"])
			noteValue = v["object"]
		}
	}

	return unwrapAnnounceObject(noteValue), actorURI, to, cc
}

func (r *RemoteStatusFetcher) resolveParent(ctx context.Context, status db.Status, note *notePayload) error {
	if note == nil {
		return nil
	}
	parentURI := extractObjectURI(note.InReplyTo)
	if parentURI == "" {
		return nil
	}

	parent, err := r.FetchStatus(ctx, parentURI)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if parent.ID == 0 {
		return nil
	}

	updated, err := r.store.UpdateStatusInReplyTo(ctx, db.UpdateStatusInReplyToParams{
		ID:                 status.ID,
		InReplyToID:        pgtype.Int8{Int64: parent.ID, Valid: true},
		InReplyToAccountID: pgtype.Int8{Int64: parent.AccountID, Valid: true},
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if updated > 0 {
		_ = r.store.IncrementStatusReplies(ctx, parent.ID)
	}
	return nil
}

func (r *RemoteStatusFetcher) linkReplyIfMissing(ctx context.Context, status db.Status, note *notePayload) {
	if note == nil || status.ID == 0 || status.InReplyToID.Valid {
		return
	}
	parentURI := extractObjectURI(note.InReplyTo)
	if parentURI == "" {
		return
	}
	parent, err := r.store.GetStatusByURI(ctx, pgtype.Text{String: parentURI, Valid: true})
	if err != nil {
		return
	}

	updated, err := r.store.UpdateStatusInReplyTo(ctx, db.UpdateStatusInReplyToParams{
		ID:                 status.ID,
		InReplyToID:        pgtype.Int8{Int64: parent.ID, Valid: true},
		InReplyToAccountID: pgtype.Int8{Int64: parent.AccountID, Valid: true},
	})
	if err != nil || updated == 0 {
		return
	}
	_ = r.store.IncrementStatusReplies(ctx, parent.ID)
}

func (r *RemoteStatusFetcher) shouldRefreshReplies(status db.Status) bool {
	if !status.FetchedRepliesAt.Valid {
		return true
	}
	return r.now().UTC().Sub(status.FetchedRepliesAt.Time) >= r.repliesRefresh
}

func (r *RemoteStatusFetcher) fetchReplies(ctx context.Context, replies any, depth int, remaining *int) error {
	if replies == nil || remaining == nil || *remaining <= 0 {
		return nil
	}
	if depth >= r.maxReplyDepth {
		return nil
	}

	items, err := r.collectReplyItems(ctx, replies, *remaining)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	for _, item := range items {
		if *remaining <= 0 {
			break
		}

		switch v := item.(type) {
		case string:
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			if _, err := r.FetchStatus(ctx, v); err != nil {
				return err
			}
			*remaining--
		default:
			fallback := noteURIFromObject(v)
			if fallback == "" {
				fallback = extractObjectURI(v)
			}
			status, err := r.ingestStatusPayload(ctx, v, fallback)
			if err != nil {
				return err
			}
			*remaining--
			if status.ID == 0 {
				continue
			}

			noteValue := unwrapAnnounceObject(v)
			note, err := parseCreateNote(noteValue)
			if err != nil || note == nil {
				continue
			}
			if note.Type == "" || !strings.EqualFold(note.Type, activitypub.TypeNote) {
				continue
			}
			if note.Replies != nil && depth+1 < r.maxReplyDepth {
				if err := r.fetchReplies(ctx, note.Replies, depth+1, remaining); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (r *RemoteStatusFetcher) collectReplyItems(ctx context.Context, value any, maxItems int) ([]any, error) {
	var items []any
	current := value
	pages := 0
	seenPages := make(map[string]bool)

	for current != nil && len(items) < maxItems && pages < r.maxReplyPages {
		if uri, ok := current.(string); ok {
			if uri == "" || seenPages[uri] {
				break
			}
			seenPages[uri] = true
			if r.objectFetcher == nil {
				return items, errors.New("object fetcher unavailable")
			}
			payload, err := r.objectFetcher.FetchObject(ctx, uri)
			if err != nil {
				return items, err
			}
			current = payload
			pages++
			continue
		}

		data, ok := r.parseCollection(current)
		if !ok {
			break
		}
		if len(data.items) > 0 {
			remaining := maxItems - len(items)
			if len(data.items) > remaining {
				items = append(items, data.items[:remaining]...)
			} else {
				items = append(items, data.items...)
			}
		}
		if len(items) >= maxItems || data.next == nil {
			break
		}
		current = data.next
		pages++
	}

	return items, nil
}

type collectionData struct {
	items []any
	next  any
}

func (r *RemoteStatusFetcher) parseCollection(value any) (collectionData, bool) {
	switch v := value.(type) {
	case activitypub.Collection:
		next := v.First
		if len(v.Items) > 0 {
			next = nil
		}
		return collectionData{items: v.Items, next: next}, true
	case *activitypub.Collection:
		if v == nil {
			return collectionData{}, false
		}
		next := v.First
		if len(v.Items) > 0 {
			next = nil
		}
		return collectionData{items: v.Items, next: next}, true
	case activitypub.OrderedCollection:
		items := v.OrderedItems
		if len(items) == 0 {
			items = v.Items
		}
		next := v.First
		if len(items) > 0 {
			next = nil
		}
		return collectionData{items: items, next: next}, true
	case *activitypub.OrderedCollection:
		if v == nil {
			return collectionData{}, false
		}
		items := v.OrderedItems
		if len(items) == 0 {
			items = v.Items
		}
		next := v.First
		if len(items) > 0 {
			next = nil
		}
		return collectionData{items: items, next: next}, true
	case activitypub.CollectionPage:
		var next any
		if v.Next != "" {
			next = v.Next
		}
		if len(v.Items) == 0 && v.First != nil && next == nil {
			next = v.First
		}
		return collectionData{items: v.Items, next: next}, true
	case *activitypub.CollectionPage:
		if v == nil {
			return collectionData{}, false
		}
		var next any
		if v.Next != "" {
			next = v.Next
		}
		if len(v.Items) == 0 && v.First != nil && next == nil {
			next = v.First
		}
		return collectionData{items: v.Items, next: next}, true
	case map[string]any:
		items := sliceValue(v["orderedItems"])
		if len(items) == 0 {
			items = sliceValue(v["items"])
		}
		var next any
		if nextURI := stringValue(v["next"]); nextURI != "" {
			next = nextURI
		}
		if next == nil && len(items) == 0 {
			if first, ok := v["first"]; ok {
				next = first
			}
		}
		return collectionData{items: items, next: next}, true
	default:
		return collectionData{}, false
	}
}
