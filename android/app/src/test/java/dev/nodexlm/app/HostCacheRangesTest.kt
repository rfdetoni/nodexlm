// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File
import java.io.InputStream
import java.io.RandomAccessFile
import java.security.MessageDigest
import java.util.concurrent.atomic.AtomicInteger

class HostCacheRangesTest {
    @get:Rule val folder = TemporaryFolder()
    private val chunk = HostCacheRanges.CHUNK_BYTES
    private val length = 4 * chunk

    @Test fun rangesHaveNoGapsOrOverlaps() {
        for (size in listOf(8L, chunk, chunk + 1, length + 77)) {
            val all = (0 until HostCacheRanges.count(size)).map { HostCacheRanges.range(it, size) }
            assertEquals(0L, all.first().first)
            assertEquals(size - 1, all.last().last)
            assertEquals(size, all.sumOf { it.length })
            all.zipWithNext().forEach { (left, right) -> assertEquals(left.last + 1, right.first) }
        }
    }

    @Test fun journalsRequireMatchingImmutableIdentityAndWholeRecords() {
        val checksum = "a".repeat(64)
        val header = "nodexlm-host-ranges-v1 $checksum $length $chunk\n"
        val flags = HostCacheRanges.parseJournal(header + "0\n2\n", checksum, length)
        assertNotNull(flags)
        assertArrayEquals(booleanArrayOf(true, false, true, false), flags)
        for (candidate in listOf(
            header + "0\n0\n",
            header + "0\n4\n",
            header + "-1\n",
            header + "0",
            header.replace(checksum, "b".repeat(64)) + "0\n",
            header + "broken\n"
        )) {
            assertNull("stale or incomplete checkpoint accepted", HostCacheRanges.parseJournal(candidate, checksum, length))
        }
    }

    /** A large file is generated from 128-KiB buffers, not loaded in RAM. */
    private fun checksum(size: Long): String {
        val sha = MessageDigest.getInstance("SHA-256")
        var position = 0L
        while (position < size) {
            val n = minOf(128 * 1024L, size - position).toInt()
            val bytes = ByteArray(n)
            if (position == 0L) "GGUF".toByteArray().copyInto(bytes)
            sha.update(bytes)
            position += n
        }
        return sha.digest().joinToString("") { "%02x".format(it.toInt() and 255) }
    }

    private fun source(range: HostCacheRanges.Range): InputStream = object : InputStream() {
        private var next = range.first
        override fun read(): Int {
            if (next > range.last) return -1
            val value = if (next < 4) "GGUF"[next.toInt()].code else 0
            next++
            return value
        }
        override fun read(data: ByteArray, offset: Int, max: Int): Int {
            if (next > range.last) return -1
            val n = minOf(max.toLong(), range.last - next + 1).toInt()
            data.fill(0, offset, offset + n)
            for (i in 0 until n) {
                val pos = next + i
                if (pos < 4) data[offset + i] = "GGUF"[pos.toInt()].code.toByte()
            }
            next += n
            return n
        }
    }

    @Test fun parallelTransferVerifiesFullFileAndCleansJournal() {
        val stage = File(folder.root, "weights.gguf.partial")
        val sha = checksum(length)
        val opened = AtomicInteger()
        var completed = 0L
        HostCacheRanges.transfer(stage, sha, length, { false }, { completed = it }) { part ->
            opened.incrementAndGet()
            source(part)
        }
        assertEquals(4, opened.get())
        assertEquals(length, completed)
        assertEquals(length, stage.length())
        assertFalse(File(stage.path + ".ranges").exists())
        RandomAccessFile(stage, "r").use { file ->
            val bytes = ByteArray(4)
            file.readFully(bytes)
            assertArrayEquals("GGUF".toByteArray(), bytes)
        }
    }

    @Test fun restartOnlyFetchesUncommittedRanges() {
        val stage = File(folder.root, "restart.gguf.partial")
        val sha = checksum(length)
        RandomAccessFile(stage, "rw").use { file ->
            file.setLength(length)
            val bytes = ByteArray(128 * 1024)
            var remaining = chunk
            var offset = 0L
            while (remaining > 0) {
                bytes.fill(0)
                if (offset == 0L) "GGUF".toByteArray().copyInto(bytes)
                file.write(bytes)
                offset += bytes.size
                remaining -= bytes.size
            }
            file.fd.sync()
        }
        File(stage.path + ".ranges").writeText(
            "nodexlm-host-ranges-v1 $sha $length $chunk\n0\n"
        )
        val opened = mutableListOf<Int>()
        val lock = Any()
        var firstProgress = -1L
        HostCacheRanges.transfer(stage, sha, length, { false },
            { if (firstProgress == -1L) firstProgress = it }
        ) { part ->
            synchronized(lock) { opened += part.index }
            source(part)
        }
        assertEquals(chunk, firstProgress)
        assertEquals(listOf(1, 2, 3), opened.sorted())
        assertFalse(File(stage.path + ".ranges").exists())
    }

    @Test fun invalidFullChecksumCannotPublishCheckpointedChunks() {
        val stage = File(folder.root, "corrupt.gguf.partial")
        try {
            HostCacheRanges.transfer(stage, "0".repeat(64), length,
                { false }, {}) { part -> source(part) }
            fail("corrupt full model was accepted")
        } catch (expected: java.io.IOException) {
            assertTrue(expected.message.orEmpty().contains("SHA-256"))
        }
        assertFalse(stage.exists())
        assertFalse(File(stage.path + ".ranges").exists())
    }
}
