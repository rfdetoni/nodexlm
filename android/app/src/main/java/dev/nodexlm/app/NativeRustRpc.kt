// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

/** Loaded only when the experimental Rust transport is explicitly selected. */
internal object NativeRustRpc {
    init { System.loadLibrary("nodexlm_rpc") }
    external fun start(host: String, port: Int, node: String, token: String, cert: ByteArray, socket: String, efficientCores: Boolean): String
    external fun stop()
    external fun setDraining(draining: Boolean)
    external fun isRunning(): Boolean
    external fun claimed(): Long
    external fun failures(): Long
    external fun failure(): String
}
