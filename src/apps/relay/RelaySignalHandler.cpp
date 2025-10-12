#include <signal.h>

#include "RelayServer.h"


void RelayServer::runSignalHandler() {
    setThreadName("signalHandler");

    sigset_t sigset;
    sigemptyset(&sigset);
    sigaddset(&sigset, SIGUSR1);
    sigaddset(&sigset, SIGTERM);
    sigaddset(&sigset, SIGINT);

    while (1) {
        int sig;
        int s = sigwait(&sigset, &sig);
        if (s != 0) throw herr("unable to sigwait: ", strerror(errno));

        if (sig == SIGUSR1) {
            LI << "Received SIGUSR1, initiating graceful shutdown...";
            tpWebsocket.dispatch(0, MsgWebsocket{MsgWebsocket::GracefulShutdown{}});
            hubTrigger->send();

            // Shutdown HTTP server gracefully
            if (httpServer) {
                httpServer->gracefulShutdown(10);
            }
        } else if (sig == SIGTERM || sig == SIGINT) {
            const char* signame = (sig == SIGTERM) ? "SIGTERM" : "SIGINT";
            LI << "Received " << signame << ", initiating graceful shutdown...";

            // Shutdown HTTP server first
            if (httpServer) {
                httpServer->gracefulShutdown(10);
            }

            // Then shutdown websocket
            tpWebsocket.dispatch(0, MsgWebsocket{MsgWebsocket::GracefulShutdown{}});
            hubTrigger->send();

            // Exit the signal handler loop to allow main thread to complete cleanup
            LI << "Graceful shutdown initiated, exiting signal handler";
            break;
        } else {
            LW << "Got unexpected signal: " << sig;
        }
    }
}
