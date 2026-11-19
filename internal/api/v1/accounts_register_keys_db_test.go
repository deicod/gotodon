package v1

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	apactivitypub "github.com/deicod/gotodon/internal/api/activitypub"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/pkg/activitypub"
	"github.com/deicod/gotodon/pkg/safehttp/safehttptest"
)

const (
	// httptest's TLS certificate covers *.example.com.
	keysTestDomain    = "social.example.com"
	keysTestPeerHost  = "peer.example.com"
	keysTestPeerActor = "https://" + keysTestPeerHost + "/users/bob"
	keysTestPeerInbox = keysTestPeerActor + "/inbox"
)

func keysTestHandler(pool *pgxpool.Pool) *AccountsHandler {
	return NewAccountsHandler(db.New(pool), pool, &config.Config{Instance: config.InstanceConfig{
		LocalDomain:       keysTestDomain,
		RegistrationsMode: "open",
	}}, nil)
}

// registerLocalAccount registers username through the API and returns the
// stored account.
func registerLocalAccount(t *testing.T, h *AccountsHandler, queries *db.Queries, username string) db.Account {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Register(rec, registerRequest(t, username))
	if rec.Code != http.StatusOK {
		t.Fatalf("register %s: status = %d: %s", username, rec.Code, rec.Body.String())
	}
	account, err := queries.GetAccountByUsername(context.Background(), username)
	if err != nil {
		t.Fatalf("load %s: %v", username, err)
	}
	return account
}

// parseAccountKeys checks that account holds a matching RSA key pair encoded
// as Mastodon stores it and returns the private key.
func parseAccountKeys(t *testing.T, account db.Account) *rsa.PrivateKey {
	t.Helper()
	if !account.PrivateKey.Valid || account.PrivateKey.String == "" {
		t.Fatalf("%s has no private key", account.Username)
	}
	block, rest := pem.Decode([]byte(account.PrivateKey.String))
	if block == nil || block.Type != "RSA PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("%s private key is not a single PKCS #1 PEM block: %q", account.Username, account.PrivateKey.String)
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("%s private key: %v", account.Username, err)
	}
	if err := privateKey.Validate(); err != nil {
		t.Fatalf("%s private key invalid: %v", account.Username, err)
	}
	if bits := privateKey.N.BitLen(); bits != 2048 {
		t.Fatalf("%s key has %d bits, want 2048", account.Username, bits)
	}

	block, rest = pem.Decode([]byte(account.PublicKey))
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("%s public key is not a single PKIX PEM block: %q", account.Username, account.PublicKey)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("%s public key: %v", account.Username, err)
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("%s public key is %T, want RSA", account.Username, parsed)
	}
	if !publicKey.Equal(privateKey.Public()) {
		t.Fatalf("%s public key does not belong to its private key", account.Username)
	}
	return privateKey
}

// TestRegisterGeneratesAccountKeyPair checks that every registered account
// gets its own valid RSA key pair.
func TestRegisterGeneratesAccountKeyPair(t *testing.T) {
	pool := openMigratedTestPool(t)
	queries := db.New(pool)
	h := keysTestHandler(pool)

	alice := parseAccountKeys(t, registerLocalAccount(t, h, queries, "alice"))
	carol := parseAccountKeys(t, registerLocalAccount(t, h, queries, "carol"))
	if alice.Equal(carol) {
		t.Fatal("alice and carol share a key pair")
	}
}

// TestRegisterRefusesWithoutKeyPair checks that registration creates nothing
// when no key pair can be generated, instead of an account that cannot sign.
func TestRegisterRefusesWithoutKeyPair(t *testing.T) {
	pool := openMigratedTestPool(t)
	h := keysTestHandler(pool)
	h.newKeyPair = func() (string, string, error) {
		return "", "", errors.New("entropy exhausted")
	}

	rec := httptest.NewRecorder()
	h.Register(rec, registerRequest(t, "dave"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d: %s, want %d", rec.Code, rec.Body.String(), http.StatusInternalServerError)
	}

	var accounts, users int
	if err := pool.QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM accounts WHERE username = 'dave'),
		        (SELECT count(*) FROM users WHERE email = 'dave@mail.test')`,
	).Scan(&accounts, &users); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if accounts != 0 || users != 0 {
		t.Fatalf("created %d accounts and %d users, want none", accounts, users)
	}
}

// TestRegisteredAccountFederatesWithMastodonPeer registers an account and
// checks, against a peer that verifies signatures as Mastodon does, that the
// account's actor document publishes its key and that the Follow and Create
// it sends verify, and that the peer's signed reply verifies at its inbox.
func TestRegisteredAccountFederatesWithMastodonPeer(t *testing.T) {
	pool := openMigratedTestPool(t)
	queries := db.New(pool)
	ctx := context.Background()
	h := keysTestHandler(pool)

	alice := registerLocalAccount(t, h, queries, "alice")
	carol := registerLocalAccount(t, h, queries, "carol")
	parseAccountKeys(t, alice)
	aliceURI := "https://" + keysTestDomain + "/users/alice"
	if alice.Uri != aliceURI {
		t.Fatalf("uri = %q, want %q", alice.Uri, aliceURI)
	}
	stored, err := json.Marshal(alice)
	if err != nil {
		t.Fatalf("marshal account: %v", err)
	}
	for _, placeholder := range []string{"://example.com", "localhost"} {
		if bytes.Contains(stored, []byte(placeholder)) {
			t.Fatalf("account data names %s: %s", placeholder, stored)
		}
	}

	// gotodon, serving the federation routes of the configured domain.
	fed := apactivitypub.NewFederationHandler(queries, keysTestDomain)
	inboxed := make(chan *activitypub.Activity, 4)
	fed.RegisterInboxHandler(activitypub.TypeAccept, func(_ context.Context, activity *activitypub.Activity) error {
		inboxed <- activity
		return nil
	})
	mux := http.NewServeMux()
	fed.RegisterRoutes(mux)
	local := httptest.NewTLSServer(mux)
	defer local.Close()

	peer := newMastodonPeer(t)
	peerServer := httptest.NewTLSServer(peer)
	defer peerServer.Close()
	client := safehttptest.Client(t, map[string]*httptest.Server{
		keysTestDomain:   local,
		keysTestPeerHost: peerServer,
	})
	peer.client = client

	// The peer's account as gotodon caches it.
	bob, err := queries.CreateAccount(ctx, db.CreateAccountParams{
		Username:     "bob",
		Domain:       pgtype.Text{String: keysTestPeerHost, Valid: true},
		PublicKey:    peer.publicKeyPEM(t),
		DisplayName:  "bob",
		Uri:          keysTestPeerActor,
		Url:          pgtype.Text{String: "https://" + keysTestPeerHost + "/@bob", Valid: true},
		Discoverable: pgtype.Bool{Bool: true, Valid: true},
		ActorType:    pgtype.Text{String: activitypub.TypePerson, Valid: true},
	})
	if err != nil {
		t.Fatalf("create remote account: %v", err)
	}
	if _, err := queries.UpdateAccount(ctx, db.UpdateAccountParams{
		ID:       bob.ID,
		InboxUrl: pgtype.Text{String: keysTestPeerInbox, Valid: true},
	}); err != nil {
		t.Fatalf("set remote inbox: %v", err)
	}

	// The actor document carries the stored public key under the key id
	// outgoing requests are signed with.
	actor := fetchActorDocument(t, client, aliceURI)
	if actor.ID != aliceURI || actor.Inbox != aliceURI+"/inbox" {
		t.Fatalf("actor id = %q, inbox = %q", actor.ID, actor.Inbox)
	}
	if actor.PublicKey == nil {
		t.Fatal("actor document has no publicKey")
	}
	if actor.PublicKey.ID != aliceURI+"#main-key" || actor.PublicKey.Owner != aliceURI {
		t.Fatalf("publicKey id = %q, owner = %q", actor.PublicKey.ID, actor.PublicKey.Owner)
	}
	if actor.PublicKey.PublicKeyPem != alice.PublicKey {
		t.Fatal("actor document publishes a different key than the stored one")
	}

	// Outgoing Follow and Create, signed with the stored key.
	outgoing := federation.NewOutgoingService(queries, keysTestDomain, &signingEnqueuer{client: client})
	if err := outgoing.DeliverFollow(ctx, alice.ID, bob.ID, 1); err != nil {
		t.Fatalf("deliver Follow: %v", err)
	}
	status, err := queries.CreateStatus(ctx, db.CreateStatusParams{
		Text:      "Hello @bob@" + keysTestPeerHost,
		AccountID: alice.ID,
		Local:     pgtype.Bool{Bool: true, Valid: true},
	})
	if err != nil {
		t.Fatalf("create status: %v", err)
	}
	if _, err := queries.CreateMention(ctx, db.CreateMentionParams{StatusID: status.ID, AccountID: bob.ID}); err != nil {
		t.Fatalf("create mention: %v", err)
	}
	if err := outgoing.DeliverStatusCreate(ctx, status.ID); err != nil {
		t.Fatalf("deliver Create: %v", err)
	}

	deliveries := peer.accepted()
	if len(deliveries) != 2 {
		t.Fatalf("peer accepted %d deliveries, want 2", len(deliveries))
	}
	follow, create := deliveries[0], deliveries[1]
	if follow.signer != aliceURI || follow.activity["type"] != activitypub.TypeFollow || follow.activity["object"] != keysTestPeerActor {
		t.Fatalf("first delivery = %+v, want alice's Follow of bob", follow)
	}
	note, _ := create.activity["object"].(map[string]any)
	if create.signer != aliceURI || create.activity["type"] != activitypub.TypeCreate || note["attributedTo"] != aliceURI {
		t.Fatalf("second delivery = %+v, want alice's Create", create)
	}

	// The key is the account's own: carol's key does not sign for alice.
	forged, err := activitypub.NewOutboxHandlerWithHTTPClient(aliceURI+"#main-key", carol.PrivateKey.String, client)
	if err != nil {
		t.Fatalf("outbox: %v", err)
	}
	err = forged.DeliverActivity(ctx, keysTestPeerInbox, activitypub.CreateFollowActivity(aliceURI, keysTestPeerActor))
	if err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Fatalf("Follow signed with carol's key: err = %v, want the peer's 401", err)
	}
	if got := len(peer.accepted()); got != 2 {
		t.Fatalf("peer accepted %d deliveries after the forged one, want 2", got)
	}

	// The peer accepts the Follow; gotodon verifies its signature.
	accept, err := json.Marshal(map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       keysTestPeerActor + "#accepts/follows/1",
		"type":     activitypub.TypeAccept,
		"actor":    keysTestPeerActor,
		"object":   follow.activity,
	})
	if err != nil {
		t.Fatalf("marshal Accept: %v", err)
	}
	tampered := bytes.Replace(accept, []byte(`"type":"Accept"`), []byte(`"type":"Reject"`), 1)
	if code := peer.deliver(t, aliceURI+"/inbox", accept, tampered); code != http.StatusUnauthorized {
		t.Fatalf("tampered Accept: status = %d, want %d", code, http.StatusUnauthorized)
	}
	if code := peer.deliver(t, aliceURI+"/inbox", accept, accept); code != http.StatusAccepted {
		t.Fatalf("Accept: status = %d, want %d", code, http.StatusAccepted)
	}
	select {
	case activity := <-inboxed:
		if activity.Actor != keysTestPeerActor {
			t.Fatalf("inbox received an Accept from %v", activity.Actor)
		}
	default:
		t.Fatal("the verified Accept did not reach the inbox handler")
	}
	if len(inboxed) != 0 {
		t.Fatal("the tampered Accept reached the inbox handler")
	}
}

// fetchActorDocument fetches an actor document as the peer does, without
// following redirects, and checks that Mastodon would read it.
func fetchActorDocument(t *testing.T, client *http.Client, uri string) activitypub.Actor {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		t.Fatalf("actor request: %v", err)
	}
	req.Header.Set("Accept", peerFetchAccept)
	noRedirects := *client
	noRedirects.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirects.Do(req)
	if err != nil {
		t.Fatalf("fetch actor: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fetch actor: status = %d, Location = %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := resp.Header.Values("Content-Type"); len(got) != 1 || got[0] != activitypub.ActivityPubContentType {
		t.Fatalf("actor Content-Type = %q, want [%s]", got, activitypub.ActivityPubContentType)
	}
	if !validActivityPubContentType(resp.Header) {
		t.Fatalf("Mastodon discards an actor served as %q", resp.Header.Get("Content-Type"))
	}
	var actor activitypub.Actor
	if err := json.NewDecoder(resp.Body).Decode(&actor); err != nil {
		t.Fatalf("decode actor: %v", err)
	}
	return actor
}

// signingEnqueuer delivers each activity at once, signed as
// federation.DeliveryService signs it: with the actor's stored private key
// under the key id "<actor URI>#main-key".
type signingEnqueuer struct {
	client *http.Client
}

func (e *signingEnqueuer) Enqueue(localActor db.Account, inbox string, activity *activitypub.Activity) error {
	outbox, err := activitypub.NewOutboxHandlerWithHTTPClient(localActor.Uri+"#main-key", localActor.PrivateKey.String, e.client)
	if err != nil {
		return err
	}
	return outbox.DeliverActivity(context.Background(), inbox, activity)
}

// peerFetchAccept is the Accept header Mastodon 4.5.3 fetches ActivityPub
// documents with (JsonLdHelper#build_request).
const peerFetchAccept = "application/activity+json, application/ld+json"

// activityStreamsContext is the JSON-LD context Mastodon requires of a fetched
// key document (ActivityPub::TagManager::CONTEXT).
const activityStreamsContext = "https://www.w3.org/ns/activitystreams"

// validActivityPubContentType reports whether Mastodon reads a fetched
// document with these response headers, as
// JsonLdHelper#valid_activitypub_content_type? decides: its media type is
// application/activity+json, or application/ld+json with a profile parameter
// that lists the ActivityStreams namespace.
func validActivityPubContentType(header http.Header) bool {
	contentType := header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil && !errors.Is(err, mime.ErrInvalidMediaParameter) {
		return false
	}
	switch mediaType {
	case "application/activity+json":
		return true
	case "application/ld+json":
		for _, part := range strings.Split(contentType, ";") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, `profile="`) && len(part) > len(`profile="`) &&
				slices.Contains(strings.Fields(part[len(`profile="`):len(part)-1]), activityStreamsContext) {
				return true
			}
		}
	}
	return false
}

// hasActivityStreamsContext reports whether document's @context is or lists
// the ActivityStreams context, as JsonLdHelper#supported_context? requires.
func hasActivityStreamsContext(document map[string]any) bool {
	switch context := document["@context"].(type) {
	case string:
		return context == activityStreamsContext
	case []any:
		return slices.Contains(context, any(activityStreamsContext))
	}
	return false
}

// mastodonPeer stands in for a Mastodon 4.5.3 server hosting the actor
// https://peer.example.com/users/bob. Its inbox accepts a delivery only when
// the request's draft-cavage signature verifies as Mastodon's SignedRequest
// (mastodon/app/lib/signed_request.rb) verifies it, with the key resolved
// from keyId over HTTP as ActivityPub::FetchRemoteKeyService resolves it.
// It fetches with Mastodon's Accept header and reads only documents that
// JsonLdHelper would read: a 200 response under an ActivityPub media type.
// Beyond Mastodon, it requires the key's id and owner to match keyId and the
// actor. It signs its own deliveries as Mastodon's Request does.
type mastodonPeer struct {
	key    *rsa.PrivateKey
	client *http.Client

	mu         sync.Mutex
	deliveries []peerDelivery
}

type peerDelivery struct {
	signer   string
	activity map[string]any
}

func newMastodonPeer(t *testing.T) *mastodonPeer {
	t.Helper()
	key, _, err := activitypub.GenerateKeyPair()
	if err != nil {
		t.Fatalf("peer key: %v", err)
	}
	return &mastodonPeer{key: key}
}

func (p *mastodonPeer) publicKeyPEM(t *testing.T) string {
	t.Helper()
	encoded, err := activitypub.EncodePublicKey(&p.key.PublicKey)
	if err != nil {
		t.Fatalf("encode peer key: %v", err)
	}
	return encoded
}

func (p *mastodonPeer) accepted() []peerDelivery {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.deliveries)
}

func (p *mastodonPeer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/users/bob/inbox" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	signer, err := p.verify(r, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	var activity map[string]any
	if err := json.Unmarshal(body, &activity); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// ActivityPub::ProcessCollectionService drops an activity whose actor is
	// not the signer unless a linked-data signature vouches for it.
	if activity["actor"] != signer {
		http.Error(w, "activity actor is not the signer", http.StatusUnauthorized)
		return
	}
	p.mu.Lock()
	p.deliveries = append(p.deliveries, peerDelivery{signer: signer, activity: activity})
	p.mu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

// verify checks the request signature and returns the signing actor.
func (p *mastodonPeer) verify(r *http.Request, body []byte) (string, error) {
	params, err := parseCavageSignature(r.Header.Get("Signature"))
	if err != nil {
		return "", err
	}
	keyID, signature := params["keyId"], params["signature"]
	if keyID == "" || signature == "" {
		return "", errors.New("keyId and signature are required")
	}
	algorithm := params["algorithm"]
	if algorithm == "" {
		algorithm = "hs2019"
	}
	if algorithm != "rsa-sha256" && algorithm != "hs2019" {
		return "", fmt.Errorf("unsupported signature algorithm %q", algorithm)
	}
	headersParam, ok := params["headers"]
	if !ok {
		headersParam = "date"
		if algorithm == "hs2019" {
			headersParam = "(created)"
		}
	}
	signed := strings.Fields(strings.ToLower(headersParam))

	if err := checkMastodonTimeWindow(r, params, algorithm); err != nil {
		return "", err
	}
	switch {
	case !slices.Contains(signed, "date") && !slices.Contains(signed, "(created)"):
		return "", errors.New("the Date header or (created) pseudo-header must be signed")
	case !slices.Contains(signed, "(request-target)") && !slices.Contains(signed, "digest"):
		return "", errors.New("the Digest header or (request-target) pseudo-header must be signed")
	case r.Method == http.MethodGet && !slices.Contains(signed, "host"):
		return "", errors.New("the Host header must be signed on GET")
	case r.Method == http.MethodPost && !slices.Contains(signed, "digest"):
		return "", errors.New("the Digest header must be signed on POST")
	}
	if slices.Contains(signed, "digest") {
		if err := checkMastodonDigest(r.Header.Get("Digest"), body); err != nil {
			return "", err
		}
	}

	owner, publicKey, err := p.fetchKey(keyID)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return "", fmt.Errorf("signature is not base64: %w", err)
	}
	// Mastodon also accepts a (request-target) signed without the query.
	for _, target := range []string{r.URL.RequestURI(), r.URL.Path} {
		signingString, err := mastodonSigningString(r, params, algorithm, signed, target)
		if err != nil {
			return "", err
		}
		digest := sha256.Sum256([]byte(signingString))
		if rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], raw) == nil {
			return owner, nil
		}
	}
	return "", fmt.Errorf("verification failed for %s", keyID)
}

func checkMastodonTimeWindow(r *http.Request, params map[string]string, algorithm string) error {
	var created, expires time.Time
	switch {
	case algorithm == "hs2019" && params["created"] != "":
		seconds, err := strconv.ParseInt(params["created"], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid (created): %w", err)
		}
		created = time.Unix(seconds, 0)
	case r.Header.Get("Date") != "":
		date, err := http.ParseTime(r.Header.Get("Date"))
		if err != nil {
			return fmt.Errorf("invalid Date header: %w", err)
		}
		created = date
	}
	if params["expires"] != "" {
		seconds, err := strconv.ParseInt(params["expires"], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid (expires): %w", err)
		}
		expires = time.Unix(seconds, 0)
	}
	if !created.IsZero() {
		if expires.IsZero() {
			expires = created.Add(5 * time.Minute)
		}
		if limit := created.Add(12 * time.Hour); expires.After(limit) {
			expires = limit
		}
	}
	now := time.Now()
	if !created.IsZero() && created.After(now.Add(time.Hour)) {
		return errors.New("signed request date outside acceptable time window")
	}
	if !expires.IsZero() && now.After(expires.Add(time.Hour)) {
		return errors.New("signed request date outside acceptable time window")
	}
	return nil
}

func checkMastodonDigest(header string, body []byte) error {
	if header == "" {
		return errors.New("digest header missing")
	}
	sum := sha256.Sum256(body)
	want := base64.StdEncoding.EncodeToString(sum[:])
	for _, part := range strings.Split(header, ",") {
		algorithm, value, _ := strings.Cut(part, "=")
		if strings.EqualFold(algorithm, "sha-256") {
			if value != want {
				return fmt.Errorf("invalid Digest value: computed %s, given %s", want, value)
			}
			return nil
		}
	}
	return fmt.Errorf("only SHA-256 is supported in Digest, offered %q", header)
}

func mastodonSigningString(r *http.Request, params map[string]string, algorithm string, signed []string, target string) (string, error) {
	lines := make([]string, 0, len(signed))
	for _, name := range signed {
		switch name {
		case "(request-target)":
			lines = append(lines, "(request-target): "+strings.ToLower(r.Method)+" "+target)
		case "(created)", "(expires)":
			param := strings.Trim(name, "()")
			if algorithm != "hs2019" || params[param] == "" {
				return "", fmt.Errorf("invalid pseudo-header %s", name)
			}
			lines = append(lines, name+": "+params[param])
		case "host":
			lines = append(lines, "host: "+r.Host)
		default:
			lines = append(lines, name+": "+strings.Join(r.Header.Values(name), ", "))
		}
	}
	return strings.Join(lines, "\n"), nil
}

// fetchKey resolves keyID as ActivityPub::FetchRemoteKeyService does for a
// key embedded in its actor: it fetches keyID following redirects, then
// refetches the document's id without following any and requires that
// document to carry the same id. It returns the actor and its key.
func (p *mastodonPeer) fetchKey(keyID string) (string, *rsa.PublicKey, error) {
	first, err := p.fetchDocument(keyID, true)
	if err != nil {
		return "", nil, err
	}
	actorURI, _ := first["id"].(string)
	if actorURI == "" {
		return "", nil, fmt.Errorf("unable to fetch key JSON at %s", keyID)
	}
	document, err := p.fetchDocument(actorURI, false)
	if err != nil {
		return "", nil, err
	}
	if document["id"] != actorURI {
		return "", nil, fmt.Errorf("unable to fetch key JSON at %s", keyID)
	}
	if !hasActivityStreamsContext(document) {
		return "", nil, fmt.Errorf("unsupported JSON-LD context for document %s", keyID)
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return "", nil, err
	}
	var actor activitypub.Actor
	if err := json.Unmarshal(raw, &actor); err != nil {
		return "", nil, fmt.Errorf("actor %s: %w", actorURI, err)
	}
	if !slices.Contains([]string{"Application", "Group", "Organization", "Person", "Service"}, actor.Type) {
		return "", nil, fmt.Errorf("unexpected object type %q for key %s", actor.Type, keyID)
	}
	keyURL, err := url.Parse(keyID)
	if err != nil {
		return "", nil, err
	}
	actorURL, err := url.Parse(actorURI)
	if err != nil {
		return "", nil, err
	}
	if !strings.EqualFold(keyURL.Host, actorURL.Host) {
		return "", nil, fmt.Errorf("key %s is not hosted with actor %s", keyID, actorURI)
	}
	if actor.PublicKey == nil || actor.PublicKey.PublicKeyPem == "" {
		return "", nil, fmt.Errorf("actor %s has no public key", actorURI)
	}
	if actor.PublicKey.ID != keyID || actor.PublicKey.Owner != actorURI {
		return "", nil, fmt.Errorf("key %s is not the key of %s (id %q, owner %q)", keyID, actorURI, actor.PublicKey.ID, actor.PublicKey.Owner)
	}
	block, _ := pem.Decode([]byte(actor.PublicKey.PublicKeyPem))
	if block == nil {
		return "", nil, fmt.Errorf("actor %s public key is not PEM", actorURI)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", nil, fmt.Errorf("actor %s public key: %w", actorURI, err)
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return "", nil, fmt.Errorf("actor %s public key is not RSA", actorURI)
	}
	return actorURI, publicKey, nil
}

// fetchDocument fetches an ActivityPub document as
// JsonLdHelper#fetch_resource_without_id_validation does: only a 200 response
// under an ActivityPub media type yields a document.
func (p *mastodonPeer) fetchDocument(uri string, allowRedirects bool) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", peerFetchAccept)
	client := *p.client
	if !allowRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", uri, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: status %d", uri, resp.StatusCode)
	}
	if !validActivityPubContentType(resp.Header) {
		return nil, fmt.Errorf("fetch %s: not an ActivityPub document: Content-Type %q", uri, resp.Header.Get("Content-Type"))
	}
	var document map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&document); err != nil {
		return nil, fmt.Errorf("fetch %s: %w", uri, err)
	}
	return document, nil
}

// deliver posts sent to inbox, signed over signedBody as Mastodon's Request
// signs a delivery: every header but User-Agent, Accept-Encoding and Accept,
// then (request-target). It returns the response status.
func (p *mastodonPeer) deliver(t *testing.T, inbox string, signedBody, sent []byte) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, inbox, bytes.NewReader(sent))
	if err != nil {
		t.Fatalf("delivery request: %v", err)
	}
	date := time.Now().UTC().Format(http.TimeFormat)
	sum := sha256.Sum256(signedBody)
	digest := "SHA-256=" + base64.StdEncoding.EncodeToString(sum[:])
	req.Header.Set("Date", date)
	req.Header.Set("Content-Type", "application/activity+json")
	req.Header.Set("Digest", digest)
	signingString := strings.Join([]string{
		"host: " + req.URL.Host,
		"date: " + date,
		"content-type: application/activity+json",
		"digest: " + digest,
		"(request-target): post " + req.URL.RequestURI(),
	}, "\n")
	hashed := sha256.Sum256([]byte(signingString))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("sign delivery: %v", err)
	}
	req.Header.Set("Signature", fmt.Sprintf(
		`keyId="%s#main-key",algorithm="rsa-sha256",headers="host date content-type digest (request-target)",signature="%s"`,
		keysTestPeerActor, base64.StdEncoding.EncodeToString(signature),
	))
	resp, err := p.client.Do(req)
	if err != nil {
		t.Fatalf("deliver to %s: %v", inbox, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// parseCavageSignature parses the parameters of a draft-cavage Signature
// header, as Mastodon's SignatureParser does.
func parseCavageSignature(header string) (map[string]string, error) {
	params := make(map[string]string)
	rest := strings.TrimSpace(header)
	for rest != "" {
		name, value, ok := strings.Cut(rest, "=")
		if !ok {
			return nil, fmt.Errorf("malformed signature parameter %q", rest)
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, `"`) {
			end := strings.Index(value[1:], `"`)
			if end < 0 {
				return nil, fmt.Errorf("unterminated signature parameter %q", name)
			}
			params[name] = value[1 : end+1]
			rest = value[end+2:]
		} else {
			token, tail, _ := strings.Cut(value, ",")
			params[name] = strings.TrimSpace(token)
			rest = "," + tail
			if tail == "" {
				rest = ""
			}
		}
		rest = strings.TrimSpace(rest)
		if rest != "" {
			if !strings.HasPrefix(rest, ",") {
				return nil, fmt.Errorf("malformed signature header %q", header)
			}
			rest = strings.TrimSpace(rest[1:])
		}
	}
	return params, nil
}

// TestValidActivityPubContentType checks the peer's media type check against
// the cases JsonLdHelper#valid_activitypub_content_type? distinguishes.
func TestValidActivityPubContentType(t *testing.T) {
	tests := []struct {
		contentType string
		want        bool
	}{
		{contentType: "application/activity+json", want: true},
		{contentType: "application/activity+json; charset=utf-8", want: true},
		{contentType: "Application/Activity+JSON", want: true},
		{contentType: `application/ld+json; profile="https://www.w3.org/ns/activitystreams"`, want: true},
		{contentType: `application/ld+json; profile="https://example.com/x https://www.w3.org/ns/activitystreams"`, want: true},
		{contentType: "application/ld+json", want: false},
		{contentType: `application/ld+json; profile="https://example.com/x"`, want: false},
		{contentType: "application/json", want: false},
		{contentType: "text/html; charset=utf-8", want: false},
		{contentType: "", want: false},
	}
	for _, tt := range tests {
		header := http.Header{}
		if tt.contentType != "" {
			header.Set("Content-Type", tt.contentType)
		}
		if got := validActivityPubContentType(header); got != tt.want {
			t.Errorf("validActivityPubContentType(%q) = %v, want %v", tt.contentType, got, tt.want)
		}
	}
}
