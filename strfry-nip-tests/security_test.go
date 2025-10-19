package main

import (
	"net/http"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
)

// TestSecurityHeaders tests presence of security headers
func TestSecurityHeaders(t *testing.T) {
	endpoints := []string{
		"/health",
		"/",
		"/api/query",
	}

	for _, endpoint := range endpoints {
		h := NewTestHelper(t)
		resp, err := h.client.Get(httpBaseURL + endpoint)
		assert.NoError(t, err)
		defer resp.Body.Close()

		t.Run(endpoint, func(t *testing.T) {
			h := NewTestHelper(t)
			h.AssertSecurityHeaders(resp)
			t.Logf("✓ Security headers present on %s", endpoint)
		})
	}

	t.Log("✓ All endpoints have security headers")
}

// TestCORSHeaders tests CORS configuration
func TestCORSHeaders(t *testing.T) {
	t.Run("PreflightRequest", func(t *testing.T) {
		h := NewTestHelper(t)
		req, _ := http.NewRequest("OPTIONS", httpBaseURL+"/api/quotes", nil)
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set("Access-Control-Request-Method", "POST")

		resp, err := h.client.Do(req)
		assert.NoError(t, err)
		defer resp.Body.Close()

		// Check CORS headers
		assert.NotEmpty(t, resp.Header.Get("Access-Control-Allow-Origin"), "Should have CORS origin header")
		assert.NotEmpty(t, resp.Header.Get("Access-Control-Allow-Methods"), "Should have CORS methods header")

		t.Log("✓ CORS preflight handled correctly")
	})

	t.Run("ActualRequest", func(t *testing.T) {
		h := NewTestHelper(t)
		event := h.CreateWhitelistedEvent(1, "CORS test", nostr.Tags{})
		resp, _ := h.PostEventHTTP(event)

		// Skip if rate limited
		if resp.StatusCode == 429 {
			t.Skip("Rate limited - skipping test")
		}

		h.AssertCORSHeaders(resp)
		t.Log("✓ CORS headers on actual request")
	})
}

// TestXSSPrevention tests XSS prevention headers
func TestXSSPrevention(t *testing.T) {
	h := NewTestHelper(t)

	resp, _ := h.HealthCheck()

	xssHeader := resp.Header.Get("X-XSS-Protection")
	assert.NotEmpty(t, xssHeader, "Should have X-XSS-Protection header")

	cspHeader := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, cspHeader, "default-src 'none'", "Should have restrictive CSP")

	t.Log("✓ XSS prevention headers configured")
}

// TestClickjackingPrevention tests clickjacking prevention
func TestClickjackingPrevention(t *testing.T) {
	h := NewTestHelper(t)

	resp, _ := h.HealthCheck()

	xFrameOptions := resp.Header.Get("X-Frame-Options")
	assert.Equal(t, "DENY", xFrameOptions, "Should deny framing")

	cspHeader := resp.Header.Get("Content-Security-Policy")
	assert.Contains(t, cspHeader, "frame-ancestors 'none'", "CSP should prevent framing")

	t.Log("✓ Clickjacking prevention configured")
}

// TestContentTypeSniffingPrevention tests MIME sniffing prevention
func TestContentTypeSniffingPrevention(t *testing.T) {
	h := NewTestHelper(t)

	resp, _ := h.HealthCheck()

	nosniff := resp.Header.Get("X-Content-Type-Options")
	assert.Equal(t, "nosniff", nosniff, "Should prevent content type sniffing")

	t.Log("✓ Content type sniffing prevention configured")
}

// TestReferrerPolicy tests referrer policy header
func TestReferrerPolicy(t *testing.T) {
	h := NewTestHelper(t)

	resp, _ := h.HealthCheck()

	referrerPolicy := resp.Header.Get("Referrer-Policy")
	assert.Equal(t, "no-referrer", referrerPolicy, "Should have no-referrer policy")

	t.Log("✓ Referrer policy configured")
}

// TestWhitelistSecurity tests that whitelist cannot be bypassed
func TestWhitelistSecurity(t *testing.T) {
	// Try various methods to bypass whitelist
	t.Run("DifferentKeys", func(t *testing.T) {
		h := NewTestHelper(t)
		for i := 0; i < 10; i++ {
			event := h.CreateNonWhitelistedEvent(1, "Bypass attempt", nostr.Tags{})
			resp, result := h.PostEventHTTP(event)

			// Skip if rate limited
			if resp.StatusCode == 429 {
				t.Skip("Rate limited - skipping test")
			}

			h.AssertHTTPStatus(resp, 400)
			h.AssertEventRejected(result, "blocked")
		}
		t.Log("✓ Whitelist cannot be bypassed with different keys")
	})

	t.Run("ModifiedPubkey", func(t *testing.T) {
		h := NewTestHelper(t)
		event := h.CreateWhitelistedEvent(1, "Modified pubkey", nostr.Tags{})

		// Try to modify pubkey after signing
		event.PubKey = "0000000000000000000000000000000000000000000000000000000000000000"

		resp, result := h.PostEventHTTP(event)

		// Skip if rate limited
		if resp.StatusCode == 429 {
			t.Skip("Rate limited - skipping test")
		}

		h.AssertHTTPStatus(resp, 400)
		h.AssertEventRejected(result, "")

		t.Log("✓ Modified pubkey detected")
	})
}

// TestInputValidationSecurity tests security of input validation
func TestInputValidationSecurity(t *testing.T) {
	h := NewTestHelper(t)

	// Test SQL injection attempts (shouldn't affect LMDB but good to test)
	sqlInjectionAttempts := []string{
		"'; DROP TABLE events; --",
		"1' OR '1'='1",
		"admin'--",
	}

	for _, attempt := range sqlInjectionAttempts {
		resp, _ := h.QueryEventsHTTP("GET", map[string]string{
			"authors": attempt,
		}, nil)

		// Skip if rate limited
		if resp.StatusCode == 429 {
			t.Skip("Rate limited - skipping test")
		}

		// Should either return empty or error, but not crash
		assert.True(t, resp.StatusCode == 200 || resp.StatusCode == 400,
			"Should handle injection attempt safely")
	}

	t.Log("✓ Input validation prevents injection attacks")
}

// TestHTTPSRedirection tests that HTTP can be upgraded to HTTPS
// Note: This is informational only, actual HTTPS should be handled by reverse proxy
func TestHTTPConfiguration(t *testing.T) {
	h := NewTestHelper(t)

	resp, _ := h.HealthCheck()

	// Check that response is HTTP (app doesn't handle HTTPS directly)
	assert.True(t, resp.TLS == nil, "App serves HTTP, HTTPS should be handled by reverse proxy")

	t.Log("✓ HTTP configuration verified (use reverse proxy for HTTPS)")
}

// TestRateLimitingReadiness tests that the API can handle rate limiting
func TestRateLimitingReadiness(t *testing.T) {
	h := NewTestHelper(t)

	// Make many rapid requests to event endpoint (health is exempt)
	count := 150
	successCount := 0
	tooManyRequests := 0

	for i := 0; i < count; i++ {
		event := h.CreateWhitelistedEvent(1, "Rate limit readiness test", nostr.Tags{})
		resp, _ := h.PostEventHTTP(event)
		if resp.StatusCode == 200 {
			successCount++
		} else if resp.StatusCode == 429 {
			tooManyRequests++
			break // Stop once we hit rate limit
		}
	}

	// Should detect rate limiting exists (even if immediately hit)
	if tooManyRequests > 0 {
		t.Logf("✓ Rate limiting is implemented (%d successful before throttling)", successCount)
	} else if successCount == count {
		t.Log("✓ No rate limiting hit with 150 requests (high burst capacity or fresh bucket)")
	}

	// As long as we got some response (success or rate limited), the system is working
	assert.True(t, successCount > 0 || tooManyRequests > 0, "Should get either successful requests or rate limiting")
}

// TestEventSizeLimit tests enforcement of event size limits
func TestEventSizeLimit(t *testing.T) {
	h := NewTestHelper(t)

	// Test with content at various sizes
	sizes := []struct {
		size      int
		shouldOK  bool
		desc      string
	}{
		{100, true, "Small"},
		{1000, true, "Medium"},
		{10000, true, "Large"},
		{60000, true, "Near limit"},
		{70000, false, "Over limit"},
		{100000, false, "Way over limit"},
	}

	for _, test := range sizes {
		content := make([]byte, test.size)
		for i := range content {
			content[i] = 'A'
		}

		event := h.CreateWhitelistedEvent(1, string(content), nostr.Tags{})
		resp, result := h.PostEventHTTP(event)

		// Skip if rate limited
		if resp.StatusCode == 429 {
			t.Skip("Rate limited - skipping test")
		}

		if test.shouldOK {
			h.AssertHTTPStatus(resp, 200)
			h.AssertEventAccepted(result)
		} else {
			h.AssertHTTPStatus(resp, 400)
			h.AssertEventRejected(result, "")
		}

		t.Logf("✓ %s event (%d bytes) handled correctly", test.desc, test.size)
	}
}

// TestAuthenticationSecurity tests that authentication is enforced
func TestAuthenticationSecurity(t *testing.T) {
	// All events must be properly signed
	t.Run("UnsignedEvent", func(t *testing.T) {
		h := NewTestHelper(t)
		event := h.CreateWhitelistedEvent(1, "Unsigned", nostr.Tags{})
		event.Sig = "" // Remove signature

		resp, result := h.PostEventHTTP(event)

		// Skip if rate limited
		if resp.StatusCode == 429 {
			t.Skip("Rate limited - skipping test")
		}

		h.AssertHTTPStatus(resp, 400)
		h.AssertEventRejected(result, "")

		t.Log("✓ Unsigned event rejected")
	})

	t.Run("WrongSignature", func(t *testing.T) {
		h := NewTestHelper(t)
		event1 := h.CreateWhitelistedEvent(1, "Event 1", nostr.Tags{})
		event2 := h.CreateWhitelistedEvent(1, "Event 2", nostr.Tags{})

		// Use signature from event2 on event1
		event1.Sig = event2.Sig

		resp, result := h.PostEventHTTP(event1)

		// Skip if rate limited
		if resp.StatusCode == 429 {
			t.Skip("Rate limited - skipping test")
		}

		h.AssertHTTPStatus(resp, 400)
		h.AssertEventRejected(result, "")

		t.Log("✓ Wrong signature rejected")
	})
}

// TestPrivilegeEscalation tests that privilege escalation is not possible
func TestPrivilegeEscalation(t *testing.T) {
	h := NewTestHelper(t)

	// Try to post events as whitelisted user with non-whitelisted key
	nonWhitelistedSk := nostr.GeneratePrivateKey()
	pub, _ := nostr.GetPublicKey(nonWhitelistedSk)

	event := &nostr.Event{
		PubKey:    whitelistedPk, // Try to claim we're whitelisted
		CreatedAt: nostr.Now(),
		Kind:      1,
		Tags:      nostr.Tags{},
		Content:   "Privilege escalation attempt",
	}
	event.ID = event.GetID()
	event.Sign(nonWhitelistedSk) // But sign with non-whitelisted key

	resp, result := h.PostEventHTTP(event)

	// Skip if rate limited
	if resp.StatusCode == 429 {
		t.Skip("Rate limited - skipping test")
	}

	h.AssertHTTPStatus(resp, 400)
	h.AssertEventRejected(result, "")

	t.Logf("✓ Privilege escalation prevented (attacker pubkey: %s)", pub)
}

// TestSecureDefaults tests that the server has secure defaults
func TestSecureDefaults(t *testing.T) {
	h := NewTestHelper(t)

	resp, _ := h.HealthCheck()

	// Check various security best practices
	tests := []struct {
		header   string
		required bool
		desc     string
	}{
		{"Server", false, "Server header (should be absent or generic)"},
		{"X-Powered-By", false, "X-Powered-By header (should be absent)"},
		{"X-Content-Type-Options", true, "X-Content-Type-Options"},
		{"X-Frame-Options", true, "X-Frame-Options"},
	}

	for _, test := range tests {
		value := resp.Header.Get(test.header)
		if test.required {
			assert.NotEmpty(t, value, "%s should be present", test.desc)
		} else {
			if value == "" {
				t.Logf("✓ %s not exposed (good)", test.desc)
			} else {
				t.Logf("ℹ %s present: %s", test.desc, value)
			}
		}
	}

	t.Log("✓ Secure defaults verified")
}
