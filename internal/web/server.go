package web

import (
	"net/http"
	"time"
)

// Connection limits applied by NewServer. Ordinary API requests are small and
// answered quickly, so the whole-request budgets stay tight. Handlers that
// legitimately need longer lift them for their own request: media uploads
// through ExtendUploadDeadlines, streaming responses through
// http.ResponseController (WebSocket upgrades drop them when hijacking).
const (
	// serverReadHeaderTimeout bounds how long a client may take to send the
	// request line and headers, so slow or incomplete headers cannot pin
	// connections.
	serverReadHeaderTimeout = 10 * time.Second
	// serverReadTimeout bounds reading the whole request, body included.
	serverReadTimeout = 60 * time.Second
	// serverWriteTimeout bounds the time from the end of the request headers
	// until the response has been written.
	serverWriteTimeout = 60 * time.Second
	// serverIdleTimeout closes keep-alive connections that sit idle between
	// requests.
	serverIdleTimeout = 120 * time.Second
	// serverMaxHeaderBytes caps the request line plus headers; larger
	// requests are answered with 431.
	serverMaxHeaderBytes = 64 << 10
	// uploadTimeout replaces the read and write budgets of an authenticated
	// upload: a maximum-size video (~99 MB) still arrives at ~1 Mbit/s.
	uploadTimeout = 15 * time.Minute
)

// NewServer returns an http.Server for addr with defensive connection limits,
// so slow or stalled clients cannot hold connections open indefinitely.
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    serverMaxHeaderBytes,
	}
}

// ExtendUploadDeadlines gives the current request uploadTimeout to finish
// receiving its body and writing the response, in place of the server's
// ordinary read and write timeouts. Call it once the request is
// authenticated and before reading the body. Writers without deadlines, such
// as test recorders, are left unchanged.
func ExtendUploadDeadlines(w http.ResponseWriter) {
	deadline := time.Now().Add(uploadTimeout)
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(deadline)
	_ = rc.SetWriteDeadline(deadline)
}
