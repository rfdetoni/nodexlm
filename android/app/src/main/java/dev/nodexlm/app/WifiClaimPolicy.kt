package dev.nodexlm.app

/** A single monotonic deadline; keep the radio preference during claims and
 * for 30 seconds afterward to avoid oscillation between requests.
 * A claim can outlive inference, so this is not a token-activity measurement.
 */
internal class WifiClaimPolicy(private val graceMs: Long = 30_000L) {
    private var lastActive: Long? = null
    fun active(nowMs: Long, claimed: Boolean): Boolean {
        if (claimed) lastActive = nowMs
        val last = lastActive ?: return false
        return claimed || (nowMs >= last && nowMs - last < graceMs)
    }
}
