#include "HttpServer.h"
#include "httplib.h"
#include "events.h"
#include "golpe.h"
#include "DBQuery.h"
#include "Decompressor.h"
#include <stdexcept>

// Use tao::json consistently throughout (already included via golpe.h)
// JSON helper class to provide nlohmann-like syntax with tao::json
class json;

// Wrapper for JSON values to support nlohmann-like .get<T>() syntax
class JsonValueRef {
public:
    const tao::json::value& ref;
    JsonValueRef(const tao::json::value& r) : ref(r) {}

    template<typename T>
    T get() const { return ref.as<T>(); }

    bool is_string() const { return ref.is_string(); }
    bool is_number() const { return ref.is_number(); }
    bool is_array() const { return ref.is_array(); }
};

class json {
public:
    tao::json::value val;

    json() : val(tao::json::empty_object) {}
    json(std::initializer_list<std::pair<const char*, tao::json::value>> init) : val(tao::json::empty_object) {
        for (const auto& [key, v] : init) {
            val[key] = v;
        }
    }
    json(const tao::json::value& v) : val(v) {}

    std::string dump(int indent = -1) const {
        if (indent >= 0) {
            return tao::json::to_string(val, indent);
        }
        return tao::json::to_string(val);
    }

    // For reading parsed JSON - returns wrapper with get<T>() support
    bool contains(const std::string& key) const { return val.find(key) != nullptr; }
    JsonValueRef operator[](const std::string& key) const { return JsonValueRef(val.at(key)); }

    bool is_string() const { return val.is_string(); }
    bool is_number() const { return val.is_number(); }
    bool is_array() const { return val.is_array(); }

    template<typename T>
    T get() const { return val.as<T>(); }

    struct parse_error : public std::runtime_error {
        using std::runtime_error::runtime_error;
    };

    static json parse(const std::string& str) {
        try {
            return json(tao::json::from_string(str));
        } catch (const std::exception& e) {
            throw parse_error(e.what());
        }
    }
};

// Maximum number of events to return in a single query to prevent unbounded responses
static constexpr uint64_t MAX_QUERY_RESULTS = 10000;

// Helper to safely parse double from config string
static double safeParseDouble(const std::string& str, double defaultVal) {
    try {
        return std::stod(str);
    } catch (const std::exception& e) {
        LW << "Failed to parse config value '" << str << "' as double, using default: " << defaultVal;
        return defaultVal;
    }
}

// Validate IP address format with proper structure validation
// Returns true if the string is a valid IPv4 or IPv6 address
static bool isValidIPv4(const std::string& ip) {
    if (ip.empty() || ip.size() > 15) return false;  // Max "255.255.255.255"

    int octets = 0;
    int currentValue = 0;
    int digitCount = 0;
    bool lastWasDot = true;  // Start true to catch leading dot

    for (size_t i = 0; i < ip.size(); i++) {
        char c = ip[i];
        if (c == '.') {
            if (lastWasDot || digitCount == 0) return false;  // ".." or leading "."
            if (currentValue > 255) return false;
            octets++;
            currentValue = 0;
            digitCount = 0;
            lastWasDot = true;
        } else if (c >= '0' && c <= '9') {
            // Check for leading zeros (invalid: "01.01.01.01")
            if (digitCount == 1 && currentValue == 0) return false;
            currentValue = currentValue * 10 + (c - '0');
            if (currentValue > 255) return false;
            digitCount++;
            if (digitCount > 3) return false;
            lastWasDot = false;
        } else {
            return false;  // Invalid character
        }
    }

    // Must end with a valid octet, not a dot
    if (lastWasDot || digitCount == 0) return false;
    if (currentValue > 255) return false;
    octets++;

    return octets == 4;
}

static bool isValidIPv6(const std::string& ip) {
    if (ip.empty() || ip.size() > 45) return false;  // Max with IPv4-mapped

    // Check for IPv4-mapped address (::ffff:192.168.1.1)
    size_t lastColon = ip.rfind(':');
    if (lastColon != std::string::npos && lastColon + 1 < ip.size()) {
        std::string suffix = ip.substr(lastColon + 1);
        if (suffix.find('.') != std::string::npos) {
            // Has IPv4 suffix - validate the IPv4 part
            if (!isValidIPv4(suffix)) return false;
            // Continue validating the IPv6 prefix (before the IPv4 part)
            // For simplicity, just check prefix has valid IPv6 chars and structure
        }
    }

    int colonCount = 0;
    int doubleColonCount = 0;
    int groupCount = 0;
    int hexDigits = 0;
    bool lastWasColon = false;

    for (size_t i = 0; i < ip.size(); i++) {
        char c = ip[i];
        if (c == ':') {
            if (lastWasColon) {
                doubleColonCount++;
                if (doubleColonCount > 1) return false;  // Only one :: allowed
            } else if (hexDigits > 0) {
                groupCount++;
                hexDigits = 0;
            }
            colonCount++;
            lastWasColon = true;
        } else if (c == '.') {
            // IPv4-mapped portion - already validated above
            break;
        } else if ((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
            hexDigits++;
            if (hexDigits > 4) return false;  // Max 4 hex digits per group
            lastWasColon = false;
        } else {
            return false;  // Invalid character
        }
    }

    // Count final group if present
    if (hexDigits > 0) groupCount++;

    // Valid IPv6 has 8 groups, or fewer with :: compression
    if (doubleColonCount == 0 && groupCount != 8) return false;
    if (doubleColonCount == 1 && groupCount > 7) return false;

    // Must have at least one colon
    return colonCount >= 2;
}

static bool isValidIPAddress(const std::string& ip) {
    if (ip.empty() || ip.size() > 45) return false;

    // Try IPv4 first (more common)
    if (ip.find(':') == std::string::npos) {
        return isValidIPv4(ip);
    }

    // Must be IPv6
    return isValidIPv6(ip);
}

// Sanitize user input for safe logging (prevents log injection attacks)
static std::string sanitizeForLog(const std::string& input, size_t maxLen = 100) {
    std::string result;
    result.reserve(std::min(input.size(), maxLen));

    for (size_t i = 0; i < input.size() && result.size() < maxLen; ++i) {
        char c = input[i];
        // Only allow printable ASCII characters, replace others
        if (c >= 32 && c < 127 && c != '\n' && c != '\r') {
            result += c;
        } else {
            result += '?';
        }
    }

    if (input.size() > maxLen) {
        result += "...[truncated]";
    }

    return result;
}

// Sanitize error messages before sending to client
// Removes potentially sensitive internal details like file paths, memory addresses, etc.
static std::string sanitizeErrorForClient(const std::string& errorMsg) {
    // For JSON parse errors, keep enough detail to be useful but not internal paths
    if (errorMsg.find("parse") != std::string::npos ||
        errorMsg.find("JSON") != std::string::npos ||
        errorMsg.find("json") != std::string::npos) {
        // Keep position info but truncate long messages
        if (errorMsg.size() > 100) {
            return errorMsg.substr(0, 100) + "...";
        }
        return errorMsg;
    }

    // For other errors, return generic messages to avoid leaking internals
    if (errorMsg.find("LMDB") != std::string::npos ||
        errorMsg.find("lmdb") != std::string::npos ||
        errorMsg.find("database") != std::string::npos) {
        return "Database error";
    }

    if (errorMsg.find("memory") != std::string::npos ||
        errorMsg.find("alloc") != std::string::npos) {
        return "Server resource error";
    }

    // For filter/query related errors, keep them as they're user-facing
    if (errorMsg.find("filter") != std::string::npos ||
        errorMsg.find("invalid") != std::string::npos ||
        errorMsg.find("parameter") != std::string::npos) {
        if (errorMsg.size() > 150) {
            return errorMsg.substr(0, 150) + "...";
        }
        return errorMsg;
    }

    // Default: truncate and sanitize
    if (errorMsg.size() > 100) {
        return "Internal error";
    }
    return errorMsg;
}

HttpServer::HttpServer(uint16_t port, lmdb::env& envRef, lmdb::dbi& dbiRef,
                       const std::string &bindAddr, bool enableCors, bool trustProxy,
                       const std::string &corsOrigin, size_t maxBodySize)
    : port(port), bindAddr(bindAddr), enableCors(enableCors), trustProxy(trustProxy),
      corsOrigin(corsOrigin), env(envRef), dbi_dbById(dbiRef)
{
    server = std::make_unique<httplib::Server>();

    // Set maximum request body size to prevent large payload attacks
    server->set_payload_max_length(maxBodySize);
    LI << "HTTP server max request body size: " << maxBodySize << " bytes";

    // Initialize rate limiter with safe config parsing
    RateLimiter::Config rateLimitConfig;
    rateLimitConfig.enabled = cfg().relay__ratelimit__enabled;
    rateLimitConfig.perIpPerMinute = cfg().relay__ratelimit__perIpPerMinute;
    rateLimitConfig.perPubkeyPerMinute = cfg().relay__ratelimit__perPubkeyPerMinute;
    rateLimitConfig.globalPerMinute = cfg().relay__ratelimit__globalPerMinute;
    rateLimitConfig.burstMultiplier = safeParseDouble(cfg().relay__ratelimit__burstMultiplier, 2.0);
    rateLimitConfig.cleanupIntervalSeconds = cfg().relay__ratelimit__cleanupIntervalSeconds;
    rateLimiter = std::make_unique<RateLimiter>(rateLimitConfig);

    // Initialize metrics collector
    metrics = std::make_unique<HttpMetrics>();

    LI << "HTTP server initialized with logging and metrics enabled"
       << " (trustProxy=" << (trustProxy ? "true" : "false")
       << ", corsOrigin=" << corsOrigin << ")";

    setupRoutes();
}

HttpServer::~HttpServer()
{
    stop();
}

void HttpServer::setEventProcessor(std::function<bool(const std::string&, std::string&)> processor)
{
    std::lock_guard<std::mutex> lock(eventProcessorMutex_);
    eventProcessor = processor;
}

void HttpServer::setupRoutes()
{
    // Pre-routing handler: security headers, CORS, request tracking, and rate limiting
    server->set_pre_routing_handler([this](const httplib::Request& req, httplib::Response& res) {
        // Track active requests for graceful shutdown
        // Using relaxed ordering is safe here (see decrementActiveRequests for details)
        activeRequests_.fetch_add(1, std::memory_order_relaxed);

        // Set up request tracing
        std::string reqId = RequestLogger::generateRequestId();
        res.set_header("X-Request-ID", reqId);
        uint64_t startMs = RequestLogger::getTimestampMs();

        // Set up headers using extracted methods
        setupCorsHeaders(res);
        setupSecurityHeaders(res);

        // Log incoming request
        std::string clientIP = getClientIP(req);
        logRequest(reqId, req.method, req.path, clientIP);

        // Handle CORS preflight requests
        if (req.method == "OPTIONS" && enableCors) {
            res.status = 200;
            metrics->incrementRequestTotal("OPTIONS", 200);
            logResponse(reqId, 200, RequestLogger::getTimestampMs() - startMs);
            decrementActiveRequests();
            return httplib::Server::HandlerResponse::Handled;
        }

        // Rate limiting check (skip for health check and metrics endpoints)
        if (req.path != "/health" && req.path != "/metrics" && rateLimiter) {
            std::string pubkey = extractPubkeyFromRequest(req);

            metrics->incrementRateLimitHits();
            auto checkResult = rateLimiter->checkAndConsume(clientIP, pubkey);
            if (!checkResult.allowed) {
                metrics->incrementRateLimitBlocks();
                res.status = 429;
                res.set_header("Retry-After", "60");

                logRateLimit(reqId, clientIP, checkResult.reason);

                res.set_content(json({
                    {"ok", false},
                    {"message", checkResult.reason}
                }).dump(), "application/json");

                metrics->incrementRequestTotal(req.method, 429);
                logResponse(reqId, 429, RequestLogger::getTimestampMs() - startMs);
                decrementActiveRequests();
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

    server->Post("/api/quotes/batch", [this](const httplib::Request& req, httplib::Response& res) {
        handleEventPostBatch(req, res);
    });

    server->Get("/api/quotes", [this](const httplib::Request& req, httplib::Response& res) {
        handleGetQuoteByDTag(req, res);
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
                {"/api/quotes/batch", "POST - Submit batch of Nostr quote events (atomic)"},
                {"/api/quotes/:id", "GET - Get quote by ID"},
                {"/api/query", "GET - Query events"},
                {"/health", "GET - Health check"},
                {"/metrics", "GET - Prometheus metrics"}
            }}
        };
        res.set_content(response.dump(2), "application/json");
    });

    // Post-routing handler to decrement active request count
    server->set_post_routing_handler([this](const httplib::Request& req, httplib::Response& res) {
        decrementActiveRequests();
    });
}

void HttpServer::handleEventPost(const httplib::Request& req, httplib::Response& res)
{
    uint64_t startMs = RequestLogger::getTimestampMs();
    std::string reqId = res.get_header_value("X-Request-ID");

    try {
        // Parse JSON once and reuse
        json eventJson = json::parse(req.body);

        // Validate required fields (single pass)
        if (!eventJson.contains("id") || !eventJson["id"].is_string() ||
            !eventJson.contains("pubkey") || !eventJson["pubkey"].is_string() ||
            !eventJson.contains("created_at") || !eventJson["created_at"].is_number() ||
            !eventJson.contains("kind") || !eventJson["kind"].is_number() ||
            !eventJson.contains("tags") || !eventJson["tags"].is_array() ||
            !eventJson.contains("content") || !eventJson["content"].is_string() ||
            !eventJson.contains("sig") || !eventJson["sig"].is_string()) {
            res.status = 400;
            res.set_content(json({
                {"ok", false},
                {"message", "Invalid event structure: missing or invalid required fields"}
            }).dump(), "application/json");
            metrics->incrementRequestTotal("POST", 400);
            metrics->observeRequestDuration("/api/quotes", RequestLogger::getTimestampMs() - startMs);
            logResponse(reqId, 400, RequestLogger::getTimestampMs() - startMs);
            return;
        }

        std::string eventId = eventJson["id"].get<std::string>();
        std::string errorMsg;
        bool accepted = false;

        // Thread-safe call to event processor
        {
            std::lock_guard<std::mutex> lock(eventProcessorMutex_);
            if (eventProcessor) {
                accepted = eventProcessor(req.body, errorMsg);
            }
        }

        if (accepted) {
            metrics->incrementEventsAccepted();
            res.status = 200;
            res.set_content(json({
                {"ok", true},
                {"message", "Quote accepted"},
                {"id", eventId}
            }).dump(), "application/json");
            LI << "[" << reqId << "] Event accepted: " << eventId.substr(0, 8) << "...";
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
            {"message", std::string("Invalid JSON: ") + sanitizeErrorForClient(e.what())}
        }).dump(), "application/json");
        metrics->incrementRequestTotal("POST", 400);
        metrics->observeRequestDuration("/api/quotes", RequestLogger::getTimestampMs() - startMs);
        LE << "[" << reqId << "] JSON parse error: " << e.what();
        logResponse(reqId, 400, RequestLogger::getTimestampMs() - startMs);
    } catch (const std::exception& e) {
        res.status = 500;
        res.set_content(json({
            {"ok", false},
            {"message", "Internal server error"}  // Don't expose internal details
        }).dump(), "application/json");
        metrics->incrementRequestTotal("POST", 500);
        metrics->observeRequestDuration("/api/quotes", RequestLogger::getTimestampMs() - startMs);
        LE << "[" << reqId << "] Internal error: " << e.what();
        logResponse(reqId, 500, RequestLogger::getTimestampMs() - startMs);
    }
}

void HttpServer::handleEventPostBatch(const httplib::Request& req, httplib::Response& res)
{
    uint64_t startMs = RequestLogger::getTimestampMs();
    std::string reqId = res.get_header_value("X-Request-ID");

    try {
        // Parse JSON array
        tao::json::value batchJson;
        try {
            batchJson = tao::json::from_string(req.body);
        } catch (const std::exception& e) {
            res.status = 400;
            res.set_content(json({
                {"ok", false},
                {"message", std::string("Invalid JSON: ") + sanitizeErrorForClient(e.what())}
            }).dump(), "application/json");
            metrics->incrementRequestTotal("POST", 400);
            metrics->observeRequestDuration("/api/quotes/batch", RequestLogger::getTimestampMs() - startMs);
            logResponse(reqId, 400, RequestLogger::getTimestampMs() - startMs);
            return;
        }

        if (!batchJson.is_array()) {
            res.status = 400;
            res.set_content(json({
                {"ok", false},
                {"message", "Request body must be a JSON array of events"}
            }).dump(), "application/json");
            metrics->incrementRequestTotal("POST", 400);
            metrics->observeRequestDuration("/api/quotes/batch", RequestLogger::getTimestampMs() - startMs);
            logResponse(reqId, 400, RequestLogger::getTimestampMs() - startMs);
            return;
        }

        const auto& events = batchJson.get_array();
        size_t eventCount = events.size();

        if (eventCount == 0) {
            res.status = 400;
            res.set_content(json({
                {"ok", false},
                {"message", "Empty event array"}
            }).dump(), "application/json");
            metrics->incrementRequestTotal("POST", 400);
            metrics->observeRequestDuration("/api/quotes/batch", RequestLogger::getTimestampMs() - startMs);
            logResponse(reqId, 400, RequestLogger::getTimestampMs() - startMs);
            return;
        }

        // Limit batch size to prevent DoS
        static constexpr size_t MAX_BATCH_SIZE = 1000;
        if (eventCount > MAX_BATCH_SIZE) {
            res.status = 400;
            res.set_content(json({
                {"ok", false},
                {"message", "Batch size exceeds maximum of 1000 events"}
            }).dump(), "application/json");
            metrics->incrementRequestTotal("POST", 400);
            metrics->observeRequestDuration("/api/quotes/batch", RequestLogger::getTimestampMs() - startMs);
            logResponse(reqId, 400, RequestLogger::getTimestampMs() - startMs);
            return;
        }

        LI << "[" << reqId << "] Processing batch of " << eventCount << " events";

        // Validate and process each event - atomic: all must succeed
        std::vector<std::string> acceptedIds;
        acceptedIds.reserve(eventCount);

        for (size_t i = 0; i < eventCount; i++) {
            const auto& eventJson = events[i];

            // Validate required fields
            if (!eventJson.is_object()) {
                res.status = 400;
                res.set_content(json({
                    {"ok", false},
                    {"message", "Event at index " + std::to_string(i) + " is not an object"}
                }).dump(), "application/json");
                metrics->incrementRequestTotal("POST", 400);
                metrics->observeRequestDuration("/api/quotes/batch", RequestLogger::getTimestampMs() - startMs);
                logResponse(reqId, 400, RequestLogger::getTimestampMs() - startMs);
                return;
            }

            const auto* idPtr = eventJson.find("id");
            const auto* pubkeyPtr = eventJson.find("pubkey");
            const auto* createdAtPtr = eventJson.find("created_at");
            const auto* kindPtr = eventJson.find("kind");
            const auto* tagsPtr = eventJson.find("tags");
            const auto* contentPtr = eventJson.find("content");
            const auto* sigPtr = eventJson.find("sig");

            if (!idPtr || !idPtr->is_string() ||
                !pubkeyPtr || !pubkeyPtr->is_string() ||
                !createdAtPtr || !createdAtPtr->is_number() ||
                !kindPtr || !kindPtr->is_number() ||
                !tagsPtr || !tagsPtr->is_array() ||
                !contentPtr || !contentPtr->is_string() ||
                !sigPtr || !sigPtr->is_string()) {
                res.status = 400;
                res.set_content(json({
                    {"ok", false},
                    {"message", "Event at index " + std::to_string(i) + " has invalid structure"}
                }).dump(), "application/json");
                metrics->incrementRequestTotal("POST", 400);
                metrics->observeRequestDuration("/api/quotes/batch", RequestLogger::getTimestampMs() - startMs);
                logResponse(reqId, 400, RequestLogger::getTimestampMs() - startMs);
                return;
            }

            std::string eventId = idPtr->as<std::string>();
            std::string eventStr = tao::json::to_string(eventJson);
            std::string errorMsg;
            bool accepted = false;

            // Thread-safe call to event processor
            {
                std::lock_guard<std::mutex> lock(eventProcessorMutex_);
                if (eventProcessor) {
                    accepted = eventProcessor(eventStr, errorMsg);
                }
            }

            if (!accepted) {
                // Atomic semantics: reject entire batch if any event fails
                metrics->incrementEventsRejected();
                res.status = 400;
                res.set_content(json({
                    {"ok", false},
                    {"message", "Event at index " + std::to_string(i) + " rejected: " +
                               (errorMsg.empty() ? "validation failed" : errorMsg)},
                    {"failed_index", i},
                    {"failed_id", eventId}
                }).dump(), "application/json");
                metrics->incrementRequestTotal("POST", 400);
                metrics->observeRequestDuration("/api/quotes/batch", RequestLogger::getTimestampMs() - startMs);
                LW << "[" << reqId << "] Batch rejected at index " << i << ": " << errorMsg;
                logResponse(reqId, 400, RequestLogger::getTimestampMs() - startMs);
                return;
            }

            acceptedIds.push_back(eventId);
            metrics->incrementEventsAccepted();
        }

        // All events accepted
        res.status = 201;
        res.set_content(json({
            {"ok", true},
            {"message", "Batch accepted"},
            {"count", eventCount}
        }).dump(), "application/json");
        metrics->incrementRequestTotal("POST", 201);
        metrics->observeRequestDuration("/api/quotes/batch", RequestLogger::getTimestampMs() - startMs);
        LI << "[" << reqId << "] Batch of " << eventCount << " events accepted";
        logResponse(reqId, 201, RequestLogger::getTimestampMs() - startMs);

    } catch (const std::exception& e) {
        res.status = 500;
        res.set_content(json({
            {"ok", false},
            {"message", "Internal server error"}
        }).dump(), "application/json");
        metrics->incrementRequestTotal("POST", 500);
        metrics->observeRequestDuration("/api/quotes/batch", RequestLogger::getTimestampMs() - startMs);
        LE << "[" << reqId << "] Batch processing error: " << e.what();
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

        std::string eventIdBytes;
        try {
            eventIdBytes = from_hex(eventId);
            if (eventIdBytes.size() != 32) {
                throw std::runtime_error("Invalid event ID length");
            }
        } catch (const std::exception& e) {
            res.status = 400;
            res.set_content(json({{"error", "Invalid event ID format"}}).dump(), "application/json");
            metrics->incrementRequestTotal("GET", 400);
            return;
        }

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
            {"error", "Internal server error"}  // Don't expose internal details
        }).dump(), "application/json");
        metrics->incrementRequestTotal("GET", 500);
    }
}

void HttpServer::handleGetQuoteByDTag(const httplib::Request& req, httplib::Response& res)
{
    std::string reqId = res.get_header_value("X-Request-ID");
    uint64_t startMs = RequestLogger::getTimestampMs();

    try {
        // Get the 'd' query parameter
        auto dTag = req.get_param_value("d");

        if (dTag.empty()) {
            res.status = 400;
            res.set_content(json({{"error", "Missing 'd' query parameter"}}).dump(), "application/json");
            metrics->incrementRequestTotal("GET", 400);
            logResponse(reqId, 400, RequestLogger::getTimestampMs() - startMs);
            return;
        }

        std::string safeDTag = sanitizeForLog(dTag, 64);
        LI << "[" << reqId << "] Fetching event by d tag from relay"
           << " url=\"http://localhost:" << port << "/api/quotes?d=" << safeDTag << "\""
           << " dTag=" << safeDTag;

        // Build a Nostr filter for the 'd' tag using proper JSON construction
        // This safely handles special characters in dTag
        tao::json::value filterJson = {
            {"#d", tao::json::value::array({dTag})},
            {"limit", 1}
        };

        // Query the database
        auto txn = lmdb::txn::begin(env, nullptr, MDB_RDONLY);
        Decompressor decomp;
        std::string eventJsonStr;
        bool found = false;

        try {
            foreachByFilter(txn, filterJson, [&](uint64_t levId) {
                if (!found) {
                    eventJsonStr = getEventJson(txn, decomp, levId);
                    found = true;
                }
            });
        } catch (const std::exception& e) {
            txn.abort();
            LE << "[" << reqId << "] Filter error: " << e.what();
            res.status = 500;
            res.set_content(json({
                {"error", "Internal server error"}  // Don't expose internal details
            }).dump(), "application/json");
            metrics->incrementRequestTotal("GET", 500);
            logResponse(reqId, 500, RequestLogger::getTimestampMs() - startMs);
            return;
        }

        txn.commit();

        if (!found) {
            LI << "[" << reqId << "] Event not found for d tag: " << safeDTag;
            res.status = 404;
            res.set_content(json({
                {"error", "event not found for specified d tag"}
            }).dump(), "application/json");
            metrics->incrementRequestTotal("GET", 404);
            logResponse(reqId, 404, RequestLogger::getTimestampMs() - startMs);
            return;
        }

        LI << "[" << reqId << "] Returning event for d tag: " << safeDTag;
        res.status = 200;
        res.set_content(eventJsonStr, "application/json");
        metrics->incrementRequestTotal("GET", 200);
        logResponse(reqId, 200, RequestLogger::getTimestampMs() - startMs);

    } catch (const std::exception& e) {
        LE << "[" << reqId << "] Query error: " << e.what();
        res.status = 500;
        res.set_content(json({
            {"error", "Internal server error"}  // Don't expose internal details
        }).dump(), "application/json");
        metrics->incrementRequestTotal("GET", 500);
        logResponse(reqId, 500, RequestLogger::getTimestampMs() - startMs);
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
                    {"error", std::string("Invalid JSON in request body: ") + sanitizeErrorForClient(e.what())}
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

        // Query the database - build JSON array directly to avoid per-event parsing overhead
        std::string reqId = res.get_header_value("X-Request-ID");
        auto txn = lmdb::txn::begin(env, nullptr, MDB_RDONLY);
        Decompressor decomp;

        // Build JSON array incrementally without parsing each event
        std::string responseStr = "[";
        uint64_t resultCount = 0;
        bool first = true;

        try {
            foreachByFilter(txn, filterJson, [&](uint64_t levId) {
                // Enforce maximum result limit to prevent unbounded responses
                if (resultCount >= MAX_QUERY_RESULTS) {
                    return;  // Stop processing more results
                }

                std::string_view eventJsonStr = getEventJson(txn, decomp, levId);
                if (!first) {
                    responseStr += ",";
                }
                first = false;
                responseStr += eventJsonStr;
                resultCount++;
            });
        } catch (const std::exception& e) {
            txn.abort();
            // Distinguish between filter/query errors and internal errors
            std::string errMsg = e.what();
            bool isFilterError = errMsg.find("filter") != std::string::npos ||
                                 errMsg.find("invalid") != std::string::npos;
            res.status = isFilterError ? 400 : 500;
            res.set_content(json({
                {"error", isFilterError ? sanitizeErrorForClient(errMsg) : "Internal server error"}
            }).dump(), "application/json");
            metrics->incrementRequestTotal(req.method, res.status);
            return;
        }

        responseStr += "]";
        txn.commit();

        metrics->observeQueryResults(resultCount);
        metrics->incrementRequestTotal(req.method, 200);

        LI << "[" << reqId << "] Query returned " << resultCount << " events";
        res.status = 200;
        res.set_content(responseStr, "application/json");

    } catch (const std::exception& e) {
        std::string reqId = res.get_header_value("X-Request-ID");
        metrics->incrementRequestTotal(req.method, 500);
        LE << "[" << reqId << "] Query error: " << e.what();
        res.status = 500;
        res.set_content(json({
            {"error", "Internal server error"}  // Don't expose internal details
        }).dump(), "application/json");
    }
}

bool HttpServer::start()
{
    LI << "Starting HTTP server on " << bindAddr << ":" << port;
    try {
        bool success = server->listen(bindAddr.c_str(), port);
        if (!success) {
            LE << "Failed to start HTTP server on " << bindAddr << ":" << port
               << " (address may be in use or permission denied)";
            return false;
        }
        return true;
    } catch (const std::exception& e) {
        LE << "HTTP server failed to start: " << e.what();
        return false;
    } catch (...) {
        LE << "HTTP server failed to start with unknown error";
        return false;
    }
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

    // Signal that we're shutting down (release ordering for visibility to request handlers)
    shuttingDown_.store(true, std::memory_order_release);

    auto startTime = std::chrono::steady_clock::now();
    auto timeout = std::chrono::seconds(timeoutSeconds);

    // Wait for active requests to complete using condition variable
    {
        std::unique_lock<std::mutex> lock(shutdownMutex_);
        int activeCount = activeRequests_.load(std::memory_order_acquire);

        if (activeCount > 0) {
            LI << "Waiting for " << activeCount << " active request(s) to complete...";

            // Wait with timeout - condition variable is notified when activeRequests_ reaches 0
            bool completed = shutdownCv_.wait_for(lock, timeout, [this] {
                return activeRequests_.load(std::memory_order_acquire) == 0;
            });

            if (!completed) {
                int remaining = activeRequests_.load(std::memory_order_acquire);
                LW << "Graceful shutdown timeout reached with " << remaining << " active requests, forcing stop";
            }
        }
    }

    auto elapsed = std::chrono::steady_clock::now() - startTime;
    auto elapsedMs = std::chrono::duration_cast<std::chrono::milliseconds>(elapsed).count();

    int finalCount = activeRequests_.load(std::memory_order_acquire);
    if (finalCount == 0) {
        LI << "All requests completed, shutting down HTTP server (took " << elapsedMs << "ms)";
    }

    // Stop the rate limiter's cleanup thread
    if (rateLimiter) {
        rateLimiter->stop();
    }

    server->stop();
    LI << "HTTP server stopped";
}

std::string HttpServer::getClientIP(const httplib::Request& req)
{
    // Only trust proxy headers if explicitly configured
    // This prevents IP spoofing attacks when not behind a trusted proxy
    if (trustProxy) {
        // Check for X-Forwarded-For header (if behind proxy)
        if (req.has_header("X-Forwarded-For")) {
            std::string xff = req.get_header_value("X-Forwarded-For");
            // Take the first IP in the list (original client)
            std::string ip;
            size_t commaPos = xff.find(',');
            if (commaPos != std::string::npos) {
                // Trim whitespace
                ip = xff.substr(0, commaPos);
                size_t start = ip.find_first_not_of(" \t");
                size_t end = ip.find_last_not_of(" \t");
                if (start != std::string::npos && end != std::string::npos) {
                    ip = ip.substr(start, end - start + 1);
                }
            } else {
                // Trim whitespace from single IP
                size_t start = xff.find_first_not_of(" \t");
                size_t end = xff.find_last_not_of(" \t");
                if (start != std::string::npos && end != std::string::npos) {
                    ip = xff.substr(start, end - start + 1);
                } else {
                    ip = xff;
                }
            }

            // Validate IP format to prevent spoofing with arbitrary strings
            if (isValidIPAddress(ip)) {
                return ip;
            }
            // Invalid IP in header - fall back to remote_addr
            LW << "Invalid IP address in X-Forwarded-For header: " << sanitizeForLog(ip, 50);
        }

        // Check for X-Real-IP header (nginx style)
        if (req.has_header("X-Real-IP")) {
            std::string realIP = req.get_header_value("X-Real-IP");
            // Trim whitespace
            size_t start = realIP.find_first_not_of(" \t");
            size_t end = realIP.find_last_not_of(" \t");
            if (start != std::string::npos && end != std::string::npos) {
                realIP = realIP.substr(start, end - start + 1);
            }

            if (isValidIPAddress(realIP)) {
                return realIP;
            }
            LW << "Invalid IP address in X-Real-IP header: " << sanitizeForLog(realIP, 50);
        }
    }

    // Fall back to remote address (always safe)
    return req.remote_addr;
}

std::string HttpServer::extractPubkeyFromRequest(const httplib::Request& req)
{
    // Only extract pubkey from POST requests with JSON body
    if (req.method != "POST" || req.body.empty()) {
        return "";
    }

    // Fast pubkey extraction using string search to avoid double JSON parsing
    // (the actual handler will parse the full JSON later)
    // Look for "pubkey":"<64 hex chars>" pattern
    static const std::string pubkeyPrefix = "\"pubkey\":\"";
    size_t pos = req.body.find(pubkeyPrefix);
    if (pos == std::string::npos) {
        // Try with space after colon
        static const std::string pubkeyPrefixSpace = "\"pubkey\": \"";
        pos = req.body.find(pubkeyPrefixSpace);
        if (pos != std::string::npos) {
            pos += pubkeyPrefixSpace.length();
        }
    } else {
        pos += pubkeyPrefix.length();
    }

    if (pos == std::string::npos || pos >= req.body.size()) {
        return "";
    }

    // Extract exactly 64 hex characters (32 bytes = Nostr pubkey)
    if (pos + 64 > req.body.size()) {
        return "";
    }

    std::string pubkey = req.body.substr(pos, 64);

    // Validate it's all hex characters
    for (char c : pubkey) {
        if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'))) {
            return "";
        }
    }

    return pubkey;
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

void HttpServer::decrementActiveRequests()
{
    // Release ordering ensures all request processing is visible before decrement
    // Notify condition variable if this was the last active request during shutdown
    if (activeRequests_.fetch_sub(1, std::memory_order_release) == 1 &&
        shuttingDown_.load(std::memory_order_acquire)) {
        shutdownCv_.notify_one();
    }
}

void HttpServer::setupSecurityHeaders(httplib::Response& res)
{
    res.set_header("X-Content-Type-Options", "nosniff");
    res.set_header("X-Frame-Options", "DENY");
    res.set_header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'");
    res.set_header("X-XSS-Protection", "1; mode=block");
    res.set_header("Referrer-Policy", "no-referrer");
}

void HttpServer::setupCorsHeaders(httplib::Response& res)
{
    if (enableCors) {
        res.set_header("Access-Control-Allow-Origin", corsOrigin);
        res.set_header("Access-Control-Allow-Methods", "POST, GET, OPTIONS");
        res.set_header("Access-Control-Allow-Headers", "Content-Type");
    }
}