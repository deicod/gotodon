package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

// serveRoutes serves req through the routes that register registers, as the
// server does, so that the scopes RegisterRoutes declares apply.
func serveRoutes(register func(middleware.RouteRegistrar), req *http.Request) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// wantOutsideScopes fails the test unless rec is the refusal of a token that
// lacks the route's scopes.
func wantOutsideScopes(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error body %q: %v", rec.Body.String(), err)
	}
	if body.Error != middleware.OutsideScopesMessage {
		t.Fatalf("error = %q, want %q", body.Error, middleware.OutsideScopesMessage)
	}
}
