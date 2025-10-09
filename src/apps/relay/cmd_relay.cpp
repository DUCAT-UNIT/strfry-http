#include <pthread.h>
#include <signal.h>
#include <sstream>
#include <cstdlib>

#include "HttpServer.h"
#include "RelayServer.h"
#include "PackedEvent.h"
#include "events.h"

static void checkConfig()
{
    if (cfg().relay__info__pubkey.size()) {
        try {
            auto p = from_hex(cfg().relay__info__pubkey);
            if (p.size() != 32) {
                throw herr("bad size");
            }
        } catch (std::exception& e) {
            LW << "Your relay.info.pubkey is incorrectly formatted. It should be 64 hex digits.";
        }
    }

    if (cfg().events__rejectEphemeralEventsOlderThanSeconds >= cfg().events__ephemeralEventsLifetimeSeconds) {
        LW << "rejectEphemeralEventsOlderThanSeconds is >= ephemeralEventsLifetimeSeconds, which could result in unnecessary disk activity";
    }
}

void cmd_relay(const std::vector<std::string>& subArgs)
{
    RelayServer s;
    s.run();
}

void RelayServer::run()
{
    {
        sigset_t set;
        sigemptyset(&set);
        sigaddset(&set, SIGUSR1);
        int s = pthread_sigmask(SIG_BLOCK, &set, NULL);
        if (s != 0) {
            throw herr("Unable to set sigmask: ", strerror(errno));
        }
    }

    tpWebsocket.init("Websocket", 1, [this](auto& thr) {
        runWebsocket(thr);
    });

    tpIngester.init("Ingester", cfg().relay__numThreads__ingester, [this](auto& thr) {
        runIngester(thr);
    });

    tpWriter.init("Writer", 1, [this](auto& thr) {
        runWriter(thr);
    });

    tpReqWorker.init("ReqWorker", cfg().relay__numThreads__reqWorker, [this](auto& thr) {
        runReqWorker(thr);
    });

    tpReqMonitor.init("ReqMonitor", cfg().relay__numThreads__reqMonitor, [this](auto& thr) {
        runReqMonitor(thr);
    });

    tpNegentropy.init("Negentropy", cfg().relay__numThreads__negentropy, [this](auto& thr) {
        runNegentropy(thr);
    });

    cronThread = std::thread([this] {
        runCron();
    });

    signalHandlerThread = std::thread([this] {
        runSignalHandler();
    });

    checkConfig();

    if (cfg().relay__http__enabled) {
        LI << "Starting HTTP server thread";
        httpThread = std::thread([this]() {
            try {
                std::this_thread::sleep_for(std::chrono::milliseconds(100));
                runHttpServer();
            } catch (const std::exception& e) {
                LE << "HTTP server error: " << e.what();
            }
        });
    }

    auto configFileChangeWatcher = hoytech::file_change_monitor(configFile);
    configFileChangeWatcher.setDebounce(100);
    configFileChangeWatcher.run([&]() {
        loadConfig(configFile);
        checkConfig();
    });

    tpWebsocket.join();
}

void RelayServer::runHttpServer()
{
    if (!cfg().relay__http__enabled) {
        return;
    }

    httpServer = std::make_unique<HttpServer>(
        cfg().relay__http__port,
        env.lmdb_env,
        env.dbi_Event__id,
        cfg().relay__http__bind,
        cfg().relay__http__cors
    );
    
    LI << "HTTP API enabled on " << cfg().relay__http__bind << ":" << cfg().relay__http__port;

    httpServer->setEventProcessor([this](const std::string& eventJson, std::string& errorMsg) -> bool {
        try {
            LI << "HTTP: Received event for processing";
            
            auto eventData = tao::json::from_string(eventJson);
            
            if (!eventData.is_object()) {
                errorMsg = "Event is not a JSON object";
                return false;
            }
            
            std::string eventId;
            try {
                eventId = eventData.at("id").get_string();
                eventData.at("pubkey").get_string();
                eventData.at("sig").get_string();
            } catch (const std::exception& e) {
                errorMsg = std::string("Missing required fields: ") + e.what();
                return false;
            }
            
            auto wrappedEvent = tao::json::value::array({"EVENT", eventData});
            std::string wrappedPayload = tao::json::to_string(wrappedEvent);
            
            uint64_t httpConnId = 0;
            
            auto resultPromise = std::make_shared<std::promise<ProcessResult>>();
            auto resultFuture = resultPromise->get_future();
            
            tpIngester.dispatch(0, MsgIngester{MsgIngester::ClientMessage{
                httpConnId,
                "http-post",
                std::move(wrappedPayload),
                resultPromise
            }});
            
            if (resultFuture.wait_for(std::chrono::seconds(5)) == std::future_status::timeout) {
                errorMsg = "Event processing timeout";
                LE << "HTTP event processing timeout: " << eventId;
                return false;
            }
            
            ProcessResult result = resultFuture.get();
            
            if (!result.success) {
                errorMsg = result.message;
                LE << "HTTP event rejected: " << errorMsg;
                return false;
            }
            
            LI << "HTTP event accepted: " << eventId;
            return true;
            
        } catch (const std::exception& e) {
            errorMsg = std::string("Processing error: ") + e.what();
            LE << "HTTP event error: " << errorMsg;
            return false;
        }
    });

    LI << "Starting HTTP server on " << cfg().relay__http__bind << ":" << cfg().relay__http__port;
    httpServer->start();
}