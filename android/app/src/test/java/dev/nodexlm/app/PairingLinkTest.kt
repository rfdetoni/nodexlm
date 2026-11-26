package dev.nodexlm.app

import org.junit.Assert.assertEquals
import org.junit.Test

class PairingLinkTest {
    @Test fun parsesLocalEndpoint() {
        assertEquals("https://192.168.1.5:8443" to "ABCD2345",
            parsePairingLink("nodexlm://pair?host=https%3A%2F%2F192.168.1.5%3A8443&code=ABCD2345"))
    }
    @Test(expected=IllegalArgumentException::class)
    fun rejectsPlainHttp() {
        parsePairingLink("nodexlm://pair?host=http%3A%2F%2F192.168.1.5&code=ABCD2345")
    }
    @Test(expected=IllegalArgumentException::class)
    fun rejectsDuplicateFields() {
        parsePairingLink("nodexlm://pair?code=ABCD2345&code=ABCD2345")
    }
}
