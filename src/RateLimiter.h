#pragma once

#include <string>
#include <unordered_map>
#include <chrono>
#include <mutex>

// Token bucket rate limiter
// Implements per-IP, per-pubkey, and global rate limiting
class RateLimiter {
public:
    struct Config {
        bool enabled = true;
        uint64_t perIpPerMinute = 60;        // Max requests per IP per minute
        uint64_t perPubkeyPerMinute = 100;   // Max requests per pubkey per minute
        uint64_t globalPerMinute = 1000;     // Global max requests per minute
        double burstMultiplier = 2.0;        // Burst capacity (2.0 = allow 2x burst)
        uint64_t cleanupIntervalSeconds = 60; // Cleanup interval
    };

    explicit RateLimiter(const Config& config);
    ~RateLimiter() = default;

    // Check if request is allowed and consume a token if so
    // Returns true if allowed, false if rate limited
    bool checkAndConsume(const std::string& ipAddress, const std::string& pubkey = "");

    // Get the reason for rate limiting (call after checkAndConsume returns false)
    std::string getRateLimitReason() const { return lastReason; }

    // Manually trigger cleanup of old entries
    void cleanup();

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

    bool checkBucket(const std::string& key,
                     std::unordered_map<std::string, TokenBucket>& buckets,
                     double capacity, double refillRate, const std::string& limitType);

    Config config_;
    mutable std::mutex mutex_;

    // Separate maps for different rate limit types
    std::unordered_map<std::string, TokenBucket> ipBuckets_;
    std::unordered_map<std::string, TokenBucket> pubkeyBuckets_;
    TokenBucket globalBucket_;

    // Statistics
    mutable uint64_t totalRequests_ = 0;
    mutable uint64_t rateLimitedRequests_ = 0;
    std::chrono::steady_clock::time_point lastCleanup_;

    // Last rate limit reason (for error messages)
    mutable std::string lastReason;
};
