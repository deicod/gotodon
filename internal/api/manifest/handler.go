package manifest

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strconv"
	"strings"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/web"
)

const (
	manifestJSONType              = "application/json"
	manifestCacheControl          = "public, max-age=180" // 3 minutes
	defaultManifestTitle          = "Gotodon"
	defaultManifestDomainFallback = "example.com"
	settingSiteTitle              = "site_title"
)

var manifestAndroidIconSizes = []int{36, 48, 72, 96, 144, 192, 256, 384, 512}

type querier interface {
	GetSettingsByVars(ctx context.Context, vars []string) ([]db.GetSettingsByVarsRow, error)
}

type Handler struct {
	queries querier
	cfg     *config.Config
}

func NewHandler(queries querier, cfg *config.Config) *Handler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &Handler{queries: queries, cfg: cfg}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /manifest", h.Show)
	mux.HandleFunc("GET /{$}", h.ShowHome)
	mux.HandleFunc("GET /share", h.ShowShare)
	mux.HandleFunc("GET /publish", h.ShowPublish)
	mux.HandleFunc("GET /notifications", h.ShowNotifications)
	mux.HandleFunc("GET /explore", h.ShowExplore)
	mux.HandleFunc("GET /icons/{file}", h.ShowIcon)
}

func (h *Handler) Show(w http.ResponseWriter, r *http.Request) {
	title := h.loadSiteTitle(r.Context())
	domain := manifestPublicDomain(h.cfg)
	if domain == "" {
		domain = defaultManifestDomainFallback
	}
	baseURL := "https://" + domain

	icons := make([]map[string]string, 0, len(manifestAndroidIconSizes))
	for _, size := range manifestAndroidIconSizes {
		sizeStr := strconv.Itoa(size) + "x" + strconv.Itoa(size)
		icons = append(icons, map[string]string{
			"src":     baseURL + "/icons/android-chrome-" + sizeStr + ".png",
			"sizes":   sizeStr,
			"type":    "image/png",
			"purpose": "any maskable",
		})
	}

	resp := map[string]any{
		"id":               "/home",
		"name":             title,
		"short_name":       title,
		"icons":            icons,
		"theme_color":      "#191b22",
		"background_color": "#191b22",
		"display":          "standalone",
		"start_url":        "/",
		"scope":            "/",
		"share_target": map[string]any{
			"url_template": "share?title={title}&text={text}&url={url}",
			"action":       "share",
			"method":       "GET",
			"enctype":      "application/x-www-form-urlencoded",
			"params": map[string]string{
				"title": "title",
				"text":  "text",
				"url":   "url",
			},
		},
		"shortcuts": []map[string]string{
			{
				"name": "Compose new post",
				"url":  "/publish",
			},
			{
				"name": "Notifications",
				"url":  "/notifications",
			},
			{
				"name": "Explore",
				"url":  "/explore",
			},
		},
		"prefer_related_applications": true,
		"related_applications": []map[string]string{
			{
				"platform": "play",
				"url":      "https://play.google.com/store/apps/details?id=org.joinmastodon.android",
				"id":       "org.joinmastodon.android",
			},
			{
				"platform": "itunes",
				"url":      "https://apps.apple.com/us/app/mastodon-for-iphone/id1571998974",
				"id":       "id1571998974",
			},
			{
				"platform": "f-droid",
				"url":      "https://f-droid.org/en/packages/org.joinmastodon.android/",
				"id":       "org.joinmastodon.android",
			},
		},
	}

	w.Header().Set("Cache-Control", manifestCacheControl)
	w.Header().Set("Content-Type", manifestJSONType)
	web.JSON(w, http.StatusOK, resp)
}

func (h *Handler) loadSiteTitle(ctx context.Context) string {
	title := defaultManifestTitle
	if h == nil || h.queries == nil {
		return title
	}

	rows, err := h.queries.GetSettingsByVars(ctx, []string{settingSiteTitle})
	if err != nil {
		return title
	}

	for _, row := range rows {
		if row.Var != settingSiteTitle || !row.Value.Valid {
			continue
		}
		if value := strings.TrimSpace(row.Value.String); value != "" {
			title = value
		}
	}

	return title
}

func manifestPublicDomain(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if strings.TrimSpace(cfg.Instance.WebDomain) != "" {
		return cfg.Instance.WebDomain
	}
	return cfg.Instance.LocalDomain
}

func (h *Handler) ShowHome(w http.ResponseWriter, r *http.Request) {
	title := h.loadSiteTitle(r.Context())
	renderAppPlaceholder(w, title, "Home", `<p>Gotodon web entry point.</p>`)
}

func (h *Handler) ShowShare(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	text := strings.TrimSpace(r.URL.Query().Get("text"))
	url := strings.TrimSpace(r.URL.Query().Get("url"))

	parts := make([]string, 0, 3)
	if title != "" {
		parts = append(parts, fmt.Sprintf("<li><strong>Title:</strong> %s</li>", html.EscapeString(title)))
	}
	if text != "" {
		parts = append(parts, fmt.Sprintf("<li><strong>Text:</strong> %s</li>", html.EscapeString(text)))
	}
	if url != "" {
		parts = append(parts, fmt.Sprintf("<li><strong>URL:</strong> %s</li>", html.EscapeString(url)))
	}

	content := `<p>Share target endpoint compatible with the web app manifest.</p>`
	if len(parts) > 0 {
		content += "<ul>" + strings.Join(parts, "") + "</ul>"
	}

	renderAppPlaceholder(w, h.loadSiteTitle(r.Context()), "Share", content)
}

func (h *Handler) ShowPublish(w http.ResponseWriter, r *http.Request) {
	renderAppPlaceholder(w, h.loadSiteTitle(r.Context()), "Compose", `<p>Compose placeholder route for Progressive Web App shortcut.</p>`)
}

func (h *Handler) ShowNotifications(w http.ResponseWriter, r *http.Request) {
	renderAppPlaceholder(w, h.loadSiteTitle(r.Context()), "Notifications", `<p>Notifications placeholder route for Progressive Web App shortcut.</p>`)
}

func (h *Handler) ShowExplore(w http.ResponseWriter, r *http.Request) {
	renderAppPlaceholder(w, h.loadSiteTitle(r.Context()), "Explore", `<p>Explore placeholder route for Progressive Web App shortcut.</p>`)
}

func (h *Handler) ShowIcon(w http.ResponseWriter, r *http.Request) {
	fileName := r.PathValue("file")
	size, ok := parseManifestAndroidIconSize(fileName)
	if !ok {
		http.NotFound(w, r)
		return
	}

	icon, err := generateManifestIconPNG(size)
	if err != nil {
		http.Error(w, "failed to generate icon", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", manifestCacheControl)
	w.Header().Set("Content-Type", "image/png")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(icon)
}

func parseManifestAndroidIconSize(fileName string) (int, bool) {
	if !strings.HasPrefix(fileName, "android-chrome-") || !strings.HasSuffix(fileName, ".png") {
		return 0, false
	}

	dims := strings.TrimPrefix(fileName, "android-chrome-")
	dims = strings.TrimSuffix(dims, ".png")
	parts := strings.Split(dims, "x")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, false
	}
	if parts[0] != parts[1] {
		return 0, false
	}

	size, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, false
	}
	for _, allowed := range manifestAndroidIconSizes {
		if size == allowed {
			return size, true
		}
	}
	return 0, false
}

func generateManifestIconPNG(size int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{R: 0x19, G: 0x1b, B: 0x22, A: 0xff}
	fg := color.RGBA{R: 0x63, G: 0xe6, B: 0xbe, A: 0xff}

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, bg)
		}
	}

	inset := size / 8
	if inset < 2 {
		inset = 2
	}
	for y := inset; y < size-inset; y++ {
		for x := inset; x < size-inset; x++ {
			if x == inset || y == inset || x == size-inset-1 || y == size-inset-1 {
				img.SetRGBA(x, y, fg)
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func renderAppPlaceholder(w http.ResponseWriter, instanceTitle, pageTitle, content string) {
	if instanceTitle == "" {
		instanceTitle = defaultManifestTitle
	}
	if pageTitle == "" {
		pageTitle = "App"
	}

	fullTitle := html.EscapeString(pageTitle + " · " + instanceTitle)
	bodyTitle := html.EscapeString(pageTitle)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>` + fullTitle + `</title></head><body>` +
		`<main><h1>` + bodyTitle + `</h1>` + content +
		`<nav><a href="/">Home</a> · <a href="/publish">Publish</a> · <a href="/notifications">Notifications</a> · <a href="/explore">Explore</a></nav>` +
		`</main></body></html>`))
}
