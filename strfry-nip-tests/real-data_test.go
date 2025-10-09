package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const relayURL = "ws://localhost:7777"

// Use the whitelisted key for all tests
const whitelistedSk = "8ce73a2db5cbaf4b0ab3cabece9408e3b898c64474c0dbe27826c65d1180370e"

// ThresholdData represents the data stored in event content
type ThresholdData struct {
	Origin      string  `json:"origin"`
	OriginPrice float64 `json:"origin_price"`
	OriginStamp int64   `json:"origin_stamp"`
	CommitHash  string  `json:"commit_hash"`
	CommitKey   string  `json:"commit_key"`
}

// Helper function to create threshold event
func createThresholdEvent(sk string, tholdPrice float64, tholdStamp int64, isTriggered bool, data ThresholdData) *nostr.Event {
	pub, _ := nostr.GetPublicKey(sk)
	contentJSON, _ := json.Marshal(data)

	event := &nostr.Event{
		PubKey:    pub,
		CreatedAt: nostr.Now(),
		Kind:      1,
		Tags: nostr.Tags{
			[]string{"-"},
			[]string{"x", fmt.Sprintf("%.2f", tholdPrice)}, // Price threshold
			[]string{"y", fmt.Sprintf("%d", tholdStamp)},   // Stamp
			[]string{"z", fmt.Sprintf("%t", isTriggered)},  // Triggered
		},
		Content: string(contentJSON),
	}
	event.Sign(sk)
	return event
}

func TestWriteThresholdData(t *testing.T) {
	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	startMessageLogger(t, conn)

	// Create threshold event with whitelisted key
	data := ThresholdData{
		Origin:      "binance",
		OriginPrice: 45000.50,
		OriginStamp: time.Now().Unix(),
		CommitHash:  "abc123def456",
		CommitKey:   "key789",
	}

	event := createThresholdEvent(whitelistedSk, 44000.00, time.Now().Unix()+3600, false, data)

	eventMsg, _ := json.Marshal([]interface{}{"EVENT", event})
	t.Logf("→ Writing threshold event: thold_price=44000.00, is_triggered=false")
	conn.WriteMessage(websocket.TextMessage, eventMsg)

	time.Sleep(500 * time.Millisecond)
	t.Log("✓ Threshold data written")
}

func TestQueryByTholdPrice(t *testing.T) {
	whitelistedPub, _ := nostr.GetPublicKey(whitelistedSk)

	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	startMessageLogger(t, conn)

	// Write events with different threshold prices
	prices := []float64{100.00, 200.00, 300.00}

	for _, price := range prices {
		data := ThresholdData{
			Origin:      "coinbase",
			OriginPrice: price + 10,
			OriginStamp: time.Now().Unix(),
			CommitHash:  fmt.Sprintf("hash_%v", price),
			CommitKey:   fmt.Sprintf("key_%v", price),
		}

		event := createThresholdEvent(whitelistedSk, price, time.Now().Unix()+3600, false, data)
		eventMsg, _ := json.Marshal([]interface{}{"EVENT", event})
		conn.WriteMessage(websocket.TextMessage, eventMsg)
		time.Sleep(100 * time.Millisecond)
	}

	t.Logf("Written 3 events with prices: %v", prices)
	time.Sleep(500 * time.Millisecond)

	// Query for specific threshold price
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relay, err := nostr.RelayConnect(ctx, relayURL)
	require.NoError(t, err)
	defer relay.Close()

	sub, err := relay.Subscribe(ctx, []nostr.Filter{{
		Authors: []string{whitelistedPub},
		Tags: nostr.TagMap{
			"x": []string{"200.00"},
		},
	}})
	require.NoError(t, err)

	found := false
	timeout := time.After(2 * time.Second)

	for {
		select {
		case event := <-sub.Events:
			var data ThresholdData
			json.Unmarshal([]byte(event.Content), &data)
			t.Logf("✓ Found event with thold_price=200.00: origin=%s", data.Origin)
			found = true
			return
		case <-sub.EndOfStoredEvents:
			goto done
		case <-timeout:
			goto done
		}
	}

done:
	assert.True(t, found, "Should find event with thold_price=200.00")
}

func TestQueryByIsTriggered(t *testing.T) {
	whitelistedPub, _ := nostr.GetPublicKey(whitelistedSk)

	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	startMessageLogger(t, conn)

	// Write mix of triggered and untriggered events
	for i := 0; i < 5; i++ {
		isTriggered := i%2 == 0
		data := ThresholdData{
			Origin:      "kraken",
			OriginPrice: float64(50000 + i*100),
			OriginStamp: time.Now().Unix(),
			CommitHash:  fmt.Sprintf("hash_%d", i),
			CommitKey:   fmt.Sprintf("key_%d", i),
		}

		event := createThresholdEvent(whitelistedSk, float64(49000+i*100), time.Now().Unix()+3600, isTriggered, data)
		eventMsg, _ := json.Marshal([]interface{}{"EVENT", event})
		conn.WriteMessage(websocket.TextMessage, eventMsg)
		time.Sleep(100 * time.Millisecond)
	}

	t.Log("Written 5 events (3 triggered, 2 not triggered)")
	time.Sleep(500 * time.Millisecond)

	// Query only triggered events
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relay, err := nostr.RelayConnect(ctx, relayURL)
	require.NoError(t, err)
	defer relay.Close()

	sub, err := relay.Subscribe(ctx, []nostr.Filter{{
		Authors: []string{whitelistedPub},
		Tags: nostr.TagMap{
			"z": []string{"true"},
		},
	}})
	require.NoError(t, err)

	triggeredCount := 0
	timeout := time.After(2 * time.Second)

	for {
		select {
		case event := <-sub.Events:
			var data ThresholdData
			json.Unmarshal([]byte(event.Content), &data)
			triggeredCount++
			t.Logf("Found triggered event: origin=%s, origin_price=%.2f", data.Origin, data.OriginPrice)
		case <-sub.EndOfStoredEvents:
			goto done
		case <-timeout:
			goto done
		}
	}

done:
	t.Logf("✓ Found %d triggered events", triggeredCount)
	assert.GreaterOrEqual(t, triggeredCount, 3, "Should find at least 3 triggered events")
	t.Logf("Note: Found %d total triggered events (including from previous test runs)", triggeredCount)
}

func TestQueryByTholdStamp(t *testing.T) {
	whitelistedPub, _ := nostr.GetPublicKey(whitelistedSk)

	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	startMessageLogger(t, conn)

	// Write events with specific threshold timestamps
	now := time.Now().Unix()
	targetStamp := now + 7200 // 2 hours from now

	stamps := []int64{now + 3600, targetStamp, now + 10800}

	for i, stamp := range stamps {
		data := ThresholdData{
			Origin:      "gemini",
			OriginPrice: float64(60000 + i*1000),
			OriginStamp: now,
			CommitHash:  fmt.Sprintf("hash_%d", i),
			CommitKey:   fmt.Sprintf("key_%d", i),
		}

		event := createThresholdEvent(whitelistedSk, float64(59000+i*1000), stamp, false, data)
		eventMsg, _ := json.Marshal([]interface{}{"EVENT", event})
		conn.WriteMessage(websocket.TextMessage, eventMsg)
		time.Sleep(100 * time.Millisecond)
	}

	t.Logf("Written 3 events with different thold_stamps")
	time.Sleep(500 * time.Millisecond)

	// Query for specific threshold timestamp
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	relay, err := nostr.RelayConnect(ctx, relayURL)
	require.NoError(t, err)
	defer relay.Close()

	sub, err := relay.Subscribe(ctx, []nostr.Filter{{
		Authors: []string{whitelistedPub},
		Tags: nostr.TagMap{
			"y": []string{fmt.Sprintf("%d", targetStamp)},
		},
	}})
	require.NoError(t, err)

	found := false
	timeout := time.After(2 * time.Second)

	for {
		select {
		case event := <-sub.Events:
			var data ThresholdData
			json.Unmarshal([]byte(event.Content), &data)
			t.Logf("✓ Found event with target thold_stamp: origin=%s, price=%.2f", data.Origin, data.OriginPrice)
			found = true
			return
		case <-sub.EndOfStoredEvents:
			goto done
		case <-timeout:
			goto done
		}
	}

done:
	assert.True(t, found, "Should find event with specific thold_stamp")
}

func TestCompleteThresholdWorkflow(t *testing.T) {
	whitelistedPub, _ := nostr.GetPublicKey(whitelistedSk)

	conn, _, err := websocket.DefaultDialer.Dial(relayURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	startMessageLogger(t, conn)

	// Step 1: Write untriggered threshold
	data := ThresholdData{
		Origin:      "bitfinex",
		OriginPrice: 48500.00,
		OriginStamp: time.Now().Unix(),
		CommitHash:  "original_hash",
		CommitKey:   "original_key",
	}

	event1 := createThresholdEvent(whitelistedSk, 48000.00, time.Now().Unix()+3600, false, data)
	eventMsg1, _ := json.Marshal([]interface{}{"EVENT", event1})
	t.Log("→ Step 1: Writing untriggered threshold")
	conn.WriteMessage(websocket.TextMessage, eventMsg1)
	time.Sleep(300 * time.Millisecond)

	// Step 2: Query untriggered events
	ctx1, cancel1 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel1()

	relay1, _ := nostr.RelayConnect(ctx1, relayURL)
	defer relay1.Close()

	sub1, _ := relay1.Subscribe(ctx1, []nostr.Filter{{
		Authors: []string{whitelistedPub},
		Tags: nostr.TagMap{
			"z": []string{"false"},
			"x": []string{"48000.00"},
		},
	}})

	foundUntriggered := false
	timeout1 := time.After(2 * time.Second)

loop1:
	for {
		select {
		case <-sub1.Events:
			foundUntriggered = true
			t.Log("✓ Step 2: Found untriggered threshold")
			break loop1
		case <-sub1.EndOfStoredEvents:
			break loop1
		case <-timeout1:
			break loop1
		}
	}

	assert.True(t, foundUntriggered, "Should find untriggered event")

	// Step 3: Write triggered version
	data.OriginPrice = 47900.00 // Price hit threshold
	event2 := createThresholdEvent(whitelistedSk, 48000.00, time.Now().Unix()+3600, true, data)
	eventMsg2, _ := json.Marshal([]interface{}{"EVENT", event2})
	t.Log("→ Step 3: Writing triggered threshold (price hit)")
	conn.WriteMessage(websocket.TextMessage, eventMsg2)
	time.Sleep(300 * time.Millisecond)

	// Step 4: Query triggered events
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	relay2, _ := nostr.RelayConnect(ctx2, relayURL)
	defer relay2.Close()

	sub2, _ := relay2.Subscribe(ctx2, []nostr.Filter{{
		Authors: []string{whitelistedPub},
		Tags: nostr.TagMap{
			"z": []string{"true"},
			"x": []string{"48000.00"},
		},
	}})

	foundTriggered := false
	timeout2 := time.After(2 * time.Second)

loop2:
	for {
		select {
		case event := <-sub2.Events:
			var retrievedData ThresholdData
			json.Unmarshal([]byte(event.Content), &retrievedData)
			assert.Equal(t, 47900.00, retrievedData.OriginPrice)
			foundTriggered = true
			t.Log("✓ Step 4: Found triggered threshold with correct data")
			break loop2
		case <-sub2.EndOfStoredEvents:
			break loop2
		case <-timeout2:
			break loop2
		}
	}

	assert.True(t, foundTriggered, "Should find triggered event")
	t.Log("✓ Complete workflow: write → query → update → query verified")
}
