// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import java.io.File
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.SimpleFileVisitor
import java.nio.file.FileVisitResult
import java.nio.file.attribute.BasicFileAttributes
import java.security.MessageDigest

/** A single app-private, non-backed-up cache scoped to the current enrollment. */
internal object RpcWeightCache {
    private fun removeTree(root: Path) {
        if (!Files.exists(root, java.nio.file.LinkOption.NOFOLLOW_LINKS)) return
        Files.walkFileTree(root, object : SimpleFileVisitor<Path>() {
            override fun visitFile(file: Path, attrs: BasicFileAttributes): FileVisitResult {
                Files.delete(file)
                return FileVisitResult.CONTINUE
            }
            override fun postVisitDirectory(dir: Path, exc: java.io.IOException?): FileVisitResult {
                if (exc != null) throw exc
                Files.delete(dir)
                return FileVisitResult.CONTINUE
            }
        })
    }


    private const val QUOTA_BYTES = 512L * 1024L * 1024L
    private const val TARGET_BYTES = 384L * 1024L * 1024L
    private const val RESERVE_BYTES = 512L * 1024L * 1024L
    private const val ENTRY_LIMIT = 16L * 1024L * 1024L
    private val entryName = Regex("[0-9a-f]{16}")

    /** Trim only an app-private enrollment directory. Test budgets are small
     * so we can verify oldest-first eviction without writing 512 MiB in CI.
     */
    internal fun prune(
        directory: File,
        maxBytes: Long = QUOTA_BYTES,
        targetBytes: Long = TARGET_BYTES,
        reserveBytes: Long = RESERVE_BYTES
    ): Boolean {
        require(maxBytes >= targetBytes && targetBytes >= 0 && reserveBytes >= 0)
        if (Files.isSymbolicLink(directory.toPath()) || !directory.isDirectory) return false
        val entries = directory.listFiles() ?: return false
        val valid = mutableListOf<File>()
        var occupied = 0L
        for (entry in entries) {
            val path = entry.toPath()
            if (Files.isSymbolicLink(path) || !entry.isFile ||
                !entryName.matches(entry.name) || entry.length() !in 1L..ENTRY_LIMIT
            ) {
                if (entry.isDirectory && !Files.isSymbolicLink(path)) removeTree(path)
                else Files.deleteIfExists(path)
                continue
            }
            valid.add(entry)
            occupied += entry.length()
        }
        // Keep free disk above the reserve, and bound cache occupancy and
        // inode count. Avoid symlink-based path escapes during eviction.
        val oldest = valid.sortedWith(compareBy<File> { it.lastModified() }.thenBy { it.name })
        var count = valid.size
        for (entry in oldest) {
            if (occupied <= targetBytes && occupied <= maxBytes &&
                directory.usableSpace >= reserveBytes && count <= 1024) break
            val length = entry.length()
            if (Files.deleteIfExists(entry.toPath())) {
                occupied -= length
                count--
            }
        }
        return occupied <= maxBytes && directory.usableSpace >= reserveBytes && count <= 1024
    }

    fun prepare(base: File, certificate: ByteArray, node: String, token: String, revision: String, enabled: Boolean): File? {
        val root = File(base, "rpc-weights-v1")
        if (!enabled) {
            removeTree(root.toPath())
            return null
        }
        // Length-separated inputs; never persist the credential itself.
        val md = MessageDigest.getInstance("SHA-256")
        for (part in listOf(certificate, node.toByteArray(Charsets.UTF_8), token.toByteArray(Charsets.UTF_8), revision.toByteArray(Charsets.UTF_8))) {
            md.update(java.nio.ByteBuffer.allocate(4).putInt(part.size).array())
            md.update(part)
        }
        val scope = md.digest().joinToString("") { "%02x".format(it.toInt() and 255) }
        val directory = File(root, scope)
        // Keep one enrollment only, and never follow links during cleanup.
        if (Files.isSymbolicLink(root.toPath()) || !root.isDirectory || root.listFiles()?.let {
                it.size != 1 || it[0].name != scope || !it[0].isDirectory || Files.isSymbolicLink(it[0].toPath())
            } != false) {
            removeTree(root.toPath())
            check(directory.mkdirs()) { "Cannot create private RPC weight cache" }
        }
        // A bounded cold-start eviction leaves headroom for newly delivered
        // tensors. Native reads verify FNV before using an entry. Untrusted
        // directory entries are never followed or counted as valid cache hits.
        if (!prune(directory)) return null
        return directory
    }
}
