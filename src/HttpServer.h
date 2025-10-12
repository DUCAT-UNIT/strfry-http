#pragma once

#include <string>
#include <memory>
#include <functional>
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
    HttpServer(uint16_t port, lmdb::env& env, lmdb::dbi& dbi_dbById,
               const std::string &bindAddr = "127.0.0.1", bool enableCors = false);
    ~HttpServer();

    void setEventProcessor(std::function<bool(const std::string&, std::string&)> processor);
    void start();
    void stop();
    void gracefulShutdown(int timeoutSeconds = 10);

private:
    std::unique_ptr<httplib::Server> server;
    uint16_t port;
    std::string bindAddr;
    bool enableCors;
    std::function<bool(const std::string&, std::string&)> eventProcessor;

    lmdb::env& env;
    lmdb::dbi& dbi_dbById;

    std::unique_ptr<RateLimiter> rateLimiter;
    std::unique_ptr<HttpMetrics> metrics;

    void setupRoutes();
    void handleEventPost(const httplib::Request& req, httplib::Response& res);
    void handleGetQuote(const httplib::Request& req, httplib::Response& res);
    void handleHealthCheck(const httplib::Request& req, httplib::Response& res);
    void handleQuery(const httplib::Request& req, httplib::Response& res);
    void handleMetrics(const httplib::Request& req, httplib::Response& res);
    bool hasRequiredEventFields(const std::string& jsonStr);
    std::string getClientIP(const httplib::Request& req);
    std::string extractPubkeyFromRequest(const httplib::Request& req);

    // Request logging helpers
    void logRequest(const std::string& reqId, const std::string& method,
                   const std::string& path, const std::string& clientIP);
    void logResponse(const std::string& reqId, int statusCode, uint64_t durationMs);
    void logRateLimit(const std::string& reqId, const std::string& clientIP,
                     const std::string& reason);
};