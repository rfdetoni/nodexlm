// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import java.io.File
import java.io.IOException
import java.io.InputStream
import java.io.RandomAccessFile
import java.nio.file.Files
import java.security.MessageDigest
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.atomic.AtomicReference
import kotlin.concurrent.thread

/**
 * Transport-agnostic parallel range assembler. Only the caller creates TLS
 * streams; this class never receives a URL, secret or a trust-manager override.
 * A checkpoint is advisory: *every* published model is hashed from disk.
 */
internal object HostCacheRanges {
    const val CHUNK_BYTES = 8L * 1024 * 1024
    const val MIN_PARALLEL_BYTES = 32L * 1024 * 1024
    private const val MAX_MODEL_BYTES = 150L * 1024 * 1024 * 1024
    private const val MAX_JOURNAL_BYTES = 1024L * 1024
    private val checksumPattern = Regex("[0-9a-f]{64}")

    internal class UnsupportedRange : IOException("Host does not support strong byte ranges")
    internal data class Range(val index: Int, val first: Long, val last: Long) {
        val length: Long get() = last - first + 1
    }

    internal fun count(size: Long): Int {
        require(size in 8..MAX_MODEL_BYTES)
        return ((size - 1) / CHUNK_BYTES + 1).toInt()
    }

    internal fun range(index: Int, size: Long): Range {
        require(index in 0 until count(size))
        val first = index.toLong() * CHUNK_BYTES
        val last = minOf(size - 1, first + CHUNK_BYTES - 1)
        return Range(index, first, last)
    }

    private fun identity(checksum: String, size: Long): String =
        "nodexlm-host-ranges-v1 $checksum $size $CHUNK_BYTES"

    internal fun parseJournal(content: String, checksum: String, size: Long): BooleanArray? {
        if (content.length > MAX_JOURNAL_BYTES || !checksumPattern.matches(checksum) ||
            !content.endsWith("\n")) return null
        val lines = content.split('\n')
        if (lines.firstOrNull() != identity(checksum, size) || lines.lastOrNull() != "") return null
        val finished = BooleanArray(count(size))
        for (line in lines.subList(1, lines.size - 1)) {
            if (line.isEmpty() || line.length > 6) return null
            val n = line.toIntOrNull() ?: return null
            if (n !in finished.indices || finished[n]) return null
            finished[n] = true
        }
        return finished
    }

    internal fun discard(stage: File) {
        File(stage.path + ".ranges").delete()
        stage.delete()
    }

    /** Holds at most four streams and four 128-KiB buffers, without model copies. */
    @Synchronized
    internal fun transfer(
        stage: File,
        checksum: String,
        size: Long,
        cancelled: () -> Boolean,
        progress: (Long) -> Unit,
        open: (Range) -> InputStream
    ) {
        require(size in MIN_PARALLEL_BYTES..MAX_MODEL_BYTES && checksumPattern.matches(checksum))
        require(!Files.isSymbolicLink(stage.toPath())) { "Unsafe model stage" }
        val journalFile = File(stage.path + ".ranges")
        require(!Files.isSymbolicLink(journalFile.toPath())) { "Unsafe range journal" }

        val prior = if (stage.isFile && stage.length() == size &&
            journalFile.isFile && journalFile.length() <= MAX_JOURNAL_BYTES
        ) runCatching {
            parseJournal(journalFile.readText(Charsets.US_ASCII), checksum, size)
        }.getOrNull() else null

        RandomAccessFile(stage, "rw").use { file ->
            if (prior == null) file.setLength(0)
            file.setLength(size)
            file.fd.sync()
        }

        RandomAccessFile(journalFile, "rw").use { journal ->
            if (prior == null) {
                journal.setLength(0)
                journal.write((identity(checksum, size) + "\n").toByteArray(Charsets.US_ASCII))
                journal.fd.sync()
            }
            val done = prior ?: BooleanArray(count(size))
            val pending = done.indices.filter { !done[it] }
            val committed = AtomicLong(size - pending.sumOf { range(it, size).length })
            progress(committed.get())
            val cursor = AtomicInteger()
            val aborted = AtomicBoolean(false)
            val error = AtomicReference<Exception?>(null)
            val workerCount = if (size < 128L * 1024 * 1024) 2 else 4
            val workers = List(workerCount) { slot ->
                thread(start = false, name = "nodexlm-host-cache-range-$slot") {
                    try {
                        while (!aborted.get() && !cancelled()) {
                            val item = cursor.getAndIncrement()
                            if (item >= pending.size) break
                            val chunk = range(pending[item], size)
                            var success = false
                            var lastError: Exception? = null
                            for (attempt in 0..2) {
                                if (aborted.get() || cancelled()) throw IOException("Cancelled")
                                try {
                                    open(chunk).use { input ->
                                        RandomAccessFile(stage, "rw").use { output ->
                                            output.seek(chunk.first)
                                            var remaining = chunk.length
                                            val buffer = ByteArray(128 * 1024)
                                            while (remaining > 0) {
                                                if (aborted.get() || cancelled()) throw IOException("Cancelled")
                                                val n = input.read(buffer, 0, minOf(remaining, buffer.size.toLong()).toInt())
                                                if (n <= 0) throw IOException("Truncated Host range")
                                                output.write(buffer, 0, n)
                                                remaining -= n
                                            }
                                            output.fd.sync()
                                        }
                                    }
                                    synchronized(journal) {
                                        journal.seek(journal.length())
                                        journal.write((chunk.index.toString() + "\n").toByteArray(Charsets.US_ASCII))
                                        journal.fd.sync()
                                    }
                                    progress(committed.addAndGet(chunk.length))
                                    success = true
                                    break
                                } catch (unsupported: UnsupportedRange) {
                                    throw unsupported
                                } catch (e: Exception) {
                                    lastError = e
                                    if (cancelled()) break
                                    if (attempt < 2) Thread.sleep(150L * (attempt + 1))
                                }
                            }
                            if (!success) throw lastError ?: IOException("Cancelled")
                        }
                        if (cancelled()) throw IOException("Cancelled")
                    } catch (e: Exception) {
                        error.compareAndSet(null, e)
                        aborted.set(true)
                    }
                }
            }
            workers.forEach { it.start() }
            workers.forEach { it.join() }
            error.get()?.let { throw it }
            if (cancelled()) throw IOException("Cancelled")
            if (committed.get() != size) throw IOException("Incomplete Host cache transfer")
        }

        // The chunk journal is not an integrity proof. Verify the full model
        // from disk, independently of which ranges were previously checkpointed.
        if (!verify(stage, checksum, size, cancelled)) {
            discard(stage)
            throw IOException("Host model SHA-256 or GGUF signature mismatch")
        }
        if (!journalFile.delete() && journalFile.exists()) {
            // Do not leave an obsolete resume marker next to a published GGUF.
            throw IOException("Cannot remove completed Host range journal")
        }
    }

    private fun verify(stage: File, checksum: String, size: Long, cancelled: () -> Boolean): Boolean {
        if (!stage.isFile || stage.length() != size) return false
        val digest = MessageDigest.getInstance("SHA-256")
        stage.inputStream().buffered(128 * 1024).use { input ->
            val buffer = ByteArray(128 * 1024)
            val magic = ByteArray(4)
            var headerRead = 0
            while (headerRead < magic.size) {
                val n = input.read(magic, headerRead, magic.size - headerRead)
                if (n <= 0) return false
                headerRead += n
            }
            if (!magic.contentEquals("GGUF".toByteArray(Charsets.US_ASCII))) return false
            digest.update(magic)
            while (true) {
                if (cancelled()) throw IOException("Cancelled")
                val n = input.read(buffer)
                if (n < 0) break
                digest.update(buffer, 0, n)
            }
        }
        val hash = digest.digest().joinToString("") { "%02x".format(it.toInt() and 255) }
        return hash == checksum
    }
}
