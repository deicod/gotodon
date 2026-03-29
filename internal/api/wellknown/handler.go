package wellknown

import "net/http"

type Handler struct{}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/change-password", h.ChangePassword)
	mux.HandleFunc("GET /.well-known/proxy", h.Proxy)
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/auth/edit", http.StatusMovedPermanently)
}

func (h *Handler) Proxy(w http.ResponseWriter, r *http.Request) {
	target := "/authorize_interaction"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}
