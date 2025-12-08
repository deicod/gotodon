package auth

import (
	"strings"
	"testing"
)

func TestHashPassword(t *testing.T) {
	password := "testpassword123"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if hash == "" {
		t.Error("Hash should not be empty")
	}

	if hash == password {
		t.Error("Hash should not equal plaintext password")
	}

	// bcrypt hashes start with $2
	if !strings.HasPrefix(hash, "$2") {
		t.Errorf("Hash should be bcrypt format, got %s", hash[:10])
	}
}

func TestCheckPassword(t *testing.T) {
	password := "testpassword123"
	hash, _ := HashPassword(password)

	tests := []struct {
		password string
		want     bool
	}{
		{password, true},
		{"wrongpassword", false},
		{"", false},
		{password + " ", false},
	}

	for _, tt := range tests {
		got := CheckPassword(tt.password, hash)
		if got != tt.want {
			t.Errorf("CheckPassword(%q) = %v, want %v", tt.password, got, tt.want)
		}
	}
}

func TestGenerateConfirmationToken(t *testing.T) {
	token1, err := GenerateConfirmationToken()
	if err != nil {
		t.Fatalf("GenerateConfirmationToken() error = %v", err)
	}

	token2, err := GenerateConfirmationToken()
	if err != nil {
		t.Fatalf("GenerateConfirmationToken() error = %v", err)
	}

	if token1 == "" {
		t.Error("Token should not be empty")
	}

	if token1 == token2 {
		t.Error("Tokens should be unique")
	}
}

func TestSecureCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"hello", "hello", true},
		{"hello", "world", false},
		{"", "", true},
		{"a", "", false},
	}

	for _, tt := range tests {
		got := SecureCompare(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("SecureCompare(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestValidateEmail(t *testing.T) {
	tests := []struct {
		email string
		want  bool
	}{
		{"user@example.com", true},
		{"user@sub.example.com", true},
		{"user+tag@example.com", true},
		{"", false},
		{"user", false},
		{"user@", false},
		{"@example.com", false},
		{"user@localhost", false},
	}

	for _, tt := range tests {
		got := ValidateEmail(tt.email)
		if got != tt.want {
			t.Errorf("ValidateEmail(%q) = %v, want %v", tt.email, got, tt.want)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		password string
		wantErr  bool
	}{
		{"password123", false},
		{"12345678", false},
		{"short", true},                  // too short
		{"", true},                       // empty
		{strings.Repeat("a", 73), true},  // too long for bcrypt
		{strings.Repeat("a", 72), false}, // max length
	}

	for _, tt := range tests {
		err := ValidatePassword(tt.password)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidatePassword(%q) error = %v, wantErr = %v", tt.password, err, tt.wantErr)
		}
	}
}

func BenchmarkHashPassword(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = HashPassword("testpassword123")
	}
}

func BenchmarkCheckPassword(b *testing.B) {
	hash, _ := HashPassword("testpassword123")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CheckPassword("testpassword123", hash)
	}
}
