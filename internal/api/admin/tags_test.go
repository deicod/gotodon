package admin

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

func TestTagDisplayName(t *testing.T) {
	base := db.Tag{Name: "golang"}

	if got := tagDisplayName(base); got != "golang" {
		t.Fatalf("tagDisplayName default = %q, want %q", got, "golang")
	}

	base.DisplayName = pgtype.Text{String: "GoLang", Valid: true}
	if got := tagDisplayName(base); got != "GoLang" {
		t.Fatalf("tagDisplayName display = %q, want %q", got, "GoLang")
	}

	base.DisplayName = pgtype.Text{String: "   ", Valid: true}
	if got := tagDisplayName(base); got != "golang" {
		t.Fatalf("tagDisplayName blank = %q, want %q", got, "golang")
	}
}

func TestTagBoolOrDefault(t *testing.T) {
	if got := tagBoolOrDefault(pgtype.Bool{Valid: false}, true); got != true {
		t.Fatalf("tagBoolOrDefault invalid = %v, want true", got)
	}
	if got := tagBoolOrDefault(pgtype.Bool{Bool: false, Valid: true}, true); got != false {
		t.Fatalf("tagBoolOrDefault false = %v, want false", got)
	}
}

func TestTagRequiresReview(t *testing.T) {
	if got := tagRequiresReview(pgtype.Timestamptz{Valid: false}); got != true {
		t.Fatalf("tagRequiresReview invalid = %v, want true", got)
	}
	if got := tagRequiresReview(pgtype.Timestamptz{Valid: true}); got != false {
		t.Fatalf("tagRequiresReview valid = %v, want false", got)
	}
}

func TestMapAdminTag(t *testing.T) {
	tag := db.Tag{
		ID:   12,
		Name: "golang",
		DisplayName: pgtype.Text{
			String: "GoLang",
			Valid:  true,
		},
		Usable:    pgtype.Bool{Valid: false},
		Listable:  pgtype.Bool{Valid: false},
		Trendable: pgtype.Bool{Valid: false},
	}

	resp := mapAdminTag(tag)
	if resp.ID != "12" {
		t.Fatalf("mapAdminTag ID = %q, want %q", resp.ID, "12")
	}
	if resp.Name != "GoLang" {
		t.Fatalf("mapAdminTag Name = %q, want %q", resp.Name, "GoLang")
	}
	if resp.URL != "/tags/golang" {
		t.Fatalf("mapAdminTag URL = %q, want %q", resp.URL, "/tags/golang")
	}
	if resp.Usable != true {
		t.Fatalf("mapAdminTag Usable = %v, want true", resp.Usable)
	}
	if resp.Listable != true {
		t.Fatalf("mapAdminTag Listable = %v, want true", resp.Listable)
	}
	if resp.Trendable != false {
		t.Fatalf("mapAdminTag Trendable = %v, want false", resp.Trendable)
	}
	if resp.RequiresReview != true {
		t.Fatalf("mapAdminTag RequiresReview = %v, want true", resp.RequiresReview)
	}
	if len(resp.History) != 0 {
		t.Fatalf("mapAdminTag History len = %d, want 0", len(resp.History))
	}
}
