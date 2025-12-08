package oauth

import (
	"strings"
	"testing"
)

func TestGenerateDeviceCode(t *testing.T) {
	code1, err := GenerateDeviceCode()
	if err != nil {
		t.Fatalf("GenerateDeviceCode() error = %v", err)
	}

	code2, err := GenerateDeviceCode()
	if err != nil {
		t.Fatalf("GenerateDeviceCode() error = %v", err)
	}

	if code1 == "" {
		t.Error("Device code should not be empty")
	}

	if code1 == code2 {
		t.Error("Device codes should be unique")
	}

	// Should be base64url encoded
	if len(code1) < DeviceCodeLength {
		t.Errorf("Device code length = %d, want >= %d", len(code1), DeviceCodeLength)
	}
}

func TestGenerateUserCode(t *testing.T) {
	code1, err := GenerateUserCode()
	if err != nil {
		t.Fatalf("GenerateUserCode() error = %v", err)
	}

	code2, err := GenerateUserCode()
	if err != nil {
		t.Fatalf("GenerateUserCode() error = %v", err)
	}

	if code1 == "" {
		t.Error("User code should not be empty")
	}

	if code1 == code2 {
		t.Error("User codes should be unique")
	}

	// Format should be XXXX-XXXX
	if len(code1) != 9 {
		t.Errorf("User code length = %d, want 9 (XXXX-XXXX)", len(code1))
	}

	if code1[4] != '-' {
		t.Errorf("User code should have dash in middle, got %s", code1)
	}

	// Should not contain confusing characters
	confusing := "0OI1L"
	for _, c := range confusing {
		if strings.ContainsRune(code1, c) {
			t.Errorf("User code should not contain %c, got %s", c, code1)
		}
	}
}

func TestNewDeviceAuthorization(t *testing.T) {
	auth, err := NewDeviceAuthorization("test-client", "read write", "https://example.com/oauth/authorize/device")
	if err != nil {
		t.Fatalf("NewDeviceAuthorization() error = %v", err)
	}

	if auth.DeviceCode == "" {
		t.Error("DeviceCode should not be empty")
	}

	if auth.UserCode == "" {
		t.Error("UserCode should not be empty")
	}

	if auth.ClientID != "test-client" {
		t.Errorf("ClientID = %q, want %q", auth.ClientID, "test-client")
	}

	if auth.Scopes != "read write" {
		t.Errorf("Scopes = %q, want %q", auth.Scopes, "read write")
	}

	if auth.ExpiresIn != DeviceCodeExpiresIn {
		t.Errorf("ExpiresIn = %d, want %d", auth.ExpiresIn, DeviceCodeExpiresIn)
	}

	if auth.Interval != DeviceCodeInterval {
		t.Errorf("Interval = %d, want %d", auth.Interval, DeviceCodeInterval)
	}

	if auth.IsExpired() {
		t.Error("New authorization should not be expired")
	}

	if auth.IsAuthorized() {
		t.Error("New authorization should not be authorized")
	}
}

func TestDeviceAuthorizationToResponse(t *testing.T) {
	auth, _ := NewDeviceAuthorization("test-client", "read", "https://example.com/oauth/authorize/device")
	resp := auth.ToResponse()

	if resp.DeviceCode != auth.DeviceCode {
		t.Error("Response DeviceCode should match")
	}

	if resp.UserCode != auth.UserCode {
		t.Error("Response UserCode should match")
	}

	expectedComplete := auth.VerificationURI + "?user_code=" + auth.UserCode
	if resp.VerificationURIComplete != expectedComplete {
		t.Errorf("VerificationURIComplete = %q, want %q", resp.VerificationURIComplete, expectedComplete)
	}
}
