package services

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

type stubLinkCrawlerStore struct {
	status            db.Status
	statusErr         error
	cards             map[string]db.PreviewCard
	providers         map[string]db.PreviewCardProvider
	createdCards      []db.CreatePreviewCardParams
	createdProviders  []db.CreatePreviewCardProviderParams
	attached          []db.AttachPreviewCardToStatusParams
	nextPreviewCardID int64
}

func (s *stubLinkCrawlerStore) GetStatusByID(ctx context.Context, id int64) (db.Status, error) {
	if s.statusErr != nil {
		return db.Status{}, s.statusErr
	}
	return s.status, nil
}

func (s *stubLinkCrawlerStore) GetPreviewCardByURL(ctx context.Context, cardURL string) (db.PreviewCard, error) {
	if s.cards == nil {
		return db.PreviewCard{}, pgx.ErrNoRows
	}
	if card, ok := s.cards[cardURL]; ok {
		return card, nil
	}
	return db.PreviewCard{}, pgx.ErrNoRows
}

func (s *stubLinkCrawlerStore) CreatePreviewCard(ctx context.Context, arg db.CreatePreviewCardParams) (db.PreviewCard, error) {
	s.createdCards = append(s.createdCards, arg)
	s.nextPreviewCardID++
	card := db.PreviewCard{
		ID:               s.nextPreviewCardID,
		Url:              arg.Url,
		Title:            arg.Title,
		Description:      arg.Description,
		ImageFileName:    arg.ImageFileName,
		ImageContentType: arg.ImageContentType,
		ImageFileSize:    arg.ImageFileSize,
		ImageUpdatedAt:   arg.ImageUpdatedAt,
		Type:             arg.Type,
		Html:             arg.Html,
		AuthorName:       arg.AuthorName,
		AuthorUrl:        arg.AuthorUrl,
		ProviderName:     arg.ProviderName,
		ProviderUrl:      arg.ProviderUrl,
		Width:            arg.Width,
		Height:           arg.Height,
		EmbedUrl:         arg.EmbedUrl,
		Blurhash:         arg.Blurhash,
		Language:         arg.Language,
		Trendable:        arg.Trendable,
		LinkType:         arg.LinkType,
		PublishedAt:      arg.PublishedAt,
		ImageDescription: arg.ImageDescription,
		AuthorAccountID:  arg.AuthorAccountID,
	}
	if s.cards == nil {
		s.cards = make(map[string]db.PreviewCard)
	}
	s.cards[arg.Url] = card
	return card, nil
}

func (s *stubLinkCrawlerStore) AttachPreviewCardToStatus(ctx context.Context, arg db.AttachPreviewCardToStatusParams) error {
	s.attached = append(s.attached, arg)
	return nil
}

func (s *stubLinkCrawlerStore) GetPreviewCardProviderByDomain(ctx context.Context, domain string) (db.PreviewCardProvider, error) {
	if s.providers == nil {
		return db.PreviewCardProvider{}, pgx.ErrNoRows
	}
	if provider, ok := s.providers[domain]; ok {
		return provider, nil
	}
	return db.PreviewCardProvider{}, pgx.ErrNoRows
}

func (s *stubLinkCrawlerStore) CreatePreviewCardProvider(ctx context.Context, arg db.CreatePreviewCardProviderParams) (db.PreviewCardProvider, error) {
	s.createdProviders = append(s.createdProviders, arg)
	provider := db.PreviewCardProvider{ID: int64(len(s.createdProviders)), Domain: arg.Domain, Trendable: arg.Trendable}
	if s.providers == nil {
		s.providers = make(map[string]db.PreviewCardProvider)
	}
	s.providers[arg.Domain] = provider
	return provider, nil
}

type memLinkStorage struct {
	files map[string][]byte
}

func (m *memLinkStorage) Save(ctx context.Context, path string, data []byte) error {
	if m.files == nil {
		m.files = make(map[string][]byte)
	}
	copyData := make([]byte, len(data))
	copy(copyData, data)
	m.files[path] = copyData
	return nil
}

func (m *memLinkStorage) Load(ctx context.Context, path string) ([]byte, error) {
	if m.files == nil {
		return nil, os.ErrNotExist
	}
	data, ok := m.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	copyData := make([]byte, len(data))
	copy(copyData, data)
	return copyData, nil
}

func (m *memLinkStorage) Delete(ctx context.Context, path string) error {
	if m.files != nil {
		delete(m.files, path)
	}
	return nil
}

func (m *memLinkStorage) URL(path string) string {
	return "https://example.com/" + path
}

func (m *memLinkStorage) Exists(ctx context.Context, path string) (bool, error) {
	_, ok := m.files[path]
	return ok, nil
}

func createTestPNG(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func linkCrawlerHTML(imageURL, canonicalURL string) string {
	return fmt.Sprintf(`<!doctype html>
<html>
<head>
<meta property="og:title" content="OG Title">
<meta property="og:description" content="OG Desc">
<meta property="og:image" content="%s">
<meta property="og:site_name" content="Example Site">
<meta name="author" content="Author Name">
<meta property="article:author" content="https://author.example/@writer">
<meta property="og:type" content="article">
<meta property="og:image:alt" content="Alt text">
<link rel="canonical" href="%s">
</head>
<body>ok</body>
</html>`, imageURL, canonicalURL)
}

func TestLinkCrawlerCrawlCreatesPreviewCard(t *testing.T) {
	imageData := createTestPNG(120, 80)
	mux := http.NewServeMux()
	var canonicalURL string

	mux.HandleFunc("/image.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageData)
	})
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(linkCrawlerHTML("/image.png", canonicalURL)))
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	canonicalURL = server.URL + "/canonical"

	store := &stubLinkCrawlerStore{
		status: db.Status{ID: 1, Local: pgtype.Bool{Bool: true, Valid: true}},
	}
	storage := &memLinkStorage{}
	crawler := NewLinkCrawler(store, storage)
	crawler.AllowPrivateNetworks(true)

	if err := crawler.Crawl(context.Background(), 1, server.URL+"/page"); err != nil {
		t.Fatalf("Crawl error = %v", err)
	}

	if len(store.createdCards) != 1 {
		t.Fatalf("createdCards = %d, want 1", len(store.createdCards))
	}
	params := store.createdCards[0]
	if params.Url != canonicalURL {
		t.Fatalf("Url = %s, want %s", params.Url, canonicalURL)
	}
	if params.Title != "OG Title" {
		t.Fatalf("Title = %s, want OG Title", params.Title)
	}
	if params.Description != "OG Desc" {
		t.Fatalf("Description = %s, want OG Desc", params.Description)
	}
	if params.ProviderName != "Example Site" {
		t.Fatalf("ProviderName = %s, want Example Site", params.ProviderName)
	}
	baseURL, _ := url.Parse(server.URL)
	if params.ProviderUrl != baseURL.Scheme+"://"+baseURL.Host {
		t.Fatalf("ProviderUrl = %s, want %s", params.ProviderUrl, baseURL.Scheme+"://"+baseURL.Host)
	}
	if !params.ImageFileName.Valid || params.ImageFileName.String == "" {
		t.Fatalf("ImageFileName = %#v, want valid path", params.ImageFileName)
	}
	if !strings.HasPrefix(params.ImageFileName.String, "preview_cards/images/") {
		t.Fatalf("ImageFileName = %s, want preview_cards/images/ prefix", params.ImageFileName.String)
	}
	if !params.ImageContentType.Valid || params.ImageContentType.String != "image/png" {
		t.Fatalf("ImageContentType = %#v, want image/png", params.ImageContentType)
	}
	if !params.ImageFileSize.Valid || int(params.ImageFileSize.Int32) != len(imageData) {
		t.Fatalf("ImageFileSize = %#v, want %d", params.ImageFileSize, len(imageData))
	}
	if !params.ImageUpdatedAt.Valid {
		t.Fatal("ImageUpdatedAt should be valid")
	}
	if params.Width != 120 || params.Height != 80 {
		t.Fatalf("dimensions = %dx%d, want 120x80", params.Width, params.Height)
	}
	if params.Type != 1 {
		t.Fatalf("Type = %d, want 1", params.Type)
	}
	if params.ImageDescription != "Alt text" {
		t.Fatalf("ImageDescription = %s, want Alt text", params.ImageDescription)
	}
	if _, ok := storage.files[params.ImageFileName.String]; !ok {
		t.Fatalf("image not saved at %s", params.ImageFileName.String)
	}

	if len(store.attached) != 1 {
		t.Fatalf("attached = %d, want 1", len(store.attached))
	}
	if store.attached[0].StatusID != 1 {
		t.Fatalf("attached statusID = %d, want 1", store.attached[0].StatusID)
	}
	if !store.attached[0].Url.Valid || store.attached[0].Url.String != canonicalURL {
		t.Fatalf("attached Url = %#v, want %s", store.attached[0].Url, canonicalURL)
	}

	if len(store.createdProviders) != 1 {
		t.Fatalf("createdProviders = %d, want 1", len(store.createdProviders))
	}
	if store.createdProviders[0].Domain != baseURL.Hostname() {
		t.Fatalf("provider domain = %s, want %s", store.createdProviders[0].Domain, baseURL.Hostname())
	}
}

func TestLinkCrawlerCrawlAttachesExistingCard(t *testing.T) {
	link := "http://127.0.0.1/example"
	store := &stubLinkCrawlerStore{
		status: db.Status{ID: 7, Local: pgtype.Bool{Bool: true, Valid: true}},
		cards: map[string]db.PreviewCard{
			link: {ID: 99, Url: link},
		},
	}
	crawler := NewLinkCrawler(store, &memLinkStorage{})
	crawler.AllowPrivateNetworks(true)

	if err := crawler.Crawl(context.Background(), 7, link); err != nil {
		t.Fatalf("Crawl error = %v", err)
	}

	if len(store.createdCards) != 0 {
		t.Fatalf("createdCards = %d, want 0", len(store.createdCards))
	}
	if len(store.attached) != 1 {
		t.Fatalf("attached = %d, want 1", len(store.attached))
	}
	if store.attached[0].PreviewCardID != 99 {
		t.Fatalf("attached PreviewCardID = %d, want 99", store.attached[0].PreviewCardID)
	}
	if !store.attached[0].Url.Valid || store.attached[0].Url.String != link {
		t.Fatalf("attached Url = %#v, want %s", store.attached[0].Url, link)
	}
}
