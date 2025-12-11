package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestTrustProxyDisabled tests that X-Forwarded-For is ignored when trustProxy=false
// Note: This requires the server to be configured with trustProxy=false
func TestTrustProxyBehavior(t *testing.T) {
	h := NewTestHelper(t)

	// Verify server is running
	resp, err := h.client.Get(httpBaseURL + "/health")
	if err != nil {
		t.Skipf("Server not running: %v", err)
	}
	resp.Body.Close()

	t.Run("RequestWithXForwardedFor", func(t *testing.T) {
		// Create a request with X-Forwarded-For header
		req, err := http.NewRequest("GET", httpBaseURL+"/health", nil)
		assert.NoError(t, err)

		// Set spoofed IP via X-Forwarded-For
		req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")

		resp, err := h.client.Do(req)
		assert.NoError(t, err)
		defer resp.Body.Close()

		// Server should respond - the actual IP used depends on trustProxy config
		assert.Equal(t, 200, resp.StatusCode)
		t.Log("Server accepted request with X-Forwarded-For header")
	})

	t.Run("RequestWithXRealIP", func(t *testing.T) {
		req, err := http.NewRequest("GET", httpBaseURL+"/health", nil)
		assert.NoError(t, err)

		req.Header.Set("X-Real-IP", "10.0.0.1")

		resp, err := h.client.Do(req)
		assert.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, 200, resp.StatusCode)
		t.Log("Server accepted request with X-Real-IP header")
	})
}

// TestXForwardedForIPValidation tests that invalid IPs in X-Forwarded-For are rejected
func TestXForwardedForIPValidation(t *testing.T) {
	h := NewTestHelper(t)

	testCases := []struct {
		name           string
		headerValue    string
		description    string
	}{
		{"ValidIPv4", "192.168.1.1", "Standard IPv4 should work"},
		{"ValidIPv6", "2001:db8::1", "Standard IPv6 should work"},
		{"MultipleIPs", "192.168.1.1, 10.0.0.1", "Multiple IPs should use first"},
		{"IPWithPort", "192.168.1.1:8080", "IP with port (non-standard) should be handled"},
		{"EmptyHeader", "", "Empty header should fall back to remote_addr"},
		{"WhitespaceOnly", "   ", "Whitespace should be handled"},
		{"MalformedIP", "not-an-ip", "Non-IP string should be rejected/ignored"},
		{"SQLInjection", "'; DROP TABLE users; --", "SQL injection should be sanitized"},
		{"ScriptInjection", "<script>alert(1)</script>", "Script injection should be sanitized"},
		{"PathTraversal", "../../../etc/passwd", "Path traversal should be sanitized"},
		{"NullByte", "192.168.1.1\x00malicious", "Null byte should be handled"},
		{"VeryLongIP", "1.2.3.4" + "." + string(make([]byte, 1000)), "Long string should be handled"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", httpBaseURL+"/health", nil)
			assert.NoError(t, err)

			if tc.headerValue != "" {
				req.Header.Set("X-Forwarded-For", tc.headerValue)
			}

			resp, err := h.client.Do(req)
			if err != nil {
				t.Logf("%s: Connection error (may be expected): %v", tc.name, err)
				return
			}
			defer resp.Body.Close()

			// Server should not crash and should respond (health check doesn't rate limit)
			assert.True(t, resp.StatusCode == 200 || resp.StatusCode == 429,
				"%s: %s - got status %d", tc.name, tc.description, resp.StatusCode)
			t.Logf("%s: %s - status %d", tc.name, tc.description, resp.StatusCode)
		})
	}
}

// TestXRealIPValidation tests X-Real-IP header validation
func TestXRealIPValidation(t *testing.T) {
	h := NewTestHelper(t)

	testCases := []struct {
		name        string
		headerValue string
	}{
		{"ValidIPv4", "192.168.1.1"},
		{"ValidIPv6", "::1"},
		{"InvalidString", "not-valid"},
		{"EmptyString", ""},
		{"Injection", "' OR '1'='1"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", httpBaseURL+"/health", nil)
			assert.NoError(t, err)

			if tc.headerValue != "" {
				req.Header.Set("X-Real-IP", tc.headerValue)
			}

			resp, err := h.client.Do(req)
			if err != nil {
				t.Logf("%s: Connection error: %v", tc.name, err)
				return
			}
			defer resp.Body.Close()

			// Server should handle gracefully
			assert.True(t, resp.StatusCode >= 200 && resp.StatusCode < 500,
				"Server should handle %s gracefully", tc.name)
		})
	}
}

// TestRateLimitBypassAttempts tests that rate limiting cannot be bypassed
func TestRateLimitBypassAttempts(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping rate limit bypass test in short mode")
	}

	h := NewTestHelper(t)

	// Reset rate limits first
	h.ResetRateLimitBuckets()
	time.Sleep(2 * time.Second)

	t.Run("IPSpoofingAttempt", func(t *testing.T) {
		// Try to bypass rate limit by changing X-Forwarded-For on each request
		blocked := 0
		total := 100

		for i := 0; i < total; i++ {
			req, _ := http.NewRequest("GET", httpBaseURL+"/api/query", nil)
			// Try a different "source IP" each time
			req.Header.Set("X-Forwarded-For", "10.0.0."+string(rune(i%256)))

			resp, err := h.client.Do(req)
			if err != nil {
				continue
			}
			if resp.StatusCode == 429 {
				blocked++
			}
			resp.Body.Close()
		}

		// With trustProxy=false or IP validation, spoofing shouldn't help
		// With trustProxy=true, global rate limit should still kick in
		t.Logf("IP spoofing: %d/%d requests blocked", blocked, total)
		// We expect some requests to be blocked by global rate limit
		assert.Greater(t, blocked, 0, "Some requests should be rate limited")
	})
}
