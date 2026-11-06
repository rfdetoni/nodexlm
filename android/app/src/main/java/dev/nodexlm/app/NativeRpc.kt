// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

/**
 * Native GGML CPU RPC worker. Unlike whole-model JNI inference this endpoint
 * accepts only buffers and computation graphs from the Host. No GGUF file
 * needs to exist on Android. Uses the exact Host llama.cpp RPC revision.
 */
internal object NativeRpc {
    const val VERSION = "b12143"
    init { System.loadLibrary("nodexlm") }
    external fun start(threads: Int): String
}
