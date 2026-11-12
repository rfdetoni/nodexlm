package dev.nodexlm.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class RuntimeLogFormatTest {
    @Test fun keepsOnlyKnownOperationalFields() {
        val e = RuntimeLogEntry(1762963200000L, RuntimeRole.HOST,
            RuntimeLevel.WARN, RuntimeEvent.HOST_RETRY, 3)
        val serialized = RuntimeLogFormat.encode(e)
        assertEquals(e, RuntimeLogFormat.decode(serialized.trimEnd()))
        assertEquals(5, serialized.trimEnd().split('\t').size)
        assertTrue(serialized.contains("HOST_RETRY"))
    }

    @Test fun dropsMalformedOrUntrustedEvents() {
        for (value in listOf(
            "", "0\tHOST\tINFO\tHOST_RETRY\t0",
            "123\tHOST\tINFO\tUNKNOWN\t0",
            "123\tHOST\tINFO\tHOST_RETRY\t0\tsecret",
            "123\tOTHER\tINFO\tHOST_RETRY\t0",
            "123\tHOST\tWARN\tHOST_RETRY\tbogus",
            "abc\tHOST\tINFO\tHOST_RETRY\t0"
        )) assertNull("Unexpected decoded log: $value", RuntimeLogFormat.decode(value))
    }

    @Test fun rendersRoleLevelAndTimeWithoutPayloads() {
        val display = RuntimeLogFormat.readable(listOf(
            RuntimeLogEntry(1762963200000, RuntimeRole.GUEST,
                RuntimeLevel.INFO, RuntimeEvent.GUEST_READY)
        ), english = false)
        assertTrue(display.contains("[GUEST]"))
        assertTrue(display.contains("Servidor RPC nativo pronto"))
    }
}
