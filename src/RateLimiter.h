#pragma once

#include <string>
#include <unordered_map>
#include <list>
#include <chrono>
#include <mutex>
#include <thread>
#include <atomic>
#include <condition_variable>

// Token bucket rate limiter with O(1) LRU eviction
// Implements per-IP, per-pubkey, and global rate limiting
// Thread-safe with background cleanup thread
class RateLimiter {
public:
    struct Config {
        bool enabled = true;
        uint64_t perIpPerMinute = 60;        // Max requests per IP per minute
        uint64_t perPubkeyPerMinute = 100;   // Max requests per pubkey per minute
        uint64_t globalPerMinute = 1000;     // Global max requests per minute
        double burstMultiplier = 2.0;        // Burst capacity (2.0 = allow 2x burst)
        uint64_t cleanupIntervalSeconds = 60; // Cleanup interval
        uint64_t maxIpBuckets = 10000;       // Max IP buckets to prevent memory exhaustion
        uint64_t maxPubkeyBuckets = 10000;   // Max pubkey buckets to prevent memory exhaustion
        uint64_t staleBucketSeconds = 60;    // Remove buckets unused for this duration
    };

    // Result of rate limit check - thread-safe, no shared state
    struct CheckResult {
        bool allowed = true;
        std::string reason;  // Only set if allowed == false
    };

    explicit RateLimiter(const Config& config);
    ~RateLimiter();

    // Check if request is allowed and consume a token if so
    // Returns CheckResult with allowed status and reason (thread-safe)
    CheckResult checkAndConsume(const std::string& ipAddress, const std::string& pubkey = "");

    // Manually trigger cleanup of old entries (called by background thread)
    void cleanup();

    // Stop the background cleanup thread
    void stop();

    // Get current statistics
    struct Stats {
        uint64_t totalRequests = 0;
        uint64_t rateLimitedRequests = 0;
        uint64_t ipBuckets = 0;
        uint64_t pubkeyBuckets = 0;
    };
    Stats getStats() const;

private:
    struct TokenBucket {
        double tokens;              // Current token count
        double capacity;            // Maximum tokens (burst capacity)
        double refillRate;          // Tokens added per second
        std::chrono::steady_clock::time_point lastRefill;
        uint64_t requestCount = 0;  // Total requests seen

        TokenBucket(double cap, double rate)
            : tokens(cap), capacity(cap), refillRate(rate),
              lastRefill(std::chrono::steady_clock::now()) {}

        bool consume(double amount = 1.0);
        void refill();
    };

    // LRU cache structure: list stores keys in LRU order (front = most recent)
    // Map stores bucket + iterator to list position for O(1) access and update
    struct LRUBucketCache {
        std::list<std::string> lruOrder;  // Front = most recently used
        std::unordered_map<std::string, std::pair<TokenBucket, std::list<std::string>::iterator>> buckets;

        TokenBucket* get(const std::string& key);
        TokenBucket* insert(const std::string& key, TokenBucket&& bucket);
        void touch(const std::string& key, std::list<std::string>::iterator it);
        void evictOldest();
        size_t size() const { return buckets.size(); }
        void eraseStale(double staleSeconds, size_t& removedCount);
    };

    // Returns empty string if allowed, otherwise returns the limit type that was exceeded
    std::string checkBucket(const std::string& key, LRUBucketCache& cache,
                            double capacity, double refillRate, const std::string& limitType,
                            uint64_t maxBuckets);

    // Background cleanup thread function
    void cleanupThreadFunc();

    Config config_;
    mutable std::mutex mutex_;

    // LRU caches for different rate limit types
    LRUBucketCache ipCache_;
    LRUBucketCache pubkeyCache_;
    TokenBucket globalBucket_;

    // Statistics (atomic for lock-free reads)
    std::atomic<uint64_t> totalRequests_{0};
    std::atomic<uint64_t> rateLimitedRequests_{0};

    // Background cleanup thread
    std::thread cleanupThread_;
    std::atomic<bool> running_{true};
    std::condition_variable cleanupCv_;
    std::mutex cleanupMutex_;
};
