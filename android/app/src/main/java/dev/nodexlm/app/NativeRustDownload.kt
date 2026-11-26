// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

/** Low-allocation Rust GGUF transfer inside libnodexlm_rpc.so.
 * TLS uses Android's platform trust manager; Rust owns streaming,
 * SHA-256, fsync, cancellation and network byte counters.
 */
internal object NativeRustDownload {
    init { System.loadLibrary("nodexlm_rpc") }
    external fun fetch(source: String, destination: String, sha256: String, size: Long): String
    external fun cancel()
    external fun transferred(): Long
    external fun bytesPerSecond(): Long
}
