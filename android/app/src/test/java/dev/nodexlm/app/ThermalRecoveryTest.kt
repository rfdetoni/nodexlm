package dev.nodexlm.app

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ThermalRecoveryTest {
    @Test
    fun resumesOnlyAfterStableCoolAndIdleInterval() {
        val policy = ThermalRecovery(stableMillis = 30_000L)
        assertFalse(policy.eligible(1000L, cool = true, idle = true))
        assertFalse(policy.eligible(30_999L, cool = true, idle = true))
        assertTrue(policy.eligible(31_000L, cool = true, idle = true))

        policy.reset()
        assertFalse(policy.eligible(40_000L, cool = true, idle = true))
        assertFalse(policy.eligible(45_000L, cool = false, idle = true))
        assertFalse(policy.eligible(50_000L, cool = true, idle = true))
        assertFalse(policy.eligible(85_000L, cool = true, idle = false))
        assertFalse(policy.eligible(86_000L, cool = true, idle = true))
        assertTrue(policy.eligible(116_000L, cool = true, idle = true))
    }

    @Test
    fun clockRollbackRestartsCooldown() {
        val policy = ThermalRecovery(30_000L)
        assertFalse(policy.eligible(100_000L, cool = true, idle = true))
        assertFalse(policy.eligible(80_000L, cool = true, idle = true))
        assertFalse(policy.eligible(109_999L, cool = true, idle = true))
        assertTrue(policy.eligible(110_000L, cool = true, idle = true))
    }
}
