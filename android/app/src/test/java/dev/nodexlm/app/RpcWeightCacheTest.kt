// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

class RpcWeightCacheTest {
    @get:Rule val folder = TemporaryFolder()
    @Test fun cacheIsReusedOnlyForIdenticalEnrollmentAndRevision() {
        val base = folder.newFolder()
        val first = RpcWeightCache.prepare(base, byteArrayOf(1), "node", "token", "v1", true)!!
        val data = File(first, "abcd").apply { writeText("weights") }
        File(first, "abcd.tmp").writeText("partial")
        assertEquals(first, RpcWeightCache.prepare(base, byteArrayOf(1), "node", "token", "v1", true))
        assertTrue(data.exists())
        assertFalse(File(first, "abcd.tmp").exists())
        val next = RpcWeightCache.prepare(base, byteArrayOf(1), "node", "new-token", "v1", true)!!
        assertNotEquals(first, next)
        assertFalse(first.exists())
        assertNull(RpcWeightCache.prepare(base, byteArrayOf(1), "node", "new-token", "v1", false))
        assertFalse(next.exists())
    }
}
