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
        val data = File(first, "0000000000000001").apply { writeText("weights") }
        File(first, "0000000000000001.tmp").writeText("partial")
        assertEquals(first, RpcWeightCache.prepare(base, byteArrayOf(1), "node", "token", "v1", true))
        assertTrue(data.exists())
        assertFalse(File(first, "abcd.tmp").exists())
        val next = RpcWeightCache.prepare(base, byteArrayOf(1), "node", "new-token", "v1", true)!!
        assertNotEquals(first, next)
        assertFalse(first.exists())
        assertNull(RpcWeightCache.prepare(base, byteArrayOf(1), "node", "new-token", "v1", false))
        assertFalse(next.exists())
    }
    @Test fun evictsOldestAndRejectsInvalidEntries() {
        val root = folder.newFolder()
        val oldest = File(root, "0000000000000001").apply {
            writeText("abcd")
            setLastModified(1000)
        }
        val newer = File(root, "0000000000000002").apply {
            writeText("efgh")
            setLastModified(2000)
        }
        val freshest = File(root, "0000000000000003").apply {
            writeText("ijkl")
            setLastModified(3000)
        }
        val unsafe = File(root, "incomplete.tmp").apply { writeText("partial") }
        assertTrue(RpcWeightCache.prune(root, maxBytes = 12, targetBytes = 8, reserveBytes = 0))
        assertFalse(oldest.exists())
        assertTrue(newer.exists())
        assertTrue(freshest.exists())
        assertFalse(unsafe.exists())
    }

    @Test fun rejectsSymlinkAsCacheRoot() {
        val real = folder.newFolder()
        val alias = File(folder.root, "linked")
        java.nio.file.Files.createSymbolicLink(alias.toPath(), real.toPath())
        assertFalse(RpcWeightCache.prune(alias, reserveBytes = 0))
    }
}
