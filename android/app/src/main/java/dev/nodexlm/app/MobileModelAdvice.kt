package dev.nodexlm.app

/** Advisory weight capacity, not a guarantee of model or driver compatibility. */
internal object MobileModelAdvice {
    fun budget(available: Long, reserve: Long, process64Bit: Boolean): Long {
        val free = (available.coerceAtLeast(0) - reserve.coerceAtLeast(0)).coerceAtLeast(0)
        val weightBudget = free / 5 * 4
        return if (process64Bit) weightBudget else minOf(weightBudget, 896L shl 20)
    }

    fun fits(bytes: Long, budget: Long): Boolean =
        bytes > 0 && budget > 0 && bytes <= Long.MAX_VALUE / 115 && bytes * 115 / 100 <= budget
}
