package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

func TestTrendsHandler_Links_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewTrendsHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/trends/links", nil)
	rec := httptest.NewRecorder()

	h.Links(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var links []TrendingLink
	if err := json.Unmarshal(rec.Body.Bytes(), &links); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("expected 0 links, got %d", len(links))
	}
}

func TestTrendsHandler_IndexAlias_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewTrendsHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/trends", nil)
	rec := httptest.NewRecorder()

	h.Index(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var tags []TrendingTag
	if err := json.Unmarshal(rec.Body.Bytes(), &tags); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("expected 0 tags, got %d", len(tags))
	}
}

func TestTrendsHandler_Statuses_MockMode(t *testing.T) {
	cfg := &config.Config{Instance: config.InstanceConfig{LocalDomain: "example.com"}}
	h := NewTrendsHandler(nil, cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/trends/statuses", nil)
	rec := httptest.NewRecorder()

	h.Statuses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var statuses []Status
	if err := json.Unmarshal(rec.Body.Bytes(), &statuses); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}
	if len(statuses) != 0 {
		t.Fatalf("expected 0 statuses, got %d", len(statuses))
	}
}

func TestSerializePreviewCard_Metadata(t *testing.T) {
	publishedAt := time.Date(2024, 7, 1, 12, 0, 0, 0, time.UTC)
	card := db.PreviewCard{
		ID:               123,
		Url:              "https://example.com/card",
		Title:            "Card Title",
		Description:      "Card Description",
		Type:             1,
		AuthorName:       "Author Name",
		AuthorUrl:        "https://example.com/author",
		ProviderName:     "Provider",
		ProviderUrl:      "https://example.com/provider",
		Html:             "<p>Preview</p>",
		Width:            640,
		Height:           360,
		ImageFileName:    pgtype.Text{String: "cards/image.png", Valid: true},
		Blurhash:         pgtype.Text{String: "abcd1234", Valid: true},
		Language:         pgtype.Text{String: "en", Valid: true},
		EmbedUrl:         "https://example.com/embed",
		ImageDescription: "Image description",
		PublishedAt:      pgtype.Timestamptz{Time: publishedAt, Valid: true},
	}

	preview := serializePreviewCard(card, nil)
	if preview == nil {
		t.Fatal("expected preview card, got nil")
	}
	if preview.Language == nil || *preview.Language != "en" {
		t.Fatalf("language = %v, want en", preview.Language)
	}
	if preview.Image == nil || *preview.Image != "/files/cards/image.png" {
		t.Fatalf("image = %v, want /files/cards/image.png", preview.Image)
	}
	if preview.Blurhash == nil || *preview.Blurhash != "abcd1234" {
		t.Fatalf("blurhash = %v, want abcd1234", preview.Blurhash)
	}
	if preview.PublishedAt == nil || !preview.PublishedAt.Equal(publishedAt) {
		t.Fatalf("published_at = %v, want %v", preview.PublishedAt, publishedAt)
	}
	if preview.ImageDescription != "Image description" {
		t.Fatalf("image_description = %q, want %q", preview.ImageDescription, "Image description")
	}
	if preview.EmbedURL != "https://example.com/embed" {
		t.Fatalf("embed_url = %q, want %q", preview.EmbedURL, "https://example.com/embed")
	}
	if preview.Type != "photo" {
		t.Fatalf("type = %q, want %q", preview.Type, "photo")
	}
}

func TestSerializePreviewCard_Authors(t *testing.T) {
	card := db.PreviewCard{
		ID:    1,
		Url:   "https://example.com/card",
		Title: "Card",
	}

	preview := serializePreviewCard(card, nil)
	if preview == nil {
		t.Fatal("expected preview card, got nil")
	}
	if len(preview.Authors) != 0 {
		t.Fatalf("authors length = %d, want 0", len(preview.Authors))
	}

	card.AuthorName = "Ada"
	card.AuthorUrl = "https://example.com/ada"
	card.AuthorAccountID = pgtype.Int8{Int64: 42, Valid: true}

	authorAccounts := map[int64]Account{
		42: {
			ID:       "42",
			Username: "ada",
		},
	}

	preview = serializePreviewCard(card, authorAccounts)
	if len(preview.Authors) != 1 {
		t.Fatalf("authors length = %d, want 1", len(preview.Authors))
	}
	if preview.Authors[0].Name != "Ada" {
		t.Fatalf("author name = %q, want %q", preview.Authors[0].Name, "Ada")
	}
	if preview.Authors[0].URL != "https://example.com/ada" {
		t.Fatalf("author url = %q, want %q", preview.Authors[0].URL, "https://example.com/ada")
	}
	if preview.Authors[0].Account == nil || preview.Authors[0].Account.ID != "42" {
		t.Fatalf("author account = %#v, want ID 42", preview.Authors[0].Account)
	}
}
