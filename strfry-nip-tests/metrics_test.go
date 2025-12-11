package main

import (
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMetricsEndpoint tests the /metrics endpoint
func TestMetricsEndpoint(t *testing.T) {
	h := NewTestHelper(t)

	resp, err := h.client.Get(httpBaseURL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	h.AssertHTTPStatus(resp, 200)

	// Check content type
	contentType := resp.Header.Get("Content-Type")
	assert.Contains(t, contentType, "text/plain", "Metrics should be text/plain")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	metricsText := string(body)

	t.Log("Metrics endpoint responded with", len(metricsText), "bytes")
}

// TestMetricsPrometheusFormat tests that metrics follow Prometheus format
func TestMetricsPrometheusFormat(t *testing.T) {
	h := NewTestHelper(t)

	// Make some requests first to generate metrics
	h.HealthCheck()
	event := h.CreateWhitelistedEvent(1, "Metrics test event", nil)
	h.PostEventHTTP(event)

	resp, err := h.client.Get(httpBaseURL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	metricsText := string(body)

	// Check for required metrics
	requiredMetrics := []string{
		"http_requests_total",
		"http_rate_limit_hits_total",
		"http_rate_limit_blocks_total",
		"nostr_events_accepted_total",
		"nostr_events_rejected_total",
	}

	for _, metric := range requiredMetrics {
		assert.Contains(t, metricsText, metric, "Should contain %s metric", metric)
	}

	// Check for HELP and TYPE declarations (Prometheus format)
	assert.Contains(t, metricsText, "# HELP", "Should have HELP declarations")
	assert.Contains(t, metricsText, "# TYPE", "Should have TYPE declarations")

	t.Log("All required metrics present in Prometheus format")
}

// TestMetricsHistogramFormat tests histogram buckets format
func TestMetricsHistogramFormat(t *testing.T) {
	h := NewTestHelper(t)

	// Generate some requests with varying response times
	for i := 0; i < 5; i++ {
		h.HealthCheck()
	}

	resp, err := h.client.Get(httpBaseURL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	metricsText := string(body)

	// Check histogram buckets
	expectedBuckets := []string{
		`le="10"`,
		`le="50"`,
		`le="100"`,
		`le="500"`,
		`le="1000"`,
		`le="5000"`,
		`le="+Inf"`,
	}

	// Only check if duration metrics are present
	if strings.Contains(metricsText, "http_request_duration") {
		for _, bucket := range expectedBuckets {
			assert.Contains(t, metricsText, bucket, "Should have histogram bucket %s", bucket)
		}
		t.Log("Histogram buckets present and correctly formatted")
	} else {
		t.Log("Duration metrics not yet populated (no requests to tracked endpoints)")
	}
}

// TestMetricsLabelFormat tests that metric labels are properly formatted
func TestMetricsLabelFormat(t *testing.T) {
	h := NewTestHelper(t)

	// Make various types of requests
	h.HealthCheck()
	event := h.CreateWhitelistedEvent(1, "Label test", nil)
	h.PostEventHTTP(event)
	h.QueryEventsHTTP("GET", map[string]string{"limit": "1"}, nil)

	resp, err := h.client.Get(httpBaseURL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	metricsText := string(body)

	// Check for proper label formatting
	// Labels should be in format: metric_name{label="value"} number
	labelPattern := regexp.MustCompile(`[a-z_]+\{[^}]+\}\s+\d+`)
	matches := labelPattern.FindAllString(metricsText, -1)

	assert.NotEmpty(t, matches, "Should have metrics with labels")
	t.Logf("Found %d metrics with labels", len(matches))

	// Check for method labels
	if strings.Contains(metricsText, `method="GET"`) {
		t.Log("GET method label found")
	}
	if strings.Contains(metricsText, `method="POST"`) {
		t.Log("POST method label found")
	}

	// Check for status labels
	statusPattern := regexp.MustCompile(`status="(\d+)"`)
	statusMatches := statusPattern.FindAllStringSubmatch(metricsText, -1)
	for _, match := range statusMatches {
		t.Logf("Found status label: %s", match[1])
	}
}

// TestMetricsCounterIncrement tests that counters increment correctly
func TestMetricsCounterIncrement(t *testing.T) {
	h := NewTestHelper(t)

	// Get initial metrics
	resp1, err := h.client.Get(httpBaseURL + "/metrics")
	require.NoError(t, err)
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()

	// Make some requests
	for i := 0; i < 3; i++ {
		h.HealthCheck()
	}

	// Get updated metrics
	resp2, err := h.client.Get(httpBaseURL + "/metrics")
	require.NoError(t, err)
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	// Compare - metrics should have changed
	assert.NotEqual(t, string(body1), string(body2), "Metrics should change after requests")
	t.Log("Metrics counters increment correctly")
}

// TestMetricsEndpointNotRateLimited tests that /metrics is exempt from rate limiting
func TestMetricsEndpointNotRateLimited(t *testing.T) {
	h := NewTestHelper(t)

	// Make many rapid requests to /metrics
	successCount := 0
	totalRequests := 100

	for i := 0; i < totalRequests; i++ {
		resp, err := h.client.Get(httpBaseURL + "/metrics")
		if err != nil {
			continue
		}
		if resp.StatusCode == 200 {
			successCount++
		}
		resp.Body.Close()
	}

	// All requests should succeed (no rate limiting on /metrics)
	assert.Equal(t, totalRequests, successCount,
		"/metrics should not be rate limited, got %d/%d success", successCount, totalRequests)
	t.Logf("Metrics endpoint: %d/%d requests succeeded (no rate limiting)", successCount, totalRequests)
}

// TestMetricsNoSensitiveData tests that metrics don't expose sensitive information
func TestMetricsNoSensitiveData(t *testing.T) {
	h := NewTestHelper(t)

	resp, err := h.client.Get(httpBaseURL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	metricsText := strings.ToLower(string(body))

	// Check that sensitive data is not exposed
	sensitivePatterns := []string{
		"password",
		"secret",
		"key",
		"token",
		"auth",
		"credential",
		"private",
	}

	for _, pattern := range sensitivePatterns {
		// These should not appear in metric names or values
		// (they might appear in HELP text describing what the metric is for)
		lines := strings.Split(metricsText, "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "#") {
				continue // Skip comments
			}
			assert.NotContains(t, line, pattern,
				"Metrics should not contain sensitive term '%s' in data", pattern)
		}
	}

	t.Log("No sensitive data found in metrics")
}
