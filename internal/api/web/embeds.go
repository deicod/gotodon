package web

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"

	"github.com/deicod/gotodon/internal/api/middleware"
	"github.com/deicod/gotodon/internal/api/oembed"
	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
	"github.com/deicod/gotodon/internal/services"
	webutil "github.com/deicod/gotodon/internal/web"
)

const (
	visibilityPublic   = 0
	visibilityUnlisted = 1
)

const oembedSandboxValue = "allow-scripts allow-same-origin allow-popups allow-popups-to-escape-sandbox allow-forms"

type embedStore interface {
	GetStatusByID(ctx context.Context, id int64) (db.Status, error)
	GetAccountByID(ctx context.Context, id int64) (db.Account, error)
	GetBlock(ctx context.Context, arg db.GetBlockParams) (db.Block, error)
}

type oembedFetcher interface {
	Fetch(ctx context.Context, rawURL string) (map[string]any, error)
}

// EmbedsHandler handles GET /api/web/embeds/:id.
type EmbedsHandler struct {
	store   embedStore
	builder *oembed.Handler
	fetcher oembedFetcher
}

// NewEmbedsHandler creates a new embeds handler.
func NewEmbedsHandler(store embedStore, cfg *config.Config) *EmbedsHandler {
	fetcher := services.NewOEmbedFetcher()
	builder := oembed.NewHandler(nil, cfg)
	return &EmbedsHandler{store: store, builder: builder, fetcher: fetcher}
}

// RegisterRoutes registers embed routes.
func (h *EmbedsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/web/embeds/{id}", h.Show)
}

// Show handles GET /api/web/embeds/:id.
func (h *EmbedsHandler) Show(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil {
		webutil.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	statusID, err := strconv.ParseInt(webutil.Param(r, "id"), 10, 64)
	if err != nil || statusID <= 0 {
		webutil.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	ctx := r.Context()
	status, err := h.store.GetStatusByID(ctx, statusID)
	if err != nil {
		webutil.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	if status.Visibility != visibilityPublic && status.Visibility != visibilityUnlisted {
		webutil.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	account, err := h.store.GetAccountByID(ctx, status.AccountID)
	if err != nil {
		webutil.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	viewerID, _ := middleware.GetAccountID(ctx)
	if viewerID > 0 {
		blocked, err := h.isBlocked(ctx, account.ID, viewerID)
		if err != nil {
			webutil.JSONError(w, http.StatusInternalServerError, "Database error")
			return
		}
		if blocked {
			webutil.JSONError(w, http.StatusNotFound, "Record not found")
			return
		}
	}

	isLocal := status.Local.Valid && status.Local.Bool
	if isLocal {
		resp := h.builder.BuildResponse(account, status, nil, nil)
		webutil.JSON(w, http.StatusOK, resp)
		return
	}

	if viewerID == 0 {
		webutil.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	remoteURL := statusOEmbedURL(status)
	if remoteURL == "" {
		webutil.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}

	if h.fetcher == nil {
		webutil.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	payload, err := h.fetcher.Fetch(ctx, remoteURL)
	if err != nil || payload == nil {
		webutil.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	htmlValue, ok := payload["html"].(string)
	if !ok {
		webutil.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	clean := sanitizeOEmbedHTML(htmlValue)
	if clean == "" {
		webutil.JSONError(w, http.StatusNotFound, "Record not found")
		return
	}
	payload["html"] = clean
	webutil.JSON(w, http.StatusOK, payload)
}

func (h *EmbedsHandler) isBlocked(ctx context.Context, authorID, viewerID int64) (bool, error) {
	if authorID == 0 || viewerID == 0 || authorID == viewerID {
		return false, nil
	}
	if _, err := h.store.GetBlock(ctx, db.GetBlockParams{AccountID: authorID, TargetAccountID: viewerID}); err == nil {
		return true, nil
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if _, err := h.store.GetBlock(ctx, db.GetBlockParams{AccountID: viewerID, TargetAccountID: authorID}); err == nil {
		return true, nil
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	return false, nil
}

func statusOEmbedURL(status db.Status) string {
	if status.Url.Valid {
		if raw := strings.TrimSpace(status.Url.String); raw != "" {
			return raw
		}
	}
	if status.Uri.Valid {
		return strings.TrimSpace(status.Uri.String)
	}
	return ""
}

var (
	oembedPolicyOnce sync.Once
	oembedPolicy     *bluemonday.Policy
)

func sanitizeOEmbedHTML(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	policy := oembedPolicyConfig()
	sanitized := policy.Sanitize(input)
	if sanitized == "" {
		return ""
	}
	return ensureIFrameSandbox(sanitized)
}

func oembedPolicyConfig() *bluemonday.Policy {
	oembedPolicyOnce.Do(func() {
		policy := bluemonday.NewPolicy()
		policy.AllowElements("audio", "iframe", "source", "video")
		policy.AllowAttrs("controls").OnElements("audio", "video")
		policy.AllowAttrs("height", "width", "loop").OnElements("video")
		policy.AllowAttrs("allowfullscreen", "frameborder", "height", "scrolling", "src", "width").OnElements("iframe")
		policy.AllowAttrs("src", "type").OnElements("source")
		policy.AllowURLSchemes("http", "https")
		policy.AllowRelativeURLs(false)
		policy.RequireParseableURLs(true)
		oembedPolicy = policy
	})
	return oembedPolicy
}

func ensureIFrameSandbox(input string) string {
	if !strings.Contains(input, "<iframe") {
		return input
	}
	doc, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return input
	}
	applySandbox(doc)
	body := findBodyNode(doc)
	if body == nil {
		body = doc
	}
	var buf bytes.Buffer
	for child := body.FirstChild; child != nil; child = child.NextSibling {
		_ = html.Render(&buf, child)
	}
	return buf.String()
}

func applySandbox(node *html.Node) {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == "iframe" {
			setAttr(child, "sandbox", oembedSandboxValue)
		}
		applySandbox(child)
	}
}

func setAttr(node *html.Node, key, value string) {
	for i, attr := range node.Attr {
		if attr.Key == key {
			node.Attr[i].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, html.Attribute{Key: key, Val: value})
}

func findBodyNode(node *html.Node) *html.Node {
	var body *html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "body" {
			body = n
			return
		}
		for c := n.FirstChild; c != nil && body == nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(node)
	return body
}
