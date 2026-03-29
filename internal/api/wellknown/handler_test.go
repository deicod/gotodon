package wellknown

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChangePasswordRedirectsToAuthEdit(t *testing.T) {
	handler := NewHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/change-password", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMovedPermanently)
	}
	if got := rec.Header().Get("Location"); got != "/auth/edit" {
		t.Fatalf("location = %q, want %q", got, "/auth/edit")
	}
}

func TestProxyRedirectsToAuthorizeInteractionWithQueryPreserved(t *testing.T) {
	handler := NewHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/proxy?acct=alice%40example.org&uri=https%3A%2F%2Fexample.org%2Fusers%2Falice", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMovedPermanently)
	}
	want := "/authorize_interaction?acct=alice%40example.org&uri=https%3A%2F%2Fexample.org%2Fusers%2Falice"
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("location = %q, want %q", got, want)
	}
}

func TestProxyRedirectsToAuthorizeInteractionWithoutQuery(t *testing.T) {
	handler := NewHandler()
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/proxy", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMovedPermanently)
	}
	if got := rec.Header().Get("Location"); got != "/authorize_interaction" {
		t.Fatalf("location = %q, want %q", got, "/authorize_interaction")
	}
}
