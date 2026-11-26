#include "NativeBridge.h"
#include "ggml-backend.h"
#include "ggml-rpc.h"
#include "ggml-metal.h"
#include <os/proc.h>
#include <atomic>
#include <mutex>
#include <thread>
#include <string>
#include <unistd.h>

static std::mutex lifecycle;
static std::thread worker;
static std::atomic<bool> running{false};

extern "C" uint64_t NodexAvailableMemory(void) {
    return os_proc_available_memory();
}

extern "C" int NodexMetalStart(const char *path) {
    std::lock_guard<std::mutex> lock(lifecycle);
    if (!path || path[0] != '/' || std::string(path).size() > 100 || running.load()) return 0;
    if (worker.joinable()) worker.join();
    try {
    ggml_backend_load_all();
    auto reg = ggml_backend_metal_reg();
    if (!reg || ggml_backend_reg_dev_count(reg) == 0) return 0;
    auto device = ggml_backend_reg_dev_get(reg, 0);
    auto probe = ggml_backend_dev_init(device, nullptr);
    if (!probe) return 0;
    ggml_backend_free(probe);
    unlink(path);
    ggml_backend_rpc_android_reset_stop();
    running.store(true);
        worker = std::thread([device, endpoint = std::string("unix:") + path] {
            auto selected = device;
            try { ggml_backend_rpc_start_server(endpoint.c_str(), nullptr, 2, 1, &selected); } catch (...) {}
            running.store(false);
        });
    } catch (...) {
        running.store(false);
        return 0;
    }
    return 1;
}

extern "C" void NodexMetalStop(void) {
    std::lock_guard<std::mutex> lock(lifecycle);
    ggml_backend_rpc_android_request_stop();
    if (worker.joinable()) worker.join();
    running.store(false);
}

extern "C" int NodexMetalRunning(void) { return running.load() ? 1 : 0; }
