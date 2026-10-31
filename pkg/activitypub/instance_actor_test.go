package activitypub

import "testing"

func TestInstanceActorURI(t *testing.T) {
	tests := []struct {
		domain string
		want   string
	}{
		{"example.com", "https://example.com/actor"},
		{" example.com\t", "https://example.com/actor"},
		{"example.com:8443", "https://example.com:8443/actor"},
		{"", ""},
		{"   ", ""},
	}
	for _, tt := range tests {
		if got := InstanceActorURI(tt.domain); got != tt.want {
			t.Errorf("InstanceActorURI(%q) = %q, want %q", tt.domain, got, tt.want)
		}
	}
}
