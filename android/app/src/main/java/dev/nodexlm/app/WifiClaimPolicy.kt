package dev.nodexlm.app

/** Retain low-latency Wi-Fi during claims. Fresh measured RPC RTT/spread
 * adjusts the post-claim grace without generating any additional probes.
 * Heartbeat response time is only a fallback application-level latency.
 */
internal class WifiClaimPolicy(private val graceMs: Long = 30_000L) {
    private var lastActive: Long? = null
    private var observedAt: Long? = null
    private var rtt = 0.0
    private var jitter = 0.0
    private var lastHeartbeat: Double? = null

    fun observe(nowMs: Long, measuredRtt: Double?, measuredJitter: Double?, heartbeatMs: Double) {
        val sample = measuredRtt?.takeIf { it.isFinite() && it in 0.0..10000.0 } ?: heartbeatMs
        if (!sample.isFinite() || sample !in 0.0..10000.0) return
        val spread = measuredJitter?.takeIf { it.isFinite() && it in 0.0..10000.0 }
            ?: kotlin.math.abs(heartbeatMs-(lastHeartbeat ?: heartbeatMs))
        lastHeartbeat = heartbeatMs
        if (observedAt == null || nowMs-(observedAt ?: nowMs)>60_000) { rtt=sample;jitter=spread }
        else { rtt=0.75*rtt+0.25*sample;jitter=0.75*jitter+0.25*spread }
        observedAt = nowMs
    }

    fun active(nowMs: Long, claimed: Boolean): Boolean {
        if (claimed) lastActive = nowMs
        val last = lastActive ?: return false
        val fresh = observedAt?.let { nowMs>=it && nowMs-it<=60_000 } ?: false
        val grace = if (fresh && (rtt>=40.0 || jitter>=12.0)) graceMs*2 else graceMs
        return claimed || (nowMs>=last && nowMs-last<grace)
    }
}
