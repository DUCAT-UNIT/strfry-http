#pragma once

#include <string>
#include <memory>
#include <functional>
#include "golpe.h"
#include "DBQuery.h"

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

private:
    std::unique_ptr<httplib::Server> server;
    uint16_t port;
    std::string bindAddr;
    bool enableCors;
    std::function<bool(const std::string&, std::string&)> eventProcessor;
    
    lmdb::env& env;
    lmdb::dbi& dbi_dbById;

    void setupRoutes();
    void handleEventPost(const httplib::Request& req, httplib::Response& res);
    void handleGetQuote(const httplib::Request& req, httplib::Response& res);
    void handleHealthCheck(const httplib::Request& req, httplib::Response& res);
    void handleQuery(const httplib::Request& req, httplib::Response& res);
    bool validateNostrEvent(const std::string& jsonStr);
};