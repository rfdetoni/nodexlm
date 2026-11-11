package dev.nodexlm.app

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.Instant

class PairingInputTest {
    @Test fun expiryAcceptsHostTimezoneOffset() {
        val expected = Instant.parse("2026-11-11T10:40:15.086295164Z")
        assertEquals(expected, parsePairingExpiry("2026-11-11T07:40:15.086295164-03:00"))
        assertEquals(expected, parsePairingExpiry("2026-11-11T10:40:15.086295164Z"))
    }
    @Test fun completePayloadAtLimitIsPreserved() {
        val bytes = ByteArray(8192) { (it % 251).toByte() }
        assertArrayEquals(bytes, boundedPairingBytes(bytes.inputStream()))
    }
    @Test(expected = IllegalArgumentException::class)
    fun oversizedUntrustedPairingBodyIsRejected() {
        boundedPairingBytes(ByteArray(8193).inputStream())
    }
}
