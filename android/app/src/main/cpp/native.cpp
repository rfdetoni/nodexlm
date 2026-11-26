// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
#include <jni.h>
#include <algorithm>
#include <atomic>
#include <memory>
#include <mutex>
#include <string>
#include <vector>
#include <utility>
#include <cstring>
#include <thread>
#include <chrono>
#include <condition_variable>
#include <exception>
#include <sstream>
#include <fstream>
#include <dlfcn.h>
#include <sched.h>
#include <android/log.h>
#include "llama.h"
#include "ggml-backend.h"
#include "ggml-rpc.h"
#include "prompt_prefix.h"

namespace {
std::mutex guard;
std::atomic<bool> cancelled{false};
std::once_flag initialized;
std::mutex backendLoadMutex;
bool cpuLoadAttempted = false;
bool gpuLoadAttempted = false;
std::string backendLibDir;
std::atomic<bool> rpcStarted{false};
std::mutex rpcThreadMutex;
std::condition_variable rpcFinished;
std::thread rpcThread;
// 1 = Vulkan, 2 = CPU. Selected for this RPC server, not measured speed.
std::atomic<int> activeRPCBackends{0};

bool isGpu(ggml_backend_dev_t d) {
    const auto type = ggml_backend_dev_type(d);
    return type == GGML_BACKEND_DEVICE_TYPE_GPU || type == GGML_BACKEND_DEVICE_TYPE_IGPU;
}

// Do not enumerate all modules: registering Vulkan can initialize a broken
// driver even when the caller explicitly requested CPU.
ggml_backend_reg_t loadNamedBackend(const char* name) {
    if (!backendLibDir.empty()) {
        const std::string path = backendLibDir + "/" + name;
        if (auto reg = ggml_backend_load(path.c_str())) return reg;
    }
    return ggml_backend_load(name);
}

void loadBackendsOnce(bool allowGpu) {
    std::lock_guard<std::mutex> lock(backendLoadMutex);
    if (!cpuLoadAttempted) {
        cpuLoadAttempted = true;
        // With extractNativeLibs=false (useLegacyPackaging=false), .so libraries live
        // directly inside base.apk!/lib/<abi>/ rather than files in nativeLibraryDir,
        // so directory enumeration finds nothing. Probe bare library names via Android's
        // classloader linker namespace and select the highest-scoring ARM64 CPU variant.
        if (!ggml_backend_dev_by_type(GGML_BACKEND_DEVICE_TYPE_CPU)) {
            static constexpr const char* kArm64CpuVariants[] = {
                "libggml-cpu-android_armv9.2_2.so",
                "libggml-cpu-android_armv9.2_1.so",
                "libggml-cpu-android_armv9.0_1.so",
                "libggml-cpu-android_armv8.6_1.so",
                "libggml-cpu-android_armv8.2_2.so",
                "libggml-cpu-android_armv8.2_1.so",
                "libggml-cpu-android_armv8.0_1.so",
            };
            using ScoreFn = int (*)();
            int bestScore = 0;
            const char* bestVariant = nullptr;
            for (const char* name : kArm64CpuVariants) {
                void* handle = nullptr;
                if (!backendLibDir.empty()) {
                    const std::string path = backendLibDir + "/" + name;
                    handle = dlopen(path.c_str(), RTLD_NOW | RTLD_LOCAL);
                }
                if (!handle) handle = dlopen(name, RTLD_NOW | RTLD_LOCAL);
                if (!handle) continue;
                auto scoreFn = reinterpret_cast<ScoreFn>(dlsym(handle, "ggml_backend_score"));
                const int score = scoreFn ? scoreFn() : 0;
                dlclose(handle);
                if (score > bestScore) {
                    bestScore = score;
                    bestVariant = name;
                }
            }
            if (bestVariant) {
                if (loadNamedBackend(bestVariant)) {
                    __android_log_print(ANDROID_LOG_INFO, "NodexLM-RPC",
                        "Loaded dynamic CPU backend %s (score=%d)", bestVariant, bestScore);
                }
            }
        }
        if (!ggml_backend_dev_by_type(GGML_BACKEND_DEVICE_TYPE_CPU)) {
            loadNamedBackend("libggml-cpu.so");
        }
    } else if (!allowGpu || gpuLoadAttempted) {
        return;
    }
    if (allowGpu && !gpuLoadAttempted) {
        gpuLoadAttempted = true;
        bool hasGpu = false;
        for (size_t i = 0; i < ggml_backend_dev_count(); ++i) {
            auto dev = ggml_backend_dev_get(i);
            if (dev && isGpu(dev)) { hasGpu = true; break; }
        }
        if (!hasGpu && loadNamedBackend("libggml-vulkan.so")) {
            __android_log_print(ANDROID_LOG_INFO, "NodexLM-RPC", "Loaded dynamic Vulkan backend");
        }
    }
    for (size_t i = 0; i < ggml_backend_dev_count(); ++i) {
        auto dev = ggml_backend_dev_get(i);
        if (dev) {
            __android_log_print(ANDROID_LOG_INFO, "NodexLM-RPC",
                "Registered GGML backend device[%zu]: %s (%s)",
                i, ggml_backend_dev_name(dev), ggml_backend_dev_description(dev));
        }
    }
}

jbyteArray bytes(JNIEnv* env,const std::string& value) {
    auto result=env->NewByteArray((jsize)value.size());
    if (result) env->SetByteArrayRegion(result,0,(jsize)value.size(),(const jbyte*)value.data());
    return result;
}
llama_model* model = nullptr;
llama_context* ctx = nullptr;
const llama_vocab* vocab = nullptr;
// Protected by guard; records only successfully decoded tokens in sequence 0.
std::vector<llama_token> cachedTokens;
void resetPromptCache() {
    cachedTokens.clear();
    if (ctx) llama_memory_clear(llama_get_memory(ctx), false);
}

std::string utf(JNIEnv* env,jstring str) {
    if (!str) return {};
    // Use Java's standard UTF-8 encoder, not JNI modified UTF-8 (emoji).
    jclass stringClass=env->GetObjectClass(str);
    jmethodID getBytes=env->GetMethodID(stringClass,"getBytes","(Ljava/lang/String;)[B");
    jstring charset=env->NewStringUTF("UTF-8");
    auto array=(jbyteArray)env->CallObjectMethod(str,getBytes,charset);
    std::string result;
    if(array&&!env->ExceptionCheck()) { result.resize(env->GetArrayLength(array));env->GetByteArrayRegion(array,0,(jsize)result.size(),reinterpret_cast<jbyte*>(result.data())); }
    if(array)env->DeleteLocalRef(array);
    env->DeleteLocalRef(charset);env->DeleteLocalRef(stringClass);
    return result;
}
void release() {
    cachedTokens.clear();
    if(ctx){llama_free(ctx);ctx=nullptr;}
    if(model){llama_model_free(model);model=nullptr;}
    vocab=nullptr;
}
// A small, bounded parser of message arrays is preferable to untrusted prompt
// template concatenation; the Java service passes validated JSON messages.
// The native shim expects a serialized prompt; it is formatted by Kotlin.
}

extern "C" JNIEXPORT jstring JNICALL
Java_dev_nodexlm_app_NativeEntry_load(JNIEnv* env,jobject,jstring path,jint contextTokens,jint threads,jboolean useGpu) {
    std::lock_guard<std::mutex> lock(guard);
    const std::string file=utf(env,path);
    if(file.empty()) return env->NewStringUTF("model path required");
    release();
    cancelled.store(false);
    loadBackendsOnce(useGpu == JNI_TRUE);
    if (!ggml_backend_dev_by_type(GGML_BACKEND_DEVICE_TYPE_CPU))
        return env->NewStringUTF("GGML CPU backend unavailable");
    std::call_once(initialized, [] { llama_backend_init(); });
    auto params=llama_model_default_params();
    params.n_gpu_layers=0; // CPU path first; Vulkan GPU is opt-in when faster on the device.
    if (useGpu == JNI_TRUE) {
        for (size_t i = 0; i < ggml_backend_dev_count(); ++i) {
            auto dev = ggml_backend_dev_get(i);
            if (!dev || !isGpu(dev)) continue;
            ggml_backend_t probe = nullptr;
            try {
                probe = ggml_backend_dev_init(dev, nullptr);
            } catch (...) {
                probe = nullptr;
            }
            if (probe) {
                ggml_backend_free(probe);
                size_t freeBytes = 0, totalBytes = 0;
                ggml_backend_dev_memory(dev, &freeBytes, &totalBytes);
                constexpr size_t minVulkanFree = 512ULL * 1024 * 1024;
                if (totalBytes > 0 && freeBytes >= minVulkanFree) {
                    params.n_gpu_layers = 999;
                }
                break;
            }
        }
    }
    params.load_mode=LLAMA_LOAD_MODE_MMAP;
    model=llama_model_load_from_file(file.c_str(),params);
    if(!model)return env->NewStringUTF("GGUF load failed (memory, format, or path)");
    auto opts=llama_context_default_params();
    opts.n_ctx=std::clamp<int>(contextTokens,512,4096);
    opts.n_batch=std::min<uint32_t>(512,opts.n_ctx);
    opts.n_ubatch=opts.n_batch;
    opts.n_threads=std::clamp<int>(threads,1,8);
    opts.n_threads_batch=opts.n_threads;
    opts.flash_attn_type=LLAMA_FLASH_ATTN_TYPE_AUTO;
    opts.type_k=GGML_TYPE_Q8_0;
    opts.type_v=GGML_TYPE_Q8_0;
    ctx=llama_init_from_model(model,opts);
    if(!ctx){release();return env->NewStringUTF("Failed to allocate inference context");}
    vocab=llama_model_get_vocab(model);
    return env->NewStringUTF("");
}

extern "C" JNIEXPORT jbyteArray JNICALL
Java_dev_nodexlm_app_NativeEntry_generate(JNIEnv* env,jobject,jobjectArray javaRoles,jobjectArray javaContents,jint tokenLimit,jfloat temperature,jfloat topP,jobjectArray javaStops,jobject sink) {
    std::lock_guard<std::mutex> lock(guard);
    if(!ctx||!model)return bytes(env,"NODEXLM_ERROR: no loaded model");
    if(!javaRoles || !javaContents)return bytes(env,"NODEXLM_ERROR: missing messages");
    // A prior cancel only interrupts its generation, not the next request.
    cancelled.store(false);
    const jsize count=env->GetArrayLength(javaRoles);
    if(count<=0||count>64||env->GetArrayLength(javaContents)!=count)return bytes(env,"NODEXLM_ERROR: invalid messages");
    std::vector<std::string> stops;
    if(javaStops){const jsize stopCount=env->GetArrayLength(javaStops);if(stopCount>4)return bytes(env,"NODEXLM_ERROR: too many stops");for(jsize i=0;i<stopCount;i++){auto stop=(jstring)env->GetObjectArrayElement(javaStops,i);stops.push_back(utf(env,stop));env->DeleteLocalRef(stop);}}
    jmethodID tokenCallback=nullptr;
    if(sink){jclass type=env->GetObjectClass(sink);tokenCallback=env->GetMethodID(type,"onToken","([B)V");env->DeleteLocalRef(type);if(!tokenCallback)return nullptr;}
    std::vector<std::string> roles;roles.reserve(count);
    std::vector<std::string> contents;contents.reserve(count);
    for(jsize i=0;i<count;i++){
        auto r=(jstring)env->GetObjectArrayElement(javaRoles,i);
        auto c=(jstring)env->GetObjectArrayElement(javaContents,i);
        roles.push_back(utf(env,r));contents.push_back(utf(env,c));
        env->DeleteLocalRef(r);env->DeleteLocalRef(c);
        if(contents.back().size()>32768)return bytes(env,"NODEXLM_ERROR: message too large");
        if(roles.back()!="system"&&roles.back()!="user"&&roles.back()!="assistant")return bytes(env,"NODEXLM_ERROR: invalid role");
    }
    std::vector<llama_chat_message> chat;
    for(jsize i=0;i<count;i++)chat.push_back({roles[i].c_str(),contents[i].c_str()});
    auto tmpl=llama_model_chat_template(model,nullptr);
    int need=llama_chat_apply_template(tmpl,chat.data(),chat.size(),true,nullptr,0);
    if(need<=0||need>131072)return bytes(env,"NODEXLM_ERROR: unsupported chat template");
    std::string prompt(need+1,'\0');
    int written=llama_chat_apply_template(tmpl,chat.data(),chat.size(),true,prompt.data(),(int)prompt.size());
    if(written<=0)return bytes(env,"NODEXLM_ERROR: chat formatting failed");
    prompt.resize(written);
    const int contextSize=(int)llama_n_ctx(ctx);
    const int maxOut=std::clamp<int>(tokenLimit,1,1024);
    const int32_t maxPrompt=contextSize-maxOut;
    if(maxPrompt<64)return bytes(env,"NODEXLM_ERROR: context too small");
    std::vector<llama_token> tokens((size_t)maxPrompt);
    int n=llama_tokenize(vocab,prompt.c_str(),(int32_t)prompt.size(),tokens.data(),maxPrompt,true,false);
    if(n<=0)return bytes(env,"NODEXLM_ERROR: prompt too long or tokenization failed");
    tokens.resize(n);
    // Append-only reuse avoids unsafe partial rewinds on recurrent/hybrid or
    // sliding-window models. Identical prompts are replayed for fresh logits.
    const size_t reused = nodexlm::appendablePrefix(cachedTokens, tokens);
    if (!reused) resetPromptCache();
    cachedTokens.reserve(static_cast<size_t>(contextSize));
    const int batchSize = std::max(1, static_cast<int>(llama_n_batch(ctx)));
    for (int i=static_cast<int>(reused);i<n;) {
        if (cancelled.load()) {
            resetPromptCache();
            return bytes(env,"");
        }
        const int count=std::min(batchSize,n-i);
        auto batch=llama_batch_get_one(tokens.data()+i,count);
        if(llama_decode(ctx,batch)!=0) {
            resetPromptCache();
            return bytes(env,"NODEXLM_ERROR: prompt decode failed");
        }
        cachedTokens.insert(cachedTokens.end(),tokens.begin()+i,tokens.begin()+i+count);
        i+=count;
    }
    auto sampler=llama_sampler_chain_init(llama_sampler_chain_default_params());
    llama_sampler_chain_add(sampler,llama_sampler_init_top_p(std::clamp<float>(topP,0.0f,1.0f),1));
    llama_sampler_chain_add(sampler,llama_sampler_init_temp(std::clamp<float>(temperature,0.0f,2.0f)));
    llama_sampler_chain_add(sampler,llama_sampler_init_dist(LLAMA_DEFAULT_SEED));
    std::string output;
    size_t emitted=0;
    bool stopped=false;
    for (int i=0;i<maxOut && n+i<contextSize && !cancelled.load();i++) {
        llama_token next=llama_sampler_sample(sampler,ctx,-1);
        if(llama_vocab_is_eog(vocab,next))break;
        char piece[2048];
        int length=llama_token_to_piece(vocab,next,piece,sizeof(piece),0,false);
        if(length>0)output.append(piece,(size_t)length);
        size_t boundary=output.size();
        for(const auto& stop:stops){if(stop.empty())continue;auto found=output.find(stop);if(found!=std::string::npos){boundary=std::min(boundary,found);stopped=true;}}
        // Hold a possible stop-sequence prefix so it never leaks into SSE.
        size_t safe=boundary;
        if(!stopped)for(const auto& stop:stops){for(size_t n=1;n<stop.size()&&n<=output.size();n++){if(output.compare(output.size()-n,n,stop,0,n)==0)safe=std::min(safe,output.size()-n);}}
        if(sink&&safe>emitted){auto chunk=bytes(env,output.substr(emitted,safe-emitted));env->CallVoidMethod(sink,tokenCallback,chunk);env->DeleteLocalRef(chunk);emitted=safe;if(env->ExceptionCheck()){llama_sampler_free(sampler);resetPromptCache();return nullptr;}}
        if(stopped){output.resize(boundary);break;}
        if(llama_decode(ctx,llama_batch_get_one(&next,1))!=0) {
            resetPromptCache();
            break;
        }
        cachedTokens.push_back(next);
    }
    if(sink&&!cancelled.load()&&output.size()>emitted){auto chunk=bytes(env,output.substr(emitted));env->CallVoidMethod(sink,tokenCallback,chunk);env->DeleteLocalRef(chunk);}
    llama_sampler_free(sampler);
    if(cancelled.load() || env->ExceptionCheck()) resetPromptCache();
    if(env->ExceptionCheck())return nullptr;
    return bytes(env,output);
}

extern "C" JNIEXPORT void JNICALL
Java_dev_nodexlm_app_NativeEntry_unload(JNIEnv*,jobject) {
    std::lock_guard<std::mutex> lock(guard);
    release();
}

extern "C" JNIEXPORT void JNICALL
Java_dev_nodexlm_app_NativeEntry_cancel(JNIEnv*,jobject) {
    cancelled.store(true);
}


// Discover real ggml compute backends. Vulkan devices are reported only
// after the driver/ICD initialized successfully; CPU remains available when
// a phone lacks a suitable Vulkan implementation.
namespace {
static std::string jsonEscaped(const char* value) {
    std::string out;
    for(const unsigned char c : std::string(value ? value : "")) {
        if(c == '"' || c == '\\') {out += '\\'; out += static_cast<char>(c);}
        else if(c >= 32 && c < 127) out += static_cast<char>(c);
        else if(c >= 128) out += static_cast<char>(c);
    }
    return out;
}
}

extern "C" JNIEXPORT void JNICALL
Java_dev_nodexlm_app_NativeRpc_initBackends(JNIEnv *env, jobject, jstring libDirValue) {
    const std::string dir = utf(env, libDirValue);
    {
        std::lock_guard<std::mutex> lock(backendLoadMutex);
        if (!dir.empty() && backendLibDir.empty()) {
            backendLibDir = dir;
        }
    }
}

// The report is informational. Android integrated GPU memory can be shared
// with system RAM; the Host's memory planner uses a max rather than a sum.
extern "C" JNIEXPORT jstring JNICALL
Java_dev_nodexlm_app_NativeRpc_inspect(JNIEnv *env, jobject) {
    // UI probes and embedded inference may run in the same process.
    // Never register a GPU while the embedded context is generating.
    std::lock_guard<std::mutex> lock(guard);
    loadBackendsOnce(true);
    std::ostringstream s;
    s << "{\"gpus\":[";
    bool first = true;
    for(size_t i = 0; i < ggml_backend_dev_count(); ++i) {
        ggml_backend_dev_t d = ggml_backend_dev_get(i);
        if(!d || !isGpu(d)) continue;
        size_t freeBytes = 0, totalBytes = 0;
        ggml_backend_dev_memory(d, &freeBytes, &totalBytes);
        if(!first) s << ",";
        first = false;
        s << "{\"name\":\"" << jsonEscaped(ggml_backend_dev_name(d))
          << "\",\"vendor\":\"vulkan\",\"vram_bytes\":" << totalBytes
          << ",\"free_vram_bytes\":" << std::min(freeBytes,totalBytes) << "}";
    }
    s << "]}";
    return env->NewStringUTF(s.str().c_str());
}

// Run in an isolated Android :rpc process. Cooperative shutdown is attempted
// first; process termination remains the fallback for a stuck GPU driver.
// Mode: auto = ONE device (Vulkan if initialized, otherwise CPU).
// cpu = CPU only; gpu = Vulkan only; hybrid = Vulkan + CPU (explicit).
// Exposing both CPU and shared-memory Vulkan on Auto causes GGML to spread
// a single decode across two RPC devices on the same phone, multiplying
// synchronization and transfers each token without evidence of speed gain.
extern "C" JNIEXPORT jint JNICALL
Java_dev_nodexlm_app_NativeRpc_recommendedThreads(JNIEnv*, jobject, jint logical) {
    cpu_set_t allowed;
    CPU_ZERO(&allowed);
    const int fallback = std::clamp<int>(logical - 1, 1, 6);
    if (sched_getaffinity(0, sizeof(allowed), &allowed) != 0) return fallback;
    std::vector<unsigned long> capacities;
    for (int cpu = 0; cpu < CPU_SETSIZE; ++cpu) {
        if (!CPU_ISSET(cpu, &allowed)) continue;
        unsigned long capacity = 0;
        std::ifstream in("/sys/devices/system/cpu/cpu" + std::to_string(cpu) + "/cpu_capacity");
        if (!(in >> capacity) || capacity == 0 || capacity > 1000000) return fallback;
        capacities.push_back(capacity);
    }
    if (capacities.size() < 2) return fallback;
    const auto range = std::minmax_element(capacities.begin(), capacities.end());
    if (*range.first * 100 >= *range.second * 80) return fallback;
    const int performance = static_cast<int>(std::count_if(capacities.begin(), capacities.end(),
        [&](unsigned long n) { return n * 100 >= *range.second * 80; }));
    // Only choose a thread count; do not pin driver or compute threads to
    // potentially restricted cores. Explicit user settings take precedence.
    return std::clamp<int>(performance, 1, std::min<int>(6, std::max<int>(1, logical)));
}

// Restrict the CPU-only server to permitted fast cores. The RPC patch
// attaches a strict GGML worker pool to that same mask when supported.
// Never expand beyond the OS cpuset or bind Vulkan/driver threads.
static bool preferPerformanceRPCThreads(int threads) {
    cpu_set_t allowed, fast;
    CPU_ZERO(&allowed);
    CPU_ZERO(&fast);
    if (sched_getaffinity(0, sizeof(allowed), &allowed) != 0) return false;
    std::vector<std::pair<int, unsigned long>> capacities;
    for (int cpu = 0; cpu < CPU_SETSIZE; ++cpu) {
        if (!CPU_ISSET(cpu, &allowed)) continue;
        unsigned long capacity = 0;
        std::ifstream in("/sys/devices/system/cpu/cpu" + std::to_string(cpu) + "/cpu_capacity");
        if (!(in >> capacity) || capacity == 0 || capacity > 1000000) return false;
        capacities.emplace_back(cpu, capacity);
    }
    if (capacities.size() < 2) return false;
    const auto range = std::minmax_element(capacities.begin(), capacities.end(),
        [](const auto &a, const auto &b) { return a.second < b.second; });
    if (range.first->second * 100 >= range.second->second * 80) return false;
    int count = 0;
    for (const auto &entry : capacities) {
        if (entry.second * 100 >= range.second->second * 80) {
            CPU_SET(entry.first, &fast);
            ++count;
        }
    }
    // Respect explicit thread counts rather than oversubscribing a cluster.
    if (count < threads) return false;
    const int result = sched_setaffinity(0, sizeof(fast), &fast);
    __android_log_print(result == 0 ? ANDROID_LOG_INFO : ANDROID_LOG_WARN,
        "NodexLM-RPC", "CPU RPC server affinity: requested %d fast cores; result=%d", count, result);
    return result == 0;
}

extern "C" JNIEXPORT jstring JNICALL
Java_dev_nodexlm_app_NativeRpc_start(JNIEnv *env, jobject, jint threads, jstring modeValue, jstring socketValue, jboolean performanceAffinity, jstring cacheValue) {
    const std::string socketPath=utf(env,socketValue);
    const std::string cachePath=utf(env,cacheValue);
    if (!cachePath.empty() && cachePath[0] != '/') return env->NewStringUTF("Invalid private cache path");
    if(socketPath.empty() || socketPath[0]!='/' || socketPath.size()>100) return env->NewStringUTF("Invalid private RPC socket path");
    if(threads < 1 || threads > 8)
        return env->NewStringUTF("RPC CPU threads must be between 1 and 8");
    const std::string mode = utf(env, modeValue);
    if(mode != "auto" && mode != "cpu" && mode != "gpu" && mode != "hybrid")
        return env->NewStringUTF("Invalid RPC backend mode");
    std::lock_guard<std::mutex> lifecycle(rpcThreadMutex);
    if(rpcStarted.exchange(true)) return env->NewStringUTF("");
    if(rpcThread.joinable()) rpcThread.join();
    ggml_backend_rpc_android_reset_stop();
    loadBackendsOnce(mode != "cpu");
    ggml_backend_dev_t cpu = ggml_backend_dev_by_type(GGML_BACKEND_DEVICE_TYPE_CPU);
    ggml_backend_dev_t gpu = nullptr;
    if (mode != "cpu") {
        for(size_t i = 0; i < ggml_backend_dev_count(); ++i) {
            auto dev = ggml_backend_dev_get(i);
            if(dev && isGpu(dev)) { gpu = dev; break; }
        }
    }
    if(gpu && mode != "cpu") {
        // Device enumeration does not prove that its backend can initialize.
        // Release the small probe before GGML RPC owns backend resources.
        ggml_backend_t probe = nullptr;
        try {
            probe = ggml_backend_dev_init(gpu, nullptr);
        } catch (const std::exception& e) {
            __android_log_print(ANDROID_LOG_WARN, "NodexLM-RPC",
                                "Vulkan backend initialization failed: %s", e.what());
        } catch (...) {
            __android_log_print(ANDROID_LOG_WARN, "NodexLM-RPC",
                                "Vulkan backend initialization failed");
        }
        if (probe) {
            ggml_backend_free(probe);
            // Some Android Vulkan drivers initialize successfully but report
            // no usable heap (or less than a single practical RPC allocation).
            // In Auto, GPU-only would then advertise zero usable capacity to
            // the Host, so no model tensors would ever be scheduled to this
            // Guest. Prefer the already available CPU in this case. Explicit
            // GPU/hybrid choices still retain their requested backend.
            if (mode == "auto" && cpu) {
                size_t freeBytes = 0;
                size_t totalBytes = 0;
                ggml_backend_dev_memory(gpu, &freeBytes, &totalBytes);
                constexpr size_t minVulkanFree = 512ULL * 1024 * 1024;
                if (totalBytes == 0 || freeBytes < minVulkanFree) {
                    __android_log_print(ANDROID_LOG_WARN, "NodexLM-RPC",
                        "Auto switching from Vulkan to CPU: usable Vulkan heap is not reported or below 512 MiB");
                    gpu = nullptr;
                }
            }
        } else {
            // Auto can use CPU; GPU-only and hybrid fail explicitly below.
            gpu = nullptr;
        }
    }
    if((mode == "gpu" || mode == "hybrid") && !gpu) {
        rpcStarted.store(false);
        return env->NewStringUTF("Vulkan unavailable on this Android device; select CPU or Auto");
    }
    if((mode == "cpu" || mode == "hybrid") && !cpu) {
        rpcStarted.store(false);
        return env->NewStringUTF("GGML CPU backend unavailable");
    }
    const bool useGPU = gpu != nullptr && mode != "cpu";
    const bool useCPU = cpu != nullptr &&
        (mode == "cpu" || mode == "hybrid" || (mode == "auto" && !useGPU));
    std::vector<ggml_backend_dev_t> devices;
    if(useGPU) devices.push_back(gpu);
    if(useCPU) devices.push_back(cpu);
    if(devices.empty()) {
        rpcStarted.store(false);
        return env->NewStringUTF("No usable CPU or Vulkan RPC backend found");
    }
    activeRPCBackends.store((useGPU ? 1 : 0) | (useCPU ? 2 : 0));
    __android_log_print(ANDROID_LOG_INFO, "NodexLM-RPC",
        "RPC devices selected: %zu, Vulkan: %s, CPU: %s",
        devices.size(),useGPU?"yes":"no",useCPU?"yes":"no");
    try {
        rpcThread = std::thread([threads, devices, socketPath, performanceAffinity, useCPU, useGPU, cachePath] {
            try {
                const bool strict = performanceAffinity && useCPU && !useGPU && preferPerformanceRPCThreads(threads);
                ggml_backend_rpc_android_set_strict_affinity(strict);
                const std::string address = "unix:" + socketPath;
                ggml_backend_rpc_start_server(address.c_str(), cachePath.empty() ? nullptr : cachePath.c_str(),
                    static_cast<size_t>(threads), devices.size(),
                    const_cast<ggml_backend_dev_t*>(devices.data()));
            } catch(const std::exception& e) {
                __android_log_print(ANDROID_LOG_ERROR,"NodexLM-RPC","RPC server: %s",e.what());
            } catch(...) {
                __android_log_print(ANDROID_LOG_ERROR,"NodexLM-RPC","Unknown native RPC error");
            }
            {
                std::lock_guard<std::mutex> lock(rpcThreadMutex);
                activeRPCBackends.store(0);
                rpcStarted.store(false);
            }
            rpcFinished.notify_all();
        });
    } catch(const std::exception& e) {
        rpcStarted.store(false);
        activeRPCBackends.store(0);
        return env->NewStringUTF(e.what());
    }
    return env->NewStringUTF("");
}

extern "C" JNIEXPORT jboolean JNICALL
Java_dev_nodexlm_app_NativeRpc_stopAndJoin(JNIEnv*, jobject, jint timeoutMs) {
    std::unique_lock<std::mutex> lock(rpcThreadMutex);
    ggml_backend_rpc_android_request_stop();
    if (!rpcFinished.wait_for(lock, std::chrono::milliseconds(std::clamp<int>(timeoutMs, 0, 5000)),
                              [] { return !rpcStarted.load(); })) return JNI_FALSE;
    if (rpcThread.joinable()) rpcThread.join();
    return JNI_TRUE;
}

// Expose lifecycle to the foreground service so dead workers stop heartbeats.
extern "C" JNIEXPORT jboolean JNICALL
Java_dev_nodexlm_app_NativeRpc_isRunning(JNIEnv*, jobject) {
    return rpcStarted.load() ? JNI_TRUE : JNI_FALSE;
}

// Reports the devices this RPC server actually selected after Vulkan init
// probe. Does not imply an inference benchmark or successful graph execution.
extern "C" JNIEXPORT jstring JNICALL
Java_dev_nodexlm_app_NativeRpc_selectedBackends(JNIEnv *env, jobject) {
    const int selected = activeRPCBackends.load();
    if (selected == 3) return env->NewStringUTF("cpu-vulkan");
    if (selected == 1) return env->NewStringUTF("vulkan");
    if (selected == 2) return env->NewStringUTF("cpu");
    return env->NewStringUTF("unavailable");
}
