// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
#include <jni.h>
#include <algorithm>
#include <atomic>
#include <memory>
#include <mutex>
#include <string>
#include <vector>
#include <cstring>
#include <thread>
#include <exception>
#include <sstream>
#include <android/log.h>
#include "llama.h"
#include "ggml-backend.h"
#include "ggml-rpc.h"

namespace {
std::mutex guard;
std::atomic<bool> cancelled{false};
std::once_flag initialized;
std::atomic<bool> rpcStarted{false};
jbyteArray bytes(JNIEnv* env,const std::string& value) {
    auto result=env->NewByteArray((jsize)value.size());
    if (result) env->SetByteArrayRegion(result,0,(jsize)value.size(),(const jbyte*)value.data());
    return result;
}
llama_model* model = nullptr;
llama_context* ctx = nullptr;
const llama_vocab* vocab = nullptr;

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
    if(ctx){llama_free(ctx);ctx=nullptr;}
    if(model){llama_model_free(model);model=nullptr;}
    vocab=nullptr;
}
// A small, bounded parser of message arrays is preferable to untrusted prompt
// template concatenation; the Java service passes validated JSON messages.
// The native shim expects a serialized prompt; it is formatted by Kotlin.
}

extern "C" JNIEXPORT jstring JNICALL
Java_dev_nodexlm_app_NativeEntry_load(JNIEnv* env,jobject,jstring path,jint contextTokens,jint threads) {
    std::lock_guard<std::mutex> lock(guard);
    const std::string file=utf(env,path);
    if(file.empty()) return env->NewStringUTF("model path required");
    release();
    cancelled.store(false);
    std::call_once(initialized, [] { llama_backend_init(); });
    auto params=llama_model_default_params();
    params.n_gpu_layers=0; // Android CPU path first; Vulkan backend is a separate opt-in.
    params.load_mode=LLAMA_LOAD_MODE_MMAP;
    model=llama_model_load_from_file(file.c_str(),params);
    if(!model)return env->NewStringUTF("GGUF load failed (memory, format, or path)");
    auto opts=llama_context_default_params();
    opts.n_ctx=std::clamp<int>(contextTokens,512,4096);
    opts.n_batch=std::min<uint32_t>(512,opts.n_ctx);
    opts.n_threads=std::clamp<int>(threads,1,8);
    opts.n_threads_batch=opts.n_threads;
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
    llama_memory_clear(llama_get_memory(ctx),false);
    // Decode prompt in bounded batches; batch_get_one only valid while tokens live.
    for (int i=0;i<n;i+=256) {
        int count=std::min(256,n-i);
        auto batch=llama_batch_get_one(tokens.data()+i,count);
        if(llama_decode(ctx,batch)!=0)return bytes(env,"NODEXLM_ERROR: prompt decode failed");
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
        if(sink&&safe>emitted){auto chunk=bytes(env,output.substr(emitted,safe-emitted));env->CallVoidMethod(sink,tokenCallback,chunk);env->DeleteLocalRef(chunk);emitted=safe;if(env->ExceptionCheck()){llama_sampler_free(sampler);return nullptr;}}
        if(stopped){output.resize(boundary);break;}
        if(llama_decode(ctx,llama_batch_get_one(&next,1))!=0)break;
    }
    if(sink&&!cancelled.load()&&output.size()>emitted){auto chunk=bytes(env,output.substr(emitted));env->CallVoidMethod(sink,tokenCallback,chunk);env->DeleteLocalRef(chunk);}
    llama_sampler_free(sampler);
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
static bool isGpu(ggml_backend_dev_t d) {
    const auto type = ggml_backend_dev_type(d);
    return type == GGML_BACKEND_DEVICE_TYPE_GPU || type == GGML_BACKEND_DEVICE_TYPE_IGPU;
}
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

// The report is informational. Android integrated GPU memory can be shared
// with system RAM; the Host's memory planner uses a max rather than a sum.
extern "C" JNIEXPORT jstring JNICALL
Java_dev_nodexlm_app_NativeRpc_inspect(JNIEnv *env, jobject) {
    ggml_backend_load_all();
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

// Run in an isolated Android :rpc process. The pinned upstream has no
// shutdown API; killing this process releases all Vulkan/CPU allocations.
// Mode: auto = GPU + CPU when available else CPU; cpu = CPU only;
// gpu = Vulkan only; hybrid = Vulkan + CPU (requires Vulkan).
extern "C" JNIEXPORT jstring JNICALL
Java_dev_nodexlm_app_NativeRpc_start(JNIEnv *env, jobject, jint threads, jstring modeValue, jstring socketValue) {
    const std::string socketPath=utf(env,socketValue);
    if(socketPath.empty() || socketPath[0]!='/' || socketPath.size()>100) return env->NewStringUTF("Invalid private RPC socket path");
    if(threads < 1 || threads > 8)
        return env->NewStringUTF("RPC CPU threads must be between 1 and 8");
    const std::string mode = utf(env, modeValue);
    if(mode != "auto" && mode != "cpu" && mode != "gpu" && mode != "hybrid")
        return env->NewStringUTF("Invalid RPC backend mode");
    if(rpcStarted.exchange(true)) return env->NewStringUTF("");
    ggml_backend_load_all();
    ggml_backend_dev_t cpu = ggml_backend_dev_by_type(GGML_BACKEND_DEVICE_TYPE_CPU);
    ggml_backend_dev_t gpu = nullptr;
    for(size_t i = 0; i < ggml_backend_dev_count(); ++i) {
        auto dev = ggml_backend_dev_get(i);
        if(dev && isGpu(dev)) { gpu = dev; break; }
    }
    if((mode == "gpu" || mode == "hybrid") && !gpu) {
        rpcStarted.store(false);
        return env->NewStringUTF("Vulkan unavailable on this Android device; select CPU or Auto");
    }
    if((mode == "auto" || mode == "cpu" || mode == "hybrid") && !cpu) {
        rpcStarted.store(false);
        return env->NewStringUTF("GGML CPU backend unavailable");
    }
    std::vector<ggml_backend_dev_t> devices;
    if(mode != "cpu" && gpu) devices.push_back(gpu);
    if(mode != "gpu" && cpu) devices.push_back(cpu);
    if(devices.empty()) {
        rpcStarted.store(false);
        return env->NewStringUTF("No usable CPU or Vulkan RPC backend found");
    }
    __android_log_print(ANDROID_LOG_INFO, "NodexLM-RPC",
        "RPC devices selected: %zu, Vulkan: %s, CPU: %s",
        devices.size(),gpu?"yes":"no",cpu?"yes":"no");
    try {
        std::thread([threads, devices, socketPath] {
            try {
                const std::string address = "unix:" + socketPath;
                ggml_backend_rpc_start_server(address.c_str(), nullptr,
                    static_cast<size_t>(threads), devices.size(),
                    const_cast<ggml_backend_dev_t*>(devices.data()));
            } catch(const std::exception& e) {
                __android_log_print(ANDROID_LOG_ERROR,"NodexLM-RPC","RPC server: %s",e.what());
            } catch(...) {
                __android_log_print(ANDROID_LOG_ERROR,"NodexLM-RPC","Unknown native RPC error");
            }
            rpcStarted.store(false);
        }).detach();
    } catch(const std::exception& e) {
        rpcStarted.store(false);
        return env->NewStringUTF(e.what());
    }
    return env->NewStringUTF("");
}

// The detached native RPC server has no shutdown callback. Expose its
// lifecycle to the Android foreground service so dead workers stop heartbeats.
extern "C" JNIEXPORT jboolean JNICALL
Java_dev_nodexlm_app_NativeRpc_isRunning(JNIEnv*, jobject) {
    return rpcStarted.load() ? JNI_TRUE : JNI_FALSE;
}
