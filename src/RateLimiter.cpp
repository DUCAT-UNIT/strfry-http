#include "RateLimiter.h"
#include "golpe.h"
#include <algorithm>

RateLimiter::RateLimiter(const Config& config)
    : config_(config),
      globalBucket_(
          config.globalPerMinute * config.burstMultiplier,
          config.globalPerMinute / 60.0  // Convert per-minute to per-second
      )
{
    // Start background cleanup thread
    if (config_.enabled && config_.cleanupIntervalSeconds > 0) {
        cleanupThread_ = std::thread(&RateLimiter::cleanupThreadFunc, this);
    }
}

RateLimiter::~RateLimiter() {
    stop();
}

void RateLimiter::stop() {
    if (running_.exchange(false)) {
        cleanupCv_.notify_all();
        if (cleanupThread_.joinable()) {
            cleanupThread_.join();
        }
    }
}

void RateLimiter::cleanupThreadFunc() {
    while (running_) {
        try {
            std::unique_lock<std::mutex> lock(cleanupMutex_);

            // Wait for cleanup interval or until stopped
            cleanupCv_.wait_for(lock, std::chrono::seconds(config_.cleanupIntervalSeconds), [this] {
                return !running_.load();
            });

            if (!running_) break;

            // Perform cleanup under the main mutex
            {
                std::lock_guard<std::mutex> dataLock(mutex_);
                cleanup();
            }
        } catch (const std::exception& e) {
            // Log error but continue running - cleanup is best-effort
            LW << "RateLimiter cleanup thread error: " << e.what();
            // Sleep briefly to avoid tight loop on persistent errors
            std::this_thread::sleep_for(std::chrono::seconds(1));
        } catch (...) {
            // Catch any other exceptions to prevent thread termination
            LE << "RateLimiter cleanup thread unknown error";
            std::this_thread::sleep_for(std::chrono::seconds(1));
        }
    }
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

// LRU Cache implementation - O(1) operations

RateLimiter::TokenBucket* RateLimiter::LRUBucketCache::get(const std::string& key) {
    auto it = buckets.find(key);
    if (it == buckets.end()) {
        return nullptr;
    }
    // Move to front of LRU list (most recently used)
    touch(key, it->second.second);
    return &it->second.first;
}

RateLimiter::TokenBucket* RateLimiter::LRUBucketCache::insert(const std::string& key, TokenBucket&& bucket) {
    // Add to front of LRU list
    lruOrder.push_front(key);
    auto listIt = lruOrder.begin();

    // Insert into map with iterator to list position
    auto result = buckets.emplace(key, std::make_pair(std::move(bucket), listIt));
    return &result.first->second.first;
}

void RateLimiter::LRUBucketCache::touch(const std::string& key, std::list<std::string>::iterator it) {
    // Move to front of LRU list - O(1) with splice
    lruOrder.splice(lruOrder.begin(), lruOrder, it);
}

void RateLimiter::LRUBucketCache::evictOldest() {
    if (lruOrder.empty()) return;

    // Back of list is least recently used - O(1)
    const std::string& oldestKey = lruOrder.back();
    buckets.erase(oldestKey);
    lruOrder.pop_back();
}

void RateLimiter::LRUBucketCache::eraseStale(double staleSeconds, size_t& removedCount) {
    auto now = std::chrono::steady_clock::now();

    // Iterate from back (oldest) and remove stale buckets
    // Stop when we hit a non-stale bucket since list is in LRU order
    while (!lruOrder.empty()) {
        const std::string& key = lruOrder.back();
        auto it = buckets.find(key);
        if (it == buckets.end()) {
            // Shouldn't happen, but clean up anyway
            lruOrder.pop_back();
            continue;
        }

        auto& bucket = it->second.first;
        bucket.refill();

        auto timeSinceRefill = std::chrono::duration<double>(now - bucket.lastRefill).count();
        bool isStale = bucket.tokens >= bucket.capacity * 0.95 && timeSinceRefill > staleSeconds;

        if (isStale) {
            buckets.erase(it);
            lruOrder.pop_back();
            removedCount++;
        } else {
            // List is in LRU order, so if this one isn't stale, stop
            // (more recently used items are even less likely to be stale)
            break;
        }
    }
}

std::string RateLimiter::checkBucket(const std::string& key, LRUBucketCache& cache,
                                      double capacity, double refillRate,
                                      const std::string& limitType,
                                      uint64_t maxBuckets) {
    // Try to get existing bucket - O(1)
    TokenBucket* bucket = cache.get(key);

    if (!bucket) {
        // Check if we've hit the max bucket limit
        if (cache.size() >= maxBuckets) {
            // Use LRU eviction to make room for new client - O(1)
            cache.evictOldest();
        }
        // Create new bucket - O(1)
        bucket = cache.insert(key, TokenBucket(capacity, refillRate));
    }

    if (!bucket->consume()) {
        return "rate_limit_exceeded: " + limitType;
    }

    return "";  // Empty string means allowed
}

RateLimiter::CheckResult RateLimiter::checkAndConsume(const std::string& ipAddress, const std::string& pubkey) {
    CheckResult result;

    if (!config_.enabled) {
        return result;  // allowed = true by default
    }

    std::lock_guard<std::mutex> lock(mutex_);

    totalRequests_.fetch_add(1, std::memory_order_relaxed);

    // Check global rate limit first (most restrictive)
    if (config_.globalPerMinute > 0) {
        if (!globalBucket_.consume()) {
            result.allowed = false;
            result.reason = "rate_limit_exceeded: global";
            rateLimitedRequests_.fetch_add(1, std::memory_order_relaxed);
            return result;
        }
    }

    // Check IP-based rate limit - O(1) with LRU cache
    if (config_.perIpPerMinute > 0 && !ipAddress.empty()) {
        double ipCapacity = config_.perIpPerMinute * config_.burstMultiplier;
        double ipRefillRate = config_.perIpPerMinute / 60.0;

        std::string reason = checkBucket(ipAddress, ipCache_, ipCapacity, ipRefillRate,
                                         "per_ip", config_.maxIpBuckets);
        if (!reason.empty()) {
            result.allowed = false;
            result.reason = reason;
            rateLimitedRequests_.fetch_add(1, std::memory_order_relaxed);
            return result;
        }
    }

    // Check pubkey-based rate limit - O(1) with LRU cache
    if (config_.perPubkeyPerMinute > 0 && !pubkey.empty()) {
        double pubkeyCapacity = config_.perPubkeyPerMinute * config_.burstMultiplier;
        double pubkeyRefillRate = config_.perPubkeyPerMinute / 60.0;

        std::string reason = checkBucket(pubkey, pubkeyCache_, pubkeyCapacity, pubkeyRefillRate,
                                         "per_pubkey", config_.maxPubkeyBuckets);
        if (!reason.empty()) {
            result.allowed = false;
            result.reason = reason;
            rateLimitedRequests_.fetch_add(1, std::memory_order_relaxed);
            return result;
        }
    }

    return result;  // allowed = true
}

void RateLimiter::cleanup() {
    // Note: Must be called while holding mutex_
    // Remove stale buckets from back of LRU list (least recently used)
    double staleSeconds = static_cast<double>(config_.staleBucketSeconds);
    size_t ipRemoved = 0, pubkeyRemoved = 0;

    ipCache_.eraseStale(staleSeconds, ipRemoved);
    pubkeyCache_.eraseStale(staleSeconds, pubkeyRemoved);

    if (ipRemoved > 0 || pubkeyRemoved > 0) {
        LI << "RateLimiter cleanup: removed " << ipRemoved << " IP buckets, "
           << pubkeyRemoved << " pubkey buckets (remaining: "
           << ipCache_.size() << " IP, " << pubkeyCache_.size() << " pubkey)";
    }
}

RateLimiter::Stats RateLimiter::getStats() const {
    std::lock_guard<std::mutex> lock(mutex_);

    Stats stats;
    stats.totalRequests = totalRequests_.load(std::memory_order_relaxed);
    stats.rateLimitedRequests = rateLimitedRequests_.load(std::memory_order_relaxed);
    stats.ipBuckets = ipCache_.size();
    stats.pubkeyBuckets = pubkeyCache_.size();

    return stats;
}
