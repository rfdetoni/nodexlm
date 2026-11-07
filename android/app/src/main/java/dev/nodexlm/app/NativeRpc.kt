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
    external fun start(threads: Int, mode: String, port: Int): String
    /** Reports whether the native GGML RPC server thread is still running. */
    external fun isRunning(): Boolean
    /** JSON object with hardware-backed Vulkan GPU metrics (can be empty). */
    external fun inspect(): String
}
