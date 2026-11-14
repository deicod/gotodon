package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/gotodon/internal/config"
)

// TestAccountListLinksUseConfiguredDomain checks that pagination links name
// the configured domain whatever host or scheme the request came in with.
func TestAccountListLinksUseConfiguredDomain(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{
			name: "configured domain",
			cfg:  &config.Config{Instance: config.InstanceConfig{LocalDomain: "social.example"}},
			want: "https://social.example/api/v1/admin/accounts?limit=2&max_id=5",
		},
		{
			name: "no domain is taken from the request",
			cfg:  nil,
			want: "https:///api/v1/admin/accounts?limit=2&max_id=5",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := NewAccountList(nil, tt.cfg, "/api/v1/admin/accounts", []string{"limit"})
			req := httptest.NewRequest(http.MethodGet, "http://attacker.test/api/v1/admin/accounts?limit=2&other=1", nil)
			req.Header.Set("X-Forwarded-Proto", "gopher")
			if got := list.pageURL(req, "max_id", 5); got != tt.want {
				t.Fatalf("pageURL = %q, want %q", got, tt.want)
			}
		})
	}
}
