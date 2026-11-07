package dev.nodexlm.app

import org.junit.Assert.assertArrayEquals
import org.junit.Test

class PairingInputTest {
    @Test fun completePayloadAtLimitIsPreserved() {
        val bytes = ByteArray(8192) { (it % 251).toByte() }
        assertArrayEquals(bytes, boundedPairingBytes(bytes.inputStream()))
    }
    @Test(expected = IllegalArgumentException::class)
    fun oversizedUntrustedPairingBodyIsRejected() {
        boundedPairingBytes(ByteArray(8193).inputStream())
    }
}
