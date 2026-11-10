// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import java.io.File

/** Measured device-wide CPU load, if Android exposes /proc/stat. */
internal class CpuUsageSampler {
    private var lastTotal = 0L
    private var lastIdle = 0L

    @Synchronized
    fun sample(): Double? {
        // Some Android ROMs restrict these counters. Unavailable is not zero.
        val parts = try {
            File("/proc/stat").bufferedReader().use { reader ->
                reader.readLine()?.trim()?.split(Regex("\\s+"))
            } ?: return null
        } catch (_: Exception) { return null }
        if (parts.size < 6 || parts[0] != "cpu") return null
        val ticks = parts.drop(1).take(8).map { it.toLongOrNull() ?: return null }
        val total = ticks.sum()
        val idle = ticks[3] + ticks[4]
        val result = if (lastTotal > 0 && total > lastTotal && idle >= lastIdle) {
            val delta = total - lastTotal
            100.0 * (delta - (idle - lastIdle).coerceAtMost(delta)) / delta
        } else null
        lastTotal = total
        lastIdle = idle
        return result
    }
}
