package dev.nodexlm.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class MobileModelAdviceTest {
    @Test fun capsActual32BitProcess() {
        val gib = 1L shl 30
        assertEquals(896L shl 20, MobileModelAdvice.budget(8*gib, gib, false))
        assertTrue(MobileModelAdvice.budget(8*gib, gib, true) > 5*gib)
    }

    @Test fun preservesReserveAndHeadroom() {
        assertEquals(0L, MobileModelAdvice.budget(100, 200, true))
        assertFalse(MobileModelAdvice.fits(100, 114))
        assertTrue(MobileModelAdvice.fits(100, 115))
        assertFalse(MobileModelAdvice.fits(Long.MAX_VALUE, Long.MAX_VALUE))
    }
}
