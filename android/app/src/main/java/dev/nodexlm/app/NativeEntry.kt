// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

/** Model weights are owned by C++ and never copied into the JVM heap. */
object NativeEntry {
    init { System.loadLibrary("nodexlm") }
    external fun load(modelPath: String, contextTokens: Int, threads: Int): String
    external fun generate(roles: Array<String>, contents: Array<String>, maxTokens: Int): ByteArray
    external fun cancel()
    external fun unload()
}
