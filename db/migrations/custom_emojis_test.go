package migrations

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
)

// insertCustomEmoji inserts a custom emoji and returns the unique index it
// violated, or "" when the insert succeeded. A domain of "" inserts a local
// emoji.
func insertCustomEmoji(t *testing.T, db *sql.DB, shortcode, domain string) string {
	t.Helper()

	var domainValue any
	if domain != "" {
		domainValue = domain
	}
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO custom_emojis (shortcode, domain) VALUES ($1, $2)`, shortcode, domainValue)
	if err == nil {
		return ""
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName
	}
	t.Fatalf("insert custom emoji %q (domain %q): %v", shortcode, domain, err)
	return ""
}

func TestUniqueLocalCustomEmojiShortcodesMigration(t *testing.T) {
	const (
		before = 47
		target = 48

		localIndex  = "index_custom_emojis_on_shortcode_local"
		domainIndex = "index_custom_emojis_on_shortcode_and_domain"
	)

	db, schema := openSchemaTestDB(t)
	ctx := context.Background()
	migrateUpTo(t, db, before)

	// Before 048, two local emojis can share a shortcode.
	if violated := insertCustomEmoji(t, db, "blobcat", ""); violated != "" {
		t.Fatalf("first local blobcat violated %s", violated)
	}
	if violated := insertCustomEmoji(t, db, "blobcat", ""); violated != "" {
		t.Fatalf("before %d, a duplicate local shortcode violated %s", target, violated)
	}

	// The migration refuses to run over the duplicates and leaves the
	// schema at the previous version.
	configureGoose(t)
	if err := goose.UpTo(db, ".", target); err == nil {
		t.Fatalf("migrating to %d succeeded with duplicate local shortcodes", target)
	}
	version, err := goose.GetDBVersion(db)
	if err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if version != before {
		t.Fatalf("version after the failed migration = %d, want %d", version, before)
	}

	if _, err := db.ExecContext(ctx,
		`DELETE FROM custom_emojis WHERE id = (SELECT max(id) FROM custom_emojis WHERE shortcode = 'blobcat')`,
	); err != nil {
		t.Fatalf("remove duplicate: %v", err)
	}
	migrateUpTo(t, db, target)

	unique, err := lookupIndexUnique(ctx, db, schema, localIndex)
	if err != nil {
		t.Fatalf("look up %s: %v", localIndex, err)
	}
	if !unique {
		t.Fatalf("%s is not unique", localIndex)
	}

	cases := []struct {
		name      string
		shortcode string
		domain    string
		violates  string
	}{
		{"duplicate local shortcode", "blobcat", "", localIndex},
		{"local shortcode differing in case", "BlobCat", "", ""},
		{"remote emoji with a local emoji's shortcode", "blobcat", "example.com", ""},
		{"duplicate remote shortcode on the same domain", "blobcat", "example.com", domainIndex},
		{"remote shortcode on another domain", "blobcat", "example.org", ""},
		{"new local shortcode", "blobfox", "", ""},
	}
	for _, tc := range cases {
		if got := insertCustomEmoji(t, db, tc.shortcode, tc.domain); got != tc.violates {
			t.Errorf("%s: violated %q, want %q", tc.name, got, tc.violates)
		}
	}

	migrateDownTo(t, db, before)

	if _, err := lookupIndexUnique(ctx, db, schema, localIndex); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("%s after down: err = %v, want it dropped", localIndex, err)
	}
	if violated := insertCustomEmoji(t, db, "blobcat", ""); violated != "" {
		t.Fatalf("after down, a duplicate local shortcode violated %s", violated)
	}
}

func TestAnnouncementReactionsCustomEmojiForeignKeyMigration(t *testing.T) {
	const (
		before = 48
		target = 49

		constraint = "fk_announcement_reactions_custom_emoji"
	)

	db, _ := openSchemaTestDB(t)
	ctx := context.Background()
	migrateUpTo(t, db, before)

	queryID := func(query string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := db.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return id
	}
	addReaction := func(announcementID int64, name string, customEmojiID any) (int64, error) {
		var id int64
		err := db.QueryRowContext(ctx,
			`INSERT INTO announcement_reactions (announcement_id, name, custom_emoji_id) VALUES ($1, $2, $3) RETURNING id`,
			announcementID, name, customEmojiID,
		).Scan(&id)
		return id, err
	}
	reactionExists := func(id int64) bool {
		t.Helper()
		var exists bool
		if err := db.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM announcement_reactions WHERE id = $1)`, id,
		).Scan(&exists); err != nil {
			t.Fatalf("look up reaction %d: %v", id, err)
		}
		return exists
	}
	mustAddReaction := func(announcementID int64, name string, customEmojiID any) int64 {
		t.Helper()
		id, err := addReaction(announcementID, name, customEmojiID)
		if err != nil {
			t.Fatalf("add reaction %q: %v", name, err)
		}
		return id
	}

	announcement := queryID(`INSERT INTO announcements (text) VALUES ('hello') RETURNING id`)
	blobcat := queryID(`INSERT INTO custom_emojis (shortcode) VALUES ('blobcat') RETURNING id`)
	blobfox := queryID(`INSERT INTO custom_emojis (shortcode) VALUES ('blobfox') RETURNING id`)
	missing := queryID(`SELECT coalesce(max(id), 0) + 1000 FROM custom_emojis`)

	withBlobcat := mustAddReaction(announcement, "blobcat", blobcat)
	withBlobfox := mustAddReaction(announcement, "blobfox", blobfox)
	unicode := mustAddReaction(announcement, "👍", nil)
	orphaned := mustAddReaction(announcement, "gone", missing)

	migrateUpTo(t, db, target)

	for _, tc := range []struct {
		name string
		id   int64
		want bool
	}{
		{"reaction with an existing custom emoji", withBlobcat, true},
		{"another reaction with an existing custom emoji", withBlobfox, true},
		{"unicode reaction", unicode, true},
		{"reaction whose custom emoji is gone", orphaned, false},
	} {
		if got := reactionExists(tc.id); got != tc.want {
			t.Errorf("after up, %s exists = %t, want %t", tc.name, got, tc.want)
		}
	}

	_, err := addReaction(announcement, "gone", missing)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" || pgErr.ConstraintName != constraint {
		t.Fatalf("reaction with a missing custom emoji: err = %v, want a %s violation", err, constraint)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM custom_emojis WHERE id = $1`, blobcat); err != nil {
		t.Fatalf("delete custom emoji: %v", err)
	}
	if reactionExists(withBlobcat) {
		t.Error("reaction outlived its deleted custom emoji")
	}
	if !reactionExists(withBlobfox) || !reactionExists(unicode) {
		t.Error("deleting one custom emoji deleted reactions made with something else")
	}

	migrateDownTo(t, db, before)

	if _, err := addReaction(announcement, "gone", missing); err != nil {
		t.Fatalf("after down, reaction with a missing custom emoji: %v", err)
	}
}
