package cmd

import (
	"strings"
	"testing"
)

// TestRunServerValidatesDomains checks that serve refuses to start without a
// valid local domain, before it connects to anything, instead of handing out
// URLs and ActivityPub IDs under an invented domain.
func TestRunServerValidatesDomains(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{name: "no local domain", env: map[string]string{}, wantErr: "instance.local_domain is required"},
		{name: "only a web domain", env: map[string]string{"WEB_DOMAIN": "web.social.example"}, wantErr: "instance.local_domain is required"},
		{name: "local domain with scheme", env: map[string]string{"LOCAL_DOMAIN": "https://social.example"}, wantErr: "instance.local_domain"},
		{name: "web domain with path", env: map[string]string{"LOCAL_DOMAIN": "social.example", "WEB_DOMAIN": "web.social.example/app"}, wantErr: "instance.web_domain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"LOCAL_DOMAIN", "WEB_DOMAIN", "GOTODON_INSTANCE_LOCAL_DOMAIN", "GOTODON_INSTANCE_WEB_DOMAIN"} {
				t.Setenv(key, "")
			}
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			// Nothing listens here: reaching the database would fail
			// differently.
			t.Setenv("DB_HOST", "127.0.0.1")
			t.Setenv("DB_PORT", "1")

			err := runServer(serveCmd, nil)
			if err == nil {
				t.Fatal("runServer() error = nil, want a configuration error")
			}
			if !strings.Contains(err.Error(), "invalid configuration") || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("runServer() error = %v, want an invalid configuration error mentioning %q", err, tt.wantErr)
			}
		})
	}
}
