package workers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/federation"
	"github.com/deicod/gotodon/internal/media"
	"github.com/deicod/gotodon/internal/services"
	"github.com/deicod/gotodon/pkg/activitypub"
)

var errInjected = errors.New("injected failure")

// failingDeletionStore is the database with DeleteAccountBlocksBatch
// failing while failBlocks is set.
type failingDeletionStore struct {
	*db.Queries
	failBlocks bool
}

func (s *failingDeletionStore) DeleteAccountBlocksBatch(ctx context.Context, arg db.DeleteAccountBlocksBatchParams) (int64, error) {
	if s.failBlocks {
		return 0, errInjected
	}
	return s.Queries.DeleteAccountBlocksBatch(ctx, arg)
}

// failingTransactor runs status batches in real transactions, failing
// their final delete while *fail is set, after the batch's other changes.
type failingTransactor struct {
	inner services.DBTransactor
	fail  *bool
}

func (t failingTransactor) InTx(ctx context.Context, fn func(services.AccountDeletionBatchStore) error) error {
	return t.inner.InTx(ctx, func(store services.AccountDeletionBatchStore) error {
		return fn(failingBatchStore{AccountDeletionBatchStore: store, fail: t.fail})
	})
}

type failingBatchStore struct {
	services.AccountDeletionBatchStore
	fail *bool
}

func (s failingBatchStore) DeleteAccountStatusesByIDs(ctx context.Context, arg db.DeleteAccountStatusesByIDsParams) (int64, error) {
	if *s.fail {
		return 0, errInjected
	}
	return s.AccountDeletionBatchStore.DeleteAccountStatusesByIDs(ctx, arg)
}

// deletionWorld is a database with alice, a local account to delete, and
// the accounts and content around her.
type deletionWorld struct {
	t       *testing.T
	ctx     context.Context
	conn    *pgx.Conn
	queries *db.Queries
	storage *media.LocalStorage

	alice, carol, bob, erin, dave int64
	// alice's statuses: public, a reply to carol, direct to carol, and
	// one that carol's unresolved report names.
	public, reply, direct, reported int64
	// carol's statuses: one alice replies to, favourites and bookmarks,
	// one that mentions alice, and carol's reblog of alice's public one.
	carolStatus, carolMention, carolReblog int64
	aliceUser                              int64
	publicMedia, reportedMedia             string
}

func (w *deletionWorld) id(sql string, args ...any) int64 {
	w.t.Helper()
	var id int64
	if err := w.conn.QueryRow(w.ctx, sql, args...).Scan(&id); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return id
}

func (w *deletionWorld) count(sql string, args ...any) int64 {
	w.t.Helper()
	return w.id(sql, args...)
}

func (w *deletionWorld) file(path string) string {
	w.t.Helper()
	full := filepath.Join(w.storage.BasePath, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("image"), 0o644); err != nil {
		w.t.Fatal(err)
	}
	return path
}

func (w *deletionWorld) exists(path string) bool {
	_, err := os.Stat(filepath.Join(w.storage.BasePath, path))
	return err == nil
}

func newDeletionWorld(t *testing.T) *deletionWorld {
	conn := openMigratedTestDB(t)
	w := &deletionWorld{
		t:       t,
		ctx:     context.Background(),
		conn:    conn,
		queries: db.New(conn),
		storage: media.NewLocalStorage(t.TempDir(), "https://local.test/files"),
	}
	local := func(username string) int64 {
		id := w.id(`INSERT INTO accounts (username, uri, private_key, public_key, display_name, note) VALUES ($1, $2, 'private-key', 'public-key', $1, 'bio') RETURNING id`,
			username, "https://local.test/users/"+username)
		w.id(`INSERT INTO account_stats (account_id, statuses_count, followers_count, following_count) VALUES ($1, 4, 2, 1) RETURNING id`, id)
		return id
	}
	remote := func(domain, username, inbox, sharedInbox string) int64 {
		return w.id(`INSERT INTO accounts (username, domain, uri, inbox_url, shared_inbox_url) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			username, domain, "https://"+domain+"/users/"+username, inbox, sharedInbox)
	}
	status := func(account int64, visibility int, extra string, args ...any) int64 {
		id := w.id(`INSERT INTO statuses (account_id, text, visibility, local`+extra+`) VALUES ($1, 'text', $2, true`+placeholders(3, len(args))+`) RETURNING id`,
			append([]any{account, visibility}, args...)...)
		w.id(`INSERT INTO status_stats (status_id) VALUES ($1) RETURNING id`, id)
		return id
	}

	w.alice = local("alice")
	w.carol = local("carol")
	w.bob = remote("remote.example", "bob", "https://remote.example/users/bob/inbox", "https://remote.example/inbox")
	w.erin = remote("other.example", "erin", "https://other.example/users/erin/inbox", "")
	w.dave = remote("remote.example", "dave", "https://remote.example/users/dave/inbox", "https://remote.example/inbox")

	avatar := w.file("accounts/avatars/alice.png")
	if _, err := conn.Exec(w.ctx, `UPDATE accounts SET avatar_file_name = $2, locked = true, discoverable = true WHERE id = $1`, w.alice, avatar); err != nil {
		t.Fatal(err)
	}
	w.aliceUser = w.id(`INSERT INTO users (email, account_id, confirmed_at, approved) VALUES ('alice@local.test', $1, now(), true) RETURNING id`, w.alice)
	w.id(`INSERT INTO oauth_access_tokens (token, resource_owner_id, scopes) VALUES ('alice-token', $1, 'read') RETURNING id`, w.aliceUser)
	w.id(`INSERT INTO invites (user_id, code, uses) VALUES ($1, 'unused', 0) RETURNING id`, w.aliceUser)
	carolUser := w.id(`INSERT INTO users (email, account_id, confirmed_at, approved) VALUES ('carol@local.test', $1, now(), true) RETURNING id`, w.carol)
	w.id(`INSERT INTO oauth_access_tokens (token, resource_owner_id, scopes) VALUES ('carol-token', $1, 'read') RETURNING id`, carolUser)

	w.carolStatus = status(w.carol, 0, "")
	w.public = status(w.alice, 0, "")
	w.reply = status(w.alice, 0, ", in_reply_to_id, in_reply_to_account_id", w.carolStatus, w.carol)
	if _, err := conn.Exec(w.ctx, `UPDATE status_stats SET replies_count = 1, favourites_count = 1 WHERE status_id = $1`, w.carolStatus); err != nil {
		t.Fatal(err)
	}
	conversation := w.id(`INSERT INTO conversations DEFAULT VALUES RETURNING id`)
	w.direct = status(w.alice, 3, ", conversation_id", conversation)
	w.id(`INSERT INTO mentions (account_id, status_id) VALUES ($1, $2) RETURNING id`, w.carol, w.direct)
	w.id(`INSERT INTO account_conversations (account_id, conversation_id, participant_account_ids, status_ids, last_status_id) VALUES ($1, $2, ARRAY[$3::bigint], ARRAY[$4::bigint], $4) RETURNING id`,
		w.carol, conversation, w.alice, w.direct)
	w.id(`INSERT INTO account_conversations (account_id, conversation_id, participant_account_ids, status_ids, last_status_id) VALUES ($1, $2, ARRAY[$3::bigint], ARRAY[$4::bigint], $4) RETURNING id`,
		w.alice, conversation, w.carol, w.direct)
	w.reported = status(w.alice, 0, "")
	w.carolReblog = status(w.carol, 0, ", reblog_of_id", w.public)
	w.carolMention = status(w.carol, 0, "")
	w.id(`INSERT INTO mentions (account_id, status_id) VALUES ($1, $2) RETURNING id`, w.alice, w.carolMention)

	w.publicMedia = w.file("media_attachments/files/public.png")
	w.reportedMedia = w.file("media_attachments/files/reported.png")
	w.id(`INSERT INTO media_attachments (account_id, status_id, file_file_name) VALUES ($1, $2, $3) RETURNING id`, w.alice, w.public, w.publicMedia)
	w.id(`INSERT INTO media_attachments (account_id, status_id, file_file_name) VALUES ($1, $2, $3) RETURNING id`, w.alice, w.reported, w.reportedMedia)

	w.id(`INSERT INTO favourites (account_id, status_id) VALUES ($1, $2) RETURNING id`, w.alice, w.carolStatus)
	w.id(`INSERT INTO bookmarks (account_id, status_id) VALUES ($1, $2) RETURNING id`, w.alice, w.carolStatus)
	w.id(`INSERT INTO notifications (account_id, from_account_id, activity_type, activity_id) VALUES ($1, $2, 'Favourite', 1) RETURNING id`, w.carol, w.alice)
	w.id(`INSERT INTO notifications (account_id, from_account_id, activity_type, activity_id) VALUES ($1, $2, 'Follow', 2) RETURNING id`, w.alice, w.carol)
	w.id(`INSERT INTO notifications (account_id, from_account_id, activity_type, activity_id) VALUES ($1, $2, 'Follow', 3) RETURNING id`, w.carol, w.bob)
	for _, follow := range [][2]int64{{w.bob, w.alice}, {w.alice, w.carol}, {w.carol, w.alice}, {w.carol, w.bob}} {
		w.id(`INSERT INTO follows (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, follow[0], follow[1])
	}
	w.id(`INSERT INTO blocks (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, w.alice, w.erin)
	w.id(`INSERT INTO blocks (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, w.carol, w.erin)
	w.id(`INSERT INTO mutes (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, w.carol, w.alice)
	w.id(`INSERT INTO account_notes (account_id, target_account_id, comment) VALUES ($1, $2, 'about carol') RETURNING id`, w.alice, w.carol)
	w.id(`INSERT INTO account_notes (account_id, target_account_id, comment) VALUES ($1, $2, 'about alice') RETURNING id`, w.carol, w.alice)
	aliceList := w.id(`INSERT INTO lists (account_id, title) VALUES ($1, 'alice''s') RETURNING id`, w.alice)
	w.id(`INSERT INTO list_accounts (account_id, list_id) VALUES ($1, $2) RETURNING id`, w.carol, aliceList)
	carolList := w.id(`INSERT INTO lists (account_id, title) VALUES ($1, 'carol''s') RETURNING id`, w.carol)
	w.id(`INSERT INTO list_accounts (account_id, list_id) VALUES ($1, $2) RETURNING id`, w.alice, carolList)
	w.id(`INSERT INTO reports (account_id, target_account_id, status_ids) VALUES ($1, $2, ARRAY[$3::bigint]) RETURNING id`, w.carol, w.alice, w.reported)
	w.id(`INSERT INTO reports (account_id, target_account_id, status_ids, action_taken_at) VALUES ($1, $2, ARRAY[$3::bigint], now()) RETURNING id`, w.carol, w.alice, w.public)

	if err := w.queries.AdminSuspendAccount(w.ctx, w.alice); err != nil {
		t.Fatalf("suspend alice: %v", err)
	}
	return w
}

// placeholders lists $from... for n extra arguments.
func placeholders(from, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += fmt.Sprintf(", $%d", from+i)
	}
	return out
}

// deletionRun deletes accounts through AccountDeletionHandler and runs every
// delivery it queues through DeliveryHandler against the database.
type deletionRun struct {
	w          *deletionWorld
	store      *failingDeletionStore
	failStatus bool
	handler    *AccountDeletionHandler
	enqueuer   *taskEnqueuer
	deliveries []delivered
	delivery   *DeliveryHandler
}

func newDeletionRun(w *deletionWorld) *deletionRun {
	r := &deletionRun{w: w, store: &failingDeletionStore{Queries: w.queries}, enqueuer: &taskEnqueuer{}}
	outgoing := federation.NewOutgoingService(w.queries, "local.test", r.enqueuer)
	tx := failingTransactor{inner: services.DBTransactor{DB: w.conn, Queries: w.queries}, fail: &r.failStatus}
	service := services.NewAccountDeletionService(r.store, tx, outgoing, w.storage, nil, nil)
	r.handler = &AccountDeletionHandler{deleter: service}
	r.delivery = &DeliveryHandler{
		store: w.queries,
		deliver: func(_ context.Context, actor db.Account, inbox string, activity *activitypub.Activity) error {
			r.deliveries = append(r.deliveries, delivered{signer: actor.Username, suspended: actor.SuspendedAt.Valid, inbox: inbox, activity: activity})
			return nil
		},
	}
	return r
}

// run processes the admin deletion of accountID and returns its error and
// the deliveries it caused, each as "Type by signer to inbox".
func (r *deletionRun) run(accountID int64) (error, []string) {
	r.w.t.Helper()
	r.enqueuer.tasks, r.deliveries = nil, nil
	task, err := NewAccountDeletionTask(accountID, services.AdminAccountDeletionOptions())
	if err != nil {
		r.w.t.Fatalf("NewAccountDeletionTask: %v", err)
	}
	runErr := r.handler.ProcessTask(r.w.ctx, task)
	for _, task := range r.enqueuer.tasks {
		if err := r.delivery.ProcessTask(r.w.ctx, task); err != nil {
			r.w.t.Fatalf("delivery ProcessTask: %v", err)
		}
	}
	var got []string
	for _, d := range r.deliveries {
		got = append(got, fmt.Sprintf("%s %s by %s to %s", d.activity.Type, d.activity.ID, d.signer, d.inbox))
	}
	sort.Strings(got)
	return runErr, got
}

// TestAccountDeletionDeletesLocalAccount deletes alice as a moderator does,
// through the worker, and checks what is federated, what is gone and that
// nobody else's data went with it. A second run changes nothing.
func TestAccountDeletionDeletesLocalAccount(t *testing.T) {
	w := newDeletionWorld(t)
	r := newDeletionRun(w)

	err, got := r.run(w.alice)
	if err != nil {
		t.Fatalf("deletion: %v", err)
	}
	want := []string{
		"Delete https://local.test/users/alice#delete by alice to https://other.example/users/erin/inbox",
		"Delete https://local.test/users/alice#delete by alice to https://remote.example/inbox",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("deliveries = %v, want %v", got, want)
	}
	for _, d := range r.deliveries {
		if !d.suspended || d.activity.ActivityObject != "https://local.test/users/alice" {
			t.Fatalf("delivery %+v, want the suspended alice's actor Delete", d)
		}
	}
	w.assertAliceDeleted()

	// A finished deletion is left alone.
	err, got = r.run(w.alice)
	if err != nil || len(got) != 0 {
		t.Fatalf("second run: err %v, deliveries %v; want nothing", err, got)
	}
	w.assertAliceDeleted()
}

// TestAccountDeletionResumesAfterFailure fails alice's deletion twice, in a
// status batch and later in a batched delete, and runs it again each time:
// a failed run keeps the deletion request, a failed batch leaves nothing of
// itself behind, and the last run finishes the deletion.
func TestAccountDeletionResumesAfterFailure(t *testing.T) {
	w := newDeletionWorld(t)
	r := newDeletionRun(w)

	r.failStatus = true
	if err, _ := r.run(w.alice); !errors.Is(err, errInjected) {
		t.Fatalf("first run: err = %v, want the injected failure", err)
	}
	// The batch rolled back whole: the statuses, the reply count and
	// carol's conversation are as they were.
	if n := w.count(`SELECT count(*) FROM statuses WHERE account_id = $1`, w.alice); n != 4 {
		t.Fatalf("alice has %d statuses after a failed batch, want 4", n)
	}
	if n := w.count(`SELECT replies_count FROM status_stats WHERE status_id = $1`, w.carolStatus); n != 1 {
		t.Fatalf("replies_count = %d after a failed batch, want 1", n)
	}
	if n := w.count(`SELECT count(*) FROM account_conversations WHERE account_id = $1`, w.carol); n != 1 {
		t.Fatalf("carol has %d conversations after a failed batch, want 1", n)
	}
	// The steps before it are done, and the deletion is still pending.
	if n := w.count(`SELECT count(*) FROM accounts WHERE id = $1 AND display_name = ''`, w.alice); n != 1 {
		t.Fatal("alice's profile wasn't purged before the statuses")
	}
	w.assertPending(true)

	r.failStatus = false
	r.store.failBlocks = true
	if err, _ := r.run(w.alice); !errors.Is(err, errInjected) {
		t.Fatalf("second run: err = %v, want the injected failure", err)
	}
	if n := w.count(`SELECT count(*) FROM statuses WHERE account_id = $1`, w.alice); n != 1 {
		t.Fatalf("alice has %d statuses after the second run, want the reported one", n)
	}
	if n := w.count(`SELECT count(*) FROM blocks WHERE account_id = $1`, w.alice); n != 1 {
		t.Fatalf("alice has %d blocks after the failed block purge, want 1", n)
	}
	w.assertPending(true)

	r.store.failBlocks = false
	err, got := r.run(w.alice)
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("third run delivered %v, want the actor Delete to both inboxes again", got)
	}
	w.assertAliceDeleted()
}

// TestAccountDeletionDeletesRemoteAccount deletes dave, a remote account:
// his follow of carol is rejected and carol's follow of him undone, each
// signed by carol, and his record is kept suspended.
func TestAccountDeletionDeletesRemoteAccount(t *testing.T) {
	w := newDeletionWorld(t)
	r := newDeletionRun(w)
	w.id(`INSERT INTO follows (account_id, target_account_id, uri) VALUES ($1, $2, 'https://remote.example/follows/9') RETURNING id`, w.dave, w.carol)
	undone := w.id(`INSERT INTO follows (account_id, target_account_id) VALUES ($1, $2) RETURNING id`, w.carol, w.dave)
	daveStatus := w.id(`INSERT INTO statuses (account_id, text, uri) VALUES ($1, 'remote', 'https://remote.example/statuses/1') RETURNING id`, w.dave)
	if err := w.queries.AdminSuspendAccount(w.ctx, w.dave); err != nil {
		t.Fatalf("suspend dave: %v", err)
	}

	err, got := r.run(w.dave)
	if err != nil {
		t.Fatalf("deletion: %v", err)
	}
	var rejects, undos []string
	for _, d := range r.deliveries {
		follow, _ := d.activity.ActivityObject.(map[string]any)
		entry := fmt.Sprintf("%s by %s to %s of %v", d.activity.Type, d.signer, d.inbox, follow["id"])
		if d.activity.Type == activitypub.TypeReject {
			rejects = append(rejects, entry)
		} else {
			undos = append(undos, entry)
		}
	}
	if want := "[Reject by carol to https://remote.example/users/dave/inbox of https://remote.example/follows/9]"; fmt.Sprint(rejects) != want {
		t.Fatalf("rejects = %v, want %s (deliveries %v)", rejects, want, got)
	}
	if want := fmt.Sprintf("[Undo by carol to https://remote.example/users/dave/inbox of https://local.test/follows/%d]", undone); fmt.Sprint(undos) != want {
		t.Fatalf("undos = %v, want %s", undos, want)
	}
	if n := w.count(`SELECT count(*) FROM follows WHERE account_id = $1 OR target_account_id = $1`, w.dave); n != 0 {
		t.Fatalf("dave has %d follows left", n)
	}
	if n := w.count(`SELECT count(*) FROM statuses WHERE id = $1`, daveStatus); n != 0 {
		t.Fatal("dave's status is still there")
	}
	if n := w.count(`SELECT count(*) FROM accounts WHERE id = $1 AND suspended_at IS NOT NULL`, w.dave); n != 1 {
		t.Fatal("dave's record wasn't kept suspended")
	}
	if n := w.count(`SELECT count(*) FROM account_deletion_requests WHERE account_id = $1`, w.dave); n != 0 {
		t.Fatal("dave's deletion request is still there")
	}
	// Alice, whose deletion nobody asked for, keeps everything.
	if n := w.count(`SELECT count(*) FROM statuses WHERE account_id = $1`, w.alice); n != 4 {
		t.Fatalf("alice has %d statuses, want 4", n)
	}
}

// TestAccountDeletionDeletesUnconfirmedAccountWhole deletes an account whose
// user never confirmed: it goes whole, with no activity.
func TestAccountDeletionDeletesUnconfirmedAccountWhole(t *testing.T) {
	w := newDeletionWorld(t)
	r := newDeletionRun(w)
	if _, err := w.conn.Exec(w.ctx, `UPDATE users SET confirmed_at = NULL WHERE account_id = $1`, w.alice); err != nil {
		t.Fatal(err)
	}

	err, got := r.run(w.alice)
	if err != nil {
		t.Fatalf("deletion: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("deliveries = %v, want none", got)
	}
	for table, sql := range map[string]string{
		"accounts":   `SELECT count(*) FROM accounts WHERE id = $1`,
		"users":      `SELECT count(*) FROM users WHERE account_id = $1`,
		"statuses":   `SELECT count(*) FROM statuses WHERE account_id = $1`,
		"reports":    `SELECT count(*) FROM reports WHERE target_account_id = $1`,
		"media rows": `SELECT count(*) FROM media_attachments WHERE account_id = $1`,
	} {
		if n := w.count(sql, w.alice); n != 0 {
			t.Fatalf("%d %s left", n, table)
		}
	}
	if w.exists(w.publicMedia) || w.exists(w.reportedMedia) {
		t.Fatal("media files are still there")
	}
	w.assertOthersIntact()
}

func (w *deletionWorld) assertPending(want bool) {
	w.t.Helper()
	n := w.count(`SELECT count(*) FROM account_deletion_requests WHERE account_id = $1`, w.alice)
	if (n == 1) != want {
		w.t.Fatalf("alice has %d deletion requests, want pending = %v", n, want)
	}
}

// assertAliceDeleted checks what an admin deletion leaves of alice: her
// record, suspended for good with an empty profile, her disabled user, the
// reported status with its media, and the moderation history; and that
// carol's data is intact.
func (w *deletionWorld) assertAliceDeleted() {
	w.t.Helper()
	w.assertPending(false)

	var displayName, note string
	var locked, suspended bool
	if err := w.conn.QueryRow(w.ctx, `SELECT display_name, note, locked, suspended_at IS NOT NULL FROM accounts WHERE id = $1`, w.alice).
		Scan(&displayName, &note, &locked, &suspended); err != nil {
		w.t.Fatalf("alice's record: %v", err)
	}
	if displayName != "" || note != "" || locked || !suspended {
		w.t.Fatalf("alice = %q %q locked %v suspended %v, want an empty suspended profile", displayName, note, locked, suspended)
	}
	if n := w.count(`SELECT statuses_count + followers_count + following_count FROM account_stats WHERE account_id = $1`, w.alice); n != 0 {
		w.t.Fatalf("alice's counts add up to %d, want 0", n)
	}
	if n := w.count(`SELECT count(*) FROM users WHERE account_id = $1 AND disabled`, w.alice); n != 1 {
		w.t.Fatal("alice's user isn't kept disabled")
	}
	if n := w.count(`SELECT count(*) FROM oauth_access_tokens WHERE resource_owner_id = $1 AND revoked_at IS NULL`, w.aliceUser); n != 0 {
		w.t.Fatalf("alice has %d usable tokens", n)
	}
	if n := w.count(`SELECT count(*) FROM invites WHERE user_id = $1`, w.aliceUser); n != 0 {
		w.t.Fatalf("alice has %d unused invites", n)
	}

	if n := w.count(`SELECT count(*) FROM statuses WHERE account_id = $1 AND id <> $2`, w.alice, w.reported); n != 0 {
		w.t.Fatalf("alice has %d statuses besides the reported one", n)
	}
	if n := w.count(`SELECT count(*) FROM statuses WHERE id = $1`, w.reported); n != 1 {
		w.t.Fatal("the reported status is gone")
	}
	if n := w.count(`SELECT count(*) FROM media_attachments WHERE account_id = $1`, w.alice); n != 1 || !w.exists(w.reportedMedia) {
		w.t.Fatalf("alice has %d attachments, want the reported status's, with its file", n)
	}
	if w.exists(w.publicMedia) || w.exists("accounts/avatars/alice.png") {
		w.t.Fatal("a deleted media file or the avatar is still there")
	}
	for table, sql := range map[string]string{
		"mentions":         `SELECT count(*) FROM mentions WHERE account_id = $1`,
		"favourites":       `SELECT count(*) FROM favourites WHERE account_id = $1`,
		"bookmarks":        `SELECT count(*) FROM bookmarks WHERE account_id = $1`,
		"notifications":    `SELECT count(*) FROM notifications WHERE account_id = $1 OR from_account_id = $1`,
		"follows":          `SELECT count(*) FROM follows WHERE account_id = $1 OR target_account_id = $1`,
		"blocks":           `SELECT count(*) FROM blocks WHERE account_id = $1 OR target_account_id = $1`,
		"mutes":            `SELECT count(*) FROM mutes WHERE account_id = $1 OR target_account_id = $1`,
		"conversations":    `SELECT count(*) FROM account_conversations WHERE account_id = $1`,
		"notes":            `SELECT count(*) FROM account_notes WHERE account_id = $1`,
		"lists":            `SELECT count(*) FROM lists WHERE account_id = $1`,
		"list memberships": `SELECT count(*) FROM list_accounts WHERE account_id = $1`,
	} {
		if n := w.count(sql, w.alice); n != 0 {
			w.t.Fatalf("alice has %d %s left", n, table)
		}
	}
	// The reports about her, and carol's note about her, are kept.
	if n := w.count(`SELECT count(*) FROM reports WHERE target_account_id = $1`, w.alice); n != 2 {
		w.t.Fatalf("%d reports about alice, want both kept", n)
	}
	if n := w.count(`SELECT count(*) FROM account_notes WHERE target_account_id = $1`, w.alice); n != 1 {
		w.t.Fatal("carol's note about alice is gone")
	}
	w.assertOthersIntact()
}

// assertOthersIntact checks that carol's data is intact, but for what was
// about alice: carol's reblog of alice's status, carol's conversation with
// her, her mute of alice and her follow of alice.
func (w *deletionWorld) assertOthersIntact() {
	w.t.Helper()
	if n := w.count(`SELECT count(*) FROM statuses WHERE account_id = $1`, w.carol); n != 2 {
		w.t.Fatalf("carol has %d statuses, want her own two (her reblog of alice's status goes with it)", n)
	}
	if n := w.count(`SELECT count(*) FROM statuses WHERE id = $1`, w.carolReblog); n != 0 {
		w.t.Fatal("carol's reblog of alice's deleted status is still there")
	}
	var replies, favourites int64
	if err := w.conn.QueryRow(w.ctx, `SELECT replies_count, favourites_count FROM status_stats WHERE status_id = $1`, w.carolStatus).Scan(&replies, &favourites); err != nil {
		w.t.Fatalf("carol's status stats: %v", err)
	}
	if replies != 0 || favourites != 0 {
		w.t.Fatalf("carol's status counts %d replies and %d favourites, want alice's taken out", replies, favourites)
	}
	if n := w.count(`SELECT count(*) FROM account_conversations WHERE account_id = $1`, w.carol); n != 0 {
		w.t.Fatal("carol's conversation with alice is still there")
	}
	if n := w.count(`SELECT count(*) FROM notifications WHERE account_id = $1`, w.carol); n != 1 {
		w.t.Fatalf("carol has %d notifications, want bob's", n)
	}
	if n := w.count(`SELECT count(*) FROM follows WHERE account_id = $1`, w.carol); n != 1 {
		w.t.Fatalf("carol has %d follows, want hers of bob", n)
	}
	if n := w.count(`SELECT count(*) FROM blocks WHERE account_id = $1`, w.carol); n != 1 {
		w.t.Fatal("carol's block of erin is gone")
	}
	if n := w.count(`SELECT count(*) FROM lists WHERE account_id = $1`, w.carol); n != 1 {
		w.t.Fatal("carol's list is gone")
	}
	if n := w.count(`SELECT count(*) FROM oauth_access_tokens WHERE token = 'carol-token' AND revoked_at IS NULL`); n != 1 {
		w.t.Fatal("carol's token was revoked")
	}
	if n := w.count(`SELECT count(*) FROM users WHERE account_id = $1 AND NOT disabled`, w.carol); n != 1 {
		w.t.Fatal("carol's user was disabled")
	}
	if n := w.count(`SELECT count(*) FROM accounts WHERE id = $1 AND display_name = 'carol'`, w.carol); n != 1 {
		w.t.Fatal("carol's profile changed")
	}
}
