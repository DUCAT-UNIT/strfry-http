#pragma once

#include <string>
#include <chrono>
#include <random>
#include <sstream>
#include <iomanip>

class RequestLogger {
public:
    // Generate a unique request ID
    static std::string generateRequestId() {
        static std::random_device rd;
        static std::mt19937_64 gen(rd());
        static std::uniform_int_distribution<uint64_t> dis;

        uint64_t id = dis(gen);
        std::stringstream ss;
        ss << std::hex << std::setw(16) << std::setfill('0') << id;
        return ss.str();
    }

    // Get current timestamp in milliseconds
    static uint64_t getTimestampMs() {
        auto now = std::chrono::system_clock::now();
        auto ms = std::chrono::duration_cast<std::chrono::milliseconds>(now.time_since_epoch());
        return ms.count();
    }

    // Format duration in milliseconds
    static std::string formatDuration(uint64_t startMs) {
        uint64_t endMs = getTimestampMs();
        uint64_t duration = endMs - startMs;
        return std::to_string(duration) + "ms";
    }
};
