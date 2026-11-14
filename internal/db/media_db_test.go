package db

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestCreateMediaAttachmentStoresCreatedAt checks that an attachment keeps
// the upload time it is given, which dates its file's storage path, through
// a round trip that returns it in the server's local zone.
func TestCreateMediaAttachmentStoresCreatedAt(t *testing.T) {
	conn := openMigratedDB(t)
	q := New(conn)
	ctx := context.Background()

	var accountID int64
	if err := conn.QueryRow(ctx,
		`INSERT INTO accounts (username, uri) VALUES ('alice', 'https://example.com/users/alice') RETURNING id`,
	).Scan(&accountID); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	// A minute before midnight UTC: the database clock is a day later in
	// zones east of UTC and would date the path differently.
	createdAt := time.Date(2026, 11, 13, 23, 59, 0, 123456000, time.UTC)
	created, err := q.CreateMediaAttachment(ctx, CreateMediaAttachmentParams{
		AccountID:       pgtype.Int8{Int64: accountID, Valid: true},
		FileFileName:    pgtype.Text{String: "0123abcd.png", Valid: true},
		FileContentType: pgtype.Text{String: "image/png", Valid: true},
		FileFileSize:    pgtype.Int4{Int32: 68, Valid: true},
		Type:            0,
		Processing:      pgtype.Int4{Int32: 2, Valid: true},
		Shortcode:       pgtype.Text{String: "abc", Valid: true},
		CreatedAt:       pgtype.Timestamptz{Time: createdAt, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateMediaAttachment: %v", err)
	}
	if !created.CreatedAt.Valid || !created.CreatedAt.Time.Equal(createdAt) {
		t.Fatalf("created_at = %v, want %v", created.CreatedAt, createdAt)
	}
	if !created.UpdatedAt.Valid || !created.UpdatedAt.Time.Equal(createdAt) {
		t.Fatalf("updated_at = %v, want %v", created.UpdatedAt, createdAt)
	}

	loaded, err := q.GetMediaAttachment(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMediaAttachment: %v", err)
	}
	if !loaded.CreatedAt.Time.Equal(createdAt) {
		t.Fatalf("loaded created_at = %v, want %v", loaded.CreatedAt.Time, createdAt)
	}
	if got := loaded.CreatedAt.Time.UTC().Format("2006/01/02"); got != "2026/11/13" {
		t.Fatalf("UTC date = %s, want 2026/11/13", got)
	}
}
