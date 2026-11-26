package dev.nodexlm.app

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class WifiClaimPolicyTest {
    @Test fun boundedIdleGrace() {
        val p = WifiClaimPolicy()
        assertFalse(p.active(0,false))
        assertTrue(p.active(100,true))
        assertTrue(p.active(30_099,false))
        assertFalse(p.active(30_100,false))
        assertTrue(p.active(40_000,true))
        assertTrue(p.active(70_000,true))
        assertTrue(p.active(99_999,false))
        assertFalse(p.active(100_000,false))
    }
}
