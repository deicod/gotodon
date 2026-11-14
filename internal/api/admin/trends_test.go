package admin

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
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
	files := media.NewLocalStorage("", "https://social.example/files")
	tests := []struct {
		name  string
		image pgtype.Text
		want  *string
	}{
		{name: "no image", image: pgtype.Text{}, want: nil},
		{name: "blank image", image: pgtype.Text{String: "  ", Valid: true}, want: nil},
		{name: "remote image", image: pgtype.Text{String: "https://example.com/image.png", Valid: true}, want: ptr("https://example.com/image.png")},
		{name: "stored image", image: pgtype.Text{String: "preview_cards/images/image.png", Valid: true}, want: ptr("https://social.example/files/preview_cards/images/image.png")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := previewCardImageURL(files, db.PreviewCard{ImageFileName: tt.image})
			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("image URL = %q, want nil", *got)
			case tt.want != nil && got == nil:
				t.Fatalf("image URL = nil, want %q", *tt.want)
			case tt.want != nil && *got != *tt.want:
				t.Fatalf("image URL = %q, want %q", *got, *tt.want)
			}
		})
	}
}

func ptr(value string) *string { return &value }

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
