package dev.nodexlm.app

// A thermal recovery is not evidence of a live-safe GPU workload. It only
// permits the same already running RPC process to reconnect after a stable
// cool and idle interval; severe heat still kills the service immediately.
internal class ThermalRecovery(private val stableMillis: Long = 30_000L) {
    private var safeSince: Long? = null

    fun eligible(nowMillis: Long, cool: Boolean, idle: Boolean): Boolean {
        if (!cool || !idle) {
            safeSince = null
            return false
        }
        val since = safeSince
        if (since == null || nowMillis < since) {
            safeSince = nowMillis
            return false
        }
        return nowMillis - since >= stableMillis
    }

    fun reset() {
        safeSince = null
    }
}
