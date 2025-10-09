package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"

	"github.com/gorilla/websocket"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestEventWithPrivateKey creates a signed event with a specific private key
func createTestEventWithPrivateKey(privKeyHex string, kind int, content string, tags [][]string) (map[string]interface{}, error) {
	privKeyBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	privKey := secp256k1.PrivKeyFromBytes(privKeyBytes)
	pubKey := privKey.PubKey()
	pubKeyHex := hex.EncodeToString(pubKey.SerializeCompressed()[1:])

	createdAt := time.Now().Unix()

	event := map[string]interface{}{
		"pubkey":     pubKeyHex,
		"created_at": createdAt,
		"kind":       kind,
		"tags":       tags,
		"content":    content,
	}

	serialized := []interface{}{0, pubKeyHex, createdAt, kind, tags, content}
	serializedBytes, _ := json.Marshal(serialized)

	hash := sha256.Sum256(serializedBytes)
	eventID := hex.EncodeToString(hash[:])
	event["id"] = eventID

	idBytes, _ := hex.DecodeString(eventID)
	signature := ecdsa.Sign(privKey, idBytes)
	event["sig"] = hex.EncodeToString(signature.Serialize())

	return event, nil
}

func TestWriteAndReadConsistency(t *testing.T) {
	whitelistedPub, _ := nostr.GetPublicKey(whitelistedSk)

	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	startMessageLogger(t, conn)

	// Write 10 events directly with whitelisted key
	writtenEvents := make([]*nostr.Event, 10)
	for i := 0; i < 10; i++ {
		event := createTestEvent(whitelistedSk, 1,
			fmt.Sprintf("Test event number %d", i),
			nostr.Tags{[]string{"-"}})
		writtenEvents[i] = event

		eventMsg, _ := json.Marshal([]interface{}{"EVENT", event})
		conn.WriteMessage(websocket.TextMessage, eventMsg)
		time.Sleep(50 * time.Millisecond)
	}

	t.Logf("Wrote %d events", len(writtenEvents))
	time.Sleep(500 * time.Millisecond)

	// Now read all events back
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relay, err := nostr.RelayConnect(ctx, relayURL)
	require.NoError(t, err)
	defer relay.Close()

	// Query for all events by this pubkey
	sub, err := relay.Subscribe(ctx, []nostr.Filter{{
		Authors: []string{whitelistedPub},
		Kinds:   []int{1},
	}})
	require.NoError(t, err)

	readEvents := make(map[string]*nostr.Event)
	timeout := time.After(3 * time.Second)

	for {
		select {
		case event := <-sub.Events:
			readEvents[event.ID] = event
			t.Logf("Read event %d: %s", len(readEvents), event.Content)
		case <-sub.EndOfStoredEvents:
			goto done
		case <-timeout:
			goto done
		}
	}

done:
	t.Logf("Read %d events back from relay", len(readEvents))

	// Verify all written events can be read
	for i, written := range writtenEvents {
		read, found := readEvents[written.ID]
		if assert.True(t, found, "Event %d (ID: %s) not found in relay", i, written.ID[:16]) {
			assert.Equal(t, written.Content, read.Content, "Content mismatch for event %d", i)
			assert.Equal(t, written.PubKey, read.PubKey, "PubKey mismatch for event %d", i)
			assert.Equal(t, written.Kind, read.Kind, "Kind mismatch for event %d", i)
		}
	}

	assert.GreaterOrEqual(t, len(readEvents), len(writtenEvents),
		"Expected to read at least %d events, got %d", len(writtenEvents), len(readEvents))

	if len(readEvents) >= len(writtenEvents) {
		t.Log("✓ Write/Read consistency verified - all events readable")
	}
}

const httpURL = "http://127.0.0.1:8080/api/event"

func TestHTTPEventPost(t *testing.T) {
	// Create threshold event with whitelisted key
	data := ThresholdData{
		Origin:      "http-test",
		OriginPrice: 50000.00,
		OriginStamp: time.Now().Unix(),
		CommitHash:  "http_test_hash",
		CommitKey:   "http_test_key",
	}

	event := createThresholdEvent(whitelistedSk, 49000.00, time.Now().Unix()+3600, false, data)

	// Marshal event to JSON
	eventJSON, err := json.Marshal(event)
	require.NoError(t, err)

	t.Logf("→ Posting event via HTTP: thold_price=49000.00")

	// POST to HTTP endpoint
	resp, err := http.Post(
		httpURL,
		"application/json",
		bytes.NewBuffer(eventJSON),
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Read response body
	body, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)

	t.Logf("← HTTP Response [%d]: %s", resp.StatusCode, string(body))

	// Verify status code
	assert.True(t, resp.StatusCode >= 200 && resp.StatusCode < 300,
		"Expected 2xx status, got %d", resp.StatusCode)

	// Parse response
	var apiResp map[string]interface{}
	if err := json.Unmarshal(body, &apiResp); err == nil {
		t.Logf("✓ Response parsed: %v", apiResp)
	}

	t.Log("✓ Event posted via HTTP successfully")
}

func TestHTTPNonWhitelistedRejected(t *testing.T) {
	// Generate random non-whitelisted key
	randomSk := nostr.GeneratePrivateKey()

	data := ThresholdData{
		Origin:      "http-test-blocked",
		OriginPrice: 51000.00,
		OriginStamp: time.Now().Unix(),
		CommitHash:  "blocked_hash",
		CommitKey:   "blocked_key",
	}

	event := createThresholdEvent(randomSk, 50000.00, time.Now().Unix()+3600, false, data)
	eventJSON, _ := json.Marshal(event)

	t.Log("→ Posting event from non-whitelisted key via HTTP")

	resp, err := http.Post(httpURL, "application/json", bytes.NewBuffer(eventJSON))
	require.NoError(t, err)
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)
	t.Logf("← HTTP Response [%d]: %s", resp.StatusCode, string(body))

	// Should be rejected (400 or similar error status)
	assert.True(t, resp.StatusCode >= 400,
		"Expected error status for non-whitelisted key, got %d", resp.StatusCode)

	t.Log("✓ Correctly rejected non-whitelisted key via HTTP")
}
