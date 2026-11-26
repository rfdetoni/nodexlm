// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import java.net.URI
import java.net.URL
import java.util.Locale
import javax.net.ssl.HttpsURLConnection

/**
 * Resolve every GGUF shard to one immutable Hugging Face commit and require
 * a SHA-256 identity before DownloadManager is allowed to enqueue it.
 * Network metadata is requested off the UI thread; file verification is
 * performed separately after the download completes.
 */
internal object VerifiedHuggingFaceDownload {
    data class Metadata(val revision: String?, val sha256: String, val size: Long)
    data class Part(val name: String, val url: String, val sha256: String, val size: Long)
    data class Plan(val parts: List<Part>, val totalBytes: Long)

    private val sha256Pattern = Regex("[0-9a-f]{64}")
    private val commitPattern = Regex("[0-9a-f]{40}")
    private val safePartPattern = Regex("[a-zA-Z0-9._-]{1,100}\\.gguf", RegexOption.IGNORE_CASE)

    private fun digest(raw: String?): String? {
        val value = raw?.trim() ?: return null
        if (value.startsWith("W/")) return null
        return value.removeSurrounding("\"").lowercase(Locale.ROOT).takeIf(sha256Pattern::matches)
    }

    internal fun parseMetadata(
        revision: String?, linkedEtag: String?, etag: String?,
        linkedSize: String?, contentLength: Long
    ): Metadata {
        val commit = revision?.trim()?.lowercase(Locale.ROOT)
        require(commit == null || commitPattern.matches(commit)) { "Invalid model revision" }
        val checksum = digest(linkedEtag) ?: digest(etag)
        require(checksum != null) { "Model has no verifiable SHA-256 identity" }
        val size = linkedSize?.trim()?.toLongOrNull() ?: contentLength
        require(size >= 8) { "Invalid GGUF shard size" }
        return Metadata(commit, checksum, size)
    }

    internal fun pinnedUrls(source: String, revision: String, parts: List<String>): List<String> {
        val uri = URI(source)
        require(uri.scheme == "https" && uri.host.equals("huggingface.co", true) &&
            uri.port == -1 && uri.userInfo == null && uri.rawQuery == null && uri.rawFragment == null) {
            "Only canonical Hugging Face HTTPS model URLs are accepted"
        }
        require(commitPattern.matches(revision) && parts.isNotEmpty() && parts.size <= 128)
        require(parts.all(safePartPattern::matches)) { "Invalid GGUF shard filename" }
        require(uri.rawPath?.contains("/resolve/main/") == true) { "Expected mutable Hugging Face resolve URL" }
        // Sanitizing an original sharded filename changes the remote shard names.
        // Reject that case rather than silently requesting the wrong siblings.
        if (parts.size > 1) require(uri.path.substringAfterLast('/') == parts.first()) {
            "Sharded GGUF filenames must not require normalization"
        }
        val pinnedFirst = source.replace("/resolve/main/", "/resolve/$revision/")
        val prefix = pinnedFirst.substringBeforeLast('/') + "/"
        return listOf(pinnedFirst) + parts.drop(1).map { prefix + it }
    }

    fun prepare(source: String, names: List<String>, expectedSize: Long): Plan {
        require(expectedSize >= 8) { "Invalid expected model size" }
        // Validate the URL before issuing any request.
        pinnedUrls(source, "0".repeat(40), names)
        val revision = head(source).revision ?: error("Model revision cannot be pinned")
        val urls = pinnedUrls(source, revision, names)
        var total = 0L
        val parts = urls.mapIndexed { index, url ->
            val meta = head(url)
            require(meta.revision == null || meta.revision == revision) { "Model revision changed" }
            require(meta.size <= expectedSize - total) { "Model size changed; refresh the catalog" }
            total += meta.size
            Part(names[index], url, meta.sha256, meta.size)
        }
        require(total == expectedSize) { "Model size changed; refresh the catalog" }
        return Plan(parts, total)
    }

    private fun head(source: String): Metadata {
        val connection = URL(source).openConnection() as HttpsURLConnection
        connection.requestMethod = "HEAD"
        connection.instanceFollowRedirects = false
        connection.connectTimeout = 8_000
        connection.readTimeout = 15_000
        connection.setRequestProperty("Accept-Encoding", "identity")
        try {
            require(connection.responseCode in listOf(200, 302, 307, 308)) {
                "Model metadata HTTP ${connection.responseCode}"
            }
            return parseMetadata(
                connection.getHeaderField("X-Repo-Commit"),
                connection.getHeaderField("X-Linked-Etag"),
                connection.getHeaderField("ETag"),
                connection.getHeaderField("X-Linked-Size"),
                connection.contentLengthLong
            )
        } finally {
            connection.disconnect()
        }
    }
}
