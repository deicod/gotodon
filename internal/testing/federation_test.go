// Package testing provides federation compatibility testing.
package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// FederationTester tests ActivityPub federation compatibility
type FederationTester struct {
	LocalDomain  string
	RemoteDomain string
	Client       *http.Client
}

// NewFederationTester creates a new federation tester
func NewFederationTester(localDomain, remoteDomain string) *FederationTester {
	return &FederationTester{
		LocalDomain:  localDomain,
		RemoteDomain: remoteDomain,
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// FederationTestResult holds the result of a federation test
type FederationTestResult struct {
	TestName string
	Passed   bool
	Message  string
	Duration time.Duration
}

// TestWebFinger tests WebFinger discovery
func (ft *FederationTester) TestWebFinger(ctx context.Context, username string) FederationTestResult {
	start := time.Now()
	result := FederationTestResult{TestName: "WebFinger Discovery"}

	url := fmt.Sprintf("https://%s/.well-known/webfinger?resource=acct:%s@%s",
		ft.RemoteDomain, username, ft.RemoteDomain)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		result.Message = fmt.Sprintf("failed to create request: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	req.Header.Set("Accept", "application/jrd+json")

	resp, err := ft.Client.Do(req)
	if err != nil {
		result.Message = fmt.Sprintf("request failed: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		result.Message = fmt.Sprintf("unexpected status: %d", resp.StatusCode)
		result.Duration = time.Since(start)
		return result
	}

	var webfinger struct {
		Subject string `json:"subject"`
		Links   []struct {
			Rel  string `json:"rel"`
			Type string `json:"type"`
			Href string `json:"href"`
		} `json:"links"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&webfinger); err != nil {
		result.Message = fmt.Sprintf("failed to parse response: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	// Verify self link exists
	hasSelfLink := false
	for _, link := range webfinger.Links {
		if link.Rel == "self" && strings.Contains(link.Type, "activity") {
			hasSelfLink = true
			break
		}
	}

	if !hasSelfLink {
		result.Message = "missing ActivityPub self link"
		result.Duration = time.Since(start)
		return result
	}

	result.Passed = true
	result.Message = "WebFinger discovery successful"
	result.Duration = time.Since(start)
	return result
}

// TestActorFetch tests fetching an actor
func (ft *FederationTester) TestActorFetch(ctx context.Context, actorURL string) FederationTestResult {
	start := time.Now()
	result := FederationTestResult{TestName: "Actor Fetch"}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, actorURL, nil)
	if err != nil {
		result.Message = fmt.Sprintf("failed to create request: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	req.Header.Set("Accept", "application/activity+json")

	resp, err := ft.Client.Do(req)
	if err != nil {
		result.Message = fmt.Sprintf("request failed: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		result.Message = fmt.Sprintf("unexpected status: %d", resp.StatusCode)
		result.Duration = time.Since(start)
		return result
	}

	var actor map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&actor); err != nil {
		result.Message = fmt.Sprintf("failed to parse response: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	// Verify required fields
	required := []string{"@context", "id", "type", "inbox", "outbox", "preferredUsername"}
	for _, field := range required {
		if _, exists := actor[field]; !exists {
			result.Message = fmt.Sprintf("missing required field: %s", field)
			result.Duration = time.Since(start)
			return result
		}
	}

	result.Passed = true
	result.Message = "Actor fetch successful"
	result.Duration = time.Since(start)
	return result
}

// TestNodeInfo tests NodeInfo endpoint
func (ft *FederationTester) TestNodeInfo(ctx context.Context) FederationTestResult {
	start := time.Now()
	result := FederationTestResult{TestName: "NodeInfo"}

	// First get the nodeinfo location
	wellKnownURL := fmt.Sprintf("https://%s/.well-known/nodeinfo", ft.RemoteDomain)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnownURL, nil)
	if err != nil {
		result.Message = fmt.Sprintf("failed to create request: %v", err)
		result.Duration = time.Since(start)
		return result
	}

	resp, err := ft.Client.Do(req)
	if err != nil {
		result.Message = fmt.Sprintf("request failed: %v", err)
		result.Duration = time.Since(start)
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		result.Message = fmt.Sprintf("unexpected status: %d", resp.StatusCode)
		result.Duration = time.Since(start)
		return result
	}

	result.Passed = true
	result.Message = "NodeInfo endpoint accessible"
	result.Duration = time.Since(start)
	return result
}

// RunAllTests runs all federation tests
func (ft *FederationTester) RunAllTests(ctx context.Context, username string) []FederationTestResult {
	results := make([]FederationTestResult, 0)

	// Test WebFinger
	results = append(results, ft.TestWebFinger(ctx, username))

	// Test NodeInfo
	results = append(results, ft.TestNodeInfo(ctx))

	return results
}

// PrintResults prints test results
func PrintResults(results []FederationTestResult) {
	fmt.Println("\n=== Federation Test Results ===")
	passed := 0
	for _, r := range results {
		status := "✗"
		if r.Passed {
			status = "✓"
			passed++
		}
		fmt.Printf("%s %s (%s): %s\n", status, r.TestName, r.Duration, r.Message)
	}
	fmt.Printf("\nTotal: %d/%d passed\n", passed, len(results))
}
