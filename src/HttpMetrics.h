#pragma once

#include <string>
#include <atomic>
#include <mutex>
#include <map>
#include <sstream>

// Simple Prometheus metrics collector for HTTP server
// Thread-safe counters and histograms
class HttpMetrics {
public:
    HttpMetrics() {
        reset();
    }

    // Request counting
    void incrementRequestTotal(const std::string& method, int statusCode) {
        std::lock_guard<std::mutex> lock(mutex_);
        std::string key = method + "_" + std::to_string(statusCode);
        requestCounts_[key]++;
        totalRequests_++;
    }

    // Rate limiting
    void incrementRateLimitHits() {
        rateLimitHits_++;
    }

    void incrementRateLimitBlocks() {
        rateLimitBlocks_++;
    }

    // Request duration tracking (in milliseconds)
    void observeRequestDuration(const std::string& endpoint, uint64_t durationMs) {
        std::lock_guard<std::mutex> lock(mutex_);
        auto& hist = requestDurations_[endpoint];
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

    // Event processing
    void incrementEventsAccepted() {
        eventsAccepted_++;
    }

    void incrementEventsRejected() {
        eventsRejected_++;
    }

    // Query metrics
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
            size_t pos = key.find('_');
            std::string method = key.substr(0, pos);
            std::string status = key.substr(pos + 1);
            ss << "http_requests_total{method=\"" << method
               << "\",status=\"" << status << "\"} " << count << "\n";
        }
        ss << "http_requests_total_all " << totalRequests_ << "\n";

        ss << "\n# HELP http_request_duration_milliseconds HTTP request duration\n";
        ss << "# TYPE http_request_duration_milliseconds histogram\n";
        for (const auto& [endpoint, hist] : requestDurations_) {
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpoint << "\",le=\"10\"} " << hist.le_10 << "\n";
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpoint << "\",le=\"50\"} " << hist.le_50 << "\n";
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpoint << "\",le=\"100\"} " << hist.le_100 << "\n";
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpoint << "\",le=\"500\"} " << hist.le_500 << "\n";
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpoint << "\",le=\"1000\"} " << hist.le_1000 << "\n";
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpoint << "\",le=\"5000\"} " << hist.le_5000 << "\n";
            ss << "http_request_duration_milliseconds_bucket{endpoint=\"" << endpoint << "\",le=\"+Inf\"} " << hist.count << "\n";
            ss << "http_request_duration_milliseconds_sum{endpoint=\"" << endpoint << "\"} " << hist.sum << "\n";
            ss << "http_request_duration_milliseconds_count{endpoint=\"" << endpoint << "\"} " << hist.count << "\n";
        }

        ss << "\n# HELP http_rate_limit_hits_total Total rate limit checks\n";
        ss << "# TYPE http_rate_limit_hits_total counter\n";
        ss << "http_rate_limit_hits_total " << rateLimitHits_ << "\n";

        ss << "\n# HELP http_rate_limit_blocks_total Total rate limit blocks\n";
        ss << "# TYPE http_rate_limit_blocks_total counter\n";
        ss << "http_rate_limit_blocks_total " << rateLimitBlocks_ << "\n";

        ss << "\n# HELP nostr_events_accepted_total Total events accepted\n";
        ss << "# TYPE nostr_events_accepted_total counter\n";
        ss << "nostr_events_accepted_total " << eventsAccepted_ << "\n";

        ss << "\n# HELP nostr_events_rejected_total Total events rejected\n";
        ss << "# TYPE nostr_events_rejected_total counter\n";
        ss << "nostr_events_rejected_total " << eventsRejected_ << "\n";

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
        rateLimitHits_ = 0;
        rateLimitBlocks_ = 0;
        eventsAccepted_ = 0;
        eventsRejected_ = 0;
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

    std::mutex mutex_;
    std::map<std::string, uint64_t> requestCounts_;
    std::map<std::string, HistogramData> requestDurations_;
    std::atomic<uint64_t> totalRequests_{0};
    std::atomic<uint64_t> rateLimitHits_{0};
    std::atomic<uint64_t> rateLimitBlocks_{0};
    std::atomic<uint64_t> eventsAccepted_{0};
    std::atomic<uint64_t> eventsRejected_{0};
    SummaryData queryResultCounts_;
};
