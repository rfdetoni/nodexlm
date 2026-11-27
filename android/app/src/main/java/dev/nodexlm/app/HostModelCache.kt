// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.content.Context
import java.io.File
import java.io.FileOutputStream
import java.net.URI
import java.net.URL
import java.security.MessageDigest
import javax.net.ssl.HttpsURLConnection

/** Download only SHA-256-identified models from the paired TLS-pinned Host.
 * A cache miss allows the public Hugging Face transport to take over.
 */
internal object HostModelCache {
    fun available(context: Context): Boolean {
        val prefs = context.getSharedPreferences("nodexlm", Context.MODE_PRIVATE)
        val base = prefs.getString("coordinator", "").orEmpty()
        return runCatching {
            val u = URI(base)
            u.scheme == "https" && u.host != null && u.userInfo == null &&
                u.rawQuery == null && u.rawFragment == null &&
                u.path.orEmpty() in listOf("", "/") &&
                prefs.getString("node_id", "").orEmpty().isNotBlank() &&
                NodeSecrets.read(context).isNotBlank()
        }.getOrDefault(false)
    }

    /** Existing Host cache is enrolled, pinned and content-addressed. */
    fun fetch(
        context: Context, checksum: String, size: Long, temporary: File,
        cancelled: () -> Boolean, progress: (Long) -> Unit
    ): Boolean {
        if (!checksum.matches(Regex("[0-9a-f]{64}")) || size !in 8L..(150L shl 30) ||
            !available(context)) return false
        val prefs = context.getSharedPreferences("nodexlm", Context.MODE_PRIVATE)
        val url = prefs.getString("coordinator", "").orEmpty().trimEnd('/') +
            "/v1/nodes/cache/" + checksum
        val credential = try {
            Credential(
                PinnedHostTls.certificate(context),
                NodeSecrets.read(context),
                prefs.getString("node_id", "").orEmpty()
            )
        } catch (_: Exception) {
            return false
        }
        // The authenticated HEAD forces the Host to check the indexed SHA-256
        // before parallel requests enter the bounded transfer slots. It also
        // prevents allocating multi-GB sparse stages for cache misses.
        val verified = runCatching {
            var accepted = false
            for (attempt in 0..3) {
                if (cancelled()) break
                val conn = open(url, credential, "HEAD")
                val result = try {
                    val code = conn.responseCode
                    code to (code == 200 && conn.contentLengthLong == size &&
                        conn.getHeaderField("ETag") == "\"$checksum\"")
                } finally {
                    conn.disconnect()
                }
                if (result.second) {
                    accepted = true
                    break
                }
                if (result.first != 429 || attempt == 3) break
                Thread.sleep(500L * (attempt + 1))
            }
            accepted
        }.getOrDefault(false)
        if (!verified || cancelled()) return false

        if (size >= HostCacheRanges.MIN_PARALLEL_BYTES) {
            try {
                HostCacheRanges.transfer(temporary, checksum, size, cancelled, progress) { range ->
                    val conn = open(url, credential, "GET")
                    try {
                        conn.setRequestProperty("Range", "bytes=" + range.first + "-" + range.last)
                        conn.setRequestProperty("If-Range", "\"$checksum\"")
                        val code = conn.responseCode
                        if (code == 200 || code == 416) throw HostCacheRanges.UnsupportedRange()
                        if (code != 206 ||
                            conn.getHeaderField("ETag") != "\"$checksum\"" ||
                            conn.getHeaderField("Content-Range") !=
                                "bytes " + range.first + "-" + range.last + "/" + size ||
                            conn.contentLengthLong != range.length
                        ) throw java.io.IOException("Invalid Host byte range metadata")
                        object : java.io.FilterInputStream(conn.inputStream) {
                            override fun close() {
                                try { super.close() } finally { conn.disconnect() }
                            }
                        }
                    } catch (error: Exception) {
                        conn.disconnect()
                        throw error
                    }
                }
                return true
            } catch (_: HostCacheRanges.UnsupportedRange) {
                // Old Hosts may not support partial content. Do not retry
                // fragments against a single-stream server; preserve fallback.
                HostCacheRanges.discard(temporary)
            } catch (_: Exception) {
                // Keep fsync-committed ranges for a future resumed transfer.
                // The UI falls back to a verified public download on this miss.
                return false
            }
        }

        // Serial fallback for small models and Hosts lacking strong Range
        // support. It never publishes anything before the complete SHA-256.
        // A small model must not accidentally inherit a stale range journal.
        if (size < HostCacheRanges.MIN_PARALLEL_BYTES) HostCacheRanges.discard(temporary)
        val conn = try { open(url, credential, "GET") } catch (_: Exception) { return false }
        try {
            if (conn.responseCode != 200 || conn.contentLengthLong != size ||
                conn.getHeaderField("ETag") != "\"$checksum\"") return false
            val digest = MessageDigest.getInstance("SHA-256")
            var written = 0L
            FileOutputStream(temporary).use { output ->
                conn.inputStream.use { input ->
                    val buffer = ByteArray(128 * 1024)
                    while (true) {
                        if (cancelled()) return false
                        val n = input.read(buffer)
                        if (n < 0) break
                        written += n
                        if (written > size) return false
                        digest.update(buffer, 0, n)
                        output.write(buffer, 0, n)
                        progress(written)
                    }
                }
                output.fd.sync()
            }
            if (cancelled() || written != size) return false
            val actual = digest.digest().joinToString("") { "%02x".format(it.toInt() and 255) }
            if (actual != checksum) {
                temporary.delete()
                return false
            }
            return true
        } catch (_: Exception) {
            return false
        } finally {
            conn.disconnect()
            if (cancelled()) temporary.delete()
        }
    }

    private data class Credential(val certificate: ByteArray, val token: String, val node: String)

    private fun open(url: String, credential: Credential, method: String): HttpsURLConnection {
        val conn = URL(url).openConnection() as HttpsURLConnection
        try {
            conn.requestMethod = method
            conn.instanceFollowRedirects = false
            conn.connectTimeout = 7_000
            // The first authenticated HEAD after a Host restart may hash
            // tens of GB before returning. Do not spuriously treat its
            // bounded cold verification as a network failure.
            conn.readTimeout = if (method == "HEAD") 300_000 else 20_000
            PinnedHostTls.configure(conn, credential.certificate)
            conn.setRequestProperty("Authorization", "Bearer " + credential.token)
            conn.setRequestProperty("X-Nodex-Node", credential.node)
            conn.setRequestProperty("Accept-Encoding", "identity")
            return conn
        } catch (error: Exception) {
            conn.disconnect()
            throw error
        }
    }
}
