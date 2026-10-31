package middleware

import (
	"errors"
	"net/http"

	"github.com/deicod/gotodon/internal/web"
)

// LimitRequestBody refuses request bodies larger than maxBytes, which must be
// positive, with 413 Request Entity Too Large. A request that declares a larger
// Content-Length is refused before its body is read. Otherwise reading the body
// past maxBytes fails with an *http.MaxBytesError, which handlers should answer
// with 413 as well.
func LimitRequestBody(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				writeRequestTooLarge(w)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// writeBodyReadError answers a request whose body could not be read: 413 when
// it exceeded the LimitRequestBody limit, 400 otherwise.
func writeBodyReadError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeRequestTooLarge(w)
		return
	}
	web.JSONError(w, http.StatusBadRequest, "Invalid request body")
}

func writeRequestTooLarge(w http.ResponseWriter) {
	web.JSONError(w, http.StatusRequestEntityTooLarge, "Request body too large")
}
