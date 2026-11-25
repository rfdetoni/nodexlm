// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

/**
 * Native GGML CPU and Vulkan RPC worker. Unlike whole-model JNI inference this endpoint
 * accepts only buffers and computation graphs from the Host. No GGUF file
 * needs to exist on Android. Uses the exact Host llama.cpp RPC revision.
 */
internal object NativeRpc {
    val VERSION = BuildConfig.LLAMA_VERSION
    init { System.loadLibrary("nodexlm") }
    /** Register dynamic ARM64 CPU/Vulkan backend shared libraries from nativeLibraryDir or APK namespace. */
    external fun initBackends(nativeLibDir: String)
    /** Count faster CPU cores within the current Android cpuset, with safe fallback. */
    external fun recommendedThreads(logical: Int): Int
    external fun start(threads: Int, mode: String, socketPath: String): String
    /** Request socket shutdown and join after backend cleanup; false on timeout. */
    external fun stopAndJoin(timeoutMs: Int): Boolean
    /** Reports whether the native GGML RPC server thread is still running. */
    external fun isRunning(): Boolean
    /** Selected native devices after backend initialization; not measured throughput. */
    external fun selectedBackends(): String
    /** JSON object with hardware-backed Vulkan GPU metrics (can be empty). */
    external fun inspect(): String
}
