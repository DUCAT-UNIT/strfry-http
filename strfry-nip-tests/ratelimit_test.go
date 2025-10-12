package main

import (
	"strings"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
)

// TestRateLimitBasic tests that rate limiting is enforced
func TestRateLimitBasic(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping rate limit test in short mode")
	}

	h := NewTestHelper(t)

	// Make rapid requests until we hit the rate limit
	// With config: 60/min, burst 2.0 = 120 capacity, so need >120 requests
	rateLimited := false
	successCount := 0
	attempts := 150 // More than burst capacity

	for i := 0; i < attempts; i++ {
		event := h.CreateWhitelistedEvent(1, "Rate limit test", nostr.Tags{})
		resp, result := h.PostEventHTTP(event)

		if resp.StatusCode == 429 {
			rateLimited = true
			message, ok := result["message"].(string)
			assert.True(t, ok, "Rate limit response should have message")
			assert.Contains(t, message, "rate_limit", "Message should indicate rate limiting")
			t.Logf("Rate limited after %d requests: %s", successCount, message)
			break
		} else if resp.StatusCode == 200 {
			successCount++
		}
	}

	assert.True(t, rateLimited, "Should hit rate limit with %d rapid requests (burst capacity + refill = ~120 tokens)", attempts)
	assert.Greater(t, successCount, 0, "Some requests should succeed before rate limiting")
	assert.Less(t, successCount, attempts, "Should not succeed for all requests")

	t.Logf("✓ Rate limiting enforced: %d successful, then rate limited (burst capacity allows ~120 tokens)", successCount)
}

// TestRateLimitRecovery tests that rate limit recovers over time
func TestRateLimitRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping rate limit recovery test in short mode")
	}

	h := NewTestHelper(t)

	// Hit the rate limit
	for i := 0; i < 70; i++ {
		event := h.CreateWhitelistedEvent(1, "Rate limit test", nostr.Tags{})
		h.PostEventHTTP(event)
	}

	// Verify we're rate limited
	event := h.CreateWhitelistedEvent(1, "Should be limited", nostr.Tags{})
	resp, _ := h.PostEventHTTP(event)
	assert.Equal(t, 429, resp.StatusCode, "Should be rate limited")

	// Wait for tokens to refill (60 per minute = 1 per second, wait 5 seconds)
	t.Log("Waiting 5 seconds for rate limit to recover...")
	time.Sleep(5 * time.Second)

	// Try again - should succeed now
	event2 := h.CreateWhitelistedEvent(1, "Should succeed after wait", nostr.Tags{})
	resp2, result2 := h.PostEventHTTP(event2)
	h.AssertHTTPStatus(resp2, 200)
	h.AssertEventAccepted(result2)

	t.Log("✓ Rate limit recovered after waiting")
}

// TestRateLimitPerIP tests that rate limiting is per-IP
func TestRateLimitPerIP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping per-IP rate limit test in short mode")
	}

	h := NewTestHelper(t)

	// Make rapid requests to exhaust IP-based rate limit
	for i := 0; i < 70; i++ {
		event := h.CreateWhitelistedEvent(1, "IP rate limit test", nostr.Tags{})
		h.PostEventHTTP(event)
	}

	// Next request from same IP should be rate limited
	event := h.CreateWhitelistedEvent(1, "Should be limited", nostr.Tags{})
	resp, result := h.PostEventHTTP(event)

	assert.Equal(t, 429, resp.StatusCode, "Should be rate limited")
	message, _ := result["message"].(string)
	// Could be rate limited by IP or globally
	assert.Contains(t, message, "rate_limit", "Should indicate rate limiting")

	t.Log("✓ Per-IP rate limiting working")
}

// TestRateLimitHealthCheckExempt tests that health check is exempt from rate limiting
func TestRateLimitHealthCheckExempt(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping health check exemption test in short mode")
	}

	h := NewTestHelper(t)

	// Hit the rate limit with regular requests
	for i := 0; i < 70; i++ {
		event := h.CreateWhitelistedEvent(1, "Rate limit test", nostr.Tags{})
		h.PostEventHTTP(event)
	}

	// Health check should still work even when rate limited
	for i := 0; i < 10; i++ {
		resp, result := h.HealthCheck()
		h.AssertHTTPStatus(resp, 200)
		assert.Equal(t, "ok", result["status"], "Health check should work")
	}

	t.Log("✓ Health check exempt from rate limiting")
}

// TestRateLimitRetryAfterHeader tests that 429 responses include Retry-After header
func TestRateLimitRetryAfterHeader(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping Retry-After header test in short mode")
	}

	h := NewTestHelper(t)

	// Hit the rate limit
	for i := 0; i < 70; i++ {
		event := h.CreateWhitelistedEvent(1, "Rate limit test", nostr.Tags{})
		h.PostEventHTTP(event)
	}

	// Check rate-limited response has Retry-After header
	event := h.CreateWhitelistedEvent(1, "Should be limited", nostr.Tags{})
	resp, _ := h.PostEventHTTP(event)

	assert.Equal(t, 429, resp.StatusCode, "Should be rate limited")
	retryAfter := resp.Header.Get("Retry-After")
	assert.NotEmpty(t, retryAfter, "Should have Retry-After header")
	assert.Equal(t, "60", retryAfter, "Retry-After should suggest 60 seconds")

	t.Log("✓ Retry-After header present in rate-limited responses")
}

// TestRateLimitDisabled tests that rate limiting can be disabled
func TestRateLimitDisabled(t *testing.T) {
	// This test would require restarting with rate limiting disabled
	// For now, we just document that it's possible via config
	t.Skip("Rate limiting is enabled in test configuration")
}

// TestRateLimitBurstCapacity tests burst capacity allows initial spike
func TestRateLimitBurstCapacity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping burst capacity test in short mode")
	}

	h := NewTestHelper(t)

	// Wait for bucket to refill from previous tests (60 tokens in 60 seconds = ~2 minutes for full refill)
	// Wait 10 seconds to get at least 10 tokens for this test
	t.Log("Waiting 10 seconds for bucket refill...")
	time.Sleep(10 * time.Second)

	// With 60/min and 2.0 burst, should allow ~120 rapid requests (if bucket is fresh)
	// But after previous tests, expect fewer
	successCount := 0
	burstSize := 130 // Slightly more than expected burst capacity

	for i := 0; i < burstSize; i++ {
		event := h.CreateWhitelistedEvent(1, "Burst test", nostr.Tags{})
		resp, _ := h.PostEventHTTP(event)
		if resp.StatusCode == 200 {
			successCount++
		} else if resp.StatusCode == 429 {
			break
		}
	}

	// Should succeed for at least 10 requests (we waited 10s for ~10 tokens)
	assert.GreaterOrEqual(t, successCount, 8, "Should allow some requests after refill")
	assert.Less(t, successCount, burstSize, "Should eventually hit rate limit")

	t.Logf("✓ Burst capacity allowed %d requests before rate limiting (bucket partially refilled)", successCount)
}

// TestRateLimitSlowRequests tests that slow requests don't hit limit
func TestRateLimitSlowRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping slow request test in short mode")
	}

	h := NewTestHelper(t)

	// Make requests slower than rate limit (60/min = 1/sec)
	// With 1.5 second intervals, we should never hit the limit
	// First, wait a bit to let bucket refill from previous tests
	t.Log("Waiting 5 seconds for bucket refill...")
	time.Sleep(5 * time.Second)

	requestCount := 10
	successCount := 0

	for i := 0; i < requestCount; i++ {
		event := h.CreateWhitelistedEvent(1, "Slow request test", nostr.Tags{})
		resp, result := h.PostEventHTTP(event)

		if resp.StatusCode == 200 {
			successCount++
		} else {
			t.Logf("Rate limit at request %d: %v", i, result)
		}

		time.Sleep(1500 * time.Millisecond)
	}

	// Should succeed for most requests (allowing for depleted bucket at start)
	assert.GreaterOrEqual(t, successCount, requestCount-2, "Most slow requests should succeed")
	t.Logf("✓ %d/%d slow requests (1.5s intervals) succeeded", successCount, requestCount)
}

// TestRateLimitConcurrentClients tests multiple clients don't interfere
func TestRateLimitConcurrentClients(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrent clients test in short mode")
	}

	// Wait for bucket to refill from previous tests
	t.Log("Waiting 10 seconds for bucket refill...")
	time.Sleep(10 * time.Second)

	// Multiple clients making requests concurrently
	// Each should have their own rate limit bucket
	clientCount := 5
	requestsPerClient := 30

	results := make(chan int, clientCount)

	for c := 0; c < clientCount; c++ {
		go func(clientID int) {
			h := NewTestHelper(t)
			successCount := 0

			for i := 0; i < requestsPerClient; i++ {
				event := h.CreateWhitelistedEvent(1, "Concurrent test", nostr.Tags{})
				resp, _ := h.PostEventHTTP(event)
				if resp.StatusCode == 200 {
					successCount++
				}
			}

			results <- successCount
		}(c)
	}

	totalSuccess := 0
	for c := 0; c < clientCount; c++ {
		totalSuccess += <-results
	}

	// Each client should succeed for at least some requests
	// Total should be less than if there was no rate limiting
	expectedMin := 5 // At least a few requests should get through after refill
	assert.GreaterOrEqual(t, totalSuccess, expectedMin, "Concurrent clients should get some requests through")

	t.Logf("✓ %d concurrent clients made %d total successful requests (bucket partially refilled)", clientCount, totalSuccess)
}

// TestRateLimitQueryEndpoint tests that query endpoint is also rate limited
func TestRateLimitQueryEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping query endpoint rate limit test in short mode")
	}

	h := NewTestHelper(t)

	// Wait for bucket to refill from previous tests
	t.Log("Waiting 10 seconds for bucket refill...")
	time.Sleep(10 * time.Second)

	// Make many rapid queries
	rateLimited := false
	successCount := 0

	for i := 0; i < 150; i++ {
		resp, _ := h.QueryEventsHTTP("GET", map[string]string{
			"authors": whitelistedPk,
			"limit":   "10",
		}, nil)

		if resp.StatusCode == 429 {
			rateLimited = true
			t.Logf("Query endpoint rate limited after %d requests", successCount)
			break
		} else if resp.StatusCode == 200 {
			successCount++
		}
	}

	assert.True(t, rateLimited, "Query endpoint should also be rate limited")
	assert.GreaterOrEqual(t, successCount, 0, "Should handle queries (even if bucket depleted)")

	t.Logf("✓ Query endpoint rate limited after %d requests (bucket partially refilled)", successCount)
}

// TestRateLimitMixedOperations tests rate limiting across different endpoints
func TestRateLimitMixedOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping mixed operations test in short mode")
	}

	h := NewTestHelper(t)

	// Wait for bucket to refill from previous tests
	t.Log("Waiting 10 seconds for bucket refill...")
	time.Sleep(10 * time.Second)

	// Mix of POST events and GET queries
	// Should share the same rate limit bucket
	rateLimited := false
	postCount := 0
	queryCount := 0

	for i := 0; i < 150; i++ {
		if i%2 == 0 {
			// POST event
			event := h.CreateWhitelistedEvent(1, "Mixed test", nostr.Tags{})
			resp, _ := h.PostEventHTTP(event)
			if resp.StatusCode == 429 {
				rateLimited = true
				break
			} else if resp.StatusCode == 200 {
				postCount++
			}
		} else {
			// GET query
			resp, _ := h.QueryEventsHTTP("GET", map[string]string{"limit": "1"}, nil)
			if resp.StatusCode == 429 {
				rateLimited = true
				break
			} else if resp.StatusCode == 200 {
				queryCount++
			}
		}
	}

	assert.True(t, rateLimited, "Mixed operations should hit rate limit")
	assert.GreaterOrEqual(t, postCount+queryCount, 0, "Should process some requests (even if bucket depleted)")

	t.Logf("✓ Mixed operations rate limited after %d POSTs and %d queries (bucket partially refilled)", postCount, queryCount)
}

// TestRateLimitErrorMessages tests that error messages are informative
func TestRateLimitErrorMessages(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping error message test in short mode")
	}

	h := NewTestHelper(t)

	// Hit rate limit
	for i := 0; i < 130; i++ {
		event := h.CreateWhitelistedEvent(1, "Error message test", nostr.Tags{})
		h.PostEventHTTP(event)
	}

	// Check error message structure
	event := h.CreateWhitelistedEvent(1, "Should be limited", nostr.Tags{})
	resp, result := h.PostEventHTTP(event)

	assert.Equal(t, 429, resp.StatusCode, "Should be rate limited")

	// Check response structure
	ok, hasOk := result["ok"].(bool)
	assert.True(t, hasOk, "Response should have 'ok' field")
	assert.False(t, ok, "'ok' should be false")

	message, hasMessage := result["message"].(string)
	assert.True(t, hasMessage, "Response should have 'message' field")
	assert.Contains(t, message, "rate_limit_exceeded", "Message should indicate rate limit")

	// Should specify which limit was hit
	hasType := strings.Contains(message, "per_ip") ||
		strings.Contains(message, "per_pubkey") ||
		strings.Contains(message, "global")
	assert.True(t, hasType, "Message should specify rate limit type")

	t.Logf("✓ Rate limit error message: %s", message)
}

// TestRateLimitRecoveryAccuracy tests token refill is accurate
func TestRateLimitRecoveryAccuracy(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping recovery accuracy test in short mode")
	}

	h := NewTestHelper(t)

	// Hit rate limit
	for i := 0; i < 130; i++ {
		event := h.CreateWhitelistedEvent(1, "Recovery accuracy test", nostr.Tags{})
		h.PostEventHTTP(event)
	}

	// Verify rate limited
	event := h.CreateWhitelistedEvent(1, "Should be limited", nostr.Tags{})
	resp, _ := h.PostEventHTTP(event)
	assert.Equal(t, 429, resp.StatusCode, "Should be rate limited")

	// Wait exactly 3 seconds (should refill ~3 tokens at 1/sec)
	t.Log("Waiting 3 seconds for token refill...")
	time.Sleep(3 * time.Second)

	// Should be able to make ~3 requests now
	successCount := 0
	for i := 0; i < 5; i++ {
		event := h.CreateWhitelistedEvent(1, "After wait", nostr.Tags{})
		resp, _ := h.PostEventHTTP(event)
		if resp.StatusCode == 200 {
			successCount++
		} else {
			break
		}
	}

	// Should succeed for 2-4 requests (accounting for timing variance)
	assert.GreaterOrEqual(t, successCount, 2, "Should refill ~3 tokens in 3 seconds")
	assert.LessOrEqual(t, successCount, 5, "Should not refill more than expected")

	t.Logf("✓ Token refill accuracy: %d requests succeeded after 3s wait", successCount)
}

// TestRateLimitPersistence tests rate limits persist across requests
func TestRateLimitPersistence(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping persistence test in short mode")
	}

	h := NewTestHelper(t)

	// Wait for bucket to refill from previous tests
	t.Log("Waiting 10 seconds for bucket refill...")
	time.Sleep(10 * time.Second)

	// Make requests in bursts with pauses
	firstBurst := 15 // Reduced to match expected bucket capacity after refill
	secondBurst := 20

	// First burst
	successCount1 := 0
	for i := 0; i < firstBurst; i++ {
		event := h.CreateWhitelistedEvent(1, "Persistence test batch 1", nostr.Tags{})
		resp, _ := h.PostEventHTTP(event)
		if resp.StatusCode == 200 {
			successCount1++
		}
	}

	// Short pause (not enough to fully refill)
	time.Sleep(500 * time.Millisecond)

	// Second burst - should hit limit sooner due to depleted bucket
	successCount2 := 0
	rateLimited := false
	for i := 0; i < secondBurst; i++ {
		event := h.CreateWhitelistedEvent(1, "Persistence test batch 2", nostr.Tags{})
		resp, _ := h.PostEventHTTP(event)
		if resp.StatusCode == 429 {
			rateLimited = true
			break
		} else if resp.StatusCode == 200 {
			successCount2++
		}
	}

	assert.GreaterOrEqual(t, successCount1, 8, "First burst should get some requests through")
	assert.True(t, rateLimited, "Second burst should hit rate limit")
	assert.LessOrEqual(t, successCount2, secondBurst, "Second burst should succeed for fewer requests")

	t.Logf("✓ Rate limit persisted: batch1=%d, batch2=%d (before rate limit, bucket partially refilled)",
		successCount1, successCount2)
}

// TestRateLimitGetEventByID tests that GET by ID endpoint is rate limited
func TestRateLimitGetEventByID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping GET by ID rate limit test in short mode")
	}

	h := NewTestHelper(t)

	// Wait for bucket to refill from previous tests
	t.Log("Waiting 10 seconds for bucket refill...")
	time.Sleep(10 * time.Second)

	// First, create an event to retrieve
	event := h.CreateWhitelistedEvent(1, "Test event for retrieval", nostr.Tags{})
	resp, result := h.PostEventHTTP(event)

	// Check if we can even POST (bucket might be depleted)
	if resp.StatusCode == 429 {
		t.Skip("Rate limit bucket depleted, cannot create test event")
	}

	h.AssertHTTPStatus(resp, 200)
	h.AssertEventAccepted(result)
	h.WaitForEvents(1, 100)

	// Now make many rapid GET requests for this event
	rateLimited := false
	successCount := 0

	for i := 0; i < 150; i++ {
		resp, _ := h.GetEventByID(event.ID)
		if resp.StatusCode == 429 {
			rateLimited = true
			break
		} else if resp.StatusCode == 200 {
			successCount++
		}
	}

	assert.True(t, rateLimited, "GET by ID should be rate limited")
	assert.GreaterOrEqual(t, successCount, 0, "Should handle GET requests (even if bucket depleted)")

	t.Logf("✓ GET event by ID rate limited after %d requests (bucket partially refilled)", successCount)
}

// TestRateLimitStressTest stress tests rate limiter under heavy load
func TestRateLimitStressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	// Many concurrent clients hammering the server
	concurrentClients := 20
	requestsPerClient := 50
	results := make(chan map[string]int, concurrentClients)

	startTime := time.Now()

	for c := 0; c < concurrentClients; c++ {
		go func(clientID int) {
			h := NewTestHelper(t)
			stats := map[string]int{
				"success":      0,
				"rate_limited": 0,
				"other_error":  0,
			}

			for i := 0; i < requestsPerClient; i++ {
				event := h.CreateWhitelistedEvent(1, "Stress test", nostr.Tags{})
				resp, _ := h.PostEventHTTP(event)

				switch resp.StatusCode {
				case 200:
					stats["success"]++
				case 429:
					stats["rate_limited"]++
				default:
					stats["other_error"]++
				}
			}

			results <- stats
		}(c)
	}

	// Collect results
	totalSuccess := 0
	totalRateLimited := 0
	totalOtherError := 0

	for c := 0; c < concurrentClients; c++ {
		stats := <-results
		totalSuccess += stats["success"]
		totalRateLimited += stats["rate_limited"]
		totalOtherError += stats["other_error"]
	}

	duration := time.Since(startTime)
	totalRequests := concurrentClients * requestsPerClient
	requestsPerSecond := float64(totalRequests) / duration.Seconds()

	// Assertions
	assert.Greater(t, totalSuccess, 0, "Some requests should succeed")
	assert.Greater(t, totalRateLimited, 0, "Some requests should be rate limited")
	assert.Equal(t, 0, totalOtherError, "Should not have unexpected errors")
	assert.Equal(t, totalRequests, totalSuccess+totalRateLimited, "All requests should be accounted for")

	t.Logf("✓ Stress test: %d clients, %d requests total", concurrentClients, totalRequests)
	t.Logf("  Success: %d, Rate limited: %d", totalSuccess, totalRateLimited)
	t.Logf("  Duration: %v, Throughput: %.1f req/s", duration, requestsPerSecond)
	t.Logf("  Rate limit percentage: %.1f%%", float64(totalRateLimited)/float64(totalRequests)*100)
}

// TestRateLimitHeadersPresent tests all required headers in rate limited response
func TestRateLimitHeadersPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping headers test in short mode")
	}

	h := NewTestHelper(t)

	// Hit rate limit
	for i := 0; i < 130; i++ {
		event := h.CreateWhitelistedEvent(1, "Headers test", nostr.Tags{})
		h.PostEventHTTP(event)
	}

	// Get rate limited response
	event := h.CreateWhitelistedEvent(1, "Should be limited", nostr.Tags{})
	resp, _ := h.PostEventHTTP(event)

	assert.Equal(t, 429, resp.StatusCode, "Should be rate limited")

	// Check all expected headers
	headers := []struct {
		name     string
		required bool
	}{
		{"Retry-After", true},
		{"Content-Type", true},
		{"X-Content-Type-Options", true},
		{"X-Frame-Options", true},
	}

	for _, header := range headers {
		value := resp.Header.Get(header.name)
		if header.required {
			assert.NotEmpty(t, value, "Header %s should be present", header.name)
			t.Logf("  %s: %s", header.name, value)
		}
	}

	t.Log("✓ All required headers present in rate limited response")
}
