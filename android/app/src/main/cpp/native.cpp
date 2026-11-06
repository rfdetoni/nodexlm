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
    const char* c=env->GetStringUTFChars(str,nullptr);
    if(!c) return {};
    std::string result(c);
    env->ReleaseStringUTFChars(str,c);
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
Java_dev_nodexlm_app_NativeEntry_generate(JNIEnv* env,jobject,jobjectArray javaRoles,jobjectArray javaContents,jint tokenLimit) {
    std::lock_guard<std::mutex> lock(guard);
    if(!ctx||!model)return bytes(env,"NODEXLM_ERROR: no loaded model");
    if(!javaRoles || !javaContents)return bytes(env,"NODEXLM_ERROR: missing messages");
    const jsize count=env->GetArrayLength(javaRoles);
    if(count<=0||count>64||env->GetArrayLength(javaContents)!=count)return bytes(env,"NODEXLM_ERROR: invalid messages");
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
    llama_sampler_chain_add(sampler,llama_sampler_init_temp(0.7f));
    llama_sampler_chain_add(sampler,llama_sampler_init_dist(LLAMA_DEFAULT_SEED));
    std::string output;
    for (int i=0;i<maxOut && n+i<contextSize && !cancelled.load();i++) {
        llama_token next=llama_sampler_sample(sampler,ctx,-1);
        if(llama_vocab_is_eog(vocab,next))break;
        char piece[2048];
        int length=llama_token_to_piece(vocab,next,piece,sizeof(piece),0,false);
        if(length>0)output.append(piece,(size_t)length);
        if(llama_decode(ctx,llama_batch_get_one(&next,1))!=0)break;
    }
    llama_sampler_free(sampler);
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


// Started only inside RpcGuestService's dedicated :rpc Android process.
// ggml_backend_rpc_start_server() is intentionally blocking and provides no
// stop function in this pinned upstream version. Stopping that foreground
// service tears down the isolated process instead of affecting the main UI.
// The server binds to loopback only; only the authenticated TLS tunnel reaches
// the Host. The entire GGUF stays on the Host.
extern "C" JNIEXPORT jstring JNICALL
Java_dev_nodexlm_app_NativeRpc_start(JNIEnv *env, jobject, jint threads) {
    if (threads < 1 || threads > 8) {
        return env->NewStringUTF("RPC CPU threads must be between 1 and 8");
    }
    if (rpcStarted.exchange(true)) {
        return env->NewStringUTF("");
    }
    try {
        std::thread([threads] {
            try {
                ggml_backend_load_all();
                ggml_backend_dev_t cpu = ggml_backend_dev_by_type(GGML_BACKEND_DEVICE_TYPE_CPU);
                if (!cpu) {
                    __android_log_print(ANDROID_LOG_ERROR, "NodexLM-RPC", "CPU backend not found");
                    return;
                }
                ggml_backend_rpc_start_server("127.0.0.1:50052", nullptr,
                                              static_cast<size_t>(threads), 1, &cpu);
            } catch (const std::exception &e) {
                __android_log_print(ANDROID_LOG_ERROR, "NodexLM-RPC", "Native RPC exception: %s", e.what());
            } catch (...) {
                __android_log_print(ANDROID_LOG_ERROR, "NodexLM-RPC", "Unknown native RPC exception");
            }
        }).detach();
    } catch (const std::exception &e) {
        rpcStarted.store(false);
        return env->NewStringUTF(e.what());
    }
    return env->NewStringUTF("");
}
