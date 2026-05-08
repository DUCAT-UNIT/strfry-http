#include "RelayServer.h"

#include "PluginEventSifter.h"


void RelayServer::runWriter(ThreadPool<MsgWriter>::Thread &thr) {
    PluginEventSifter writePolicyPlugin;
    NegentropyFilterCache neFilterCache;

    while(1) {
        auto newMsgs = thr.inbox.pop_all();

        // Filter out messages from already closed sockets

        {
            flat_hash_set<uint64_t> closedConns;

            for (auto &newMsg : newMsgs) {
                if (auto msg = std::get_if<MsgWriter::CloseConn>(&newMsg.msg)) closedConns.insert(msg->connId);
            }

            if (closedConns.size()) {
                decltype(newMsgs) newMsgsFiltered;

                for (auto &newMsg : newMsgs) {
                    if (auto msg = std::get_if<MsgWriter::AddEvent>(&newMsg.msg)) {
                        if (!closedConns.contains(msg->connId)) newMsgsFiltered.emplace_back(std::move(newMsg));
                    }
                }

                std::swap(newMsgs, newMsgsFiltered);
            }
        }

        // Prepare messages

        std::vector<EventToWrite> newEvents;

        for (auto &newMsg : newMsgs) {
            if (auto msg = std::get_if<MsgWriter::AddEvent>(&newMsg.msg)) {
                tao::json::value evJson = tao::json::from_string(msg->jsonStr);
                EventSourceType sourceType = msg->ipAddr.size() == 4 ? EventSourceType::IP4 : EventSourceType::IP6;
                std::string okMsg;
                auto res = writePolicyPlugin.acceptEvent(cfg().relay__writePolicy__plugin, evJson, sourceType, msg->ipAddr, okMsg);

                if (res == PluginEventSifterResult::Accept) {
                    newEvents.emplace_back(std::move(msg->packedStr), std::move(msg->jsonStr), msg);
                } else {
                    PackedEventView packed(msg->packedStr);
                    auto eventIdHex = to_hex(packed.id());

                    if (okMsg.size()) LI << "[" << msg->connId << "] write policy blocked event " << eventIdHex << ": " << okMsg;

                    // For HTTP requests, resolve the promise with rejection
                    if (msg->resultPromise) {
                        msg->resultPromise->set_value({false, okMsg.empty() ? "blocked by write policy" : okMsg, eventIdHex});
                    }
                    sendOKResponse(msg->connId, eventIdHex, res == PluginEventSifterResult::ShadowReject, okMsg);
                }
            }
        }

        if (!newEvents.size()) continue;

        // Do write

        try {
            auto txn = env.txn_rw();
            writeEvents(txn, neFilterCache, newEvents);
            txn.commit();
        } catch (std::exception &e) {
            LE << "Error writing " << newEvents.size() << " events: " << e.what();

            for (auto &newEvent : newEvents) {
                PackedEventView packed(newEvent.packedStr);
                auto eventIdHex = to_hex(packed.id());
                MsgWriter::AddEvent *addEventMsg = static_cast<MsgWriter::AddEvent*>(newEvent.userData);

                std::string message = "Write error: ";
                message += e.what();

                // For HTTP requests, resolve the promise with error
                if (addEventMsg->resultPromise) {
                    addEventMsg->resultPromise->set_value({false, message, eventIdHex});
                }
                sendOKResponse(addEventMsg->connId, eventIdHex, false, message);
            }

            continue;
        }

        // Log and resolve HTTP promises after actual persistence

        for (auto &newEvent : newEvents) {
            PackedEventView packed(newEvent.packedStr);
            auto eventIdHex = to_hex(packed.id());
            std::string message;
            bool written = false;
            bool success = false;

            if (newEvent.status == EventWriteStatus::Written) {
                LI << "Inserted event. id=" << eventIdHex << " levId=" << newEvent.levId;
                written = true;
                success = true;
                message = "";
            } else if (newEvent.status == EventWriteStatus::Duplicate) {
                message = "duplicate: have this event";
                written = true;
                success = true;  // Duplicates are considered successful (event exists)
            } else if (newEvent.status == EventWriteStatus::Replaced) {
                message = "replaced: have newer event";
                success = false;  // Event was NOT persisted
            } else if (newEvent.status == EventWriteStatus::Deleted) {
                message = "deleted: user requested deletion";
                success = false;  // Event was NOT persisted
            }

            if (newEvent.status != EventWriteStatus::Written) {
                LI << "Rejected event. " << message << ", id=" << eventIdHex;
            }

            MsgWriter::AddEvent *addEventMsg = static_cast<MsgWriter::AddEvent*>(newEvent.userData);

            // For HTTP requests, resolve the promise with the actual write result
            // This ensures HTTP callers know whether the event was truly persisted
            if (addEventMsg->resultPromise) {
                addEventMsg->resultPromise->set_value({success, message, eventIdHex});
            }

            sendOKResponse(addEventMsg->connId, eventIdHex, written, message);
        }
    }
}
