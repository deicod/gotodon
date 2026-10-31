package middleware

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/pkg/activitypub"
)

// countingReader records how many bytes were read from it.
type countingReader struct {
	r    io.Reader
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += n
	return n, err
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("connection reset")
}

func TestLimitRequestBody(t *testing.T) {
	const limit = 16

	tests := []struct {
		name          string
		body          string
		unknownLength bool
		wantStatus    int
		wantNext      bool
		wantUnread    bool
	}{
		{name: "empty body", body: "", wantStatus: http.StatusOK, wantNext: true},
		{name: "within limit", body: strings.Repeat("a", limit-1), wantStatus: http.StatusOK, wantNext: true},
		{name: "at limit", body: strings.Repeat("a", limit), wantStatus: http.StatusOK, wantNext: true},
		{name: "declared length over limit", body: strings.Repeat("a", limit+1), wantStatus: http.StatusRequestEntityTooLarge, wantUnread: true},
		{name: "unknown length at limit", body: strings.Repeat("a", limit), unknownLength: true, wantStatus: http.StatusOK, wantNext: true},
		{name: "unknown length over limit", body: strings.Repeat("a", limit+1), unknownLength: true, wantStatus: http.StatusRequestEntityTooLarge, wantNext: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &countingReader{r: strings.NewReader(tt.body)}
			req := httptest.NewRequest(http.MethodPost, sigInboxURL, body)
			req.ContentLength = int64(len(tt.body))
			if tt.unknownLength {
				req.ContentLength = -1
			}

			nextCalled := false
			var got []byte
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				data, err := io.ReadAll(r.Body)
				if err != nil {
					writeBodyReadError(w, err)
					return
				}
				got = data
				w.WriteHeader(http.StatusOK)
			})

			rec := httptest.NewRecorder()
			LimitRequestBody(limit)(next).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if nextCalled != tt.wantNext {
				t.Fatalf("next handler called = %v, want %v", nextCalled, tt.wantNext)
			}
			if tt.wantUnread && body.read != 0 {
				t.Fatalf("read %d body bytes, want none", body.read)
			}
			if body.read > limit+1 {
				t.Fatalf("read %d body bytes, want at most %d", body.read, limit+1)
			}
			if tt.wantStatus == http.StatusOK && string(got) != tt.body {
				t.Fatalf("next handler body = %q, want %q", got, tt.body)
			}
		})
	}
}

func TestVerifyHTTPSignature_BodyLimit(t *testing.T) {
	key, publicPEM := generateSigningKey(t)
	actors := map[string]*activitypub.Actor{
		sigActorURI: {
			Object:    activitypub.Object{ID: sigActorURI},
			PublicKey: &activitypub.PublicKey{ID: sigKeyID, Owner: sigActorURI, PublicKeyPem: publicPEM},
		},
	}
	body := followActivity(sigActorURI)
	size := int64(len(body))

	tests := []struct {
		name          string
		limit         int64
		unknownLength bool
		wantStatus    int
		wantFetches   int
	}{
		{name: "within limit", limit: size + 1, wantStatus: http.StatusOK, wantFetches: 1},
		{name: "at limit", limit: size, wantStatus: http.StatusOK, wantFetches: 1},
		{name: "declared length over limit", limit: size - 1, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "unknown length at limit", limit: size, unknownLength: true, wantStatus: http.StatusOK, wantFetches: 1},
		{name: "unknown length over limit", limit: size - 1, unknownLength: true, wantStatus: http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher := &recordingActorFetcher{actors: actors}

			var signed SignedActivity
			nextCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				signed, _ = SignedActivityFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			})

			req := newSignedInboxPost(t, key, sigKeyID, sigMandatoryHeaders, body)
			if tt.unknownLength {
				req.ContentLength = -1
			}

			rec := httptest.NewRecorder()
			LimitRequestBody(tt.limit)(VerifyHTTPSignature(fetcher)(next)).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if fetcher.calls != tt.wantFetches {
				t.Fatalf("actor fetches = %d, want %d", fetcher.calls, tt.wantFetches)
			}
			if wantNext := tt.wantStatus == http.StatusOK; nextCalled != wantNext {
				t.Fatalf("next handler called = %v, want %v", nextCalled, wantNext)
			}
			if nextCalled && !bytes.Equal(signed.Body, body) {
				t.Fatalf("SignedActivity.Body = %q, want %q", signed.Body, body)
			}
		})
	}
}

func TestVerifyHTTPSignature_UnreadableBody(t *testing.T) {
	key, _ := generateSigningKey(t)
	fetcher := &recordingActorFetcher{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not run")
	})

	req := newSignedInboxPost(t, key, sigKeyID, sigMandatoryHeaders, followActivity(sigActorURI))
	req.Body = io.NopCloser(failingReader{})

	rec := httptest.NewRecorder()
	VerifyHTTPSignature(fetcher)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if fetcher.calls != 0 {
		t.Fatalf("actor fetches = %d, want 0", fetcher.calls)
	}
}
