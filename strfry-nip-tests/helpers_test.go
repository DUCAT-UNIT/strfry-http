package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/require"
)

const (
	relayURL      = "ws://localhost:7777"
	httpBaseURL   = "http://localhost:8080"
	whitelistedSk = "8ce73a2db5cbaf4b0ab3cabece9408e3b898c64474c0dbe27826c65d1180370e"
	whitelistedPk = "6b5008a293291c14effeb0e8b7c56a80ecb5ca7b801768e17ec93092be6c0621"
)

// TestHelper provides common test utilities
type TestHelper struct {
	t      *testing.T
	client *http.Client
}

// NewTestHelper creates a new test helper instance
func NewTestHelper(t *testing.T) *TestHelper {
	return &TestHelper{
		t:      t,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// ResetRateLimitBuckets restarts the server to reset rate limit buckets
func (h *TestHelper) ResetRateLimitBuckets() {
	h.t.Helper()
	h.t.Log("🔄 Resetting rate limit buckets by restarting server...")

	// Find strfry container (using docker-compose container name)
	containerName := "strfry-relay"
	cmd := exec.Command("docker", "ps", "-q", "--filter", "name="+containerName)
	output, err := cmd.Output()
	if err != nil {
		h.t.Logf("⚠️  Could not find container (docker ps failed): %v", err)
		h.t.Log("    Skipping bucket reset - tests may fail due to depleted buckets")
		return
	}

	containerID := string(bytes.TrimSpace(output))
	if containerID == "" {
		h.t.Logf("⚠️  No strfry container found (looking for: %s)", containerName)
		h.t.Log("    Run 'make setup' or 'docker-compose up -d' to start the relay")
		h.t.Log("    Skipping bucket reset - tests may fail due to depleted buckets")
		return
	}

	// Restart the container
	cmd = exec.Command("docker", "restart", containerID)
	if err := cmd.Run(); err != nil {
		h.t.Logf("⚠️  Failed to restart container: %v", err)
		return
	}

	h.t.Log("⏳ Waiting 3 seconds for server to start...")
	time.Sleep(3 * time.Second)

	// Verify server is responsive
	for i := 0; i < 10; i++ {
		resp, err := http.Get(httpBaseURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				h.t.Log("✅ Server restarted successfully - rate limit buckets reset!")
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	h.t.Log("⚠️  Server may not be fully ready yet")
}

// CreateTestEvent creates and signs a Nostr event
func (h *TestHelper) CreateTestEvent(sk string, kind int, content string, tags nostr.Tags) *nostr.Event {
	pub, _ := nostr.GetPublicKey(sk)
	event := &nostr.Event{
		PubKey:    pub,
		CreatedAt: nostr.Now(),
		Kind:      kind,
		Tags:      tags,
		Content:   content,
	}
	event.Sign(sk)
	return event
}

// CreateWhitelistedEvent creates an event from the whitelisted key
func (h *TestHelper) CreateWhitelistedEvent(kind int, content string, tags nostr.Tags) *nostr.Event {
	return h.CreateTestEvent(whitelistedSk, kind, content, tags)
}

// CreateNonWhitelistedEvent creates an event from a random key
func (h *TestHelper) CreateNonWhitelistedEvent(kind int, content string, tags nostr.Tags) *nostr.Event {
	sk := nostr.GeneratePrivateKey()
	return h.CreateTestEvent(sk, kind, content, tags)
}

// PostEventHTTP posts an event via HTTP API
func (h *TestHelper) PostEventHTTP(event *nostr.Event) (*http.Response, map[string]interface{}) {
	eventJSON, err := json.Marshal(event)
	require.NoError(h.t, err)

	resp, err := h.client.Post(
		httpBaseURL+"/api/quotes",
		"application/json",
		bytes.NewBuffer(eventJSON),
	)
	require.NoError(h.t, err)

	body, err := io.ReadAll(resp.Body)
	require.NoError(h.t, err)
	resp.Body.Close()

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	require.NoError(h.t, err)

	return resp, result
}

// QueryEventsHTTP queries events via HTTP API
func (h *TestHelper) QueryEventsHTTP(method string, queryParams map[string]string, body interface{}) (*http.Response, []map[string]interface{}) {
	var req *http.Request
	var err error

	url := httpBaseURL + "/api/query"

	if method == "POST" && body != nil {
		bodyJSON, err := json.Marshal(body)
		require.NoError(h.t, err)
		req, err = http.NewRequest("POST", url, bytes.NewBuffer(bodyJSON))
		require.NoError(h.t, err)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest("GET", url, nil)
		require.NoError(h.t, err)

		if queryParams != nil && len(queryParams) > 0 {
			q := req.URL.Query()
			for k, v := range queryParams {
				q.Add(k, v)
			}
			req.URL.RawQuery = q.Encode()
		}
	}

	resp, err := h.client.Do(req)
	require.NoError(h.t, err)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(h.t, err)
	resp.Body.Close()

	// Check if it's an error response
	if resp.StatusCode >= 400 {
		var errorResult map[string]interface{}
		json.Unmarshal(bodyBytes, &errorResult)
		return resp, nil
	}

	var result []map[string]interface{}
	err = json.Unmarshal(bodyBytes, &result)
	require.NoError(h.t, err)

	return resp, result
}

// GetEventByID fetches a single event by ID via HTTP
func (h *TestHelper) GetEventByID(eventID string) (*http.Response, map[string]interface{}) {
	resp, err := h.client.Get(httpBaseURL + "/api/quotes/" + eventID)
	require.NoError(h.t, err)

	body, err := io.ReadAll(resp.Body)
	require.NoError(h.t, err)
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errorResult map[string]interface{}
		json.Unmarshal(body, &errorResult)
		return resp, errorResult
	}

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	require.NoError(h.t, err)

	return resp, result
}

// HealthCheck performs a health check
func (h *TestHelper) HealthCheck() (*http.Response, map[string]interface{}) {
	resp, err := h.client.Get(httpBaseURL + "/health")
	require.NoError(h.t, err)

	body, err := io.ReadAll(resp.Body)
	require.NoError(h.t, err)
	resp.Body.Close()

	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	require.NoError(h.t, err)

	return resp, result
}

// ConnectWebSocket establishes a WebSocket connection
func (h *TestHelper) ConnectWebSocket() *websocket.Conn {
	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	require.NoError(h.t, err)
	return conn
}

// SendEventWebSocket sends an event over WebSocket and waits for OK response
func (h *TestHelper) SendEventWebSocket(conn *websocket.Conn, event *nostr.Event, timeout time.Duration) (bool, string) {
	eventMsg, _ := json.Marshal([]interface{}{"EVENT", event})
	err := conn.WriteMessage(websocket.TextMessage, eventMsg)
	require.NoError(h.t, err)

	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			return false, "timeout waiting for OK"
		default:
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return false, fmt.Sprintf("read error: %v", err)
			}

			var parsed []interface{}
			if err := json.Unmarshal(msg, &parsed); err != nil {
				continue
			}

			if len(parsed) >= 3 && parsed[0] == "OK" && parsed[1] == event.ID {
				accepted := parsed[2].(bool)
				message := ""
				if len(parsed) >= 4 {
					message = parsed[3].(string)
				}
				return accepted, message
			}
		}
	}
}

// WaitForEvents waits for multiple events to be processed
func (h *TestHelper) WaitForEvents(count int, duration time.Duration) {
	time.Sleep(duration * time.Duration(count))
}

// AssertHTTPStatus asserts the HTTP response status
func (h *TestHelper) AssertHTTPStatus(resp *http.Response, expectedStatus int) {
	require.Equal(h.t, expectedStatus, resp.StatusCode,
		"Expected HTTP %d, got %d", expectedStatus, resp.StatusCode)
}

// AssertEventAccepted asserts an event was accepted
func (h *TestHelper) AssertEventAccepted(result map[string]interface{}) {
	ok, exists := result["ok"].(bool)
	require.True(h.t, exists, "Response missing 'ok' field")
	require.True(h.t, ok, "Event was rejected: %v", result["message"])
}

// AssertEventRejected asserts an event was rejected
func (h *TestHelper) AssertEventRejected(result map[string]interface{}, expectedMessage string) {
	ok, exists := result["ok"].(bool)
	require.True(h.t, exists, "Response missing 'ok' field")
	require.False(h.t, ok, "Event should have been rejected")

	if expectedMessage != "" {
		message, _ := result["message"].(string)
		require.Contains(h.t, message, expectedMessage,
			"Expected rejection message to contain '%s', got '%s'", expectedMessage, message)
	}
}

// AssertSecurityHeaders checks for required security headers
func (h *TestHelper) AssertSecurityHeaders(resp *http.Response) {
	headers := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Content-Security-Policy": "default-src 'none'",
		"Referrer-Policy":        "no-referrer",
	}

	for header, expectedValue := range headers {
		actualValue := resp.Header.Get(header)
		require.NotEmpty(h.t, actualValue, "Missing security header: %s", header)
		require.Contains(h.t, actualValue, expectedValue,
			"Header %s: expected to contain '%s', got '%s'", header, expectedValue, actualValue)
	}
}

// AssertCORSHeaders checks for CORS headers
func (h *TestHelper) AssertCORSHeaders(resp *http.Response) {
	require.NotEmpty(h.t, resp.Header.Get("Access-Control-Allow-Origin"), "Missing CORS header")
}

// GenerateTestEvents generates multiple test events
func (h *TestHelper) GenerateTestEvents(count int, kind int, contentPrefix string) []*nostr.Event {
	events := make([]*nostr.Event, count)
	for i := 0; i < count; i++ {
		content := fmt.Sprintf("%s %d", contentPrefix, i)
		events[i] = h.CreateWhitelistedEvent(kind, content, nostr.Tags{})
	}
	return events
}

// BatchPostEvents posts multiple events and waits for processing
func (h *TestHelper) BatchPostEvents(events []*nostr.Event) []map[string]interface{} {
	results := make([]map[string]interface{}, len(events))
	for i, event := range events {
		_, result := h.PostEventHTTP(event)
		results[i] = result
		time.Sleep(50 * time.Millisecond) // Small delay between posts
	}
	time.Sleep(500 * time.Millisecond) // Wait for processing
	return results
}
