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

    @Test fun autoFilterUsesLatestRecordedRuntimeRole() {
        val history = mutableListOf(
            RuntimeLogEntry(1L, RuntimeRole.GUEST, RuntimeLevel.INFO, RuntimeEvent.WORKER_START),
            RuntimeLogEntry(2L, RuntimeRole.GUEST, RuntimeLevel.INFO, RuntimeEvent.WORKER_STOP),
            RuntimeLogEntry(3L, RuntimeRole.GUEST, RuntimeLevel.INFO, RuntimeEvent.GUEST_START)
        )
        assertEquals(RuntimeMode.RPC_GUEST, RuntimeLogFormat.lastMode(history))
        history.add(RuntimeLogEntry(4L, RuntimeRole.HOST, RuntimeLevel.INFO, RuntimeEvent.HOST_TUNNEL_CLAIMED))
        assertEquals(RuntimeMode.RPC_GUEST, RuntimeLogFormat.lastMode(history))
        // Stopping an old worker can race a newly started RPC service.
        history.add(RuntimeLogEntry(5L, RuntimeRole.GUEST, RuntimeLevel.WARN, RuntimeEvent.WORKER_STOP))
        assertEquals(RuntimeMode.RPC_GUEST, RuntimeLogFormat.lastMode(history))
        history.add(RuntimeLogEntry(6L, RuntimeRole.GUEST, RuntimeLevel.WARN, RuntimeEvent.GUEST_STOP))
        assertEquals(RuntimeMode.INACTIVE, RuntimeLogFormat.lastMode(history))
        assertEquals(RuntimeMode.INACTIVE, RuntimeLogFormat.lastMode(emptyList()))
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
