// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

internal object GuestResourceBudget {
    private const val MIB = 1024L * 1024L

    // Android's low-memory threshold is not spare memory. Keep additional
    // headroom for driver allocations, application launches and LMKD.
    // Existing explicit reserves remain effective. Zero selects auto.
    fun reserveBytes(configuredMiB: Int, totalBytes: Long, thresholdBytes: Long): Long {
        val threshold = thresholdBytes.coerceIn(0, Long.MAX_VALUE - 256 * MIB)
        val systemFloor = maxOf(512 * MIB, threshold + 256 * MIB)
        val proportional = (totalBytes.coerceAtLeast(0) / 12).coerceIn(512 * MIB, 1024 * MIB)
        val chosen = if (configuredMiB == 0) proportional
            else configuredMiB.coerceIn(512, 65536).toLong() * MIB
        return maxOf(systemFloor, chosen)
    }
}

// Claim-driven growth retains two standby streams without eagerly opening
// eight TLS sessions. Shrinking is intentionally deferred: closing a blocked
// TLS reader can race the Host's claim marker and lose a native RPC session.
internal class RpcTunnelBudget(private val maximum: Int = 8) {
    init { require(maximum in 2..8) }
    private var started = 0

    @Synchronized
    fun grow(claimed: Long): IntRange {
        val desired = (claimed.coerceIn(0, maximum.toLong()).toInt() + 2).coerceAtMost(maximum)
        val old = started
        started = maxOf(started, desired)
        return old until started
    }
}
