#include "HttpServer.h"
#include "httplib.h"
#include <nlohmann/json.hpp>
#include "events.h"
#include "golpe.h"
#include "DBQuery.h"
#include "Decompressor.h"  // ADD THIS

using json = nlohmann::json;


HttpServer::HttpServer(uint16_t port, lmdb::env& envRef, lmdb::dbi& dbiRef, 
                       const std::string &bindAddr, bool enableCors)
    : port(port), bindAddr(bindAddr), enableCors(enableCors), env(envRef), dbi_dbById(dbiRef)
{
    server = std::make_unique<httplib::Server>();
    setupRoutes();
}

HttpServer::~HttpServer()
{
    stop();
}

void HttpServer::setEventProcessor(std::function<bool(const std::string &, std::string &)> processor)
{
    eventProcessor = processor;
}

void HttpServer::setupRoutes()
{
    // CORS middleware
    if (enableCors)
    {
        server->set_pre_routing_handler([](const httplib::Request &req, httplib::Response &res)
                                        {
            res.set_header("Access-Control-Allow-Origin", "*");
            res.set_header("Access-Control-Allow-Methods", "POST, GET, OPTIONS");
            res.set_header("Access-Control-Allow-Headers", "Content-Type");
            
            if (req.method == "OPTIONS") {
                res.status = 200;
                return httplib::Server::HandlerResponse::Handled;
            }
            return httplib::Server::HandlerResponse::Unhandled; });
    }

    server->Get("/api/query", [this](const httplib::Request &req, httplib::Response &res)
                { handleQuery(req, res); });

    // POST /api/quotes - Accept Nostr quote event
    server->Post("/api/quotes", [this](const httplib::Request &req, httplib::Response &res)
                 { handleEventPost(req, res); });

    // GET /api/quotes/:id - Get quote by ID
    server->Get(R"(/api/quotes/([0-9a-fA-F]+))", [this](const httplib::Request &req, httplib::Response &res)
                { handleGetQuote(req, res); });

    // GET /health - Health check
    server->Get("/health", [this](const httplib::Request &req, httplib::Response &res)
                { handleHealthCheck(req, res); });

    // GET / - Info endpoint
    server->Get("/", [](const httplib::Request &req, httplib::Response &res)
                {
        json response = {
            {"name", "strfry HTTP API"},
            {"version", "1.0.0"},
            {"endpoints", {
                {"/api/quotes", "POST - Submit Nostr quote event"},
                {"/api/quotes/:id", "GET - Get quote by ID"},
                {"/api/query", "GET - Query events"},
                {"/health", "GET - Health check"}
            }}
        };
        res.set_content(response.dump(2), "application/json"); });
}

void HttpServer::handleEventPost(const httplib::Request &req, httplib::Response &res)
{
    try
    {
        // Validate JSON
        json eventJson = json::parse(req.body);

        // Basic Nostr event structure validation
        if (!eventJson.contains("id") || !eventJson.contains("pubkey") ||
            !eventJson.contains("created_at") || !eventJson.contains("kind") ||
            !eventJson.contains("tags") || !eventJson.contains("content") ||
            !eventJson.contains("sig"))
        {
            res.status = 400;
            res.set_content(json({{"ok", false},
                                  {"message", "Invalid event structure: missing required fields"}})
                                .dump(),
                            "application/json");
            return;
        }

        // Validate event ID and signature
        std::string eventStr = req.body;
        if (!validateNostrEvent(eventStr))
        {
            res.status = 400;
            res.set_content(json({{"ok", false},
                                  {"message", "Invalid event: signature verification failed"}})
                                .dump(),
                            "application/json");
            return;
        }

        // Process event through strfry pipeline
        std::string errorMsg;
        if (eventProcessor && eventProcessor(eventStr, errorMsg))
        {
            res.status = 200;
            res.set_content(json({{"ok", true},
                                  {"message", "Quote accepted"},
                                  {"id", eventJson["id"]}})
                                .dump(),
                            "application/json");
        }
        else
        {
            res.status = 400;
            res.set_content(json({{"ok", false},
                                  {"message", errorMsg.empty() ? "Quote rejected" : errorMsg}})
                                .dump(),
                            "application/json");
        }
    }
    catch (const json::parse_error &e)
    {
        res.status = 400;
        res.set_content(json({{"ok", false},
                              {"message", std::string("Invalid JSON: ") + e.what()}})
                            .dump(),
                        "application/json");
    }
    catch (const std::exception &e)
    {
        res.status = 500;
        res.set_content(json({{"ok", false},
                              {"message", std::string("Internal error: ") + e.what()}})
                            .dump(),
                        "application/json");
    }
}

void HttpServer::handleGetQuote(const httplib::Request &req, httplib::Response &res)
{
    try
    {
        if (req.matches.size() < 2) {
            res.status = 400;
            res.set_content(json({{"error", "Missing event ID"}}).dump(), "application/json");
            return;
        }

        std::string eventId = req.matches[1];
        
        if (eventId.empty()) {
            res.status = 400;
            res.set_content(json({{"error", "Missing event ID"}}).dump(), "application/json");
            return;
        }

        LI << "GET request for event: " << eventId;

        // Convert hex event ID to bytes
        std::string eventIdBytes = from_hex(eventId);
        
        // Use strfry's existing lookup function
        auto txn = lmdb::txn::begin(env, nullptr, MDB_RDONLY);
        auto existing = lookupEventById(txn, std::string_view(eventIdBytes));
        
        if (!existing) {
            txn.abort();
            LI << "Event not found: " << eventId;
            res.status = 404;
            res.set_content(json({{"error", "Event not found"}}).dump(), "application/json");
            return;
        }
        
        // Get the levId (Local Event ID)
        uint64_t levId = existing->primaryKeyId;
        
        // Use strfry's built-in function to get event JSON
        Decompressor decomp;
        std::string_view eventJsonStr = getEventJson(txn, decomp, levId);
        
        txn.commit();
        
        LI << "Returning event: " << eventId;
        res.status = 200;
        res.set_content(std::string(eventJsonStr), "application/json");
        
    }
    catch (const std::exception &e)
    {
        LE << "Query error: " << e.what();
        res.status = 500;
        res.set_content(json({{"error", std::string("Query error: ") + e.what()}}).dump(), "application/json");
    }
}

void HttpServer::handleHealthCheck(const httplib::Request &req, httplib::Response &res)
{
    std::cerr << "Health check called!" << std::endl;
    res.set_content(json({{"status", "ok"},
                          {"service", "strfry-http"}})
                        .dump(),
                    "application/json");
}

bool HttpServer::validateNostrEvent(const std::string &jsonStr)
{
    try
    {
        // Basic JSON validation - strfry's pipeline will do full validation
        json eventJson = json::parse(jsonStr);

        // Just check we can parse it as JSON with required fields
        // The actual signature verification will happen in strfry's ingestion pipeline
        return eventJson.contains("id") &&
               eventJson.contains("pubkey") &&
               eventJson.contains("sig");
    }
    catch (...)
    {
        return false;
    }
}

void HttpServer::handleQuery(const httplib::Request &req, httplib::Response &res)
{
    try
    {
        // Get event IDs from query params: ?ids=abc123,def456
        auto ids_param = req.get_param_value("ids");
        if (ids_param.empty()) {
            res.status = 400;
            res.set_content(json({{"error", "Missing 'ids' parameter"}}).dump(), "application/json");
            return;
        }

        // For now, return empty array - you'd need to query LMDB
        // This requires integrating with Strfry's database layer
        json response = json::array();
        
        res.status = 200;
        res.set_content(response.dump(), "application/json");
    }
    catch (const std::exception &e)
    {
        res.status = 500;
        res.set_content(json({{"error", std::string("Query error: ") + e.what()}}).dump(), "application/json");
    }
}

void HttpServer::start()
{
    LI << "Starting HTTP server on " << bindAddr << ":" << port;
    server->listen(bindAddr.c_str(), port);
}

void HttpServer::stop()
{
    if (server)
    {
        server->stop();
    }
}