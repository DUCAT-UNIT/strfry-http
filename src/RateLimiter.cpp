#include "RateLimiter.h"
#include <algorithm>

RateLimiter::RateLimiter(const Config& config)
    : config_(config),
      globalBucket_(
          config.globalPerMinute * config.burstMultiplier,
          config.globalPerMinute / 60.0  // Convert per-minute to per-second
      ),
      lastCleanup_(std::chrono::steady_clock::now())
{
}

bool RateLimiter::TokenBucket::consume(double amount) {
    refill();

    if (tokens >= amount) {
        tokens -= amount;
        requestCount++;
        return true;
    }
    return false;
}

void RateLimiter::TokenBucket::refill() {
    auto now = std::chrono::steady_clock::now();
    auto elapsed = std::chrono::duration<double>(now - lastRefill).count();

    // Add tokens based on elapsed time
    tokens = std::min(capacity, tokens + (elapsed * refillRate));
    lastRefill = now;
}

bool RateLimiter::checkBucket(const std::string& key,
                               std::unordered_map<std::string, TokenBucket>& buckets,
                               double capacity, double refillRate,
                               const std::string& limitType) {
    auto it = buckets.find(key);

    if (it == buckets.end()) {
        // Create new bucket
        auto result = buckets.emplace(key, TokenBucket(capacity, refillRate));
        it = result.first;
    }

    if (!it->second.consume()) {
        lastReason = "rate_limit_exceeded: " + limitType;
        return false;
    }

    return true;
}

bool RateLimiter::checkAndConsume(const std::string& ipAddress, const std::string& pubkey) {
    if (!config_.enabled) {
        return true;
    }

    std::lock_guard<std::mutex> lock(mutex_);

    totalRequests_++;
    lastReason.clear();

    // Check global rate limit first (most restrictive)
    if (config_.globalPerMinute > 0) {
        if (!globalBucket_.consume()) {
            lastReason = "rate_limit_exceeded: global";
            rateLimitedRequests_++;
            return false;
        }
    }

    // Check IP-based rate limit
    if (config_.perIpPerMinute > 0 && !ipAddress.empty()) {
        double ipCapacity = config_.perIpPerMinute * config_.burstMultiplier;
        double ipRefillRate = config_.perIpPerMinute / 60.0;

        if (!checkBucket(ipAddress, ipBuckets_, ipCapacity, ipRefillRate, "per_ip")) {
            rateLimitedRequests_++;
            return false;
        }
    }

    // Check pubkey-based rate limit
    if (config_.perPubkeyPerMinute > 0 && !pubkey.empty()) {
        double pubkeyCapacity = config_.perPubkeyPerMinute * config_.burstMultiplier;
        double pubkeyRefillRate = config_.perPubkeyPerMinute / 60.0;

        if (!checkBucket(pubkey, pubkeyBuckets_, pubkeyCapacity, pubkeyRefillRate, "per_pubkey")) {
            rateLimitedRequests_++;
            return false;
        }
    }

    // Periodic cleanup
    auto now = std::chrono::steady_clock::now();
    auto timeSinceCleanup = std::chrono::duration<double>(now - lastCleanup_).count();
    if (timeSinceCleanup >= config_.cleanupIntervalSeconds) {
        cleanup();
        lastCleanup_ = now;
    }

    return true;
}

void RateLimiter::cleanup() {
    // Remove buckets that are full and haven't been used recently
    auto now = std::chrono::steady_clock::now();

    auto cleanupBuckets = [&now](auto& buckets) {
        for (auto it = buckets.begin(); it != buckets.end();) {
            auto& bucket = it->second;
            bucket.refill();

            // If bucket is at full capacity and hasn't been used in 2+ minutes, remove it
            auto timeSinceRefill = std::chrono::duration<double>(now - bucket.lastRefill).count();
            if (bucket.tokens >= bucket.capacity * 0.99 && timeSinceRefill > 120.0) {
                it = buckets.erase(it);
            } else {
                ++it;
            }
        }
    };

    cleanupBuckets(ipBuckets_);
    cleanupBuckets(pubkeyBuckets_);
}

RateLimiter::Stats RateLimiter::getStats() const {
    std::lock_guard<std::mutex> lock(mutex_);

    Stats stats;
    stats.totalRequests = totalRequests_;
    stats.rateLimitedRequests = rateLimitedRequests_;
    stats.ipBuckets = ipBuckets_.size();
    stats.pubkeyBuckets = pubkeyBuckets_.size();

    return stats;
}
