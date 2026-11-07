package dev.nodexlm.app

/** Severe thermal status always overrides the charging policy. */
internal data class EnergyPolicy(
    val onlyCharging: Boolean,
    val minimumBattery: Int,
    val startHour: Int,
    val endHour: Int
) {
    fun pauseReason(percent: Int, charging: Boolean, hour: Int): String? {
        if (onlyCharging && !charging) return "charging"
        if (!charging && (percent !in 0..100 || percent <= minimumBattery.coerceIn(5,95)))
            return "battery"
        val from = startHour.coerceIn(0,23)
        val end = endHour.coerceIn(0,23)
        val allowed = from == end || if (from < end) hour in from until end else hour >= from || hour < end
        if (!allowed) return "schedule"
        return null
    }
}
