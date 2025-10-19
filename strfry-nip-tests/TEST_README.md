# Strfry HTTP API Test Suite

Comprehensive test suite for the Strfry HTTP API implementation, covering HTTP endpoints, WebSocket compatibility, query filters, security, edge cases, and load testing.

## Overview

This test suite provides thorough validation of:
- ✅ HTTP REST API endpoints
- ✅ Event submission and retrieval
- ✅ Whitelist enforcement
- ✅ Query filters (ids, authors, kinds, time ranges, limits)
- ✅ Security headers and CORS
- ✅ Edge cases and error handling
- ✅ Concurrent operations
- ✅ Load and performance testing

## Test Organization

```
strfry-nip-tests/
├── helpers_test.go          # Common test utilities and helpers
├── http_api_test.go         # HTTP endpoint functionality tests
├── query_filters_test.go    # Query filter and search tests
├── edge_cases_test.go       # Edge cases and error handling
├── security_test.go         # Security and CORS validation
├── ratelimit_test.go        # Rate limiting tests
├── load_test.go             # Load and performance tests
├── nip_test.go              # Original NIP compliance tests
├── real-data_test.go        # Real-world data tests
├── rw_test.go               # Read/write consistency tests
└── TEST_README.md           # This file
```

## Prerequisites

```bash
# Install Go dependencies (from strfry-nip-tests directory)
go mod tidy

# Ensure Docker and Docker Compose are installed
docker --version
docker-compose --version
```

## Quick Start

### First Time Setup
```bash
# From the strfry root directory
cd /path/to/strfry

# Build and start the relay (first time only)
make test-setup
```

### Running Tests

**The Makefile automatically restarts the relay with fresh rate limit buckets before each test run!**

```bash
# From the strfry root directory
cd /path/to/strfry

# Run all tests (recommended)
make test

# Run tests with verbose output
make test-verbose

# Run quick tests only (skip load tests)
make test-short
```

### Run Tests by Category
```bash
# Security tests
make test-security

# Rate limit tests
make test-ratelimit

# Query tests
make test-query

# Load tests
make test-load
```

### Other Commands
```bash
# Docker management
make docker-build    # Build Docker image
make docker-up       # Start relay
make docker-down     # Stop relay
make docker-restart  # Restart with fresh state
make docker-logs     # View logs

# Testing
make test-coverage   # Generate coverage report
make test-clean      # Clean test artifacts

# Show all available commands
make help-test
```

### Manual Test Execution (Advanced)
```bash
# If you want to run tests manually without the Makefile:

# 1. Start relay with docker-compose
cd .. && docker-compose up -d

# 2. Wait for it to be ready
sleep 5

# 3. Run tests
cd strfry-nip-tests
go test -v -timeout 5m

# Run specific tests
go test -v -run TestQuery
go test -v -run TestSecurity
```

## Test Categories

### 1. HTTP API Tests (`http_api_test.go`)

**Core Functionality:**
- ✅ Health check endpoint
- ✅ Root endpoint information
- ✅ Event posting via HTTP
- ✅ Event retrieval by ID
- ✅ Whitelist enforcement
- ✅ Duplicate event handling

**Advanced Features:**
- ✅ Batch event posting
- ✅ Various event kinds (0, 1, 3, 5, 7, 1000, 10000, 30000)
- ✅ Events with tags (single, multiple, d-tags)
- ✅ Large content handling
- ✅ Timestamp validation
- ✅ Concurrent posting

**Example:**
```bash
go test -v -run TestHTTPPostEventSuccess
```

### 2. Query Filter Tests (`query_filters_test.go`)

**Filter Types:**
- ✅ Query by IDs (single and multiple)
- ✅ Query by authors (pubkeys)
- ✅ Query by kinds (event types)
- ✅ Time range queries (since/until)
- ✅ Limit parameter
- ✅ Combined filters

**Query Methods:**
- ✅ GET with query parameters
- ✅ POST with JSON body
- ✅ Complex filter combinations
- ✅ Empty result handling
- ✅ Invalid filter rejection

**Example:**
```bash
go test -v -run TestQueryByAuthors
go test -v -run TestQueryCombinedFilters
```

### 3. Edge Cases & Error Handling (`edge_cases_test.go`)

**Input Validation:**
- ✅ Malformed JSON
- ✅ Missing required fields
- ✅ Invalid signatures
- ✅ Invalid event IDs
- ✅ Empty content
- ✅ Very long tags
- ✅ Special characters (Unicode, emojis, control chars)

**HTTP Handling:**
- ✅ Invalid HTTP methods
- ✅ Wrong content types
- ✅ Extremely large payloads
- ✅ Negative kind values
- ✅ Invalid hex encoding
- ✅ Rapid fire requests

**Example:**
```bash
go test -v -run TestMalformedJSON
go test -v -run TestInvalidSignature
```

### 4. Security Tests (`security_test.go`)

**Security Headers:**
- ✅ X-Content-Type-Options: nosniff
- ✅ X-Frame-Options: DENY
- ✅ Content-Security-Policy
- ✅ X-XSS-Protection
- ✅ Referrer-Policy: no-referrer

**CORS Configuration:**
- ✅ Preflight requests (OPTIONS)
- ✅ CORS headers on actual requests
- ✅ Cross-origin support

**Security Features:**
- ✅ Whitelist bypass prevention
- ✅ Input validation (injection prevention)
- ✅ Authentication enforcement
- ✅ Signature validation
- ✅ Event size limits
- ✅ Privilege escalation prevention

**Example:**
```bash
go test -v -run TestSecurityHeaders
go test -v -run TestWhitelistSecurity
```

### 5. Rate Limiting Tests (`ratelimit_test.go`)

**Rate Limiting Features:**
- ✅ Per-IP rate limiting (60 req/min default)
- ✅ Per-pubkey rate limiting (100 req/min default)
- ✅ Global rate limiting (1000 req/min default)
- ✅ Burst capacity (2x multiplier)
- ✅ Token bucket algorithm
- ✅ Automatic recovery over time
- ✅ Health check exemption
- ✅ 429 status code with Retry-After header

**Tests:**
- ✅ Basic rate limiting enforcement
- ✅ Rate limit recovery after waiting
- ✅ Per-IP tracking
- ✅ Health check exemption
- ✅ Retry-After header validation

**Configuration:**
```
relay {
    ratelimit {
        enabled = true
        perIpPerMinute = 60
        perPubkeyPerMinute = 100
        globalPerMinute = 1000
        burstMultiplier = "2.0"
        cleanupIntervalSeconds = 60
    }
}
```

**Example:**
```bash
go test -v -run TestRateLimit
```

### 6. Load & Performance Tests (`load_test.go`)

**Performance Metrics:**
- ✅ Basic throughput (events/sec)
- ✅ Concurrent write performance
- ✅ Concurrent read performance
- ✅ Mixed workload (reads + writes)
- ✅ Large event handling
- ✅ Complex query performance
- ✅ Sustained load testing
- ✅ Memory stability
- ✅ Recovery after high load

**Note:** Load tests are skipped in short mode. Run explicitly:
```bash
go test -v -run TestLoad
```

**Example:**
```bash
# Run all load tests
go test -v -run TestLoad

# Run specific load test
go test -v -run TestLoadConcurrentWrites
```

## Test Helpers

The `helpers_test.go` file provides utilities for:

```go
// Create test helper
h := NewTestHelper(t)

// Create events
event := h.CreateWhitelistedEvent(1, "content", tags)
event := h.CreateNonWhitelistedEvent(1, "content", tags)

// HTTP operations
resp, result := h.PostEventHTTP(event)
resp, results := h.QueryEventsHTTP("GET", params, nil)
resp, event := h.GetEventByID(eventID)
resp, status := h.HealthCheck()

// WebSocket operations
conn := h.ConnectWebSocket()
accepted, message := h.SendEventWebSocket(conn, event, timeout)

// Assertions
h.AssertHTTPStatus(resp, 200)
h.AssertEventAccepted(result)
h.AssertEventRejected(result, "expected message")
h.AssertSecurityHeaders(resp)
h.AssertCORSHeaders(resp)

// Batch operations
events := h.GenerateTestEvents(10, kind, "prefix")
results := h.BatchPostEvents(events)
```

## Configuration

### Test Constants (`helpers_test.go`)
```go
const (
    relayURL      = "ws://localhost:7777"
    httpBaseURL   = "http://localhost:8080"
    whitelistedSk = "8ce73a2db5cbaf4b0ab3cabece9408e3b898c64474c0dbe27826c65d1180370e"
    whitelistedPk = "6b5008a293291c14effeb0e8b7c56a80ecb5ca7b801768e17ec93092be6c0621"
)
```

### Server Configuration
Ensure `strfry.conf` has:
```
relay {
    http {
        enabled = true
        port = 8080
        bind = "0.0.0.0"
        cors = true
        timeout = 5
    }

    whitelist {
        enabled = true
        pubkeys = "6b5008a293291c14effeb0e8b7c56a80ecb5ca7b801768e17ec93092be6c0621"
    }

    ratelimit {
        enabled = true
        perIpPerMinute = 60
        perPubkeyPerMinute = 100
        globalPerMinute = 1000
        burstMultiplier = "2.0"
        cleanupIntervalSeconds = 60
    }
}
```

## Test Results

### Expected Output
```
=== RUN   TestHTTPHealthCheck
    http_api_test.go:18: ✓ Health check endpoint working
--- PASS: TestHTTPHealthCheck (0.00s)

=== RUN   TestHTTPPostEventSuccess
    http_api_test.go:34: ✓ Successfully posted event: abc123...
--- PASS: TestHTTPPostEventSuccess (0.01s)

...

PASS
ok      github.com/zk-bits/strfry-nip-tests    7.543s
```

### Performance Benchmarks
Typical performance on standard hardware:
- **Throughput**: 50-100 events/sec (single-threaded)
- **Concurrent writes**: 200-500 events/sec (20 workers)
- **Concurrent reads**: 500-1000 queries/sec (30 workers)
- **Query latency**: < 100ms for most queries

## Troubleshooting

### Server Not Running
```bash
# Check if server is running
curl http://localhost:8080/health

# View server logs
docker logs <container-id>

# Restart server
docker stop <container-id>
docker start <container-id>
```

### Test Failures

**Whitelist Rejection:**
- Ensure whitelist is configured correctly
- Check that `relay.whitelist.enabled = true`
- Verify whitelisted pubkey matches test constant

**Connection Refused:**
- Ensure server is running on correct ports
- Check Docker port mappings: `-p 7777:7777 -p 8080:8080`

**Timeout Errors:**
- Increase timeout values in test helpers
- Check server load and performance
- Review server logs for bottlenecks

## CI/CD Integration

### GitHub Actions Example
```yaml
name: Test Suite
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Start Strfry
        run: |
          docker build -t strfry-http .
          docker run -d -p 7777:7777 -p 8080:8080 \
            -v $PWD/strfry-db:/app/strfry-db \
            -v $PWD/strfry.conf:/app/strfry.conf \
            strfry-http relay
          sleep 5

      - name: Run Tests
        run: |
          cd strfry-nip-tests
          go test -v -short

      - name: Run Load Tests
        run: |
          cd strfry-nip-tests
          go test -v -run TestLoad
```

## Adding New Tests

### Template
```go
func TestMyNewFeature(t *testing.T) {
    h := NewTestHelper(t)

    // Setup
    event := h.CreateWhitelistedEvent(1, "test content", nostr.Tags{})

    // Action
    resp, result := h.PostEventHTTP(event)

    // Assert
    h.AssertHTTPStatus(resp, 200)
    h.AssertEventAccepted(result)
    assert.Equal(t, event.ID, result["id"])

    t.Log("✓ Test passed")
}
```

## Coverage Goals

- ✅ HTTP API: 90%+ coverage
- ✅ Query Filters: 95%+ coverage
- ✅ Security: 85%+ coverage
- ✅ Edge Cases: 80%+ coverage
- ✅ Overall: 85%+ coverage

## Contributing

When adding tests:
1. Use descriptive test names
2. Include test documentation
3. Use test helpers for common operations
4. Add success log messages with ✓
5. Group related tests with t.Run()
6. Clean up resources (defer conn.Close())

## License

Same as parent project (GPLv3)
