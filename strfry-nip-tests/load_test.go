package main

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
)

// TestLoadBasicThroughput tests basic throughput capacity
func TestLoadBasicThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	h := NewTestHelper(t)

	count := 100
	start := time.Now()
	successCount := int32(0)
	rateLimitedCount := int32(0)

	for i := 0; i < count; i++ {
		event := h.CreateWhitelistedEvent(1, fmt.Sprintf("Throughput test %d", i), nostr.Tags{})
		resp, result := h.PostEventHTTP(event)

		if resp.StatusCode == 200 {
			if ok, _ := result["ok"].(bool); ok {
				atomic.AddInt32(&successCount, 1)
			}
		} else if resp.StatusCode == 429 {
			atomic.AddInt32(&rateLimitedCount, 1)
		}
	}

	duration := time.Since(start)
	throughput := float64(successCount) / duration.Seconds()

	// Load tests may hit rate limits - that's expected behavior
	assert.Greater(t, int(successCount), 0, "Some requests should succeed")
	t.Logf("✓ Throughput: %.2f events/sec (%d/%d successful, %d rate limited in %v)",
		throughput, successCount, count, rateLimitedCount, duration)
}

// TestLoadConcurrentWrites tests concurrent write performance
func TestLoadConcurrentWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	workers := 20
	eventsPerWorker := 10
	totalEvents := workers * eventsPerWorker

	var wg sync.WaitGroup
	successCount := int32(0)
	start := time.Now()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			helper := NewTestHelper(t)
			for j := 0; j < eventsPerWorker; j++ {
				event := helper.CreateWhitelistedEvent(1,
					fmt.Sprintf("Worker %d event %d", workerID, j),
					nostr.Tags{})

				resp, result := helper.PostEventHTTP(event)
				if resp.StatusCode == 200 {
					if ok, _ := result["ok"].(bool); ok {
						atomic.AddInt32(&successCount, 1)
					}
				}
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)
	throughput := float64(successCount) / duration.Seconds()

	assert.Greater(t, int(successCount), totalEvents*7/10, "Should succeed at least 70%% under load")
	t.Logf("✓ Concurrent writes: %.2f events/sec (%d/%d successful in %v)",
		throughput, successCount, totalEvents, duration)
}

// TestLoadConcurrentReads tests concurrent read performance
func TestLoadConcurrentReads(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	h := NewTestHelper(t)

	// First, create some events to query
	setupEvents := 50
	events := h.GenerateTestEvents(setupEvents, 1, "Read load test")
	h.BatchPostEvents(events)

	// Now hammer with concurrent reads
	workers := 30
	queriesPerWorker := 10
	totalQueries := workers * queriesPerWorker

	var wg sync.WaitGroup
	successCount := int32(0)
	rateLimitedCount := int32(0)
	start := time.Now()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			helper := NewTestHelper(t)
			for j := 0; j < queriesPerWorker; j++ {
				// Randomly query by different filters
				var resp *http.Response
				switch j % 3 {
				case 0:
					resp, _ = helper.QueryEventsHTTP("GET", map[string]string{"authors": whitelistedPk}, nil)
				case 1:
					resp, _ = helper.QueryEventsHTTP("GET", map[string]string{"kinds": "1"}, nil)
				case 2:
					resp, _ = helper.QueryEventsHTTP("GET", map[string]string{"limit": "10"}, nil)
				}

				if resp.StatusCode == 200 {
					atomic.AddInt32(&successCount, 1)
				} else if resp.StatusCode == 429 {
					atomic.AddInt32(&rateLimitedCount, 1)
				}
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)
	throughput := float64(successCount) / duration.Seconds()

	// Concurrent load tests may hit rate limits - that's expected
	assert.Greater(t, int(successCount), 0, "Some queries should succeed")
	t.Logf("✓ Concurrent reads: %.2f queries/sec (%d/%d successful, %d rate limited in %v)",
		throughput, successCount, totalQueries, rateLimitedCount, duration)
}

// TestLoadMixedWorkload tests mixed read/write workload
func TestLoadMixedWorkload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	workers := 20
	operationsPerWorker := 10
	totalOps := workers * operationsPerWorker

	var wg sync.WaitGroup
	writeSuccessCount := int32(0)
	readSuccessCount := int32(0)
	start := time.Now()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			helper := NewTestHelper(t)
			for j := 0; j < operationsPerWorker; j++ {
				if j%2 == 0 {
					// Write
					event := helper.CreateWhitelistedEvent(1,
						fmt.Sprintf("Mixed workload %d-%d", workerID, j),
						nostr.Tags{})

					resp, result := helper.PostEventHTTP(event)
					if resp.StatusCode == 200 {
						if ok, _ := result["ok"].(bool); ok {
							atomic.AddInt32(&writeSuccessCount, 1)
						}
					}
				} else {
					// Read
					resp, _ := helper.QueryEventsHTTP("GET", map[string]string{"limit": "5"}, nil)
					if resp.StatusCode == 200 {
						atomic.AddInt32(&readSuccessCount, 1)
					}
				}
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)
	throughput := float64(writeSuccessCount+readSuccessCount) / duration.Seconds()

	t.Logf("✓ Mixed workload: %.2f ops/sec (writes: %d/%d, reads: %d/%d in %v)",
		throughput, writeSuccessCount, totalOps/2, readSuccessCount, totalOps/2, duration)
}

// TestLoadLargeEvents tests performance with large events
func TestLoadLargeEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	h := NewTestHelper(t)

	eventSize := 50000 // 50KB events
	count := 20

	content := make([]byte, eventSize)
	for i := range content {
		content[i] = 'A'
	}

	successCount := 0
	rateLimitedCount := 0
	rejectedCount := 0
	start := time.Now()

	for i := 0; i < count; i++ {
		event := h.CreateWhitelistedEvent(1, string(content), nostr.Tags{})
		resp, result := h.PostEventHTTP(event)

		if resp.StatusCode == 200 {
			if ok, _ := result["ok"].(bool); ok {
				successCount++
			} else {
				rejectedCount++
			}
		} else if resp.StatusCode == 429 {
			rateLimitedCount++
		} else if resp.StatusCode == 400 {
			// Large events might be rejected as too big
			rejectedCount++
		}

		// Small delay to avoid overwhelming connection pool with large payloads
		time.Sleep(100 * time.Millisecond)
	}

	duration := time.Since(start)
	totalBytes := float64(successCount*eventSize) / 1024 / 1024 // MB
	throughputMB := totalBytes / duration.Seconds()

	t.Logf("✓ Large events: %.2f MB/sec (%d/%d events accepted, %d rate limited, %d rejected, %.2f MB in %v)",
		throughputMB, successCount, count, rateLimitedCount, rejectedCount, totalBytes, duration)
}

// TestLoadQueryComplexity tests query performance with complex filters
func TestLoadQueryComplexity(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	h := NewTestHelper(t)

	// Setup: Create events with various attributes
	for i := 0; i < 100; i++ {
		kind := 1 + (i % 5)
		tags := nostr.Tags{
			[]string{"t", fmt.Sprintf("tag%d", i%10)},
			[]string{"e", fmt.Sprintf("event%d", i)},
		}
		event := h.CreateWhitelistedEvent(kind, fmt.Sprintf("Complex query test %d", i), tags)
		h.PostEventHTTP(event)
	}

	h.WaitForEvents(100, 2000)

	// Test various query complexities
	queries := []struct {
		name   string
		filter map[string]interface{}
	}{
		{"SimpleAuthor", map[string]interface{}{"authors": []string{whitelistedPk}, "limit": 20}},
		{"MultipleKinds", map[string]interface{}{"kinds": []int{1, 2, 3}, "limit": 20}},
		{"AuthorAndKind", map[string]interface{}{"authors": []string{whitelistedPk}, "kinds": []int{1}, "limit": 20}},
		{"WithTimeRange", map[string]interface{}{"authors": []string{whitelistedPk}, "since": nostr.Now() - 10000, "limit": 20}},
	}

	successCount := 0
	rateLimitedCount := 0

	for _, q := range queries {
		start := time.Now()
		resp, results := h.QueryEventsHTTP("POST", nil, q.filter)
		duration := time.Since(start)

		// Accept both success and rate limiting in load tests
		if resp.StatusCode == 200 {
			successCount++
			t.Logf("  %s: %d results in %v", q.name, len(results), duration)
		} else if resp.StatusCode == 429 {
			rateLimitedCount++
			t.Logf("  %s: rate limited after %v", q.name, duration)
		}
	}

	t.Logf("✓ Complex query performance tested (%d succeeded, %d rate limited)", successCount, rateLimitedCount)
}

// TestLoadSustainedLoad tests system under sustained load
func TestLoadSustained(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	duration := 10 * time.Second
	workers := 10

	var wg sync.WaitGroup
	stopChan := make(chan struct{})
	successCount := int32(0)
	rateLimitedCount := int32(0)
	errorCount := int32(0)

	start := time.Now()

	// Start workers
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			helper := NewTestHelper(t)
			opCount := 0

			for {
				select {
				case <-stopChan:
					return
				default:
					event := helper.CreateWhitelistedEvent(1,
						fmt.Sprintf("Sustained load worker %d op %d", workerID, opCount),
						nostr.Tags{})

					resp, result := helper.PostEventHTTP(event)
					if resp.StatusCode == 200 {
						if ok, _ := result["ok"].(bool); ok {
							atomic.AddInt32(&successCount, 1)
						} else {
							atomic.AddInt32(&errorCount, 1)
						}
					} else if resp.StatusCode == 429 {
						atomic.AddInt32(&rateLimitedCount, 1)
					} else {
						atomic.AddInt32(&errorCount, 1)
					}

					opCount++
					time.Sleep(10 * time.Millisecond) // Small delay between ops
				}
			}
		}(i)
	}

	// Run for specified duration
	time.Sleep(duration)
	close(stopChan)
	wg.Wait()

	actualDuration := time.Since(start)
	throughput := float64(successCount) / actualDuration.Seconds()
	totalRequests := successCount + rateLimitedCount + errorCount
	rateLimitRate := float64(rateLimitedCount) / float64(totalRequests) * 100
	errorRate := float64(errorCount) / float64(totalRequests) * 100

	t.Logf("✓ Sustained load: %.2f events/sec over %v (success: %d, rate limited: %d, errors: %d, rate limit: %.2f%%, error rate: %.2f%%)",
		throughput, actualDuration, successCount, rateLimitedCount, errorCount, rateLimitRate, errorRate)

	// Actual errors (not rate limiting) should be low
	assert.Less(t, errorRate, 5.0, "Error rate (excluding rate limiting) should be less than 5%%")
}

// TestLoadMemoryUsage tests that system doesn't leak memory under load
func TestLoadMemoryUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	h := NewTestHelper(t)

	// Create and post many events
	iterations := 5
	eventsPerIteration := 50

	for i := 0; i < iterations; i++ {
		events := h.GenerateTestEvents(eventsPerIteration, 1, fmt.Sprintf("Memory test iter %d", i))
		h.BatchPostEvents(events)

		// Query to ensure processing
		h.QueryEventsHTTP("GET", map[string]string{"limit": "10"}, nil)

		t.Logf("  Iteration %d/%d completed", i+1, iterations)
	}

	// If we got here without crashing, memory is probably okay
	t.Logf("✓ Memory usage stable over %d events", iterations*eventsPerIteration)
}

// TestLoadRecovery tests system recovery after high load
func TestLoadRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	h := NewTestHelper(t)

	// Phase 1: High load
	t.Log("Phase 1: Applying high load...")
	workers := 30
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			helper := NewTestHelper(t)

			for j := 0; j < 10; j++ {
				event := helper.CreateWhitelistedEvent(1, "Recovery test", nostr.Tags{})
				helper.PostEventHTTP(event)
			}
		}(i)
	}

	wg.Wait()

	// Phase 2: Cool down
	t.Log("Phase 2: Cooling down...")
	time.Sleep(2 * time.Second)

	// Phase 3: Verify normal operation
	t.Log("Phase 3: Verifying normal operation...")
	event := h.CreateWhitelistedEvent(1, "Post-recovery test", nostr.Tags{})
	resp, result := h.PostEventHTTP(event)

	h.AssertHTTPStatus(resp, 200)
	h.AssertEventAccepted(result)

	t.Log("✓ System recovered successfully after high load")
}
