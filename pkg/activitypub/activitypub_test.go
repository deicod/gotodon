package activitypub

import (
	"testing"
)

func TestParseAcct(t *testing.T) {
	tests := []struct {
		input       string
		wantUser    string
		wantDomain  string
		expectError bool
	}{
		{"user@example.com", "user", "example.com", false},
		{"acct:user@example.com", "user", "example.com", false},
		{"@user@example.com", "user", "example.com", false},
		{"invalid", "", "", true},
	}

	for _, tt := range tests {
		user, domain, err := ParseAcct(tt.input)
		if tt.expectError {
			if err == nil {
				t.Errorf("ParseAcct(%q) expected error", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("ParseAcct(%q) error = %v", tt.input, err)
			}
			if user != tt.wantUser {
				t.Errorf("ParseAcct(%q) user = %q, want %q", tt.input, user, tt.wantUser)
			}
			if domain != tt.wantDomain {
				t.Errorf("ParseAcct(%q) domain = %q, want %q", tt.input, domain, tt.wantDomain)
			}
		}
	}
}

func TestBuildWebFingerResponse(t *testing.T) {
	resp := BuildWebFingerResponse("alice", "example.com", "https://example.com/users/alice", "https://example.com/@alice")

	if resp.Subject != "acct:alice@example.com" {
		t.Errorf("Subject = %q, want %q", resp.Subject, "acct:alice@example.com")
	}

	if len(resp.Links) != 3 {
		t.Errorf("Links count = %d, want 3", len(resp.Links))
	}

	// Check self link
	found := false
	for _, link := range resp.Links {
		if link.Rel == WebFingerRelSelf && link.Type == ActivityPubContentType {
			found = true
			if link.Href != "https://example.com/users/alice" {
				t.Errorf("Self link href = %q, want actor URI", link.Href)
			}
		}
	}
	if !found {
		t.Error("Missing self link with ActivityPub content type")
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"https://example.com/users/alice", "example.com"},
		{"http://foo.bar/path/to/thing", "foo.bar"},
		{"example.com", "example.com"},
	}

	for _, tt := range tests {
		got := ExtractDomain(tt.uri)
		if got != tt.want {
			t.Errorf("ExtractDomain(%q) = %q, want %q", tt.uri, got, tt.want)
		}
	}
}

func TestIsPublic(t *testing.T) {
	tests := []struct {
		to, cc []string
		want   bool
	}{
		{[]string{PublicAudience}, nil, true},
		{nil, []string{PublicAudience}, true},
		{[]string{"https://example.com/followers"}, nil, false},
		{[]string{"https://example.com/followers"}, []string{PublicAudience}, true},
	}

	for _, tt := range tests {
		got := IsPublic(tt.to, tt.cc)
		if got != tt.want {
			t.Errorf("IsPublic(%v, %v) = %v, want %v", tt.to, tt.cc, got, tt.want)
		}
	}
}

func TestDefaultContext(t *testing.T) {
	ctx := DefaultContext()
	if len(ctx) != 2 {
		t.Errorf("DefaultContext length = %d, want 2", len(ctx))
	}

	ctx1, ok := ctx[0].(string)
	if !ok || ctx1 != ActivityStreamsContext {
		t.Error("First context should be ActivityStreams")
	}
}

func TestActivityFactories(t *testing.T) {
	t.Run("CreateFollowActivity", func(t *testing.T) {
		activity := CreateFollowActivity("https://a.com/actor", "https://b.com/actor")
		if activity.Type != TypeFollow {
			t.Errorf("Type = %q, want %q", activity.Type, TypeFollow)
		}
		if activity.Actor != "https://a.com/actor" {
			t.Errorf("Actor = %v, want string", activity.Actor)
		}
	})

	t.Run("CreateLikeActivity", func(t *testing.T) {
		activity := CreateLikeActivity("https://a.com/actor", "https://b.com/status/1")
		if activity.Type != TypeLike {
			t.Errorf("Type = %q, want %q", activity.Type, TypeLike)
		}
	})

	t.Run("CreateAnnounceActivity", func(t *testing.T) {
		activity := CreateAnnounceActivity("https://a.com/actor", "https://b.com/status/1", []string{PublicAudience}, nil)
		if activity.Type != TypeAnnounce {
			t.Errorf("Type = %q, want %q", activity.Type, TypeAnnounce)
		}
		if len(activity.To) != 1 || activity.To[0] != PublicAudience {
			t.Error("To should contain public audience")
		}
	})
}

func TestGenerateKeyPair(t *testing.T) {
	priv, pub, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}

	if priv == nil {
		t.Error("Private key should not be nil")
	}
	if pub == nil {
		t.Error("Public key should not be nil")
	}

	// Test encoding
	privPEM := EncodePrivateKey(priv)
	if privPEM == "" {
		t.Error("Private key PEM should not be empty")
	}

	pubPEM, err := EncodePublicKey(pub)
	if err != nil {
		t.Errorf("EncodePublicKey() error = %v", err)
	}
	if pubPEM == "" {
		t.Error("Public key PEM should not be empty")
	}
}
