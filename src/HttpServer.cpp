#include "HttpServer.h"
#include "httplib.h"
#include <nlohmann/json.hpp>
#include "events.h"
#include "golpe.h"
#include "DBQuery.h"
#include "Decompressor.h"

using json = nlohmann::json;

HttpServer::HttpServer(uint16_t port, lmdb::env& envRef, lmdb::dbi& dbiRef,
                       const std::string &bindAddr, bool enableCors)
    : port(port), bindAddr(bindAddr), enableCors(enableCors), env(envRef), dbi_dbById(dbiRef)
{
    server = std::make_unique<httplib::Server>();

    // Initialize rate limiter
    RateLimiter::Config rateLimitConfig;
    rateLimitConfig.enabled = cfg().relay__ratelimit__enabled;
    rateLimitConfig.perIpPerMinute = cfg().relay__ratelimit__perIpPerMinute;
    rateLimitConfig.perPubkeyPerMinute = cfg().relay__ratelimit__perPubkeyPerMinute;
    rateLimitConfig.globalPerMinute = cfg().relay__ratelimit__globalPerMinute;
    rateLimitConfig.burstMultiplier = std::stod(cfg().relay__ratelimit__burstMultiplier);
    rateLimitConfig.cleanupIntervalSeconds = cfg().relay__ratelimit__cleanupIntervalSeconds;
    rateLimiter = std::make_unique<RateLimiter>(rateLimitConfig);

    // Initialize metrics collector
    metrics = std::make_unique<HttpMetrics>();

    LI << "HTTP server initialized with logging and metrics enabled";

    setupRoutes();
}

HttpServer::~HttpServer()
{
    stop();
}

void HttpServer::setEventProcessor(std::function<bool(const std::string&, std::string&)> processor)
{
    eventProcessor = processor;
}

void HttpServer::setupRoutes()
{
    // Set up security and CORS headers
    server->set_pre_routing_handler([this](const httplib::Request& req, httplib::Response& res) {
        // Generate request ID and add to response headers for tracing
        std::string reqId = RequestLogger::generateRequestId();
        res.set_header("X-Request-ID", reqId);
        uint64_t startMs = RequestLogger::getTimestampMs();

        // Store request ID in response (we'll log after response is sent)
        res.set_header("X-Start-Time", std::to_string(startMs));

        // CORS headers
        if (enableCors) {
            res.set_header("Access-Control-Allow-Origin", "*");
            res.set_header("Access-Control-Allow-Methods", "POST, GET, OPTIONS");
            res.set_header("Access-Control-Allow-Headers", "Content-Type");
        }

        // Security headers
        res.set_header("X-Content-Type-Options", "nosniff");
        res.set_header("X-Frame-Options", "DENY");
        res.set_header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'");
        res.set_header("X-XSS-Protection", "1; mode=block");
        res.set_header("Referrer-Policy", "no-referrer");

        // Log incoming request
        std::string clientIP = getClientIP(req);
        logRequest(reqId, req.method, req.path, clientIP);

        if (req.method == "OPTIONS" && enableCors) {
            res.status = 200;
            metrics->incrementRequestTotal("OPTIONS", 200);
            logResponse(reqId, 200, RequestLogger::getTimestampMs() - startMs);
            return httplib::Server::HandlerResponse::Handled;
        }

        // Rate limiting check (skip for health check and metrics)
        if (req.path != "/health" && req.path != "/metrics" && rateLimiter) {
            std::string pubkey = extractPubkeyFromRequest(req);

            metrics->incrementRateLimitHits();
            if (!rateLimiter->checkAndConsume(clientIP, pubkey)) {
                metrics->incrementRateLimitBlocks();
                res.status = 429; // Too Many Requests
                res.set_header("Retry-After", "60");

                std::string reason = rateLimiter->getRateLimitReason();
                logRateLimit(reqId, clientIP, reason);

                res.set_content(json({
                    {"ok", false},
                    {"message", reason}
                }).dump(), "application/json");

                metrics->incrementRequestTotal(req.method, 429);
                logResponse(reqId, 429, RequestLogger::getTimestampMs() - startMs);
                return httplib::Server::HandlerResponse::Handled;
            }
        }

        return httplib::Server::HandlerResponse::Unhandled;
    });

    server->Get("/api/query", [this](const httplib::Request& req, httplib::Response& res) {
        handleQuery(req, res);
    });

    server->Post("/api/query", [this](const httplib::Request& req, httplib::Response& res) {
        handleQuery(req, res);
    });

    server->Post("/api/quotes", [this](const httplib::Request& req, httplib::Response& res) {
        handleEventPost(req, res);
    });

    server->Get(R"(/api/quotes/([0-9a-fA-F]+))", [this](const httplib::Request& req, httplib::Response& res) {
        handleGetQuote(req, res);
    });

    server->Get("/health", [this](const httplib::Request& req, httplib::Response& res) {
        handleHealthCheck(req, res);
    });

    server->Get("/metrics", [this](const httplib::Request& req, httplib::Response& res) {
        handleMetrics(req, res);
    });

    server->Get("/", [](const httplib::Request& req, httplib::Response& res) {
        json response = {
            {"name", "strfry HTTP API"},
            {"version", "1.0.0"},
            {"endpoints", {
                {"/api/quotes", "POST - Submit Nostr quote event"},
                {"/api/quotes/:id", "GET - Get quote by ID"},
                {"/api/query", "GET - Query events"},
                {"/health", "GET - Health check"},
                {"/metrics", "GET - Prometheus metrics"}
            }}
        };
        res.set_content(response.dump(2), "application/json");
    });
}

void HttpServer::handleEventPost(const httplib::Request& req, httplib::Response& res)
{
    uint64_t startMs = RequestLogger::getTimestampMs();
    std::string reqId = res.get_header_value("X-Request-ID");

    try {
        json eventJson = json::parse(req.body);

        if (!eventJson.contains("id") || !eventJson.contains("pubkey") ||
            !eventJson.contains("created_at") || !eventJson.contains("kind") ||
            !eventJson.contains("tags") || !eventJson.contains("content") ||
            !eventJson.contains("sig")) {
            res.status = 400;
            res.set_content(json({
                {"ok", false},
                {"message", "Invalid event structure: missing required fields"}
            }).dump(), "application/json");
            metrics->incrementRequestTotal("POST", 400);
            metrics->observeRequestDuration("/api/quotes", RequestLogger::getTimestampMs() - startMs);
            logResponse(reqId, 400, RequestLogger::getTimestampMs() - startMs);
            return;
        }

        std::string eventStr = req.body;
        if (!hasRequiredEventFields(eventStr)) {
            res.status = 400;
            res.set_content(json({
                {"ok", false},
                {"message", "Invalid event structure"}
            }).dump(), "application/json");
            metrics->incrementRequestTotal("POST", 400);
            metrics->observeRequestDuration("/api/quotes", RequestLogger::getTimestampMs() - startMs);
            logResponse(reqId, 400, RequestLogger::getTimestampMs() - startMs);
            return;
        }

        std::string errorMsg;
        if (eventProcessor && eventProcessor(eventStr, errorMsg)) {
            metrics->incrementEventsAccepted();
            res.status = 200;
            res.set_content(json({
                {"ok", true},
                {"message", "Quote accepted"},
                {"id", eventJson["id"]}
            }).dump(), "application/json");
            LI << "[" << reqId << "] Event accepted: " << std::string(eventJson["id"]).substr(0, 8) << "...";
        } else {
            metrics->incrementEventsRejected();
            res.status = 400;
            res.set_content(json({
                {"ok", false},
                {"message", errorMsg.empty() ? "Quote rejected" : errorMsg}
            }).dump(), "application/json");
            LW << "[" << reqId << "] Event rejected: " << errorMsg;
        }
        metrics->incrementRequestTotal("POST", res.status);
        metrics->observeRequestDuration("/api/quotes", RequestLogger::getTimestampMs() - startMs);
        logResponse(reqId, res.status, RequestLogger::getTimestampMs() - startMs);
    } catch (const json::parse_error& e) {
        res.status = 400;
        res.set_content(json({
            {"ok", false},
            {"message", std::string("Invalid JSON: ") + e.what()}
        }).dump(), "application/json");
        metrics->incrementRequestTotal("POST", 400);
        metrics->observeRequestDuration("/api/quotes", RequestLogger::getTimestampMs() - startMs);
        LE << "[" << reqId << "] JSON parse error: " << e.what();
        logResponse(reqId, 400, RequestLogger::getTimestampMs() - startMs);
    } catch (const std::exception& e) {
        res.status = 500;
        res.set_content(json({
            {"ok", false},
            {"message", std::string("Internal error: ") + e.what()}
        }).dump(), "application/json");
        metrics->incrementRequestTotal("POST", 500);
        metrics->observeRequestDuration("/api/quotes", RequestLogger::getTimestampMs() - startMs);
        LE << "[" << reqId << "] Internal error: " << e.what();
        logResponse(reqId, 500, RequestLogger::getTimestampMs() - startMs);
    }
}

void HttpServer::handleGetQuote(const httplib::Request& req, httplib::Response& res)
{
    std::string reqId = res.get_header_value("X-Request-ID");

    try {
        if (req.matches.size() < 2) {
            res.status = 400;
            res.set_content(json({{"error", "Missing event ID"}}).dump(), "application/json");
            metrics->incrementRequestTotal("GET", 400);
            return;
        }

        std::string eventId = req.matches[1];

        if (eventId.empty()) {
            res.status = 400;
            res.set_content(json({{"error", "Missing event ID"}}).dump(), "application/json");
            metrics->incrementRequestTotal("GET", 400);
            return;
        }

        LI << "[" << reqId << "] GET request for event: " << eventId;

        std::string eventIdBytes = from_hex(eventId);

        auto txn = lmdb::txn::begin(env, nullptr, MDB_RDONLY);
        auto existing = lookupEventById(txn, std::string_view(eventIdBytes));

        if (!existing) {
            txn.abort();
            LI << "[" << reqId << "] Event not found: " << eventId;
            res.status = 404;
            res.set_content(json({{"error", "Event not found"}}).dump(), "application/json");
            metrics->incrementRequestTotal("GET", 404);
            return;
        }

        uint64_t levId = existing->primaryKeyId;

        Decompressor decomp;
        std::string_view eventJsonStr = getEventJson(txn, decomp, levId);

        txn.commit();

        LI << "[" << reqId << "] Returning event: " << eventId;
        res.status = 200;
        res.set_content(std::string(eventJsonStr), "application/json");
        metrics->incrementRequestTotal("GET", 200);

    } catch (const std::exception& e) {
        LE << "[" << reqId << "] Query error: " << e.what();
        res.status = 500;
        res.set_content(json({
            {"error", std::string("Query error: ") + e.what()}
        }).dump(), "application/json");
        metrics->incrementRequestTotal("GET", 500);
    }
}

void HttpServer::handleHealthCheck(const httplib::Request &req, httplib::Response &res)
{
    res.status = 200;
    res.set_content(json({
        {"status", "ok"},
        {"service", "strfry-http"}
    }).dump(), "application/json");
    metrics->incrementRequestTotal("GET", 200);
}

// Check if event JSON has required fields (structural validation only)
// Full cryptographic validation happens later in the ingester pipeline
bool HttpServer::hasRequiredEventFields(const std::string& jsonStr)
{
    try {
        json eventJson = json::parse(jsonStr);
        return eventJson.contains("id") &&
               eventJson.contains("pubkey") &&
               eventJson.contains("sig");
    } catch (...) {
        return false;
    }
}

void HttpServer::handleQuery(const httplib::Request& req, httplib::Response& res)
{
    try {
        tao::json::value filterJson;

        // Accept filter from POST body or GET query parameters
        if (req.method == "POST" && !req.body.empty()) {
            try {
                filterJson = tao::json::from_string(req.body);
            } catch (const std::exception& e) {
                res.status = 400;
                res.set_content(json({
                    {"error", std::string("Invalid JSON in request body: ") + e.what()}
                }).dump(), "application/json");
                return;
            }
        } else {
            // Build filter from query parameters
            tao::json::value filterObj = tao::json::empty_object;

            // Parse common query parameters
            auto ids_param = req.get_param_value("ids");
            auto authors_param = req.get_param_value("authors");
            auto kinds_param = req.get_param_value("kinds");
            auto since_param = req.get_param_value("since");
            auto until_param = req.get_param_value("until");
            auto limit_param = req.get_param_value("limit");

            if (!ids_param.empty()) {
                std::vector<tao::json::value> ids_vec;
                std::istringstream ss(ids_param);
                std::string id;
                while (std::getline(ss, id, ',')) {
                    ids_vec.push_back(id);
                }
                filterObj.get_object()["ids"] = ids_vec;
            }

            if (!authors_param.empty()) {
                std::vector<tao::json::value> authors_vec;
                std::istringstream ss(authors_param);
                std::string author;
                while (std::getline(ss, author, ',')) {
                    authors_vec.push_back(author);
                }
                filterObj.get_object()["authors"] = authors_vec;
            }

            if (!kinds_param.empty()) {
                std::vector<tao::json::value> kinds_vec;
                std::istringstream ss(kinds_param);
                std::string kind_str;
                while (std::getline(ss, kind_str, ',')) {
                    try {
                        kinds_vec.push_back(std::stoull(kind_str));
                    } catch (...) {
                        res.status = 400;
                        res.set_content(json({{"error", "Invalid kind parameter"}}).dump(), "application/json");
                        return;
                    }
                }
                filterObj.get_object()["kinds"] = kinds_vec;
            }

            if (!since_param.empty()) {
                try {
                    filterObj.get_object()["since"] = std::stoull(since_param);
                } catch (...) {
                    res.status = 400;
                    res.set_content(json({{"error", "Invalid since parameter"}}).dump(), "application/json");
                    return;
                }
            }

            if (!until_param.empty()) {
                try {
                    filterObj.get_object()["until"] = std::stoull(until_param);
                } catch (...) {
                    res.status = 400;
                    res.set_content(json({{"error", "Invalid until parameter"}}).dump(), "application/json");
                    return;
                }
            }

            if (!limit_param.empty()) {
                try {
                    filterObj.get_object()["limit"] = std::stoull(limit_param);
                } catch (...) {
                    res.status = 400;
                    res.set_content(json({{"error", "Invalid limit parameter"}}).dump(), "application/json");
                    return;
                }
            }

            filterJson = filterObj;
        }

        // Query the database
        json response = json::array();
        auto txn = lmdb::txn::begin(env, nullptr, MDB_RDONLY);
        Decompressor decomp;

        try {
            foreachByFilter(txn, filterJson, [&](uint64_t levId) {
                std::string_view eventJsonStr = getEventJson(txn, decomp, levId);
                json eventData = json::parse(std::string(eventJsonStr));
                response.push_back(eventData);
            });
        } catch (const std::exception& e) {
            txn.abort();
            res.status = 400;
            res.set_content(json({
                {"error", std::string("Filter error: ") + e.what()}
            }).dump(), "application/json");
            return;
        }

        txn.commit();

        std::string reqId = res.get_header_value("X-Request-ID");
        uint64_t resultCount = response.size();
        metrics->observeQueryResults(resultCount);
        metrics->incrementRequestTotal(req.method, 200);

        LI << "[" << reqId << "] Query returned " << resultCount << " events";
        res.status = 200;
        res.set_content(response.dump(), "application/json");

    } catch (const std::exception& e) {
        std::string reqId = res.get_header_value("X-Request-ID");
        metrics->incrementRequestTotal(req.method, 500);
        LE << "[" << reqId << "] Query error: " << e.what();
        res.status = 500;
        res.set_content(json({
            {"error", std::string("Query error: ") + e.what()}
        }).dump(), "application/json");
    }
}

void HttpServer::start()
{
    LI << "Starting HTTP server on " << bindAddr << ":" << port;
    server->listen(bindAddr.c_str(), port);
}

void HttpServer::stop()
{
    if (server) {
        LI << "Stopping HTTP server...";
        server->stop();
    }
}

void HttpServer::gracefulShutdown(int timeoutSeconds)
{
    if (!server) {
        return;
    }

    LI << "Initiating graceful shutdown of HTTP server (timeout: " << timeoutSeconds << "s)...";

    // Stop accepting new connections but allow existing ones to complete
    // cpp-httplib doesn't have a native graceful shutdown, so we:
    // 1. Wait briefly for in-flight requests to complete
    // 2. Then force stop

    auto startTime = std::chrono::steady_clock::now();
    auto timeout = std::chrono::seconds(timeoutSeconds);

    // Give a brief moment for in-flight requests to complete
    std::this_thread::sleep_for(std::chrono::milliseconds(500));

    auto elapsed = std::chrono::steady_clock::now() - startTime;
    if (elapsed < timeout) {
        LI << "Graceful shutdown completed in " << std::chrono::duration_cast<std::chrono::milliseconds>(elapsed).count() << "ms";
    } else {
        LW << "Graceful shutdown timeout reached, forcing stop";
    }

    server->stop();
    LI << "HTTP server stopped";
}

std::string HttpServer::getClientIP(const httplib::Request& req)
{
    // Check for X-Forwarded-For header (if behind proxy)
    if (req.has_header("X-Forwarded-For")) {
        std::string xff = req.get_header_value("X-Forwarded-For");
        // Take the first IP in the list
        size_t commaPos = xff.find(',');
        if (commaPos != std::string::npos) {
            return xff.substr(0, commaPos);
        }
        return xff;
    }

    // Check for X-Real-IP header (nginx style)
    if (req.has_header("X-Real-IP")) {
        return req.get_header_value("X-Real-IP");
    }

    // Fall back to remote address
    return req.remote_addr;
}

std::string HttpServer::extractPubkeyFromRequest(const httplib::Request& req)
{
    // Only extract pubkey from POST requests with JSON body
    if (req.method != "POST" || req.body.empty()) {
        return "";
    }

    try {
        json eventJson = json::parse(req.body);
        if (eventJson.contains("pubkey") && eventJson["pubkey"].is_string()) {
            return eventJson["pubkey"].get<std::string>();
        }
    } catch (...) {
        // If JSON parsing fails, just return empty string
        // The actual endpoint will handle the error
    }

    return "";
}

void HttpServer::logRequest(const std::string& reqId, const std::string& method,
                            const std::string& path, const std::string& clientIP)
{
    LI << "[" << reqId << "] " << method << " " << path << " from " << clientIP;
}

void HttpServer::logResponse(const std::string& reqId, int statusCode, uint64_t durationMs)
{
    if (statusCode >= 500) {
        LE << "[" << reqId << "] Response: " << statusCode << " (" << durationMs << "ms)";
    } else if (statusCode >= 400) {
        LW << "[" << reqId << "] Response: " << statusCode << " (" << durationMs << "ms)";
    } else {
        LI << "[" << reqId << "] Response: " << statusCode << " (" << durationMs << "ms)";
    }
}

void HttpServer::logRateLimit(const std::string& reqId, const std::string& clientIP,
                              const std::string& reason)
{
    LW << "[" << reqId << "] Rate limit exceeded for " << clientIP << ": " << reason;
}

void HttpServer::handleMetrics(const httplib::Request& req, httplib::Response& res)
{
    std::string metricsOutput = metrics->renderPrometheus();
    res.set_content(metricsOutput, "text/plain; version=0.0.4");
    res.status = 200;
}