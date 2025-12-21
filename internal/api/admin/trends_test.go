package admin

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

func TestPreviewCardType(t *testing.T) {
	tests := []struct {
		input int32
		want  string
	}{
		{input: 0, want: "link"},
		{input: 1, want: "photo"},
		{input: 2, want: "video"},
		{input: 3, want: "rich"},
		{input: 99, want: "link"},
	}

	for _, tt := range tests {
		card := db.PreviewCard{Type: tt.input}
		if got := previewCardType(card); got != tt.want {
			t.Fatalf("previewCardType(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPreviewCardImageURL(t *testing.T) {
	card := db.PreviewCard{}
	if got := previewCardImageURL(card); got != nil {
		t.Fatalf("expected nil image URL, got %q", *got)
	}

	card.ImageFileName = pgtype.Text{String: "https://example.com/image.png", Valid: true}
	if got := previewCardImageURL(card); got == nil || *got != "https://example.com/image.png" {
		if got == nil {
			t.Fatalf("expected image URL, got nil")
		}
		t.Fatalf("image URL = %q, want %q", *got, "https://example.com/image.png")
	}

	card.ImageFileName = pgtype.Text{String: "image.png", Valid: true}
	if got := previewCardImageURL(card); got == nil || *got != "/files/image.png" {
		if got == nil {
			t.Fatalf("expected image URL, got nil")
		}
		t.Fatalf("image URL = %q, want %q", *got, "/files/image.png")
	}
}

func TestPreviewCardDomain(t *testing.T) {
	if got := previewCardDomain("https://example.com/path"); got != "example.com" {
		t.Fatalf("previewCardDomain = %q, want %q", got, "example.com")
	}
	if got := previewCardDomain("http://example.com:8080/test"); got != "example.com" {
		t.Fatalf("previewCardDomain = %q, want %q", got, "example.com")
	}
	if got := previewCardDomain("not a url"); got != "" {
		t.Fatalf("previewCardDomain = %q, want empty", got)
	}
}

func TestPreviewCardRequiresReview(t *testing.T) {
	card := db.PreviewCard{
		Trendable: pgtype.Bool{Bool: true, Valid: true},
	}
	if got := previewCardRequiresReview(card, nil); got != false {
		t.Fatalf("previewCardRequiresReview trendable = %v, want false", got)
	}

	card.Trendable = pgtype.Bool{Valid: false}
	if got := previewCardRequiresReview(card, nil); got != true {
		t.Fatalf("previewCardRequiresReview nil provider = %v, want true", got)
	}

	provider := &db.PreviewCardProvider{
		ReviewedAt: pgtype.Timestamptz{Valid: true},
	}
	if got := previewCardRequiresReview(card, provider); got != false {
		t.Fatalf("previewCardRequiresReview reviewed provider = %v, want false", got)
	}
}

func TestStatusRequiresReview(t *testing.T) {
	status := db.Status{
		Trendable: pgtype.Bool{Bool: true, Valid: true},
	}
	account := db.Account{
		ReviewedAt: pgtype.Timestamptz{Valid: false},
	}
	if got := statusRequiresReview(status, account); got != false {
		t.Fatalf("statusRequiresReview trendable = %v, want false", got)
	}

	status.Trendable = pgtype.Bool{Valid: false}
	if got := statusRequiresReview(status, account); got != true {
		t.Fatalf("statusRequiresReview unreviewed = %v, want true", got)
	}

	account.ReviewedAt = pgtype.Timestamptz{Valid: true}
	if got := statusRequiresReview(status, account); got != false {
		t.Fatalf("statusRequiresReview reviewed account = %v, want false", got)
	}
}
