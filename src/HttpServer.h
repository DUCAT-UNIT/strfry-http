#pragma once

#include <string>
#include <memory>
#include <functional>
#include <atomic>
#include <mutex>
#include <condition_variable>
#include "golpe.h"
#include "DBQuery.h"
#include "RateLimiter.h"
#include "HttpMetrics.h"
#include "RequestLogger.h"

namespace httplib {
    class Server;
    class Request;
    class Response;
}

class HttpServer {
public:
    // Default max body size: 1MB (sufficient for most Nostr events)
    static constexpr size_t DEFAULT_MAX_BODY_SIZE = 1024 * 1024;

    HttpServer(uint16_t port, lmdb::env& env, lmdb::dbi& dbi_dbById,
               const std::string &bindAddr = "127.0.0.1", bool enableCors = false,
               bool trustProxy = false, const std::string &corsOrigin = "*",
               size_t maxBodySize = DEFAULT_MAX_BODY_SIZE);
    ~HttpServer();

    void setEventProcessor(std::function<bool(const std::string&, std::string&)> processor);
    bool start();  // Returns false if server fails to bind
    void stop();
    void gracefulShutdown(int timeoutSeconds = 10);

private:
    std::unique_ptr<httplib::Server> server;
    uint16_t port;
    std::string bindAddr;
    bool enableCors;
    bool trustProxy;  // Whether to trust X-Forwarded-For and X-Real-IP headers
    std::string corsOrigin;  // Configurable CORS origin (default "*")

    // Thread-safe event processor with mutex protection
    std::function<bool(const std::string&, std::string&)> eventProcessor;
    mutable std::mutex eventProcessorMutex_;

    lmdb::env& env;
    lmdb::dbi& dbi_dbById;

    std::unique_ptr<RateLimiter> rateLimiter;
    std::unique_ptr<HttpMetrics> metrics;

    // Track active requests for graceful shutdown
    std::atomic<int> activeRequests_{0};
    std::mutex shutdownMutex_;
    std::condition_variable shutdownCv_;
    std::atomic<bool> shuttingDown_{false};

    void setupRoutes();
    void handleEventPost(const httplib::Request& req, httplib::Response& res);
    void handleEventPostBatch(const httplib::Request& req, httplib::Response& res);
    void handleBatchFetch(const httplib::Request& req, httplib::Response& res);
    void handleGetQuote(const httplib::Request& req, httplib::Response& res);
    void handleGetQuoteByDTag(const httplib::Request& req, httplib::Response& res);
    void handleHealthCheck(const httplib::Request& req, httplib::Response& res);
    void handleQuery(const httplib::Request& req, httplib::Response& res);
    void handleMetrics(const httplib::Request& req, httplib::Response& res);
    std::string getClientIP(const httplib::Request& req);
    std::string extractPubkeyFromRequest(const httplib::Request& req);

    // Request logging helpers
    void logRequest(const std::string& reqId, const std::string& method,
                   const std::string& path, const std::string& clientIP);
    void logResponse(const std::string& reqId, int statusCode, uint64_t durationMs);
    void logRateLimit(const std::string& reqId, const std::string& clientIP,
                     const std::string& reason);

    // Request lifecycle helpers
    void decrementActiveRequests();  // Decrement counter and notify shutdown waiter
    void setupSecurityHeaders(httplib::Response& res);
    void setupCorsHeaders(httplib::Response& res);
};