// Package auth provides user authentication utilities.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// Errors
var (
	ErrInvalidCredentials  = errors.New("invalid email or password")
	ErrAccountDisabled     = errors.New("account is disabled")
	ErrAccountNotApproved  = errors.New("account is pending approval")
	ErrAccountNotConfirmed = errors.New("email address not confirmed")
)

// HashPassword creates a bcrypt hash of a password (Devise-compatible)
func HashPassword(password string) (string, error) {
	// Mastodon/Devise uses cost 12 by default
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return string(hash), nil
}

// CheckPassword verifies a password against a bcrypt hash
func CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// GenerateConfirmationToken generates a secure token for email confirmation
func GenerateConfirmationToken() (string, error) {
	bytes := make([]byte, 20)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// GenerateResetToken generates a secure token for password reset
func GenerateResetToken() (string, error) {
	bytes := make([]byte, 20)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// SecureCompare performs a constant-time string comparison
func SecureCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ValidateEmail performs basic email validation
func ValidateEmail(email string) bool {
	if email == "" {
		return false
	}
	// Basic check: contains @ and a dot after @
	atIdx := strings.LastIndex(email, "@")
	if atIdx < 1 {
		return false
	}
	domain := email[atIdx+1:]
	if len(domain) < 3 || !strings.Contains(domain, ".") {
		return false
	}
	return true
}

// ValidatePassword checks password requirements
func ValidatePassword(password string) error {
	if len(password) < 8 {
		return errors.New("password is too short (minimum 8 characters)")
	}
	if len(password) > 72 {
		// bcrypt has a 72-byte limit
		return errors.New("password is too long (maximum 72 characters)")
	}
	return nil
}
