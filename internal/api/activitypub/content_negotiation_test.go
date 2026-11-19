package activitypub

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/pkg/activitypub"
)

// mastodonFetchAccept is the Accept header Mastodon 4.5.3 fetches ActivityPub
// documents with (JsonLdHelper#build_request).
const mastodonFetchAccept = "application/activity+json, application/ld+json"

// mastodonAcceptsContentType reports whether Mastodon 4.5.3 reads a fetched
// document served as contentType, as JsonLdHelper#valid_activitypub_content_type?
// decides: application/activity+json, or application/ld+json whose profile
// parameter lists the ActivityStreams namespace.
func mastodonAcceptsContentType(contentType string) bool {
	parts := strings.Split(contentType, ";")
	switch strings.ToLower(strings.TrimSpace(parts[0])) {
	case "application/activity+json":
		return true
	case "application/ld+json":
		for _, part := range parts[1:] {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, `profile="`) && len(part) > len(`profile="`) &&
				slices.Contains(strings.Fields(part[len(`profile="`):len(part)-1]), "https://www.w3.org/ns/activitystreams") {
				return true
			}
		}
	}
	return false
}

type negotiationFixture struct {
	mux          *http.ServeMux
	signerKeyID  string
	signerKeyPEM string
}

// newNegotiationFixture serves the federation routes of example.com for
// alice (id 1), whose public status 10 starts a conversation and is quoted
// with approval by bob's status 20, and for hidden (id 4), who hides her
// collections.
func newNegotiationFixture(t *testing.T) negotiationFixture {
	t.Helper()
	store := newStubActivityPubStore()

	_, alicePub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	alicePEM, err := activitypub.EncodePublicKey(alicePub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}
	createdAt := pgtype.Timestamptz{Time: time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC), Valid: true}
	alice := db.Account{ID: 1, Username: "alice", Uri: "https://example.com/users/alice", PublicKey: alicePEM, CreatedAt: createdAt}
	store.addAccount(alice)
	store.addAccount(db.Account{ID: 2, Username: "bob", Uri: "https://example.com/users/bob", CreatedAt: createdAt})
	store.addAccount(db.Account{
		ID:        3,
		Username:  "example.com",
		Uri:       "https://example.com/actor",
		PublicKey: alicePEM,
		ActorType: pgtype.Text{String: activitypub.TypeApplication, Valid: true},
		CreatedAt: createdAt,
	})
	store.addAccount(db.Account{
		ID:              4,
		Username:        "hidden",
		Uri:             "https://example.com/users/hidden",
		HideCollections: pgtype.Bool{Bool: true, Valid: true},
	})

	// The remote actor that signs followers synchronization requests, as
	// gotodon has it cached.
	signerKey, signerPub, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	signerPEM, err := activitypub.EncodePublicKey(signerPub)
	if err != nil {
		t.Fatalf("EncodePublicKey() error = %v", err)
	}
	signerURI := "https://remote.example/users/admin"
	store.addAccount(db.Account{
		ID:        5,
		Username:  "admin",
		Domain:    pgtype.Text{String: "remote.example", Valid: true},
		Uri:       signerURI,
		PublicKey: signerPEM,
	})
	store.followerURIsByAccount[1] = []string{"https://remote.example/users/carol"}

	store.addConversation(db.Conversation{
		ID:              50,
		ParentAccountID: pgtype.Int8{Int64: 1, Valid: true},
		ParentStatusID:  pgtype.Int8{Int64: 10, Valid: true},
	})
	store.addStatus(db.Status{
		ID:             10,
		AccountID:      1,
		Text:           "hello",
		Visibility:     visibilityPublic,
		Local:          pgtype.Bool{Bool: true, Valid: true},
		ConversationID: pgtype.Int8{Int64: 50, Valid: true},
		CreatedAt:      createdAt,
	})
	store.addStatus(db.Status{
		ID:             11,
		AccountID:      1,
		Text:           "reply",
		InReplyToID:    pgtype.Int8{Int64: 10, Valid: true},
		Visibility:     visibilityPublic,
		Local:          pgtype.Bool{Bool: true, Valid: true},
		ConversationID: pgtype.Int8{Int64: 50, Valid: true},
		CreatedAt:      createdAt,
	})
	store.addStatus(db.Status{
		ID:         20,
		AccountID:  2,
		Text:       "quoting alice",
		Visibility: visibilityPublic,
		Local:      pgtype.Bool{Bool: true, Valid: true},
		CreatedAt:  createdAt,
	})
	store.statusStatsByID[10] = db.StatusStat{StatusID: 10, FavouritesCount: 2, ReblogsCount: 3}
	store.quotesByID[7] = db.Quote{
		ID:              7,
		AccountID:       2,
		StatusID:        20,
		QuotedStatusID:  pgtype.Int8{Int64: 10, Valid: true},
		QuotedAccountID: pgtype.Int8{Int64: 1, Valid: true},
		State:           quoteStateAccepted,
	}
	store.addPinnedStatus(1, db.Status{
		ID:         10,
		AccountID:  1,
		Text:       "hello",
		Visibility: visibilityPublic,
		CreatedAt:  createdAt,
	})
	store.addFeaturedTag(1, db.FeaturedTag{ID: 1, Name: "golang"})

	handler := NewFederationHandler(store, "example.com")
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	return negotiationFixture{
		mux:          mux,
		signerKeyID:  signerURI + "#main-key",
		signerKeyPEM: activitypub.EncodePrivateKey(signerKey),
	}
}

// get requests path with the given Accept header fields, signed by the remote
// actor when signed is set.
func (f negotiationFixture) get(t *testing.T, path string, accept []string, signed bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "https://example.com"+path, nil)
	for _, value := range accept {
		req.Header.Add("Accept", value)
	}
	if signed {
		signer, err := activitypub.NewHTTPSigner(f.signerKeyID, f.signerKeyPEM)
		if err != nil {
			t.Fatalf("NewHTTPSigner() error = %v", err)
		}
		if err := signer.Sign(req, nil); err != nil {
			t.Fatalf("Sign() error = %v", err)
		}
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

// federationRoute is a request to one federation GET route. Routes with a
// page (redirectTo set) negotiate on Accept and redirect browsers there; the
// others serve their document whatever the request accepts, as Mastodon's do.
type federationRoute struct {
	path       string
	signed     bool
	wantType   string
	wantID     string
	redirectTo string
}

var federationRoutes = []federationRoute{
	{path: "/users/alice", wantType: activitypub.TypePerson, wantID: "https://example.com/users/alice", redirectTo: "https://example.com/@alice"},
	{path: "/ap/users/1", wantType: activitypub.TypePerson, wantID: "https://example.com/users/alice", redirectTo: "https://example.com/@alice"},
	{path: "/actor", wantType: activitypub.TypeApplication, wantID: "https://example.com/actor", redirectTo: "https://example.com/about/more?instance_actor=true"},
	{path: "/actor/outbox", wantType: activitypub.TypeOrderedCollection, wantID: "https://example.com/actor/outbox"},
	{path: "/users/alice/outbox", wantType: activitypub.TypeOrderedCollection, wantID: "https://example.com/users/alice/outbox"},
	{path: "/users/alice/outbox?page=true", wantType: activitypub.TypeOrderedCollectionPage},
	{path: "/ap/users/1/outbox", wantType: activitypub.TypeOrderedCollection, wantID: "https://example.com/users/alice/outbox"},
	{path: "/users/alice/statuses/10", wantType: activitypub.TypeNote, wantID: "https://example.com/users/alice/statuses/10", redirectTo: "https://example.com/@alice/10"},
	{path: "/ap/users/1/statuses/10", wantType: activitypub.TypeNote, wantID: "https://example.com/users/alice/statuses/10", redirectTo: "https://example.com/@alice/10"},
	{path: "/users/alice/statuses/10/activity", wantType: activitypub.TypeCreate},
	{path: "/ap/users/1/statuses/10/activity", wantType: activitypub.TypeCreate},
	{path: "/users/alice/statuses/10/replies", wantType: activitypub.TypeCollection, wantID: "https://example.com/users/alice/statuses/10/replies"},
	{path: "/users/alice/statuses/10/replies?page=true", wantType: activitypub.TypeCollectionPage},
	{path: "/ap/users/1/statuses/10/replies", wantType: activitypub.TypeCollection, wantID: "https://example.com/users/alice/statuses/10/replies"},
	{path: "/users/alice/statuses/10/likes", wantType: activitypub.TypeCollection, wantID: "https://example.com/users/alice/statuses/10/likes"},
	{path: "/ap/users/1/statuses/10/likes", wantType: activitypub.TypeCollection, wantID: "https://example.com/users/alice/statuses/10/likes"},
	{path: "/users/alice/statuses/10/shares", wantType: activitypub.TypeCollection, wantID: "https://example.com/users/alice/statuses/10/shares"},
	{path: "/ap/users/1/statuses/10/shares", wantType: activitypub.TypeCollection, wantID: "https://example.com/users/alice/statuses/10/shares"},
	{path: "/users/alice/followers", wantType: activitypub.TypeOrderedCollection, wantID: "https://example.com/users/alice/followers"},
	{path: "/users/alice/followers?page=1", wantType: activitypub.TypeOrderedCollectionPage},
	{path: "/ap/users/1/followers", wantType: activitypub.TypeOrderedCollection, wantID: "https://example.com/users/alice/followers"},
	{path: "/users/alice/following", wantType: activitypub.TypeOrderedCollection, wantID: "https://example.com/users/alice/following"},
	{path: "/ap/users/1/following", wantType: activitypub.TypeOrderedCollection, wantID: "https://example.com/users/alice/following"},
	{path: "/users/hidden/followers", wantType: activitypub.TypeOrderedCollection, wantID: "https://example.com/users/hidden/followers"},
	{path: "/users/hidden/following?page=1", wantType: activitypub.TypeOrderedCollectionPage},
	{path: "/users/alice/followers_synchronization", signed: true, wantType: activitypub.TypeOrderedCollection, wantID: "https://example.com/users/alice/followers_synchronization"},
	{path: "/ap/users/1/followers_synchronization", signed: true, wantType: activitypub.TypeOrderedCollection},
	{path: "/users/alice/quote_authorizations/7", wantType: "QuoteAuthorization", wantID: "https://example.com/users/alice/quote_authorizations/7"},
	{path: "/ap/users/1/quote_authorizations/7", wantType: "QuoteAuthorization", wantID: "https://example.com/users/alice/quote_authorizations/7"},
	{path: "/users/alice/collections/featured", wantType: activitypub.TypeOrderedCollection, wantID: "https://example.com/users/alice/collections/featured"},
	{path: "/ap/users/1/collections/featured", wantType: activitypub.TypeOrderedCollection, wantID: "https://example.com/users/alice/collections/featured"},
	{path: "/users/hidden/collections/featured", wantType: activitypub.TypeOrderedCollection, wantID: "https://example.com/users/hidden/collections/featured"},
	{path: "/users/alice/collections/tags", wantType: activitypub.TypeCollection, wantID: "https://example.com/users/alice/collections/tags"},
	{path: "/ap/users/1/collections/tags", wantType: activitypub.TypeCollection, wantID: "https://example.com/users/alice/collections/tags"},
	{path: "/users/hidden/collections/tags", wantType: activitypub.TypeCollection, wantID: "https://example.com/users/hidden/collections/tags"},
	{path: "/contexts/1-10", wantType: activitypub.TypeCollection, wantID: "https://example.com/contexts/1-10"},
	{path: "/contexts/1-10/items", wantType: activitypub.TypeCollection, wantID: "https://example.com/contexts/1-10/items"},
	{path: "/contexts/1-10/items?page=true", wantType: activitypub.TypeCollectionPage},
}

// TestFederationRoutesServeActivityPubDocuments fetches every federation GET
// route as ActivityPub fetchers do and as browsers do. Fetchers always get
// the document, served under a media type Mastodon reads. Browsers get the
// page of a route that has one and the document of every other route.
func TestFederationRoutesServeActivityPubDocuments(t *testing.T) {
	fixture := newNegotiationFixture(t)
	accepts := []struct {
		name    string
		accept  []string
		fetcher bool
	}{
		{name: "Mastodon", accept: []string{mastodonFetchAccept}, fetcher: true},
		{name: "activity+json", accept: []string{"application/activity+json"}, fetcher: true},
		{name: "profiled ld+json", accept: []string{activitypub.ActivityPubLDContentType}, fetcher: true},
		{name: "bare ld+json", accept: []string{"application/ld+json"}, fetcher: true},
		{name: "activity+json and profiled ld+json", accept: []string{`application/activity+json, application/ld+json; profile="https://www.w3.org/ns/activitystreams"`}, fetcher: true},
		{name: "q-values", accept: []string{"text/html;q=0.5, application/ld+json;q=0.8, */*;q=0.1"}, fetcher: true},
		{name: "browser", accept: []string{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"}},
		{name: "no Accept"},
	}
	for _, route := range federationRoutes {
		for _, accept := range accepts {
			t.Run(route.path+"/"+accept.name, func(t *testing.T) {
				rec := fixture.get(t, route.path, accept.accept, route.signed)
				if route.redirectTo != "" {
					if vary := rec.Header().Values("Vary"); !slices.Contains(vary, "Accept") {
						t.Fatalf("Vary = %q, want Accept", vary)
					}
					if !accept.fetcher {
						if rec.Code != http.StatusFound {
							t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusFound, rec.Body.String())
						}
						if got := rec.Header().Get("Location"); got != route.redirectTo {
							t.Fatalf("Location = %q, want %q", got, route.redirectTo)
						}
						return
					}
				}
				assertActivityPubDocument(t, rec, route.wantType, route.wantID)
			})
		}
	}
}

func assertActivityPubDocument(t *testing.T, rec *httptest.ResponseRecorder, wantType, wantID string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (Location %q): %s", rec.Code, http.StatusOK, rec.Header().Get("Location"), rec.Body.String())
	}
	contentType := rec.Header().Values("Content-Type")
	if len(contentType) != 1 || contentType[0] != activitypub.ActivityPubContentType {
		t.Fatalf("Content-Type = %q, want [%s]", contentType, activitypub.ActivityPubContentType)
	}
	if !mastodonAcceptsContentType(contentType[0]) {
		t.Fatalf("Mastodon discards a document served as %q", contentType[0])
	}
	var document map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode document: %v: %s", err, rec.Body.String())
	}
	if document["@context"] == nil {
		t.Fatalf("document has no @context: %s", rec.Body.String())
	}
	if document["type"] != wantType {
		t.Fatalf("type = %v, want %s: %s", document["type"], wantType, rec.Body.String())
	}
	if wantID != "" && document["id"] != wantID {
		t.Fatalf("id = %v, want %s", document["id"], wantID)
	}
}

// TestFederationRoutesTableCoversEveryGETRoute keeps federationRoutes in step
// with RegisterRoutes: every GET pattern it registers must be requested.
func TestFederationRoutesTableCoversEveryGETRoute(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "handler.go", nil, 0)
	if err != nil {
		t.Fatalf("parse handler.go: %v", err)
	}
	var registered []string
	ast.Inspect(file, func(node ast.Node) bool {
		decl, ok := node.(*ast.FuncDecl)
		if !ok || decl.Name.Name != "RegisterRoutes" {
			return true
		}
		ast.Inspect(decl.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			pattern, err := strconv.Unquote(lit.Value)
			if err == nil && strings.HasPrefix(pattern, http.MethodGet+" ") {
				registered = append(registered, pattern)
			}
			return true
		})
		return false
	})
	if len(registered) == 0 {
		t.Fatal("found no GET routes in RegisterRoutes")
	}

	fixture := newNegotiationFixture(t)
	requested := map[string]bool{}
	for _, route := range federationRoutes {
		_, pattern := fixture.mux.Handler(httptest.NewRequest(http.MethodGet, route.path, nil))
		if pattern == "" {
			t.Errorf("%s matches no route", route.path)
		}
		requested[pattern] = true
	}
	for _, pattern := range registered {
		if !requested[pattern] {
			t.Errorf("federationRoutes requests nothing served by %q", pattern)
		}
	}
}
