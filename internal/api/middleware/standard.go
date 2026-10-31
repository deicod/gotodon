package middleware

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"
)

// Logger logs request details
func Logger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrapper to capture status code
			ww := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(ww, r)

			logger.Info("request",
				"method", r.Method,
				"uri", r.URL.RequestURI(),
				"status", ww.status,
				"duration", time.Since(start),
				"ip", r.RemoteAddr,
			)
		})
	}
}

// CORS middleware
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization, Origin")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Recover middleware
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic", "error", err, "stack", string(debug.Stack()))
					http.Error(w, `{"error":"Internal Server Error"}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the wrapped writer to http.ResponseController, so handlers
// can adjust connection deadlines through the logger.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// FlushError flushes buffered data to the client and reports writers that
// cannot flush; http.ResponseController prefers it over Flush.
func (rw *responseWriter) FlushError() error {
	return http.NewResponseController(rw.ResponseWriter).Flush()
}

// Flush implements http.Flusher for handlers that stream responses.
func (rw *responseWriter) Flush() {
	_ = rw.FlushError()
}

// Hijack implements http.Hijacker so WebSocket upgrades work through the
// logger. A hijacked request is logged as 101 Switching Protocols.
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, brw, err := http.NewResponseController(rw.ResponseWriter).Hijack()
	if err == nil {
		rw.status = http.StatusSwitchingProtocols
	}
	return conn, brw, err
}
