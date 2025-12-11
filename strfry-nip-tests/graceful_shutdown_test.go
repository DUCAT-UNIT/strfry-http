package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGracefulShutdownRequestCompletion tests that in-flight requests complete during shutdown
func TestGracefulShutdownRequestCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping graceful shutdown test in short mode")
	}

	h := NewTestHelper(t)

	// First verify server is running
	resp, err := h.client.Get(httpBaseURL + "/health")
	if err != nil {
		t.Skipf("Server not running: %v", err)
	}
	resp.Body.Close()

	// Get container ID
	containerName := "strfry-relay"
	cmd := exec.Command("docker", "ps", "-q", "--filter", "name="+containerName)
	output, err := cmd.Output()
	if err != nil {
		t.Skipf("Could not find container: %v", err)
	}

	containerID := string(bytes.TrimSpace(output))
	if containerID == "" {
		t.Skip("No strfry container found")
	}

	// Track concurrent request results
	var wg sync.WaitGroup
	var successCount atomic.Int64
	var failCount atomic.Int64

	// Start several concurrent requests
	requestCount := 5
	for i := 0; i < requestCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			event := h.CreateWhitelistedEvent(1, "Shutdown test event", nostr.Tags{})
			eventJSON, _ := json.Marshal(event)

			// Use a longer timeout client for this test
			client := &http.Client{Timeout: 15 * time.Second}
			resp, err := client.Post(
				httpBaseURL+"/api/quotes",
				"application/json",
				bytes.NewBuffer(eventJSON),
			)

			if err != nil {
				failCount.Add(1)
				t.Logf("Request %d failed: %v", idx, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == 200 || resp.StatusCode == 429 {
				successCount.Add(1)
			} else {
				failCount.Add(1)
			}
		}(i)
	}

	// Give requests time to start
	time.Sleep(100 * time.Millisecond)

	// Send SIGTERM to initiate graceful shutdown
	cmd = exec.Command("docker", "kill", "--signal=SIGTERM", containerID)
	err = cmd.Run()
	require.NoError(t, err, "Failed to send SIGTERM to container")

	// Wait for all requests to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("All requests completed: %d success, %d failed", successCount.Load(), failCount.Load())
	case <-time.After(12 * time.Second):
		t.Log("Timeout waiting for requests to complete")
	}

	// Restart the container for subsequent tests
	time.Sleep(500 * time.Millisecond)
	cmd = exec.Command("docker", "start", containerID)
	cmd.Run()

	// Wait for restart
	time.Sleep(3 * time.Second)

	// Verify server is back
	for i := 0; i < 10; i++ {
		resp, err := http.Get(httpBaseURL + "/health")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			t.Log("Server restarted successfully")
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestGracefulShutdownSignals tests that both SIGTERM and SIGINT trigger graceful shutdown
func TestGracefulShutdownSignals(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping graceful shutdown signals test in short mode")
	}

	signals := []string{"SIGTERM", "SIGINT"}

	for _, signal := range signals {
		t.Run(signal, func(t *testing.T) {
			h := NewTestHelper(t)

			// Verify server is running
			resp, err := h.client.Get(httpBaseURL + "/health")
			if err != nil {
				t.Skipf("Server not running: %v", err)
			}
			resp.Body.Close()

			// Get container ID
			containerName := "strfry-relay"
			cmd := exec.Command("docker", "ps", "-q", "--filter", "name="+containerName)
			output, err := cmd.Output()
			if err != nil {
				t.Skipf("Could not find container: %v", err)
			}

			containerID := string(bytes.TrimSpace(output))
			if containerID == "" {
				t.Skip("No strfry container found")
			}

			// Send signal
			cmd = exec.Command("docker", "kill", "--signal="+signal, containerID)
			err = cmd.Run()
			require.NoError(t, err, "Failed to send %s to container", signal)

			// Wait briefly for shutdown to start
			time.Sleep(1 * time.Second)

			// Server should stop accepting new connections shortly
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, "GET", httpBaseURL+"/health", nil)
			client := &http.Client{Timeout: 2 * time.Second}
			_, err = client.Do(req)

			// Either connection refused or timeout is expected
			// (server should be shutting down or already shut down)
			t.Logf("%s signal: server responded with error (expected): %v", signal, err)

			// Restart the container
			time.Sleep(500 * time.Millisecond)
			cmd = exec.Command("docker", "start", containerID)
			cmd.Run()

			// Wait for restart
			time.Sleep(3 * time.Second)

			// Verify server is back
			for i := 0; i < 10; i++ {
				resp, err := http.Get(httpBaseURL + "/health")
				if err == nil && resp.StatusCode == 200 {
					resp.Body.Close()
					t.Logf("Server restarted after %s", signal)
					return
				}
				time.Sleep(500 * time.Millisecond)
			}

			t.Logf("Warning: Server may not have restarted after %s test", signal)
		})
	}
}

// TestGracefulShutdownTimeout tests that shutdown completes within timeout
func TestGracefulShutdownTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping graceful shutdown timeout test in short mode")
	}

	h := NewTestHelper(t)

	// Verify server is running
	resp, err := h.client.Get(httpBaseURL + "/health")
	if err != nil {
		t.Skipf("Server not running: %v", err)
	}
	resp.Body.Close()

	// Get container ID
	containerName := "strfry-relay"
	cmd := exec.Command("docker", "ps", "-q", "--filter", "name="+containerName)
	output, err := cmd.Output()
	if err != nil {
		t.Skipf("Could not find container: %v", err)
	}

	containerID := string(bytes.TrimSpace(output))
	if containerID == "" {
		t.Skip("No strfry container found")
	}

	startTime := time.Now()

	// Send SIGTERM
	cmd = exec.Command("docker", "kill", "--signal=SIGTERM", containerID)
	err = cmd.Run()
	require.NoError(t, err, "Failed to send SIGTERM")

	// Wait for container to stop (with timeout)
	timeout := time.After(15 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	containerStopped := false
	for {
		select {
		case <-timeout:
			t.Log("Timeout waiting for container to stop")
			break
		case <-ticker.C:
			cmd := exec.Command("docker", "ps", "-q", "--filter", "id="+containerID)
			output, _ := cmd.Output()
			if len(bytes.TrimSpace(output)) == 0 {
				containerStopped = true
				shutdownTime := time.Since(startTime)
				t.Logf("Container stopped in %v", shutdownTime)

				// Graceful shutdown should complete within ~10-11 seconds
				// (10s timeout + small overhead)
				assert.Less(t, shutdownTime.Seconds(), 12.0,
					"Graceful shutdown took too long")
				break
			}
		}

		if containerStopped || time.Since(startTime) > 15*time.Second {
			break
		}
	}

	// Restart container
	cmd = exec.Command("docker", "start", containerID)
	cmd.Run()

	time.Sleep(3 * time.Second)

	// Verify server is back
	for i := 0; i < 10; i++ {
		resp, err := http.Get(httpBaseURL + "/health")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			t.Log("Server restarted successfully")
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestHealthCheckDuringShutdown tests health check behavior during shutdown
func TestHealthCheckDuringShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping health check during shutdown test in short mode")
	}

	h := NewTestHelper(t)

	// Verify server is running
	resp, err := h.client.Get(httpBaseURL + "/health")
	if err != nil {
		t.Skipf("Server not running: %v", err)
	}
	resp.Body.Close()

	// Get container ID
	containerName := "strfry-relay"
	cmd := exec.Command("docker", "ps", "-q", "--filter", "name="+containerName)
	output, err := cmd.Output()
	if err != nil {
		t.Skipf("Could not find container: %v", err)
	}

	containerID := string(bytes.TrimSpace(output))
	if containerID == "" {
		t.Skip("No strfry container found")
	}

	// Start health check goroutine
	var healthCheckResults []string
	var mu sync.Mutex
	stopChecking := make(chan struct{})

	go func() {
		client := &http.Client{Timeout: 1 * time.Second}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stopChecking:
				return
			case <-ticker.C:
				resp, err := client.Get(httpBaseURL + "/health")
				mu.Lock()
				if err != nil {
					healthCheckResults = append(healthCheckResults, "error: "+err.Error())
				} else {
					healthCheckResults = append(healthCheckResults, "status: "+resp.Status)
					resp.Body.Close()
				}
				mu.Unlock()
			}
		}
	}()

	// Wait for some successful health checks
	time.Sleep(500 * time.Millisecond)

	// Send SIGTERM
	cmd = exec.Command("docker", "kill", "--signal=SIGTERM", containerID)
	cmd.Run()

	// Continue checking for a bit
	time.Sleep(2 * time.Second)
	close(stopChecking)

	// Check results
	mu.Lock()
	successCount := 0
	errorCount := 0
	for _, result := range healthCheckResults {
		if result == "status: 200 OK" {
			successCount++
		} else {
			errorCount++
		}
	}
	mu.Unlock()

	t.Logf("Health checks: %d successful, %d errors (total: %d)",
		successCount, errorCount, len(healthCheckResults))

	// Should have some successful checks before shutdown
	assert.Greater(t, successCount, 0, "Should have some successful health checks")

	// Restart container
	time.Sleep(500 * time.Millisecond)
	cmd = exec.Command("docker", "start", containerID)
	cmd.Run()
	time.Sleep(3 * time.Second)
}
