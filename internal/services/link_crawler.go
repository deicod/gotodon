package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/net/html"

	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/media"
)

const (
	defaultLinkCrawlerHTMLLimit  = 2 * 1024 * 1024
	defaultLinkCrawlerImageLimit = 5 * 1024 * 1024
	defaultLinkCrawlerTimeout    = 12 * time.Second
	defaultLinkCrawlerUserAgent  = "Gotodon Link Preview"
	maxRedirects                 = 5
)

var ErrLinkCrawlerSkipRetry = errors.New("link crawler skip retry")

// LinkCrawlerStore defines the DB operations for preview cards.
type LinkCrawlerStore interface {
	GetStatusByID(ctx context.Context, id int64) (db.Status, error)
	GetPreviewCardByURL(ctx context.Context, url string) (db.PreviewCard, error)
	CreatePreviewCard(ctx context.Context, arg db.CreatePreviewCardParams) (db.PreviewCard, error)
	AttachPreviewCardToStatus(ctx context.Context, arg db.AttachPreviewCardToStatusParams) error
	GetPreviewCardProviderByDomain(ctx context.Context, domain string) (db.PreviewCardProvider, error)
	CreatePreviewCardProvider(ctx context.Context, arg db.CreatePreviewCardProviderParams) (db.PreviewCardProvider, error)
}

// LinkCrawler fetches link metadata and stores preview cards.
type LinkCrawler struct {
	store                LinkCrawlerStore
	storage              media.Storage
	client               *http.Client
	htmlLimit            int64
	imageLimit           int64
	userAgent            string
	allowPrivateNetworks bool
}

// NewLinkCrawler creates a new link crawler with defaults.
func NewLinkCrawler(store LinkCrawlerStore, storage media.Storage) *LinkCrawler {
	client := &http.Client{
		Timeout: defaultLinkCrawlerTimeout,
	}
	return &LinkCrawler{
		store:      store,
		storage:    storage,
		client:     client,
		htmlLimit:  defaultLinkCrawlerHTMLLimit,
		imageLimit: defaultLinkCrawlerImageLimit,
		userAgent:  defaultLinkCrawlerUserAgent,
	}
}

// AllowPrivateNetworks permits crawling private/loopback hosts (useful for tests).
func (c *LinkCrawler) AllowPrivateNetworks(allow bool) {
	if c == nil {
		return
	}
	c.allowPrivateNetworks = allow
}

// Crawl fetches metadata for a URL and attaches a preview card to a status.
func (c *LinkCrawler) Crawl(ctx context.Context, statusID int64, rawURL string) error {
	if c == nil || c.store == nil || c.storage == nil {
		return fmt.Errorf("link crawler missing dependencies")
	}
	if statusID <= 0 {
		return ErrLinkCrawlerSkipRetry
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ErrLinkCrawlerSkipRetry
	}

	status, err := c.store.GetStatusByID(ctx, statusID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLinkCrawlerSkipRetry
		}
		return fmt.Errorf("load status: %w", err)
	}
	if status.Local.Valid && !status.Local.Bool {
		return ErrLinkCrawlerSkipRetry
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || !isHTTPURL(parsed) {
		return ErrLinkCrawlerSkipRetry
	}
	if err := c.ensureSafeURL(ctx, parsed); err != nil {
		return fmt.Errorf("unsafe url: %w", ErrLinkCrawlerSkipRetry)
	}

	if card, err := c.store.GetPreviewCardByURL(ctx, parsed.String()); err == nil {
		return c.attachCard(ctx, card.ID, statusID, parsed.String())
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lookup preview card: %w", err)
	}

	meta, finalURL, err := c.fetchMetadata(ctx, parsed)
	if err != nil {
		if errors.Is(err, ErrLinkCrawlerSkipRetry) {
			return err
		}
		return fmt.Errorf("fetch metadata: %w", err)
	}
	if meta.CanonicalURL != "" {
		if canonical, err := url.Parse(meta.CanonicalURL); err == nil && isHTTPURL(canonical) {
			if err := c.ensureSafeURL(ctx, canonical); err == nil {
				finalURL = canonical
			}
		}
	}
	if finalURL == nil {
		finalURL = parsed
	}

	cardURL := finalURL.String()
	if card, err := c.store.GetPreviewCardByURL(ctx, cardURL); err == nil {
		return c.attachCard(ctx, card.ID, statusID, cardURL)
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lookup preview card: %w", err)
	}

	if domain := finalURL.Hostname(); domain != "" {
		_ = c.ensureProvider(ctx, domain)
	}

	card, err := c.buildPreviewCard(ctx, cardURL, finalURL, meta)
	if err != nil {
		if errors.Is(err, ErrLinkCrawlerSkipRetry) {
			return err
		}
		return fmt.Errorf("build preview card: %w", err)
	}

	if err := c.attachCard(ctx, card.ID, statusID, cardURL); err != nil {
		return err
	}
	return nil
}

type metadata struct {
	Title        string
	Description  string
	Image        string
	SiteName     string
	AuthorName   string
	AuthorURL    string
	CanonicalURL string
	Type         string
	ImageAlt     string
	TwitterCard  string
}

func (c *LinkCrawler) fetchMetadata(ctx context.Context, u *url.URL) (metadata, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return metadata{}, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	client := c.clientForRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return metadata{}, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return metadata{}, nil, ErrLinkCrawlerSkipRetry
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return metadata{}, nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	finalURL := resp.Request.URL

	if strings.HasPrefix(contentType, "image/") {
		return metadata{Image: finalURL.String(), Type: "photo"}, finalURL, nil
	}
	if contentType != "" && !strings.HasPrefix(contentType, "text/html") && contentType != "application/xhtml+xml" {
		return metadata{}, finalURL, nil
	}

	body, err := readLimited(resp.Body, c.htmlLimit)
	if err != nil {
		return metadata{}, finalURL, err
	}
	meta := parseHTMLMetadata(body)
	return meta, finalURL, nil
}

func (c *LinkCrawler) buildPreviewCard(ctx context.Context, cardURL string, baseURL *url.URL, meta metadata) (db.PreviewCard, error) {
	title := firstNonEmpty(meta.Title, baseURL.Hostname())
	description := meta.Description

	imageURL := meta.Image
	var (
		imagePath        string
		imageContentType string
		imageSize        int
		imageWidth       int
		imageHeight      int
		blurhash         pgtype.Text
	)

	if imageURL != "" {
		if resolved, err := resolveURL(baseURL, imageURL); err == nil {
			if err := c.ensureSafeURL(ctx, resolved); err == nil {
				data, ctype, err := c.fetchImage(ctx, resolved)
				if err == nil && ctype != "" {
					imageContentType = ctype
					imageSize = len(data)
					imagePath = previewCardImagePath(resolved.String(), ctype)
					if err := c.storage.Save(ctx, imagePath, data); err != nil {
						return db.PreviewCard{}, fmt.Errorf("save preview image: %w", err)
					}
					if hash, err := media.BlurHashFromBytes(data, 4, 3); err == nil && hash != "" {
						blurhash = pgtype.Text{String: hash, Valid: true}
					}
					if w, h := imageDimensions(data); w > 0 && h > 0 {
						imageWidth = w
						imageHeight = h
					}
				}
			}
		}
	}

	cardType := previewCardType(meta.Type, meta.TwitterCard, imagePath)
	providerURL := providerBaseURL(baseURL)
	providerName := firstNonEmpty(meta.SiteName, baseURL.Hostname())

	params := db.CreatePreviewCardParams{
		Url:              truncate(cardURL, 2048),
		Title:            truncate(title, 255),
		Description:      truncate(description, 1000),
		ImageFileName:    pgtype.Text{String: imagePath, Valid: imagePath != ""},
		ImageContentType: pgtype.Text{String: imageContentType, Valid: imageContentType != ""},
		ImageFileSize:    pgtype.Int4{Int32: int32(imageSize), Valid: imageSize > 0},
		ImageUpdatedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: imagePath != ""},
		Type:             cardType,
		Html:             "",
		AuthorName:       truncate(meta.AuthorName, 255),
		AuthorUrl:        truncate(meta.AuthorURL, 255),
		ProviderName:     truncate(providerName, 255),
		ProviderUrl:      truncate(providerURL, 255),
		Width:            int32(imageWidth),
		Height:           int32(imageHeight),
		EmbedUrl:         "",
		Blurhash:         blurhash,
		Language:         pgtype.Text{},
		Trendable:        pgtype.Bool{},
		LinkType:         pgtype.Int4{},
		PublishedAt:      pgtype.Timestamptz{},
		ImageDescription: truncate(meta.ImageAlt, 255),
		AuthorAccountID:  pgtype.Int8{},
	}

	card, err := c.store.CreatePreviewCard(ctx, params)
	if err != nil {
		if existing, getErr := c.store.GetPreviewCardByURL(ctx, cardURL); getErr == nil {
			return existing, nil
		}
		return db.PreviewCard{}, err
	}
	return card, nil
}

func (c *LinkCrawler) fetchImage(ctx context.Context, u *url.URL) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "image/*")

	resp, err := c.clientForRequest(req).Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return nil, "", ErrLinkCrawlerSkipRetry
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", ErrLinkCrawlerSkipRetry
	}

	data, err := readLimited(resp.Body, c.imageLimit)
	if err != nil {
		return nil, "", err
	}
	return data, contentType, nil
}

func (c *LinkCrawler) attachCard(ctx context.Context, cardID int64, statusID int64, cardURL string) error {
	if cardID <= 0 {
		return ErrLinkCrawlerSkipRetry
	}
	if err := c.store.AttachPreviewCardToStatus(ctx, db.AttachPreviewCardToStatusParams{
		PreviewCardID: cardID,
		StatusID:      statusID,
		Url:           pgtype.Text{String: cardURL, Valid: cardURL != ""},
	}); err != nil {
		return fmt.Errorf("attach preview card: %w", err)
	}
	return nil
}

func (c *LinkCrawler) ensureProvider(ctx context.Context, domain string) error {
	if domain == "" {
		return nil
	}
	if _, err := c.store.GetPreviewCardProviderByDomain(ctx, domain); err == nil {
		return nil
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err := c.store.CreatePreviewCardProvider(ctx, db.CreatePreviewCardProviderParams{
		Domain:    domain,
		Trendable: pgtype.Bool{Bool: false, Valid: true},
	})
	return err
}

func (c *LinkCrawler) clientForRequest(req *http.Request) *http.Client {
	client := *c.client
	client.CheckRedirect = func(redirectReq *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return http.ErrUseLastResponse
		}
		if err := c.ensureSafeURL(redirectReq.Context(), redirectReq.URL); err != nil {
			return err
		}
		return nil
	}
	return &client
}

func (c *LinkCrawler) ensureSafeURL(ctx context.Context, u *url.URL) error {
	if c.allowPrivateNetworks {
		return nil
	}
	host := u.Hostname()
	if host == "" {
		return ErrLinkCrawlerSkipRetry
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return ErrLinkCrawlerSkipRetry
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return ErrLinkCrawlerSkipRetry
	}
	for _, addr := range addrs {
		if !isPublicIP(addr.IP) {
			return ErrLinkCrawlerSkipRetry
		}
	}
	return nil
}

func isHTTPURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return scheme == "http" || scheme == "https"
}

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	return true
}

func parseHTMLMetadata(body []byte) metadata {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return metadata{}
	}

	meta := metadata{}

	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				if meta.Title == "" && n.FirstChild != nil {
					meta.Title = strings.TrimSpace(n.FirstChild.Data)
				}
			case "meta":
				var key, content string
				for _, attr := range n.Attr {
					switch strings.ToLower(attr.Key) {
					case "property", "name":
						if key == "" {
							key = strings.ToLower(strings.TrimSpace(attr.Val))
						}
					case "content":
						content = strings.TrimSpace(attr.Val)
					}
				}
				if key != "" && content != "" {
					switch key {
					case "og:title":
						meta.Title = firstNonEmpty(meta.Title, content)
					case "twitter:title":
						meta.Title = firstNonEmpty(meta.Title, content)
					case "og:description":
						meta.Description = firstNonEmpty(meta.Description, content)
					case "description":
						meta.Description = firstNonEmpty(meta.Description, content)
					case "twitter:description":
						meta.Description = firstNonEmpty(meta.Description, content)
					case "og:image":
						meta.Image = firstNonEmpty(meta.Image, content)
					case "twitter:image", "twitter:image:src":
						meta.Image = firstNonEmpty(meta.Image, content)
					case "og:site_name":
						meta.SiteName = firstNonEmpty(meta.SiteName, content)
					case "author":
						meta.AuthorName = firstNonEmpty(meta.AuthorName, content)
					case "article:author":
						meta.AuthorURL = firstNonEmpty(meta.AuthorURL, content)
					case "og:type":
						meta.Type = firstNonEmpty(meta.Type, content)
					case "og:image:alt":
						meta.ImageAlt = firstNonEmpty(meta.ImageAlt, content)
					case "twitter:card":
						meta.TwitterCard = firstNonEmpty(meta.TwitterCard, content)
					case "twitter:site":
						meta.SiteName = firstNonEmpty(meta.SiteName, strings.TrimPrefix(content, "@"))
					}
				}
			case "link":
				var rel, href string
				for _, attr := range n.Attr {
					switch strings.ToLower(attr.Key) {
					case "rel":
						rel = strings.ToLower(strings.TrimSpace(attr.Val))
					case "href":
						href = strings.TrimSpace(attr.Val)
					}
				}
				if rel == "canonical" && href != "" && meta.CanonicalURL == "" {
					meta.CanonicalURL = href
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)

	return meta
}

func resolveURL(base *url.URL, raw string) (*url.URL, error) {
	if raw == "" {
		return nil, ErrLinkCrawlerSkipRetry
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if base == nil {
		return parsed, nil
	}
	return base.ResolveReference(parsed), nil
}

func providerBaseURL(base *url.URL) string {
	if base == nil {
		return ""
	}
	if base.Scheme == "" || base.Host == "" {
		return ""
	}
	return fmt.Sprintf("%s://%s", base.Scheme, base.Host)
}

func previewCardType(rawType, twitterCard, imagePath string) int32 {
	raw := strings.ToLower(strings.TrimSpace(rawType))
	twitter := strings.ToLower(strings.TrimSpace(twitterCard))
	switch {
	case strings.Contains(raw, "video") || strings.Contains(twitter, "player"):
		return 2
	case strings.Contains(raw, "rich"):
		return 3
	case strings.Contains(raw, "photo") || strings.Contains(raw, "image") || imagePath != "":
		return 1
	default:
		return 0
	}
}

func previewCardImagePath(imageURL, contentType string) string {
	ext := media.GetExtensionFromMIME(contentType)
	if ext == "" || ext == ".bin" {
		ext = path.Ext(imageURL)
		if ext == "" {
			ext = ".img"
		}
	}
	sum := sha256.Sum256([]byte(imageURL))
	name := hex.EncodeToString(sum[:10])
	return path.Join("preview_cards", "images", name+ext)
}

func imageDimensions(data []byte) (int, int) {
	width, height, err := media.GetImageDimensions(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return width, height
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	limited := io.LimitReader(r, limit+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("payload too large: %w", ErrLinkCrawlerSkipRetry)
	}
	return data, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func truncate(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(value) <= max {
		return value
	}
	return value[:max]
}
