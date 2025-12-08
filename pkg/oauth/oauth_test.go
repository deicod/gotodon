package oauth

import (
	"testing"
)

func TestGenerateToken(t *testing.T) {
	token1, err := GenerateToken(32)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	token2, err := GenerateToken(32)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	if token1 == "" {
		t.Error("Generated token should not be empty")
	}

	if token1 == token2 {
		t.Error("Generated tokens should be unique")
	}

	// Base64 URL encoding of 32 bytes should be ~43 chars
	if len(token1) < 40 {
		t.Errorf("Token length = %d, want >= 40", len(token1))
	}
}

func TestGenerateClientCredentials(t *testing.T) {
	clientID, clientSecret, err := GenerateClientCredentials()
	if err != nil {
		t.Fatalf("GenerateClientCredentials() error = %v", err)
	}

	if clientID == "" {
		t.Error("Client ID should not be empty")
	}

	if clientSecret == "" {
		t.Error("Client secret should not be empty")
	}

	if clientID == clientSecret {
		t.Error("Client ID and secret should be different")
	}
}

func TestParseScopes(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", []string{"read"}},
		{"read", []string{"read"}},
		{"read write", []string{"read", "write"}},
		{"read write follow push", []string{"read", "write", "follow", "push"}},
		{"read:statuses write:statuses", []string{"read:statuses", "write:statuses"}},
	}

	for _, tt := range tests {
		got := ParseScopes(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("ParseScopes(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("ParseScopes(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestValidateScopes(t *testing.T) {
	tests := []struct {
		scopes  []string
		wantErr bool
	}{
		{[]string{"read"}, false},
		{[]string{"read", "write"}, false},
		{[]string{"read:statuses"}, false},
		{[]string{"admin:read"}, false},
		{[]string{"invalid"}, true},
		{[]string{"read", "invalid"}, true},
		{[]string{"readonly"}, true},
	}

	for _, tt := range tests {
		err := ValidateScopes(tt.scopes)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateScopes(%v) error = %v, wantErr = %v", tt.scopes, err, tt.wantErr)
		}
	}
}

func TestHasScope(t *testing.T) {
	tests := []struct {
		tokenScopes   string
		requiredScope string
		want          bool
	}{
		// Exact match
		{"read", "read", true},
		{"read write", "write", true},

		// Hierarchical scopes
		{"read", "read:statuses", true},
		{"write", "write:statuses", true},
		{"admin:read", "admin:read:accounts", true},
		{"admin:write", "admin:write:reports", true},

		// No match
		{"read", "write", false},
		{"read:statuses", "read:accounts", false},
		{"write", "read:statuses", false},

		// Granular scope doesn't grant parent
		{"read:statuses", "read", false},
		{"write:statuses", "write", false},
	}

	for _, tt := range tests {
		got := HasScope(tt.tokenScopes, tt.requiredScope)
		if got != tt.want {
			t.Errorf("HasScope(%q, %q) = %v, want %v", tt.tokenScopes, tt.requiredScope, got, tt.want)
		}
	}
}

func TestNormalizeScopes(t *testing.T) {
	tests := []struct {
		scopes []string
		want   string
	}{
		{[]string{}, "read"},
		{[]string{"read"}, "read"},
		{[]string{"read", "write"}, "read write"},
	}

	for _, tt := range tests {
		got := NormalizeScopes(tt.scopes)
		if got != tt.want {
			t.Errorf("NormalizeScopes(%v) = %q, want %q", tt.scopes, got, tt.want)
		}
	}
}
