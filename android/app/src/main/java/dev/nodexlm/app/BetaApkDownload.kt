// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.app.DownloadManager
import android.content.Context
import android.net.Uri
import android.os.Environment
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest

/**
 * Opt-in testing artifact from the repository's public prerelease. It does not
 * enter the signed stable updater, and it never invokes PackageInstaller.
 */
internal object BetaApkDownload {
    private const val PREFS = "nodexlm_beta_apk"
    private const val API = "https://api.github.com/repos/rfdetoni/nodexlm/releases/tags/android-debug"
    private const val MAX_METADATA = 256 * 1024

    internal data class State(
        val id: Long,
        val status: Int,
        val received: Long,
        val total: Long,
        val verified: Boolean,
        val error: String?
    )

    fun fetchArtifact(): BetaArtifact {
        val connection = URL(API).openConnection() as HttpURLConnection
        connection.connectTimeout = 10_000
        connection.readTimeout = 15_000
        connection.instanceFollowRedirects = false
        connection.setRequestProperty("User-Agent", "NodexLM-Android-Beta")
        connection.setRequestProperty("Accept", "application/vnd.github+json")
        try {
            check(connection.responseCode == HttpURLConnection.HTTP_OK) {
                "GitHub prerelease HTTP ${connection.responseCode}"
            }
            val output = ByteArrayOutputStream()
            connection.inputStream.use { stream ->
                val buffer = ByteArray(4096)
                while (true) {
                    val n = stream.read(buffer)
                    if (n < 0) break
                    require(output.size() + n <= MAX_METADATA) { "GitHub metadata too large" }
                    output.write(buffer, 0, n)
                }
            }
            val release = JSONObject(output.toString(Charsets.UTF_8.name()))
            val assets = release.getJSONArray("assets")
            for (i in 0 until assets.length()) {
                val item = assets.optJSONObject(i) ?: continue
                if (item.optString("name") != BetaArtifactPolicy.FILE) continue
                return BetaArtifactPolicy.validate(
                    release.optString("tag_name"),
                    release.optBoolean("prerelease"),
                    release.optBoolean("draft"),
                    item.optString("name"),
                    item.optString("browser_download_url"),
                    item.optString("digest"),
                    item.optLong("size")
                )
            }
            error("The public prerelease has no Android beta APK")
        } finally {
            connection.disconnect()
        }
    }

    @Synchronized
    fun enqueue(context: Context, artifact: BetaArtifact): Long {
        // Recheck the fixed URL and digest before touching DownloadManager.
        val valid = BetaArtifactPolicy.validate(
            BetaArtifactPolicy.TAG, true, false, BetaArtifactPolicy.FILE,
            artifact.url, "sha256:" + artifact.sha256, artifact.sizeBytes
        )
        val manager = context.getSystemService(DownloadManager::class.java)
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val previous = prefs.getLong("id", 0L)
        if (previous > 0L) manager.remove(previous)
        prefs.edit().clear().commit()

        // Public Downloads, not the app-private model directory: the user can
        // find and manage the APK without granting access to Android/data.
        val fileName = "NodexLM-android-beta-${valid.sha256.take(12)}.apk"
        val request = DownloadManager.Request(Uri.parse(valid.url))
            .setTitle("NodexLM Android beta")
            .setDescription("APK beta assinada: atualiza sem reinstalar após migração da antiga chave debug")
            .setMimeType("application/vnd.android.package-archive")
            .setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, fileName)
            .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            .setAllowedOverMetered(true)
            .setAllowedOverRoaming(false)
        val id = manager.enqueue(request)
        prefs.edit().putLong("id", id)
            .putString("sha256", valid.sha256)
            .putLong("expected_bytes", valid.sizeBytes)
            .putBoolean("verified", false)
            .remove("error")
            .apply()
        return id
    }

    fun state(context: Context): State {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val id = prefs.getLong("id", 0L)
        var status = -1
        var received = 0L
        var total = 0L
        if (id > 0) {
            val manager = context.getSystemService(DownloadManager::class.java)
            try {
                manager.query(DownloadManager.Query().setFilterById(id))?.use { cursor ->
                    if (cursor.moveToFirst()) {
                        status = cursor.getInt(cursor.getColumnIndexOrThrow(DownloadManager.COLUMN_STATUS))
                        received = cursor.getLong(cursor.getColumnIndexOrThrow(DownloadManager.COLUMN_BYTES_DOWNLOADED_SO_FAR)).coerceAtLeast(0)
                        total = cursor.getLong(cursor.getColumnIndexOrThrow(DownloadManager.COLUMN_TOTAL_SIZE_BYTES)).coerceAtLeast(0)
                    }
                }
            } catch (_: Exception) { /* Status is informational; verification still required. */ }
        }
        return State(id, status, received, total,
            status == DownloadManager.STATUS_SUCCESSFUL && prefs.getBoolean("verified", false),
            prefs.getString("error", null))
    }

    /**
     * Called on a worker thread only. Re-reads the exact DownloadManager
     * bytes before confirming a finished APK. Failed hashes are deleted.
     */
    fun verify(context: Context): Boolean {
        val manager = context.getSystemService(DownloadManager::class.java)
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val id = prefs.getLong("id", 0L)
        if (id <= 0 || state(context).status != DownloadManager.STATUS_SUCCESSFUL) return false
        if (prefs.getBoolean("verified", false)) return true
        return try {
            val digest = MessageDigest.getInstance("SHA-256")
            var count = 0L
            val uri = manager.getUriForDownloadedFile(id) ?: error("APK download URI missing")
            context.contentResolver.openInputStream(uri)?.use { input ->
                val buffer = ByteArray(64 * 1024)
                while (true) {
                    val n = input.read(buffer)
                    if (n < 0) break
                    digest.update(buffer, 0, n)
                    count += n
                }
            } ?: error("Cannot read the downloaded APK")
            val actual = digest.digest().joinToString("") { "%02x".format(it.toInt() and 255) }
            require(count == prefs.getLong("expected_bytes", -1L) &&
                actual == prefs.getString("sha256", null)) {
                "APK beta failed SHA-256/size verification"
            }
            if (prefs.getLong("id", 0L) != id) return false
            prefs.edit().putBoolean("verified", true).remove("error").apply()
            true
        } catch (e: Exception) {
            if (prefs.getLong("id", 0L) == id) {
                manager.remove(id)
                prefs.edit().putLong("id", 0L).putBoolean("verified", false)
                    .putString("error", (e.message ?: "APK verification failed").take(180)).apply()
            }
            false
        }
    }
}
