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
        // Interrupted writes are never cache hits.
        directory.listFiles()?.filter { it.name.endsWith(".tmp") }?.forEach { Files.deleteIfExists(it.toPath()) }
        return directory
    }
}
