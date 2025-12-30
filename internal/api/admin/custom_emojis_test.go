package admin

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

func TestMapAdminCustomEmoji_Category(t *testing.T) {
	emoji := db.CustomEmoji{
		ID:              12,
		Shortcode:       "party",
		Domain:          pgtype.Text{String: "", Valid: false},
		VisibleInPicker: true,
		Disabled:        false,
		ImageFileName:   pgtype.Text{String: "custom/party.png", Valid: true},
	}

	resp := mapAdminCustomEmoji(emoji, "Fun")
	if resp["category"] != "Fun" {
		t.Fatalf("category = %#v, want %q", resp["category"], "Fun")
	}

	resp = mapAdminCustomEmoji(emoji, "")
	if _, ok := resp["category"]; ok {
		t.Fatalf("category present for empty name")
	}
}
