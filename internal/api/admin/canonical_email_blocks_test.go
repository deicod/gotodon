package admin

import "testing"

func TestCanonicalizeEmail(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "TEST@HOST.EXAMPLE", want: "test@host.example"},
		{input: "test+more@host.example", want: "test@host.example"},
		{input: "test.user@host.example", want: "testuser@host.example"},
		{input: "te.st+more@EXAMPLE.com", want: "test@example.com"},
	}

	for _, tt := range tests {
		if got := canonicalizeEmail(tt.input); got != tt.want {
			t.Errorf("canonicalizeEmail(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCanonicalEmailHash(t *testing.T) {
	expected := "973dfe463ec85785f5f95af5ba3906eedb2d931c24e69824a89ea65dba4e813b"

	tests := []string{
		"test@example.com",
		"t.e.s.t@example.com",
		"test+mastodon1@example.com",
		"Test@EXAMPLE.com",
	}

	for _, email := range tests {
		if got := canonicalEmailHash(email); got != expected {
			t.Errorf("canonicalEmailHash(%q) = %q, want %q", email, got, expected)
		}
	}
}
