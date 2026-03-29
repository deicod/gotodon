package hostmeta

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/deicod/gotodon/internal/config"
)

const (
	hostMetaCacheControl = "public, max-age=259200" // 3 days
	hostMetaXMLType      = "application/xrd+xml"
	hostMetaJSONType     = "application/json"
)

type Handler struct {
	cfg *config.Config
}

func NewHandler(cfg *config.Config) *Handler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &Handler{cfg: cfg}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/host-meta", h.Show)
	mux.HandleFunc("GET /.well-known/host-meta.json", h.Show)
}

func (h *Handler) Show(w http.ResponseWriter, r *http.Request) {
	addVary(w, "Accept")
	addVary(w, "Origin")
	w.Header().Set("Cache-Control", hostMetaCacheControl)

	template := "https://" + h.publicDomain() + "/.well-known/webfinger?resource={uri}"
	if r.URL.Path == "/.well-known/host-meta.json" || acceptsJSON(r.Header.Get("Accept")) {
		w.Header().Set("Content-Type", hostMetaJSONType)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"links": []map[string]string{
				{
					"rel":      "lrdd",
					"template": template,
				},
			},
		})
		return
	}

	xml := `<?xml version="1.0" encoding="UTF-8"?>
<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0">
  <Link rel="lrdd" template="` + template + `"/>
</XRD>`
	w.Header().Set("Content-Type", hostMetaXMLType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml))
}

func acceptsJSON(accept string) bool {
	for _, part := range strings.Split(strings.ToLower(accept), ",") {
		mediaType := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if mediaType == hostMetaJSONType {
			return true
		}
	}
	return false
}

func addVary(w http.ResponseWriter, token string) {
	for _, value := range w.Header().Values("Vary") {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return
			}
		}
	}
	w.Header().Add("Vary", token)
}

func (h *Handler) publicDomain() string {
	if h == nil || h.cfg == nil {
		return "example.com"
	}
	if domain := strings.TrimSpace(h.cfg.Instance.WebDomain); domain != "" {
		return domain
	}
	if domain := strings.TrimSpace(h.cfg.Instance.LocalDomain); domain != "" {
		return domain
	}
	return "example.com"
}
