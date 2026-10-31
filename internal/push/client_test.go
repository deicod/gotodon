package push

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deicod/gotodon/pkg/safehttp"
	"github.com/deicod/gotodon/pkg/safehttp/safehttptest"
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
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	vapidKey, err := NewVAPIDKey(testVAPIDPrivate, "")
	if err != nil {
		t.Fatalf("NewVAPIDKey: %v", err)
	}
	client := NewClient(vapidKey, "mailto:admin@example.com", safehttptest.Client(t, map[string]*httptest.Server{"push.example.com": server}))

	payload := []byte(`{"title":"hi"}`)
	status, err := client.Send(context.Background(), Subscription{
		Endpoint: "https://push.example.com/send/abc",
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

// testSubscriptionKeys returns the p256dh and auth values of a new browser
// subscription.
func testSubscriptionKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	uaPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating ua key: %v", err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatalf("generating auth secret: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(uaPrivate.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(authSecret)
}

// newInternalPushServer starts a push service on a loopback address that
// counts the messages reaching it; none ever should.
func newInternalPushServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func TestClientSendRefusesNonPublicEndpoints(t *testing.T) {
	internal, hits := newInternalPushServer(t)
	_, port, _ := net.SplitHostPort(internal.Listener.Addr().String())
	vapidKey, err := NewVAPIDKey(testVAPIDPrivate, "")
	if err != nil {
		t.Fatalf("NewVAPIDKey: %v", err)
	}
	p256dh, auth := testSubscriptionKeys(t)

	// The default client, resolving through the system resolver.
	client := NewClient(vapidKey, "mailto:admin@example.com", nil)
	for _, endpoint := range []string{
		internal.URL + "/send/abc",
		"https://localhost:" + port + "/send/abc",
		"https://[::1]:" + port + "/send/abc",
		"https://169.254.169.254/send/abc",
	} {
		t.Run(endpoint, func(t *testing.T) {
			status, err := client.Send(context.Background(), Subscription{Endpoint: endpoint, P256dh: p256dh, Auth: auth}, []byte("hi"))
			if !safehttp.IsRefused(err) || status != 0 {
				t.Fatalf("Send = %d, %v, want a refusal", status, err)
			}
		})
	}

	t.Run("redirect to an internal address", func(t *testing.T) {
		pushService := httptest.NewTLSServer(http.RedirectHandler(internal.URL+"/send/abc", http.StatusTemporaryRedirect))
		defer pushService.Close()
		client := NewClient(vapidKey, "mailto:admin@example.com", safehttptest.Client(t, map[string]*httptest.Server{"push.example.com": pushService}))

		status, err := client.Send(context.Background(), Subscription{Endpoint: "https://push.example.com/send/abc", P256dh: p256dh, Auth: auth}, []byte("hi"))
		if !errors.Is(err, safehttp.ErrForbiddenAddress) || status != 0 {
			t.Fatalf("Send = %d, %v, want ErrForbiddenAddress", status, err)
		}
	})

	if n := hits.Load(); n != 0 {
		t.Fatalf("internal server received %d messages, want 0", n)
	}
}

func TestValidateEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"https://updates.push.services.mozilla.com/wpush/v2/token123",
		"https://fcm.googleapis.com/fcm/send/token123",
		"https://push.example.com:8443/send",
		"https://8.8.8.8/send",
		"https://[2606:4700:4700::1111]/send",
	} {
		if err := ValidateEndpoint(endpoint); err != nil {
			t.Errorf("ValidateEndpoint(%q) = %v, want nil", endpoint, err)
		}
	}

	for _, endpoint := range []string{
		"",
		"://missing-scheme",
		"/relative/send",
		"http://push.example.com/send",
		"wss://push.example.com/send",
		"mailto:admin@example.com",
		"https:///send",
		"https://user:secret@push.example.com/send",
		"https://push.example.com:0/send",
		"https://push.example.com:65536/send",
		"https://localhost/send",
		"https://LOCALHOST./send",
		"https://push.localhost/send",
		"https://127.0.0.1/send",
		"https://[::1]/send",
		"https://10.1.2.3/send",
		"https://169.254.169.254/latest/meta-data/",
		"https://[fe80::1%25eth0]/send",
		"https://[::ffff:192.168.0.1]/send",
	} {
		if err := ValidateEndpoint(endpoint); !errors.Is(err, ErrInvalidEndpoint) {
			t.Errorf("ValidateEndpoint(%q) = %v, want ErrInvalidEndpoint", endpoint, err)
		}
	}
}
