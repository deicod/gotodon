// Package oauth provides OAuth 2.0 authentication for Mastodon-compatible API.
// This file implements RFC 8628 Device Authorization Grant.
package oauth

import (
	"crypto/rand"
	"math/big"
	"time"
)

// DeviceAuthorization represents a pending device authorization request
type DeviceAuthorization struct {
	DeviceCode      string    `json:"device_code"`
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresIn       int       `json:"expires_in"`
	Interval        int       `json:"interval"`
	CreatedAt       time.Time `json:"-"`

	// Internal fields
	ClientID string `json:"-"`
	Scopes   string `json:"-"`
	UserID   *int64 `json:"-"` // Set when user authorizes
}

// DeviceAuthorizationResponse is the response for POST /oauth/authorize/device
type DeviceAuthorizationResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// DeviceTokenRequest is the request for POST /oauth/token with device_code grant
type DeviceTokenRequest struct {
	GrantType  string `json:"grant_type" form:"grant_type"`
	DeviceCode string `json:"device_code" form:"device_code"`
	ClientID   string `json:"client_id" form:"client_id"`
}

// Device authorization constants
const (
	// DeviceCodeLength is the length of the device code
	DeviceCodeLength = 40

	// UserCodeLength is the length of the user-friendly code
	UserCodeLength = 8

	// DeviceCodeExpiresIn is how long device codes are valid (seconds)
	DeviceCodeExpiresIn = 900 // 15 minutes

	// DeviceCodeInterval is how often clients should poll (seconds)
	DeviceCodeInterval = 5
)

// Device authorization errors (RFC 8628)
const (
	DeviceAuthorizationPending = "authorization_pending"
	DeviceSlowDown             = "slow_down"
	DeviceAccessDenied         = "access_denied"
	DeviceExpiredToken         = "expired_token"
)

// GenerateDeviceCode generates a cryptographically secure device code
func GenerateDeviceCode() (string, error) {
	return GenerateToken(DeviceCodeLength)
}

// GenerateUserCode generates a user-friendly code (e.g., "ABCD-1234")
// Uses uppercase letters and numbers, excluding confusing characters (0, O, I, 1, L)
func GenerateUserCode() (string, error) {
	const charset = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // No 0, O, I, 1, L
	const codeLen = 8

	code := make([]byte, codeLen)
	for i := 0; i < codeLen; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		code[i] = charset[n.Int64()]
	}

	// Format as XXXX-XXXX for readability
	return string(code[:4]) + "-" + string(code[4:]), nil
}

// NewDeviceAuthorization creates a new device authorization request
func NewDeviceAuthorization(clientID, scopes, verificationURI string) (*DeviceAuthorization, error) {
	deviceCode, err := GenerateDeviceCode()
	if err != nil {
		return nil, err
	}

	userCode, err := GenerateUserCode()
	if err != nil {
		return nil, err
	}

	return &DeviceAuthorization{
		DeviceCode:      deviceCode,
		UserCode:        userCode,
		VerificationURI: verificationURI,
		ExpiresIn:       DeviceCodeExpiresIn,
		Interval:        DeviceCodeInterval,
		CreatedAt:       time.Now(),
		ClientID:        clientID,
		Scopes:          scopes,
	}, nil
}

// IsExpired checks if the device authorization has expired
func (d *DeviceAuthorization) IsExpired() bool {
	return time.Since(d.CreatedAt) > time.Duration(d.ExpiresIn)*time.Second
}

// IsAuthorized checks if the user has authorized this device
func (d *DeviceAuthorization) IsAuthorized() bool {
	return d.UserID != nil
}

// ToResponse converts to the API response format
func (d *DeviceAuthorization) ToResponse() DeviceAuthorizationResponse {
	return DeviceAuthorizationResponse{
		DeviceCode:              d.DeviceCode,
		UserCode:                d.UserCode,
		VerificationURI:         d.VerificationURI,
		VerificationURIComplete: d.VerificationURI + "?user_code=" + d.UserCode,
		ExpiresIn:               d.ExpiresIn,
		Interval:                d.Interval,
	}
}
