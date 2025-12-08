// Package testing provides benchmarks for API performance testing.
package testing

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// BenchmarkRequest creates a benchmark test request
func BenchmarkRequest(b *testing.B, e *echo.Echo, method, path, body string, handler echo.HandlerFunc) {
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.Set("account_id", int64(12345))
		c.Set("scopes", "read write")

		_ = handler(c)
	}
}

// APIBenchmarks holds benchmark configurations
type APIBenchmarks struct {
	Echo *echo.Echo
}

// NewAPIBenchmarks creates a new benchmark runner
func NewAPIBenchmarks() *APIBenchmarks {
	return &APIBenchmarks{
		Echo: echo.New(),
	}
}

// BenchmarkConfig holds config for a single benchmark
type BenchmarkConfig struct {
	Name    string
	Method  string
	Path    string
	Body    string
	Handler echo.HandlerFunc
}

// RunBenchmarks runs all configured benchmarks
func (ab *APIBenchmarks) RunBenchmarks(b *testing.B, configs []BenchmarkConfig) {
	for _, config := range configs {
		b.Run(config.Name, func(b *testing.B) {
			BenchmarkRequest(b, ab.Echo, config.Method, config.Path, config.Body, config.Handler)
		})
	}
}

// PerformanceMetrics tracks API performance
type PerformanceMetrics struct {
	Endpoint      string
	Method        string
	RequestCount  int64
	TotalDuration int64 // nanoseconds
	MinDuration   int64
	MaxDuration   int64
}

// AverageDuration returns average request duration
func (m *PerformanceMetrics) AverageDuration() int64 {
	if m.RequestCount == 0 {
		return 0
	}
	return m.TotalDuration / m.RequestCount
}

// MetricsCollector collects performance metrics
type MetricsCollector struct {
	metrics map[string]*PerformanceMetrics
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		metrics: make(map[string]*PerformanceMetrics),
	}
}

// Record records a request metric
func (mc *MetricsCollector) Record(endpoint, method string, durationNs int64) {
	key := method + " " + endpoint
	m, exists := mc.metrics[key]
	if !exists {
		m = &PerformanceMetrics{
			Endpoint:    endpoint,
			Method:      method,
			MinDuration: durationNs,
			MaxDuration: durationNs,
		}
		mc.metrics[key] = m
	}

	m.RequestCount++
	m.TotalDuration += durationNs

	if durationNs < m.MinDuration {
		m.MinDuration = durationNs
	}
	if durationNs > m.MaxDuration {
		m.MaxDuration = durationNs
	}
}

// GetMetrics returns all collected metrics
func (mc *MetricsCollector) GetMetrics() map[string]*PerformanceMetrics {
	return mc.metrics
}
