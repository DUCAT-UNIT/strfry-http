package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
)

// TestMalformedJSON tests handling of malformed JSON
func TestMalformedJSON(t *testing.T) {
	h := NewTestHelper(t)

	malformedPayloads := []string{
		`{invalid json}`,
		`{"id": "missing closing brace"`,
		`not json at all`,
		``,
		`null`,
		`[]`,
	}

	for i, payload := range malformedPayloads {
		resp, err := h.client.Post(
			httpBaseURL+"/api/quotes",
			"application/json",
			bytes.NewBufferString(payload),
		)
		assert.NoError(t, err)

		h.AssertHTTPStatus(resp, 400)
		resp.Body.Close()

		t.Logf("✓ Malformed payload %d rejected", i+1)
	}

	t.Log("✓ All malformed JSON payloads rejected")
}

// TestMissingRequiredFields tests events missing required fields
func TestMissingRequiredFields(t *testing.T) {
	h := NewTestHelper(t)

	testCases := []struct {
		name    string
		payload string
	}{
		{"MissingID", `{"pubkey":"abc","created_at":123,"kind":1,"tags":[],"content":"test","sig":"xyz"}`},
		{"MissingPubkey", `{"id":"abc","created_at":123,"kind":1,"tags":[],"content":"test","sig":"xyz"}`},
		{"MissingSig", `{"id":"abc","pubkey":"def","created_at":123,"kind":1,"tags":[],"content":"test"}`},
		{"MissingKind", `{"id":"abc","pubkey":"def","created_at":123,"tags":[],"content":"test","sig":"xyz"}`},
		{"MissingTags", `{"id":"abc","pubkey":"def","created_at":123,"kind":1,"content":"test","sig":"xyz"}`},
		{"MissingContent", `{"id":"abc","pubkey":"def","created_at":123,"kind":1,"tags":[],"sig":"xyz"}`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := h.client.Post(
				httpBaseURL+"/api/quotes",
				"application/json",
				bytes.NewBufferString(tc.payload),
			)
			assert.NoError(t, err)
			defer resp.Body.Close()

			h.AssertHTTPStatus(resp, 400)
			t.Logf("✓ %s rejected", tc.name)
		})
	}
}

// TestInvalidSignature tests events with invalid signatures
func TestInvalidSignature(t *testing.T) {
	h := NewTestHelper(t)

	event := h.CreateWhitelistedEvent(1, "Test event", nostr.Tags{})
	// Corrupt the signature
	event.Sig = strings.Repeat("0", 128)

	resp, result := h.PostEventHTTP(event)
	h.AssertHTTPStatus(resp, 400)
	h.AssertEventRejected(result, "invalid")

	t.Log("✓ Invalid signature rejected")
}

// TestInvalidEventID tests events with incorrect ID
func TestInvalidEventID(t *testing.T) {
	h := NewTestHelper(t)

	event := h.CreateWhitelistedEvent(1, "Test event", nostr.Tags{})
	// Corrupt the ID
	event.ID = strings.Repeat("f", 64)

	resp, result := h.PostEventHTTP(event)
	h.AssertHTTPStatus(resp, 400)
	h.AssertEventRejected(result, "")

	t.Log("✓ Invalid event ID rejected")
}

// TestEmptyContent tests events with empty content
func TestEmptyContent(t *testing.T) {
	h := NewTestHelper(t)

	event := h.CreateWhitelistedEvent(1, "", nostr.Tags{})
	resp, result := h.PostEventHTTP(event)

	// Empty content should be allowed
	h.AssertHTTPStatus(resp, 200)
	h.AssertEventAccepted(result)

	t.Log("✓ Empty content accepted")
}

// TestVeryLongTags tests events with many/long tags
func TestVeryLongTags(t *testing.T) {
	h := NewTestHelper(t)

	t.Run("ManyTags", func(t *testing.T) {
		tags := nostr.Tags{}
		for i := 0; i < 100; i++ {
			tags = append(tags, []string{"t", "tag" + string(rune(i))})
		}

		event := h.CreateWhitelistedEvent(1, "Many tags", tags)
		resp, result := h.PostEventHTTP(event)

		// Should be accepted if under max
		if resp.StatusCode == 200 {
			h.AssertEventAccepted(result)
			t.Log("✓ Many tags accepted")
		} else {
			t.Log("✓ Too many tags rejected")
		}
	})

	t.Run("LongTagValue", func(t *testing.T) {
		longValue := strings.Repeat("a", 2000)
		tags := nostr.Tags{[]string{"test", longValue}}

		event := h.CreateWhitelistedEvent(1, "Long tag", tags)
		resp, result := h.PostEventHTTP(event)

		if resp.StatusCode == 200 {
			h.AssertEventAccepted(result)
			t.Log("✓ Long tag value accepted")
		} else {
			h.AssertEventRejected(result, "")
			t.Log("✓ Too long tag value rejected")
		}
	})
}

// TestSpecialCharactersInContent tests content with special characters
func TestSpecialCharactersInContent(t *testing.T) {
	h := NewTestHelper(t)

	specialContents := []string{
		"Unicode: 你好世界 🌍",
		"Emojis: 🔥💯✨",
		"Newlines:\n\nMultiple\n\nLines",
		"Quotes: \"double\" and 'single'",
		"Backslashes: \\ \\\\ \\\\\\",
		"Null bytes: \x00",
		"Control chars: \t\r\n",
	}

	for i, content := range specialContents {
		event := h.CreateWhitelistedEvent(1, content, nostr.Tags{})
		resp, result := h.PostEventHTTP(event)

		h.AssertHTTPStatus(resp, 200)
		h.AssertEventAccepted(result)

		t.Logf("✓ Special content %d accepted", i+1)
	}

	t.Log("✓ All special character content accepted")
}

// TestInvalidHTTPMethods tests using wrong HTTP methods
func TestInvalidHTTPMethods(t *testing.T) {
	h := NewTestHelper(t)

	endpoints := []struct {
		path    string
		invalid []string
	}{
		{"/api/quotes", []string{"GET", "PUT", "DELETE", "PATCH"}},
		{"/health", []string{"POST", "PUT", "DELETE", "PATCH"}},
	}

	for _, endpoint := range endpoints {
		for _, method := range endpoint.invalid {
			req, _ := http.NewRequest(method, httpBaseURL+endpoint.path, nil)
			resp, err := h.client.Do(req)
			assert.NoError(t, err)
			resp.Body.Close()

			// Should get 404 or 405
			assert.True(t, resp.StatusCode == 404 || resp.StatusCode == 405,
				"Invalid method %s on %s should return 404/405", method, endpoint.path)
		}
	}

	t.Log("✓ Invalid HTTP methods rejected")
}

// TestConcurrentDuplicates tests posting same event concurrently
func TestConcurrentDuplicates(t *testing.T) {
	h := NewTestHelper(t)

	event := h.CreateWhitelistedEvent(1, "Duplicate test", nostr.Tags{})
	concurrency := 10
	results := make(chan map[string]interface{}, concurrency)

	// Post same event multiple times concurrently
	for i := 0; i < concurrency; i++ {
		go func() {
			helper := NewTestHelper(t)
			_, result := helper.PostEventHTTP(event)
			results <- result
		}()
	}

	// Collect results
	allOK := true
	for i := 0; i < concurrency; i++ {
		result := <-results
		if ok, exists := result["ok"].(bool); !exists || !ok {
			allOK = false
		}
	}

	assert.True(t, allOK, "All concurrent duplicate posts should return ok")
	t.Log("✓ Concurrent duplicates handled correctly")
}

// TestExtremelyLargePayload tests very large payloads
func TestExtremelyLargePayload(t *testing.T) {
	h := NewTestHelper(t)

	// Create content over max size
	content := strings.Repeat("A", 100000) // 100KB

	event := h.CreateWhitelistedEvent(1, content, nostr.Tags{})
	resp, result := h.PostEventHTTP(event)

	// Should be rejected
	h.AssertHTTPStatus(resp, 400)
	h.AssertEventRejected(result, "")

	t.Log("✓ Extremely large payload rejected")
}

// TestNegativeKind tests events with negative kind values
func TestNegativeKind(t *testing.T) {
	h := NewTestHelper(t)

	// Manually craft event with negative kind
	pub, _ := nostr.GetPublicKey(whitelistedSk)
	eventMap := map[string]interface{}{
		"pubkey":     pub,
		"created_at": nostr.Now(),
		"kind":       -1,
		"tags":       []interface{}{},
		"content":    "Negative kind test",
	}

	// Calculate ID and sign manually
	eventJSON, _ := json.Marshal(eventMap)

	resp, err := h.client.Post(
		httpBaseURL+"/api/quotes",
		"application/json",
		bytes.NewBuffer(eventJSON),
	)
	assert.NoError(t, err)
	defer resp.Body.Close()

	// Should be rejected
	h.AssertHTTPStatus(resp, 400)

	t.Log("✓ Negative kind rejected")
}

// TestQueryWithNoParameters tests query endpoint with no parameters
func TestQueryWithNoParameters(t *testing.T) {
	h := NewTestHelper(t)

	resp, results := h.QueryEventsHTTP("GET", nil, nil)

	// Should return results (all events) or empty array
	h.AssertHTTPStatus(resp, 200)
	assert.NotNil(t, results, "Should return array")

	t.Logf("✓ Query with no parameters returned %d events", len(results))
}

// TestInvalidContentType tests posting with wrong content type
func TestInvalidContentType(t *testing.T) {
	h := NewTestHelper(t)

	event := h.CreateWhitelistedEvent(1, "Content type test", nostr.Tags{})
	eventJSON, _ := json.Marshal(event)

	// Post with wrong content type
	resp, err := h.client.Post(
		httpBaseURL+"/api/quotes",
		"text/plain",
		bytes.NewBuffer(eventJSON),
	)
	assert.NoError(t, err)
	defer resp.Body.Close()

	// Should still work or return 415
	assert.True(t, resp.StatusCode == 200 || resp.StatusCode == 415,
		"Should accept or reject based on content type")

	t.Log("✓ Content type handling verified")
}

// TestRapidFireRequests tests many requests in quick succession
func TestRapidFireRequests(t *testing.T) {
	h := NewTestHelper(t)

	count := 50
	successCount := 0

	for i := 0; i < count; i++ {
		event := h.CreateWhitelistedEvent(1, "Rapid fire", nostr.Tags{})
		resp, result := h.PostEventHTTP(event)

		if resp.StatusCode == 200 {
			if ok, _ := result["ok"].(bool); ok {
				successCount++
			}
		}
	}

	assert.Greater(t, successCount, count/2, "Most rapid fire requests should succeed")
	t.Logf("✓ Rapid fire: %d/%d requests successful", successCount, count)
}

// TestInvalidHexValues tests events with invalid hex encoding
func TestInvalidHexValues(t *testing.T) {
	h := NewTestHelper(t)

	// Create event with invalid hex in ID
	eventJSON := `{
		"id": "not-hex-value",
		"pubkey": "` + whitelistedPk + `",
		"created_at": 1234567890,
		"kind": 1,
		"tags": [],
		"content": "test",
		"sig": "` + strings.Repeat("0", 128) + `"
	}`

	resp, err := h.client.Post(
		httpBaseURL+"/api/quotes",
		"application/json",
		bytes.NewBufferString(eventJSON),
	)
	assert.NoError(t, err)
	defer resp.Body.Close()

	h.AssertHTTPStatus(resp, 400)

	t.Log("✓ Invalid hex values rejected")
}
