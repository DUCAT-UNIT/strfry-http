#pragma once

#include <string>
#include <atomic>
#include <mutex>
#include <map>
#include <sstream>

// Simple Prometheus metrics collector for HTTP server
// Thread-safe: uses mutex for all operations to ensure consistency
// Atomics are used only for simple counters that don't need map access
class HttpMetrics {
public:
    // HTTP method enum to avoid string allocation per request
    enum class Method : uint8_t {
        GET = 0,
        POST = 1,
        OPTIONS = 2,
        OTHER = 3
    };

    // Endpoint enum to avoid string allocation for duration tracking
    enum class Endpoint : uint8_t {
        API_QUOTES = 0,
        API_QUERY = 1,
        HEALTH = 2,
        METRICS = 3,
        OTHER = 4
    };

    HttpMetrics() {
        reset();
    }

    // Convert string method to enum (avoid allocation in hot path)
    static Method methodFromString(const std::string& method) {
        if (method == "GET") return Method::GET;
        if (method == "POST") return Method::POST;
        if (method == "OPTIONS") return Method::OPTIONS;
        return Method::OTHER;
    }

    // Convert enum to string for output
    static const char* methodToString(Method m) {
        switch (m) {
            case Method::GET: return "GET";
            case Method::POST: return "POST";
            case Method::OPTIONS: return "OPTIONS";
            default: return "OTHER";
        }
    }

    // Convert string endpoint to enum
    static Endpoint endpointFromString(const std::string& endpoint) {
        if (endpoint == "/api/quotes") return Endpoint::API_QUOTES;
        if (endpoint == "/api/query") return Endpoint::API_QUERY;
        if (endpoint == "/health") return Endpoint::HEALTH;
        if (endpoint == "/metrics") return Endpoint::METRICS;
        return Endpoint::OTHER;
    }

    // Convert endpoint enum to string for output
    static const char* endpointToString(Endpoint e) {
        switch (e) {
            case Endpoint::API_QUOTES: return "/api/quotes";
            case Endpoint::API_QUERY: return "/api/query";
            case Endpoint::HEALTH: return "/health";
            case Endpoint::METRICS: return "/metrics";
            default: return "/other";
        }
    }

    // Request counting - uses enum key to avoid string allocation
    void incrementRequestTotal(const std::string& method, int statusCode) {
        std::lock_guard<std::mutex> lock(mutex_);
        RequestKey key{methodFromString(method), statusCode};
        requestCounts_[key]++;
        totalRequests_++;
    }

    // Rate limiting - simple atomic counters (no map access needed)
    void incrementRateLimitHits() {
        rateLimitHits_.fetch_add(1, std::memory_order_relaxed);
    }

    void incrementRateLimitBlocks() {
        rateLimitBlocks_.fetch_add(1, std::memory_order_relaxed);
    }

    // Request duration tracking (in milliseconds) - uses enum to avoid allocation
    void observeRequestDuration(const std::string& endpoint, uint64_t durationMs) {
        std::lock_guard<std::mutex> lock(mutex_);
        auto& hist = requestDurations_[endpointFromString(endpoint)];
        hist.count++;
        hist.sum += durationMs;

        // Update buckets (10ms, 50ms, 100ms, 500ms, 1000ms, 5000ms)
        if (durationMs <= 10) hist.le_10++;
        if (durationMs <= 50) hist.le_50++;
        if (durationMs <= 100) hist.le_100++;
        if (durationMs <= 500) hist.le_500++;
        if (durationMs <= 1000) hist.le_1000++;
        if (durationMs <= 5000) hist.le_5000++;
    }

    // Event processing - simple atomic counters
    void incrementEventsAccepted() {
        eventsAccepted_.fetch_add(1, std::memory_order_relaxed);
    }

    void incrementEventsRejected() {
        eventsRejected_.fetch_add(1, std::memory_order_relaxed);
    }

    // Query metrics - requires mutex for struct access
    void observeQueryResults(uint64_t resultCount) {
        std::lock_guard<std::mutex> lock(mutex_);
        queryResultCounts_.count++;
        queryResultCounts_.sum += resultCount;
    }

    // Generate Prometheus metrics output
    std::string renderPrometheus() {
        std::lock_guard<std::mutex> lock(mutex_);
        std::stringstream ss;

        // Help text and type declarations
        ss << "# HELP http_requests_total Total number of HTTP requests\n";
        ss << "# TYPE http_requests_total counter\n";
        for (const auto& [key, count] : requestCounts_) {
            ss << "http_requests_total{method=\"" << methodToString(key.method)
               << "\",status=\"" << key.statusCode << "\"} " << count << "\n";
        }
        ss << "http_requests_total_all " << totalRequests_ << "\n";

        ss << "\n# HELP http_request_duration_milliseconds HTTP request duration\n";
        ss << "# TYPE http_request_duration_milliseconds histogram\n";
        for (const auto& [endpoint, hist] : requestDurations_) {
            const char* endpointStr = endpointToString(endpoint);
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpointStr << "\",le=\"10\"} " << hist.le_10 << "\n";
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpointStr << "\",le=\"50\"} " << hist.le_50 << "\n";
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpointStr << "\",le=\"100\"} " << hist.le_100 << "\n";
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpointStr << "\",le=\"500\"} " << hist.le_500 << "\n";
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpointStr << "\",le=\"1000\"} " << hist.le_1000 << "\n";
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpointStr << "\",le=\"5000\"} " << hist.le_5000 << "\n";
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpointStr << "\",le=\"+Inf\"} " << hist.count << "\n";
            ss << "http_request_duration_milliseconds_sum{endpoint=\"" << endpointStr << "\"} " << hist.sum << "\n";
            ss << "http_request_duration_milliseconds_count{endpoint=\"" << endpointStr << "\"} " << hist.count << "\n";
        }

        // Load atomic values once for consistent snapshot
        uint64_t rateLimitHitsSnapshot = rateLimitHits_.load(std::memory_order_relaxed);
        uint64_t rateLimitBlocksSnapshot = rateLimitBlocks_.load(std::memory_order_relaxed);
        uint64_t eventsAcceptedSnapshot = eventsAccepted_.load(std::memory_order_relaxed);
        uint64_t eventsRejectedSnapshot = eventsRejected_.load(std::memory_order_relaxed);

        ss << "\n# HELP http_rate_limit_hits_total Total rate limit checks\n";
        ss << "# TYPE http_rate_limit_hits_total counter\n";
        ss << "http_rate_limit_hits_total " << rateLimitHitsSnapshot << "\n";

        ss << "\n# HELP http_rate_limit_blocks_total Total rate limit blocks\n";
        ss << "# TYPE http_rate_limit_blocks_total counter\n";
        ss << "http_rate_limit_blocks_total " << rateLimitBlocksSnapshot << "\n";

        ss << "\n# HELP nostr_events_accepted_total Total events accepted\n";
        ss << "# TYPE nostr_events_accepted_total counter\n";
        ss << "nostr_events_accepted_total " << eventsAcceptedSnapshot << "\n";

        ss << "\n# HELP nostr_events_rejected_total Total events rejected\n";
        ss << "# TYPE nostr_events_rejected_total counter\n";
        ss << "nostr_events_rejected_total " << eventsRejectedSnapshot << "\n";

        ss << "\n# HELP nostr_query_results Query result counts\n";
        ss << "# TYPE nostr_query_results summary\n";
        ss << "nostr_query_results_sum " << queryResultCounts_.sum << "\n";
        ss << "nostr_query_results_count " << queryResultCounts_.count << "\n";

        return ss.str();
    }

    void reset() {
        std::lock_guard<std::mutex> lock(mutex_);
        requestCounts_.clear();
        requestDurations_.clear();
        totalRequests_ = 0;
        rateLimitHits_.store(0, std::memory_order_relaxed);
        rateLimitBlocks_.store(0, std::memory_order_relaxed);
        eventsAccepted_.store(0, std::memory_order_relaxed);
        eventsRejected_.store(0, std::memory_order_relaxed);
        queryResultCounts_ = SummaryData{};
    }

private:
    struct HistogramData {
        uint64_t count = 0;
        uint64_t sum = 0;
        uint64_t le_10 = 0;
        uint64_t le_50 = 0;
        uint64_t le_100 = 0;
        uint64_t le_500 = 0;
        uint64_t le_1000 = 0;
        uint64_t le_5000 = 0;
    };

    struct SummaryData {
        uint64_t count = 0;
        uint64_t sum = 0;
    };

    // Struct key for request counts using enum to avoid string allocation
    struct RequestKey {
        Method method;
        int statusCode;

        bool operator<(const RequestKey& other) const {
            if (method != other.method) return static_cast<int>(method) < static_cast<int>(other.method);
            return statusCode < other.statusCode;
        }
    };

    // Mutex protects map-based data structures
    std::mutex mutex_;
    std::map<RequestKey, uint64_t> requestCounts_;
    std::map<Endpoint, HistogramData> requestDurations_;  // Using enum key to avoid allocation
    uint64_t totalRequests_ = 0;  // Protected by mutex (used with requestCounts_)
    SummaryData queryResultCounts_;  // Protected by mutex

    // Standalone atomic counters - don't require map access
    std::atomic<uint64_t> rateLimitHits_{0};
    std::atomic<uint64_t> rateLimitBlocks_{0};
    std::atomic<uint64_t> eventsAccepted_{0};
    std::atomic<uint64_t> eventsRejected_{0};
};
