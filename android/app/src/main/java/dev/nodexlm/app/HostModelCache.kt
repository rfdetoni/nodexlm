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

    fun fetch(
        context: Context, checksum: String, size: Long, temporary: File,
        cancelled: () -> Boolean, progress: (Long) -> Unit
    ): Boolean {
        if (!checksum.matches(Regex("[0-9a-f]{64}")) || size < 8 || !available(context)) return false
        val prefs = context.getSharedPreferences("nodexlm", Context.MODE_PRIVATE)
        val url = prefs.getString("coordinator", "").orEmpty().trimEnd('/') +
            "/v1/nodes/cache/" + checksum
        val connection = try {
            (URL(url).openConnection() as HttpsURLConnection).also { conn ->
                conn.requestMethod = "GET"
                conn.instanceFollowRedirects = false
                conn.connectTimeout = 7_000
                conn.readTimeout = 20_000
                PinnedHostTls.configure(conn, context)
                conn.setRequestProperty("Authorization", "Bearer " + NodeSecrets.read(context))
                conn.setRequestProperty("X-Nodex-Node", prefs.getString("node_id", "").orEmpty())
                conn.setRequestProperty("Accept-Encoding", "identity")
            }
        } catch (_: Exception) { return false }
        try {
            if (connection.responseCode != 200 || connection.contentLengthLong != size ||
                connection.getHeaderField("ETag")?.trim('"') != checksum
            ) return false
            val digest = MessageDigest.getInstance("SHA-256")
            var written = 0L
            FileOutputStream(temporary).use { output ->
                connection.inputStream.use { input ->
                    val buffer = ByteArray(256 * 1024)
                    while (true) {
                        if (cancelled()) return false
                        val read = input.read(buffer)
                        if (read < 0) break
                        written += read
                        if (written > size) return false
                        digest.update(buffer, 0, read)
                        output.write(buffer, 0, read)
                        progress(written)
                    }
                }
                output.fd.sync()
            }
            if (cancelled() || written != size) return false
            val actual = digest.digest().joinToString("") { "%02x".format(it.toInt() and 255) }
            return actual == checksum
        } catch (_: Exception) {
            return false
        } finally {
            connection.disconnect()
            // The caller atomically renames a verified temporary file; on any
            // failure the file is not visible to the GGUF library.
            if (temporary.exists() && (cancelled() || temporary.length() != size)) temporary.delete()
        }
    }
}
