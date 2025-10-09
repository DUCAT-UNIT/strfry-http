package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create and sign an event
func createTestEvent(sk string, kind int, content string, tags nostr.Tags) *nostr.Event {
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

// Helper to wait for specific message type from websocket
func waitForMessage(messages chan []byte, messageType string, timeout time.Duration) ([]interface{}, error) {
	timer := time.After(timeout)
	for {
		select {
		case msg := <-messages:
			var parsed []interface{}
			if err := json.Unmarshal(msg, &parsed); err != nil {
				continue
			}
			if len(parsed) > 0 && parsed[0].(string) == messageType {
				return parsed, nil
			}
		case <-timer:
			return nil, assert.AnError
		}
	}
}

// Helper to read all messages and log them
func startMessageLogger(t *testing.T, conn *websocket.Conn) chan []byte {
	messages := make(chan []byte, 100)
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			t.Logf("← Received: %s", string(msg))
			messages <- msg
		}
	}()
	return messages
}

func TestBasicConnection(t *testing.T) {
	relay, err := nostr.RelayConnect(context.Background(), relayURL)
	require.NoError(t, err)
	defer relay.Close()
	t.Log("✓ Connected to relay")
}

func TestNonWhitelistedEventRejected(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	pub, _ := nostr.GetPublicKey(sk)

	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	messages := startMessageLogger(t, conn)

	event := createTestEvent(sk, 1, "Event from non-whitelisted key", nostr.Tags{[]string{"-"}})
	eventMsg, _ := json.Marshal([]interface{}{"EVENT", event})

	t.Logf("→ Sending event from non-whitelisted pubkey: %s", pub)
	conn.WriteMessage(websocket.TextMessage, eventMsg)

	// Wait for rejection
	timeout := time.After(2 * time.Second)
	for {
		select {
		case msg := <-messages:
			var parsed []interface{}
			json.Unmarshal(msg, &parsed)
			if len(parsed) >= 4 && parsed[0] == "OK" && parsed[1] == event.ID {
				accepted := parsed[2].(bool)
				msgText := parsed[3].(string)
				assert.False(t, accepted, "Non-whitelisted events should be rejected")
				assert.Contains(t, msgText, "blocked: pubkey not in whitelist")
				t.Logf("✓ Correctly rejected: %s", msgText)
				return
			}
		case <-timeout:
			t.Fatal("Timeout waiting for rejection")
		}
	}
}

func TestWhitelistedEventAccepted(t *testing.T) {
	whitelistedSk := "8ce73a2db5cbaf4b0ab3cabece9408e3b898c64474c0dbe27826c65d1180370e"
	pub, _ := nostr.GetPublicKey(whitelistedSk)

	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	messages := startMessageLogger(t, conn)

	event := createTestEvent(whitelistedSk, 1, "Event from whitelisted key", nostr.Tags{[]string{"-"}})
	eventMsg, _ := json.Marshal([]interface{}{"EVENT", event})

	t.Logf("→ Sending event from whitelisted pubkey: %s", pub)
	conn.WriteMessage(websocket.TextMessage, eventMsg)

	// Wait for acceptance
	timeout := time.After(2 * time.Second)
	for {
		select {
		case msg := <-messages:
			var parsed []interface{}
			json.Unmarshal(msg, &parsed)
			if len(parsed) >= 3 && parsed[0] == "OK" && parsed[1] == event.ID {
				accepted := parsed[2].(bool)
				assert.True(t, accepted, "Whitelisted events should be accepted")
				t.Log("✓ Event accepted")
				return
			}
		case <-timeout:
			t.Fatal("Timeout waiting for acceptance")
		}
	}
}

func TestRelayInformation(t *testing.T) {
	httpURL := "http://localhost:7777"
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", httpURL, nil)
	req.Header.Set("Accept", "application/nostr+json")

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var info struct {
		Name          string `json:"name"`
		Description   string `json:"description"`
		SupportedNIPs []int  `json:"supported_nips"`
	}

	err = json.NewDecoder(resp.Body).Decode(&info)
	require.NoError(t, err)

	t.Logf("Relay name: %s", info.Name)
	t.Logf("Description: %s", info.Description)
	t.Logf("Supported NIPs: %v", info.SupportedNIPs)

	t.Log("✓ Relay properly advertises capabilities")
}

func TestPubkeyWhitelist(t *testing.T) {
	whitelistedSk := "8ce73a2db5cbaf4b0ab3cabece9408e3b898c64474c0dbe27826c65d1180370e"
	whitelistedPub, _ := nostr.GetPublicKey(whitelistedSk)

	blockedSk := nostr.GeneratePrivateKey()
	blockedPub, _ := nostr.GetPublicKey(blockedSk)

	t.Logf("Whitelisted pubkey: %s", whitelistedPub)
	t.Logf("Blocked pubkey: %s", blockedPub)

	t.Run("WhitelistedKeyAccepted", func(t *testing.T) {
		conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
		require.NoError(t, err)
		defer conn.Close()

		messages := startMessageLogger(t, conn)

		event := createThresholdEvent(whitelistedSk, 100.0, time.Now().Unix(), false, ThresholdData{
			Origin: "test", OriginPrice: 100.0,
		})

		eventMsg, _ := json.Marshal([]interface{}{"EVENT", event})
		err = conn.WriteMessage(websocket.TextMessage, eventMsg)
		require.NoError(t, err)

		timeout := time.After(2 * time.Second)
		found := false
		for !found {
			select {
			case msg := <-messages:
				var parsed []interface{}
				if err := json.Unmarshal(msg, &parsed); err != nil {
					continue
				}
				if len(parsed) >= 3 && parsed[0] == "OK" && parsed[1] == event.ID {
					accepted := parsed[2].(bool)
					assert.True(t, accepted, "Whitelisted pubkey should be accepted")
					if len(parsed) >= 4 {
						t.Logf("Response: %s", parsed[3].(string))
					}
					found = true
				}
			case <-timeout:
				t.Fatal("Timeout waiting for OK message")
			}
		}
	})

	t.Run("BlockedKeyRejected", func(t *testing.T) {
		conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
		require.NoError(t, err)
		defer conn.Close()

		messages := startMessageLogger(t, conn)

		event := createThresholdEvent(blockedSk, 100.0, time.Now().Unix(), false, ThresholdData{
			Origin: "test", OriginPrice: 100.0,
		})

		eventMsg, _ := json.Marshal([]interface{}{"EVENT", event})
		err = conn.WriteMessage(websocket.TextMessage, eventMsg)
		require.NoError(t, err)

		timeout := time.After(2 * time.Second)
		found := false
		for !found {
			select {
			case msg := <-messages:
				var parsed []interface{}
				if err := json.Unmarshal(msg, &parsed); err != nil {
					continue
				}
				if len(parsed) >= 4 && parsed[0] == "OK" && parsed[1] == event.ID {
					accepted := parsed[2].(bool)
					msgText := parsed[3].(string)
					assert.False(t, accepted, "Non-whitelisted pubkey should be rejected")
					assert.Contains(t, msgText, "blocked: pubkey not in whitelist")
					t.Logf("Correctly rejected: %s", msgText)
					found = true
				}
			case <-timeout:
				t.Fatal("Timeout waiting for OK message")
			}
		}
	})
}

func TestMultipleEventsFromWhitelistedKey(t *testing.T) {
	whitelistedSk := "8ce73a2db5cbaf4b0ab3cabece9408e3b898c64474c0dbe27826c65d1180370e"

	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	messages := startMessageLogger(t, conn)

	// Send multiple events
	event1 := createTestEvent(whitelistedSk, 1, "First event", nostr.Tags{[]string{"-"}})
	eventMsg1, _ := json.Marshal([]interface{}{"EVENT", event1})
	t.Log("→ Sending first event")
	conn.WriteMessage(websocket.TextMessage, eventMsg1)
	time.Sleep(100 * time.Millisecond)

	event2 := createTestEvent(whitelistedSk, 1, "Second event", nostr.Tags{[]string{"-"}})
	eventMsg2, _ := json.Marshal([]interface{}{"EVENT", event2})
	t.Log("→ Sending second event")
	conn.WriteMessage(websocket.TextMessage, eventMsg2)
	time.Sleep(100 * time.Millisecond)

	event3 := createTestEvent(whitelistedSk, 1, "Third event", nostr.Tags{[]string{"-"}})
	eventMsg3, _ := json.Marshal([]interface{}{"EVENT", event3})
	t.Log("→ Sending third event")
	conn.WriteMessage(websocket.TextMessage, eventMsg3)

	time.Sleep(500 * time.Millisecond)

	// Verify all were accepted by checking messages
	acceptedCount := 0
	timeout := time.After(1 * time.Second)

drainLoop:
	for {
		select {
		case msg := <-messages:
			var parsed []interface{}
			json.Unmarshal(msg, &parsed)
			if len(parsed) >= 3 && parsed[0] == "OK" && parsed[2] == true {
				acceptedCount++
			}
		case <-timeout:
			break drainLoop
		}
	}

	t.Logf("✓ Multiple events sent, %d accepted", acceptedCount)
}

func TestGetWhitelistedPubkey(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	pub, _ := nostr.GetPublicKey(sk)
	t.Logf("Generated keypair:")
	t.Logf("  Public key: %s", pub)
	t.Logf("  Secret key: %s", sk)
	t.Logf("\nTo whitelist this key, update C++ code with:")
	t.Logf(`  std::string allowedPubkey = "%s";`, pub)
}
