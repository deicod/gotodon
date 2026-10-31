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
