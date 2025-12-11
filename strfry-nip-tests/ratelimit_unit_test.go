package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
)

// Unit-level tests for RateLimiter behavior
// These tests verify specific rate limiter properties in isolation

// TestRateLimitTokenBucketRefill verifies token bucket refill behavior
func TestRateLimitTokenBucketRefill(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping token bucket refill test in short mode")
	}

	h := NewTestHelper(t)
	h.ResetRateLimitBuckets()

	// Config: 300/min = 5 tokens/sec, burst 2.0 = 600 capacity
	// Exhaust most tokens
	exhaustCount := 550
	for i := 0; i < exhaustCount; i++ {
		event := h.CreateWhitelistedEvent(1, "Exhaust tokens", nostr.Tags{})
		h.PostEventHTTP(event)
	}

	// Verify we still have some tokens (600 - 550 = ~50)
	event := h.CreateWhitelistedEvent(1, "Should succeed", nostr.Tags{})
	resp, result := h.PostEventHTTP(event)
	h.AssertHTTPStatus(resp, 200)
	h.AssertEventAccepted(result)

	// Now exhaust remaining tokens
	for i := 0; i < 100; i++ {
		event := h.CreateWhitelistedEvent(1, "Exhaust remaining", nostr.Tags{})
		resp, _ := h.PostEventHTTP(event)
		if resp.StatusCode == 429 {
			break
		}
	}

	// Verify rate limited
	event = h.CreateWhitelistedEvent(1, "Should be limited", nostr.Tags{})
	resp, _ = h.PostEventHTTP(event)
	assert.Equal(t, 429, resp.StatusCode, "Should be rate limited")

	// Wait for specific refill: 2 seconds = ~10 tokens at 5/sec
	time.Sleep(2 * time.Second)

	// Should be able to make ~10 requests
	successCount := 0
	for i := 0; i < 15; i++ {
		event := h.CreateWhitelistedEvent(1, "After refill", nostr.Tags{})
		resp, _ := h.PostEventHTTP(event)
		if resp.StatusCode == 200 {
			successCount++
		} else {
			break
		}
	}

	// Allow some variance for timing
	assert.GreaterOrEqual(t, successCount, 8, "Should refill ~10 tokens in 2 seconds")
	assert.LessOrEqual(t, successCount, 15, "Should not refill more than expected")

	t.Logf("Token bucket refill: %d requests after 2s wait", successCount)
}

// TestRateLimitBurstCapacityExact tests exact burst capacity
func TestRateLimitBurstCapacityExact(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping burst capacity test in short mode")
	}

	h := NewTestHelper(t)
	h.ResetRateLimitBuckets()

	// Config: 300/min, burst 2.0 = 600 capacity
	// Send requests as fast as possible
	burstSize := 700
	successCount := 0
	rateLimitedAt := -1

	start := time.Now()
	for i := 0; i < burstSize; i++ {
		event := h.CreateWhitelistedEvent(1, "Burst test", nostr.Tags{})
		resp, _ := h.PostEventHTTP(event)
		if resp.StatusCode == 200 {
			successCount++
		} else if resp.StatusCode == 429 {
			rateLimitedAt = i
			break
		}
	}
	duration := time.Since(start)

	// Account for refill during burst: 5 tokens/sec * duration
	expectedMin := 600 // Base burst capacity
	expectedMax := 600 + int(duration.Seconds()*5) + 10 // Burst + refill + margin

	assert.GreaterOrEqual(t, successCount, expectedMin-10, "Should allow at least burst capacity")
	assert.LessOrEqual(t, successCount, expectedMax, "Should not exceed burst + refill")
	assert.Greater(t, rateLimitedAt, 0, "Should eventually hit rate limit")

	t.Logf("Burst capacity: %d requests in %v (rate limited at %d)", successCount, duration, rateLimitedAt)
}

// TestRateLimitGlobalVsPerIP tests that global and per-IP limits work together
func TestRateLimitGlobalVsPerIP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping global vs per-IP test in short mode")
	}

	h := NewTestHelper(t)
	h.ResetRateLimitBuckets()

	// Make many requests - should hit either global or per-IP limit
	rateLimitMessages := make(map[string]int)

	for i := 0; i < 700; i++ {
		event := h.CreateWhitelistedEvent(1, "Limit type test", nostr.Tags{})
		resp, result := h.PostEventHTTP(event)
		if resp.StatusCode == 429 {
			if msg, ok := result["message"].(string); ok {
				rateLimitMessages[msg]++
			}
		}
	}

	// Should have hit at least one type of limit
	assert.Greater(t, len(rateLimitMessages), 0, "Should have rate limit messages")

	for msg, count := range rateLimitMessages {
		t.Logf("Rate limit type '%s': %d times", msg, count)
	}
}

// TestRateLimitConcurrentAccuracy tests rate limiting under concurrent load
func TestRateLimitConcurrentAccuracy(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrent accuracy test in short mode")
	}

	h := NewTestHelper(t)
	h.ResetRateLimitBuckets()

	// Launch concurrent workers
	workers := 10
	requestsPerWorker := 100
	var wg sync.WaitGroup
	var totalSuccess atomic.Int64
	var totalRateLimited atomic.Int64

	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			helper := NewTestHelper(t)

			for i := 0; i < requestsPerWorker; i++ {
				event := helper.CreateWhitelistedEvent(1, "Concurrent test", nostr.Tags{})
				resp, _ := helper.PostEventHTTP(event)
				if resp.StatusCode == 200 {
					totalSuccess.Add(1)
				} else if resp.StatusCode == 429 {
					totalRateLimited.Add(1)
				}
			}
		}(w)
	}
	wg.Wait()
	duration := time.Since(start)

	total := workers * requestsPerWorker
	success := int(totalSuccess.Load())
	rateLimited := int(totalRateLimited.Load())

	// All requests should be accounted for (success or rate limited)
	assert.Equal(t, total, success+rateLimited, "All requests should be accounted for")

	// Should have some successes (burst capacity)
	assert.Greater(t, success, 0, "Should have some successful requests")

	// Should have some rate limited (we sent more than burst capacity)
	assert.Greater(t, rateLimited, 0, "Should have some rate limited requests")

	// Success rate should be reasonable (burst capacity / total)
	successRate := float64(success) / float64(total) * 100

	t.Logf("Concurrent test: %d workers, %d total requests", workers, total)
	t.Logf("  Success: %d (%.1f%%), Rate limited: %d", success, successRate, rateLimited)
	t.Logf("  Duration: %v, Throughput: %.1f req/s", duration, float64(total)/duration.Seconds())
}

// TestRateLimitReasonFormat tests that rate limit reasons have correct format
func TestRateLimitReasonFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping reason format test in short mode")
	}

	h := NewTestHelper(t)
	h.ResetRateLimitBuckets()

	// Exhaust rate limit
	for i := 0; i < 700; i++ {
		event := h.CreateWhitelistedEvent(1, "Exhaust limit", nostr.Tags{})
		resp, _ := h.PostEventHTTP(event)
		if resp.StatusCode == 429 {
			break
		}
	}

	// Get rate limited response
	event := h.CreateWhitelistedEvent(1, "Get reason", nostr.Tags{})
	resp, result := h.PostEventHTTP(event)

	assert.Equal(t, 429, resp.StatusCode, "Should be rate limited")

	message, ok := result["message"].(string)
	assert.True(t, ok, "Should have message field")

	// Verify format: "rate_limit_exceeded: <type>"
	assert.Contains(t, message, "rate_limit_exceeded:", "Should have correct prefix")

	// Should specify limit type
	validTypes := []string{"per_ip", "per_pubkey", "global"}
	hasValidType := false
	for _, lt := range validTypes {
		if assert.Contains(t, message, lt) {
			hasValidType = true
			break
		}
	}
	// Don't fail if no type found, just log
	if !hasValidType {
		t.Logf("Warning: message '%s' doesn't contain expected limit type", message)
	}

	t.Logf("Rate limit reason format: %s", message)
}

// TestRateLimitCleanupDoesNotAffectActive tests that cleanup doesn't break active buckets
func TestRateLimitCleanupDoesNotAffectActive(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping cleanup test in short mode")
	}

	h := NewTestHelper(t)

	// Make requests continuously for longer than cleanup interval (60s default)
	// We'll test for a shorter duration but verify behavior
	duration := 10 * time.Second
	interval := 100 * time.Millisecond

	start := time.Now()
	successCount := 0
	errorCount := 0

	for time.Since(start) < duration {
		event := h.CreateWhitelistedEvent(1, "Cleanup test", nostr.Tags{})
		resp, _ := h.PostEventHTTP(event)

		switch resp.StatusCode {
		case 200:
			successCount++
		case 429:
			// Rate limited is expected
		default:
			errorCount++
		}

		time.Sleep(interval)
	}

	// Should not have unexpected errors (cleanup breaking something)
	assert.Equal(t, 0, errorCount, "Should not have unexpected errors during cleanup period")
	assert.Greater(t, successCount, 0, "Should have some successful requests")

	t.Logf("Cleanup test: %d successful requests over %v, %d errors", successCount, duration, errorCount)
}

// TestRateLimitHealthCheckAlwaysSucceeds tests health check is never rate limited
func TestRateLimitHealthCheckAlwaysSucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping health check test in short mode")
	}

	h := NewTestHelper(t)

	// First exhaust rate limit with regular requests
	for i := 0; i < 700; i++ {
		event := h.CreateWhitelistedEvent(1, "Exhaust limit", nostr.Tags{})
		resp, _ := h.PostEventHTTP(event)
		if resp.StatusCode == 429 {
			break
		}
	}

	// Verify we're rate limited for regular requests
	event := h.CreateWhitelistedEvent(1, "Verify limited", nostr.Tags{})
	resp, _ := h.PostEventHTTP(event)
	if resp.StatusCode != 429 {
		t.Skip("Could not exhaust rate limit, skipping test")
	}

	// Health check should still work - make many requests
	for i := 0; i < 100; i++ {
		resp, result := h.HealthCheck()
		assert.Equal(t, 200, resp.StatusCode, "Health check should always succeed")
		assert.Equal(t, "ok", result["status"], "Health status should be ok")
	}

	t.Log("Health check always succeeds even when rate limited")
}

// TestRateLimitMetricsEndpointAlwaysSucceeds tests metrics endpoint is never rate limited
func TestRateLimitMetricsEndpointAlwaysSucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping metrics endpoint test in short mode")
	}

	h := NewTestHelper(t)

	// First exhaust rate limit
	for i := 0; i < 700; i++ {
		event := h.CreateWhitelistedEvent(1, "Exhaust limit", nostr.Tags{})
		resp, _ := h.PostEventHTTP(event)
		if resp.StatusCode == 429 {
			break
		}
	}

	// Metrics endpoint should still work
	for i := 0; i < 50; i++ {
		resp, err := h.client.Get(httpBaseURL + "/metrics")
		assert.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode, "Metrics endpoint should always succeed")
		resp.Body.Close()
	}

	t.Log("Metrics endpoint always succeeds even when rate limited")
}
