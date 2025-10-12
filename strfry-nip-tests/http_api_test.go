package main

import (
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
)

// TestHTTPHealthCheck tests the /health endpoint
func TestHTTPHealthCheck(t *testing.T) {
	h := NewTestHelper(t)

	resp, result := h.HealthCheck()

	h.AssertHTTPStatus(resp, 200)
	assert.Equal(t, "ok", result["status"], "Health status should be ok")
	assert.Equal(t, "strfry-http", result["service"], "Service name should match")

	t.Log("✓ Health check endpoint working")
}

// TestHTTPRootEndpoint tests the root / endpoint
func TestHTTPRootEndpoint(t *testing.T) {
	h := NewTestHelper(t)

	resp, err := h.client.Get(httpBaseURL + "/")
	assert.NoError(t, err)
	defer resp.Body.Close()

	h.AssertHTTPStatus(resp, 200)

	t.Log("✓ Root endpoint responding")
}

// TestHTTPPostEventSuccess tests successful event posting
func TestHTTPPostEventSuccess(t *testing.T) {
	h := NewTestHelper(t)

	event := h.CreateWhitelistedEvent(1, "Test event via HTTP", nostr.Tags{})
	resp, result := h.PostEventHTTP(event)

	h.AssertHTTPStatus(resp, 200)
	h.AssertEventAccepted(result)
	assert.Equal(t, event.ID, result["id"], "Response should include event ID")

	t.Logf("✓ Successfully posted event: %s", event.ID)
}

// TestHTTPPostEventWhitelistEnforcement tests whitelist enforcement
func TestHTTPPostEventWhitelistEnforcement(t *testing.T) {
	t.Run("WhitelistedAccepted", func(t *testing.T) {
		h := NewTestHelper(t)
		event := h.CreateWhitelistedEvent(1, "From whitelisted key", nostr.Tags{})
		resp, result := h.PostEventHTTP(event)

		h.AssertHTTPStatus(resp, 200)
		h.AssertEventAccepted(result)
		t.Log("✓ Whitelisted event accepted")
	})

	t.Run("NonWhitelistedRejected", func(t *testing.T) {
		h := NewTestHelper(t)
		event := h.CreateNonWhitelistedEvent(1, "From non-whitelisted key", nostr.Tags{})
		resp, result := h.PostEventHTTP(event)

		h.AssertHTTPStatus(resp, 400)
		h.AssertEventRejected(result, "blocked: pubkey not in whitelist")
		t.Log("✓ Non-whitelisted event rejected")
	})
}

// TestHTTPGetEventByID tests retrieving events by ID
func TestHTTPGetEventByID(t *testing.T) {
	h := NewTestHelper(t)

	// First, post an event
	event := h.CreateWhitelistedEvent(1, "Event to retrieve", nostr.Tags{})
	_, postResult := h.PostEventHTTP(event)
	h.AssertEventAccepted(postResult)

	// Wait for processing
	h.WaitForEvents(1, 100)

	// Retrieve the event
	resp, getResult := h.GetEventByID(event.ID)
	h.AssertHTTPStatus(resp, 200)

	assert.Equal(t, event.ID, getResult["id"], "Retrieved event ID should match")
	assert.Equal(t, event.Content, getResult["content"], "Content should match")
	assert.Equal(t, event.PubKey, getResult["pubkey"], "Pubkey should match")

	t.Logf("✓ Successfully retrieved event: %s", event.ID)
}

// TestHTTPGetEventNotFound tests 404 for missing events
func TestHTTPGetEventNotFound(t *testing.T) {
	h := NewTestHelper(t)

	fakeID := "0000000000000000000000000000000000000000000000000000000000000000"
	resp, result := h.GetEventByID(fakeID)

	h.AssertHTTPStatus(resp, 404)
	assert.Contains(t, result["error"], "not found", "Should return not found error")

	t.Log("✓ Correctly returns 404 for missing event")
}

// TestHTTPBatchEventPosting tests posting multiple events
func TestHTTPBatchEventPosting(t *testing.T) {
	h := NewTestHelper(t)

	count := 10
	events := h.GenerateTestEvents(count, 1, "Batch event")
	results := h.BatchPostEvents(events)

	accepted := 0
	for i, result := range results {
		if ok, exists := result["ok"].(bool); exists && ok {
			accepted++
		} else {
			t.Logf("Event %d rejected: %v", i, result)
		}
	}

	assert.Equal(t, count, accepted, "All events should be accepted")
	t.Logf("✓ Successfully posted %d events in batch", accepted)
}

// TestHTTPDuplicateEventHandling tests duplicate event detection
func TestHTTPDuplicateEventHandling(t *testing.T) {
	h := NewTestHelper(t)

	event := h.CreateWhitelistedEvent(1, "Duplicate test", nostr.Tags{})

	// Post first time
	resp1, result1 := h.PostEventHTTP(event)
	h.AssertHTTPStatus(resp1, 200)
	h.AssertEventAccepted(result1)

	// Wait and post again
	h.WaitForEvents(1, 200)

	resp2, result2 := h.PostEventHTTP(event)
	h.AssertHTTPStatus(resp2, 200)

	// Should still be "accepted" - server accepts duplicates without special indication
	ok, _ := result2["ok"].(bool)
	assert.True(t, ok, "Duplicate should return ok=true")

	t.Log("✓ Duplicate event correctly handled (accepted without error)")
}

// TestHTTPEventKinds tests different event kinds
func TestHTTPEventKinds(t *testing.T) {
	kinds := []int{0, 1, 3, 5, 7, 1000, 10000, 30000}
	h := NewTestHelper(t)

	for _, kind := range kinds {
		event := h.CreateWhitelistedEvent(kind, "Test kind", nostr.Tags{})
		resp, result := h.PostEventHTTP(event)

		h.AssertHTTPStatus(resp, 200)
		h.AssertEventAccepted(result)
	}

	t.Logf("✓ Successfully posted events of %d different kinds", len(kinds))
}

// TestHTTPEventWithTags tests events with various tags
func TestHTTPEventWithTags(t *testing.T) {
	testCases := []struct {
		name string
		tags nostr.Tags
	}{
		{"NoTags", nostr.Tags{}},
		{"SingleTag", nostr.Tags{[]string{"test", "value"}}},
		{"MultipleTags", nostr.Tags{
			[]string{"e", "0000000000000000000000000000000000000000000000000000000000000001"},
			[]string{"p", "0000000000000000000000000000000000000000000000000000000000000002"},
			[]string{"t", "hashtag"},
		}},
		{"DTag", nostr.Tags{[]string{"d", "unique-identifier"}}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewTestHelper(t)
			event := h.CreateWhitelistedEvent(1, "Event with tags", tc.tags)
			resp, result := h.PostEventHTTP(event)

			h.AssertHTTPStatus(resp, 200)
			h.AssertEventAccepted(result)
		})
	}

	t.Log("✓ All tag variations accepted")
}

// TestHTTPLargeContent tests posting events with large content
func TestHTTPLargeContent(t *testing.T) {
	h := NewTestHelper(t)

	sizes := []int{100, 1000, 10000, 50000}

	for _, size := range sizes {
		content := make([]byte, size)
		for i := range content {
			content[i] = 'A'
		}

		event := h.CreateWhitelistedEvent(1, string(content), nostr.Tags{})
		resp, result := h.PostEventHTTP(event)

		if size > 65536 {
			// Should be rejected if over max size
			h.AssertHTTPStatus(resp, 400)
		} else {
			h.AssertHTTPStatus(resp, 200)
			h.AssertEventAccepted(result)
		}
	}

	t.Log("✓ Large content handling verified")
}

// TestHTTPTimestampValidation tests event timestamp validation
func TestHTTPTimestampValidation(t *testing.T) {
	t.Run("FutureTimestamp", func(t *testing.T) {
		h := NewTestHelper(t)
		event := h.CreateWhitelistedEvent(1, "Future event", nostr.Tags{})
		event.CreatedAt = nostr.Now() + 1000 // 1000 seconds in future
		event.Sign(whitelistedSk)

		resp, result := h.PostEventHTTP(event)
		h.AssertHTTPStatus(resp, 400)
		h.AssertEventRejected(result, "")
		t.Log("✓ Future timestamp rejected")
	})

	t.Run("VeryOldTimestamp", func(t *testing.T) {
		h := NewTestHelper(t)
		event := h.CreateWhitelistedEvent(1, "Old event", nostr.Tags{})
		event.CreatedAt = nostr.Timestamp(1000000000) // Very old
		event.Sign(whitelistedSk)

		resp, result := h.PostEventHTTP(event)
		h.AssertHTTPStatus(resp, 400)
		h.AssertEventRejected(result, "")
		t.Log("✓ Very old timestamp rejected")
	})
}

// TestHTTPConcurrentPosts tests concurrent event posting
func TestHTTPConcurrentPosts(t *testing.T) {
	concurrency := 20
	results := make(chan map[string]interface{}, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(index int) {
			helper := NewTestHelper(t)
			event := helper.CreateWhitelistedEvent(1, "Concurrent event", nostr.Tags{})
			_, result := helper.PostEventHTTP(event)
			results <- result
		}(i)
	}

	accepted := 0
	for i := 0; i < concurrency; i++ {
		result := <-results
		if ok, _ := result["ok"].(bool); ok {
			accepted++
		}
	}

	assert.Equal(t, concurrency, accepted, "All concurrent posts should succeed")
	t.Logf("✓ %d concurrent posts successful", accepted)
}
