package dev.nodexlm.app

import org.junit.Assert.*
import org.junit.Test

class ModelAndEnergyTest {
    @Test fun validatesCompleteShardNames() {
        assertEquals(listOf("m-00001-of-00002.gguf","m-00002-of-00002.gguf"),ModelFiles.shards("m-00001-of-00002.gguf"))
        for(name in listOf("m-00002-of-00002.gguf","m-00001-of-00129.gguf")) {
            try {ModelFiles.shards(name);fail("Accepted invalid shard selection")} catch(_:IllegalArgumentException) {}
        }
    }
    @Test fun overnightScheduleAndCharging() {
        val policy=EnergyPolicy(true,20,22,6)
        assertEquals("charging",policy.pauseReason(80,false,23))
        assertNull(policy.pauseReason(80,true,23))
        assertNull(policy.pauseReason(80,true,2))
        assertEquals("schedule",policy.pauseReason(80,true,12))
        assertEquals("battery",EnergyPolicy(false,20,0,0).pauseReason(20,false,12))
    }
}
