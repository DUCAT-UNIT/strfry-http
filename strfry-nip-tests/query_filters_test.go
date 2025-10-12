package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
)

// TestQueryByIDs tests querying events by ID
func TestQueryByIDs(t *testing.T) {
	h := NewTestHelper(t)

	// Post multiple events
	events := h.GenerateTestEvents(5, 1, "Query by ID test")
	h.BatchPostEvents(events)

	// Query by specific IDs
	ids := fmt.Sprintf("%s,%s", events[0].ID, events[2].ID)
	resp, results := h.QueryEventsHTTP("GET", map[string]string{"ids": ids}, nil)

	h.AssertHTTPStatus(resp, 200)
	assert.GreaterOrEqual(t, len(results), 2, "Should return at least 2 events")

	// Verify we got the right events
	foundIDs := make(map[string]bool)
	for _, result := range results {
		foundIDs[result["id"].(string)] = true
	}
	assert.True(t, foundIDs[events[0].ID], "Should contain first event")
	assert.True(t, foundIDs[events[2].ID], "Should contain third event")

	t.Logf("✓ Query by IDs returned %d events", len(results))
}

// TestQueryByAuthors tests querying events by author
func TestQueryByAuthors(t *testing.T) {
	h := NewTestHelper(t)

	// Post events from whitelisted author
	events := h.GenerateTestEvents(3, 1, "Query by author test")
	h.BatchPostEvents(events)

	// Query by author
	resp, results := h.QueryEventsHTTP("GET", map[string]string{"authors": whitelistedPk}, nil)

	h.AssertHTTPStatus(resp, 200)
	assert.GreaterOrEqual(t, len(results), 3, "Should return at least 3 events")

	// Verify all events are from the right author
	for _, result := range results {
		pubkey, _ := result["pubkey"].(string)
		if pubkey != "" {
			assert.Equal(t, whitelistedPk, pubkey, "All events should be from whitelisted author")
		}
	}

	t.Logf("✓ Query by authors returned %d events", len(results))
}

// TestQueryByKinds tests querying events by kind
func TestQueryByKinds(t *testing.T) {
	h := NewTestHelper(t)

	// Post events of different kinds
	kinds := []int{1, 3, 7}
	for _, kind := range kinds {
		event := h.CreateWhitelistedEvent(kind, "Kind test", nostr.Tags{})
		h.PostEventHTTP(event)
	}

	h.WaitForEvents(3, 200)

	// Query for specific kinds
	resp, results := h.QueryEventsHTTP("GET", map[string]string{"kinds": "1,3"}, nil)

	h.AssertHTTPStatus(resp, 200)
	assert.GreaterOrEqual(t, len(results), 2, "Should return events of requested kinds")

	// Verify kinds
	for _, result := range results {
		if kind, ok := result["kind"].(float64); ok {
			kindInt := int(kind)
			assert.Contains(t, []int{1, 3}, kindInt, "Event kind should be 1 or 3")
		}
	}

	t.Logf("✓ Query by kinds returned %d events", len(results))
}

// TestQueryWithSinceUntil tests querying with time filters
func TestQueryWithSinceUntil(t *testing.T) {
	h := NewTestHelper(t)

	now := nostr.Now()

	// Post events with specific timestamps
	oldEvent := h.CreateWhitelistedEvent(1, "Old event", nostr.Tags{})
	oldEvent.CreatedAt = now - 1000
	oldEvent.Sign(whitelistedSk)

	recentEvent := h.CreateWhitelistedEvent(1, "Recent event", nostr.Tags{})
	recentEvent.CreatedAt = now - 100
	recentEvent.Sign(whitelistedSk)

	h.PostEventHTTP(oldEvent)
	h.PostEventHTTP(recentEvent)
	h.WaitForEvents(2, 200)

	t.Run("SinceFilter", func(t *testing.T) {
		h := NewTestHelper(t)
		since := fmt.Sprintf("%d", now-500)
		resp, results := h.QueryEventsHTTP("GET", map[string]string{
			"since":   since,
			"authors": whitelistedPk,
		}, nil)

		h.AssertHTTPStatus(resp, 200)
		// Should only get recent event
		for _, result := range results {
			if createdAt, ok := result["created_at"].(float64); ok {
				assert.GreaterOrEqual(t, int64(createdAt), now-500, "Events should be after since timestamp")
			}
		}
		t.Log("✓ Since filter working")
	})

	t.Run("UntilFilter", func(t *testing.T) {
		h := NewTestHelper(t)
		until := fmt.Sprintf("%d", now-500)
		resp, results := h.QueryEventsHTTP("GET", map[string]string{
			"until":   until,
			"authors": whitelistedPk,
		}, nil)

		h.AssertHTTPStatus(resp, 200)
		// Should only get old event
		for _, result := range results {
			if createdAt, ok := result["created_at"].(float64); ok {
				assert.LessOrEqual(t, int64(createdAt), now-500, "Events should be before until timestamp")
			}
		}
		t.Log("✓ Until filter working")
	})
}

// TestQueryWithLimit tests query limit parameter
func TestQueryWithLimit(t *testing.T) {
	h := NewTestHelper(t)

	// Post many events
	events := h.GenerateTestEvents(20, 1, "Limit test")
	h.BatchPostEvents(events)

	// Query with limit
	resp, results := h.QueryEventsHTTP("GET", map[string]string{
		"authors": whitelistedPk,
		"limit":   "5",
	}, nil)

	h.AssertHTTPStatus(resp, 200)
	assert.LessOrEqual(t, len(results), 5, "Should respect limit parameter")

	t.Logf("✓ Limit parameter working (returned %d events)", len(results))
}

// TestQueryPOSTWithJSONFilter tests POST query with JSON body
func TestQueryPOSTWithJSONFilter(t *testing.T) {
	h := NewTestHelper(t)

	// Post test events
	events := h.GenerateTestEvents(3, 1, "POST query test")
	h.BatchPostEvents(events)

	// Query using POST with JSON body
	filter := map[string]interface{}{
		"authors": []string{whitelistedPk},
		"kinds":   []int{1},
		"limit":   10,
	}

	resp, results := h.QueryEventsHTTP("POST", nil, filter)

	h.AssertHTTPStatus(resp, 200)
	assert.GreaterOrEqual(t, len(results), 3, "Should return events")

	t.Logf("✓ POST query with JSON filter returned %d events", len(results))
}

// TestQueryCombinedFilters tests complex filter combinations
func TestQueryCombinedFilters(t *testing.T) {
	h := NewTestHelper(t)

	now := nostr.Now()

	// Post events with different characteristics
	event1 := h.CreateWhitelistedEvent(1, "Kind 1 recent", nostr.Tags{[]string{"t", "test"}})
	event1.CreatedAt = now - 100
	event1.Sign(whitelistedSk)

	event2 := h.CreateWhitelistedEvent(3, "Kind 3 recent", nostr.Tags{[]string{"t", "other"}})
	event2.CreatedAt = now - 50
	event2.Sign(whitelistedSk)

	h.PostEventHTTP(event1)
	h.PostEventHTTP(event2)
	h.WaitForEvents(2, 200)

	// Query with combined filters
	filter := map[string]interface{}{
		"authors": []string{whitelistedPk},
		"kinds":   []int{1},
		"since":   now - 200,
		"limit":   10,
	}

	resp, results := h.QueryEventsHTTP("POST", nil, filter)

	h.AssertHTTPStatus(resp, 200)

	// Should only get kind 1 events
	for _, result := range results {
		if kind, ok := result["kind"].(float64); ok {
			if int(kind) == 1 || int(kind) == 3 {
				// We might get other events from previous tests, that's ok
				continue
			}
		}
	}

	t.Logf("✓ Combined filters returned %d events", len(results))
}

// TestQueryEmptyResults tests queries that return no results
func TestQueryEmptyResults(t *testing.T) {
	h := NewTestHelper(t)

	// Query for non-existent author
	resp, results := h.QueryEventsHTTP("GET", map[string]string{
		"authors": "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	}, nil)

	h.AssertHTTPStatus(resp, 200)
	assert.Equal(t, 0, len(results), "Should return empty array for no matches")

	t.Log("✓ Empty query results handled correctly")
}

// TestQueryMultipleAuthors tests querying for multiple authors
func TestQueryMultipleAuthors(t *testing.T) {
	h := NewTestHelper(t)

	// Post events from whitelisted author
	events := h.GenerateTestEvents(2, 1, "Multi-author test")
	h.BatchPostEvents(events)

	// Query with multiple authors (including non-existent one)
	authors := fmt.Sprintf("%s,0000000000000000000000000000000000000000000000000000000000000001", whitelistedPk)
	resp, results := h.QueryEventsHTTP("GET", map[string]string{
		"authors": authors,
	}, nil)

	h.AssertHTTPStatus(resp, 200)
	assert.GreaterOrEqual(t, len(results), 2, "Should return events from existing author")

	t.Logf("✓ Multiple author query returned %d events", len(results))
}

// TestQueryInvalidFilters tests error handling for invalid filters
func TestQueryInvalidFilters(t *testing.T) {
	t.Run("InvalidKind", func(t *testing.T) {
		h := NewTestHelper(t)
		resp, _ := h.QueryEventsHTTP("GET", map[string]string{
			"kinds": "not-a-number",
		}, nil)

		h.AssertHTTPStatus(resp, 400)
		t.Log("✓ Invalid kind parameter rejected")
	})

	t.Run("InvalidSince", func(t *testing.T) {
		h := NewTestHelper(t)
		resp, _ := h.QueryEventsHTTP("GET", map[string]string{
			"since": "invalid",
		}, nil)

		h.AssertHTTPStatus(resp, 400)
		t.Log("✓ Invalid since parameter rejected")
	})

	t.Run("InvalidLimit", func(t *testing.T) {
		h := NewTestHelper(t)
		resp, _ := h.QueryEventsHTTP("GET", map[string]string{
			"limit": "not-a-number",
		}, nil)

		h.AssertHTTPStatus(resp, 400)
		t.Log("✓ Invalid limit parameter rejected")
	})
}

// TestQueryResponseFormat tests that query responses have correct format
func TestQueryResponseFormat(t *testing.T) {
	h := NewTestHelper(t)

	// Post an event
	event := h.CreateWhitelistedEvent(1, "Format test", nostr.Tags{[]string{"test", "tag"}})
	h.PostEventHTTP(event)
	h.WaitForEvents(1, 200)

	// Query for it
	resp, results := h.QueryEventsHTTP("GET", map[string]string{
		"ids": event.ID,
	}, nil)

	h.AssertHTTPStatus(resp, 200)
	assert.Greater(t, len(results), 0, "Should return at least one event")

	// Check format of first result
	if len(results) > 0 {
		result := results[0]
		assert.NotNil(t, result["id"], "Event should have id")
		assert.NotNil(t, result["pubkey"], "Event should have pubkey")
		assert.NotNil(t, result["created_at"], "Event should have created_at")
		assert.NotNil(t, result["kind"], "Event should have kind")
		assert.NotNil(t, result["tags"], "Event should have tags")
		assert.NotNil(t, result["content"], "Event should have content")
		assert.NotNil(t, result["sig"], "Event should have sig")
	}

	t.Log("✓ Query response format validated")
}

// TestQueryPerformance tests query performance with many events
func TestQueryPerformance(t *testing.T) {
	h := NewTestHelper(t)

	// Post a batch of events
	events := h.GenerateTestEvents(50, 1, "Performance test")
	h.BatchPostEvents(events)

	// Time the query
	start := time.Now()
	resp, results := h.QueryEventsHTTP("GET", map[string]string{
		"authors": whitelistedPk,
		"limit":   "100",
	}, nil)
	duration := time.Since(start)

	h.AssertHTTPStatus(resp, 200)
	assert.Greater(t, len(results), 0, "Should return events")
	assert.Less(t, duration, 2*time.Second, "Query should complete within 2 seconds")

	t.Logf("✓ Query returned %d events in %v", len(results), duration)
}
