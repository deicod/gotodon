package push

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientSend(t *testing.T) {
	uaPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating ua key: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatalf("generating auth secret: %v", err)
	}

	var (
		gotHeaders http.Header
		gotBody    []byte
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	vapidKey, err := NewVAPIDKey(testVAPIDPrivate, "")
	if err != nil {
		t.Fatalf("NewVAPIDKey: %v", err)
	}
	client := NewClient(vapidKey, "mailto:admin@example.com", server.Client())

	payload := []byte(`{"title":"hi"}`)
	status, err := client.Send(context.Background(), Subscription{
		Endpoint: server.URL + "/send/abc",
		P256dh:   base64.RawURLEncoding.EncodeToString(uaPrivate.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(authSecret),
	}, payload)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if status != http.StatusCreated {
		t.Errorf("status = %d", status)
	}

	if got := gotHeaders.Get("Content-Encoding"); got != "aes128gcm" {
		t.Errorf("Content-Encoding = %q", got)
	}
	if got := gotHeaders.Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := gotHeaders.Get("TTL"); got != "172800" {
		t.Errorf("TTL = %q", got)
	}
	if got := gotHeaders.Get("Urgency"); got != "normal" {
		t.Errorf("Urgency = %q", got)
	}
	authorization := gotHeaders.Get("Authorization")
	if !strings.HasPrefix(authorization, "vapid t=") || !strings.Contains(authorization, ",k="+vapidKey.PublicKey()) {
		t.Errorf("Authorization = %q", authorization)
	}

	// The body must decrypt back to the payload with the subscription keys.
	if got := decryptAES128GCM(t, gotBody, uaPrivate, authSecret); !bytes.Equal(got, payload) {
		t.Errorf("decrypted body = %q, want %q", got, payload)
	}
}

func TestClientCachesVAPIDAuthorization(t *testing.T) {
	vapidKey, err := NewVAPIDKey(testVAPIDPrivate, "")
	if err != nil {
		t.Fatalf("NewVAPIDKey: %v", err)
	}
	client := NewClient(vapidKey, "mailto:admin@example.com", nil)

	first, err := client.authorization("https://push.example.org")
	if err != nil {
		t.Fatalf("authorization: %v", err)
	}
	second, err := client.authorization("https://push.example.org")
	if err != nil {
		t.Fatalf("authorization: %v", err)
	}
	if first != second {
		t.Error("expected cached Authorization header to be reused for the same audience")
	}
	other, err := client.authorization("https://other.example.org")
	if err != nil {
		t.Fatalf("authorization: %v", err)
	}
	if other == first {
		t.Error("expected a distinct token per audience")
	}
}

func TestClientSendRejectsBadKeys(t *testing.T) {
	vapidKey, err := NewVAPIDKey(testVAPIDPrivate, "")
	if err != nil {
		t.Fatalf("NewVAPIDKey: %v", err)
	}
	client := NewClient(vapidKey, "mailto:admin@example.com", nil)
	if _, err := client.Send(context.Background(), Subscription{
		Endpoint: "https://push.example.org/send/abc",
		P256dh:   "!!!",
		Auth:     "AAAA",
	}, []byte("hi")); err == nil {
		t.Error("expected error for invalid p256dh")
	}
}
