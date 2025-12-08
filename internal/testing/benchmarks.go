// Package testing provides benchmarks for API performance testing.
package testing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/gotodon/internal/api/middleware"
)

// BenchmarkRequest creates a benchmark test request
func BenchmarkRequest(b *testing.B, method, path, body string, handler http.Handler) {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	ctx := request.Context()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, int64(12345))
	ctx = context.WithValue(ctx, middleware.ContextKeyScopes, "read write")
	request = request.WithContext(ctx)

	for i := 0; i < b.N; i++ {
		// Create new recorder for each iteration to avoid buffer growth issues
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, request)
	}
}

// APIBenchmarks holds benchmark configurations
type APIBenchmarks struct {
}

// NewAPIBenchmarks creates a new benchmark runner
func NewAPIBenchmarks() *APIBenchmarks {
	return &APIBenchmarks{}
}

// BenchmarkConfig holds config for a single benchmark
type BenchmarkConfig struct {
	Name    string
	Method  string
	Path    string
	Body    string
	Handler http.Handler
}

// RunBenchmarks runs all configured benchmarks
func (ab *APIBenchmarks) RunBenchmarks(b *testing.B, configs []BenchmarkConfig) {
	for _, config := range configs {
		b.Run(config.Name, func(b *testing.B) {
			BenchmarkRequest(b, config.Method, config.Path, config.Body, config.Handler)
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
