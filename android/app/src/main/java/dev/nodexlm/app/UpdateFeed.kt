// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.app.DownloadManager
import android.content.Context
import android.net.Uri
import android.os.Environment
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.File
import java.net.HttpURLConnection
import java.net.URL

/** Network access is intentionally independent of Activity / foreground RPC. */
internal object UpdateFeed {
    const val PREFS = "nodexlm_updates"
    const val RELEASES_URL = "https://github.com/rfdetoni/nodexlm/releases"
    const val MANIFEST_URL =
        "https://github.com/rfdetoni/nodexlm/releases/latest/download/update.json"
    private const val MAX_MANIFEST_BYTES = 8192
    private const val MAX_ERROR_LENGTH = 180

    fun installedVersionCode(context: Context): Long =
        context.packageManager.getPackageInfo(context.packageName, 0).longVersionCode

    /** A GitHub 404 is an absence of a published stable Android update, not an APK. */
    fun fetch(): UpdateRelease? {
        val connection = URL(MANIFEST_URL).openConnection() as HttpURLConnection
        connection.connectTimeout = 10_000
        connection.readTimeout = 15_000
        connection.setRequestProperty("User-Agent", "NodexLM-Android-Updater")
        connection.setRequestProperty("Accept", "application/json")
        try {
            val code = connection.responseCode
            if (code == HttpURLConnection.HTTP_NOT_FOUND) return null
            check(code == HttpURLConnection.HTTP_OK) { "GitHub Releases HTTP " + code }
            val buffer = ByteArray(4096)
            val output = ByteArrayOutputStream()
            connection.inputStream.use { stream ->
                while (true) {
                    val read = stream.read(buffer)
                    if (read < 0) break
                    require(output.size() + read <= MAX_MANIFEST_BYTES) {
                        "GitHub update manifest exceeds 8 KiB"
                    }
                    output.write(buffer, 0, read)
                }
            }
            val data = JSONObject(output.toString(Charsets.UTF_8.name()))
            return UpdateReleasePolicy.validate(
                schema = data.getInt("schema"),
                versionCode = data.getInt("versionCode"),
                versionName = data.getString("versionName"),
                sha256 = data.getString("sha256"),
                apkUrl = data.getString("apkUrl")
            )
        } finally {
            connection.disconnect()
        }
    }

    @Synchronized
    fun check(context: Context): UpdateRelease? {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val now = System.currentTimeMillis()
        prefs.edit().putLong("last_attempt", now).apply()
        try {
            val release = fetch()
            val newer = release != null && release.versionCode.toLong() > installedVersionCode(context)
            val edit = prefs.edit()
                .putLong("last_success", System.currentTimeMillis())
                .remove("check_error")
                .putString("feed_status", when {
                    release == null -> "unpublished"
                    newer -> "available"
                    else -> "current"
                })
            if (newer) {
                edit.putInt("available_version", release!!.versionCode)
                    .putString("available_name", release.versionName)
                    .putString("available_sha", release.sha256)
                    .putString("available_url", release.apkUrl)
            } else {
                edit.remove("available_version").remove("available_name")
                    .remove("available_sha").remove("available_url")
            }
            edit.apply()
            return if (newer) release else null
        } catch (e: Exception) {
            prefs.edit().putString("feed_status", "error")
                .putString("check_error", (e.message ?: e.javaClass.simpleName).take(MAX_ERROR_LENGTH))
                .apply()
            throw e
        }
    }

    fun available(context: Context): UpdateRelease? {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val version = prefs.getInt("available_version", -1)
        if (version <= installedVersionCode(context) || version < 1) return null
        return try {
            UpdateReleasePolicy.validate(
                1, version,
                prefs.getString("available_name", "") ?: "",
                prefs.getString("available_sha", "") ?: "",
                prefs.getString("available_url", "") ?: ""
            )
        } catch (_: Exception) {
            null
        }
    }

    /** DownloadManager owns the transfer, including network policy and retries.
     * Automatic downloads are strictly Wi-Fi/unmetered and opt-in. Installation
     * always requires a separate explicit user action plus Android confirmation.
     */
    @Synchronized
    fun enqueue(context: Context, release: UpdateRelease, allowMetered: Boolean): Long {
        val checked = UpdateReleasePolicy.validate(1, release.versionCode, release.versionName,
            release.sha256, release.apkUrl)
        require(checked.versionCode.toLong() > installedVersionCode(context)) {
            "Cannot install the same or an older version"
        }
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val manager = context.getSystemService(DownloadManager::class.java)
        val previous = prefs.getLong("download_id", 0L)
        if (previous > 0L) {
            val existing = try {
                manager.query(DownloadManager.Query().setFilterById(previous))?.use {
                    if (it.moveToFirst()) it.getInt(it.getColumnIndexOrThrow(DownloadManager.COLUMN_STATUS))
                    else null
                }
            } catch (_: Exception) { null }
            if (prefs.getInt("version", -1) == checked.versionCode &&
                prefs.getString("sha256", "") == checked.sha256 &&
                existing != null && existing != DownloadManager.STATUS_FAILED) {
                // Explicit download may override an automatic transfer
                // waiting for unmetered Wi-Fi; DownloadManager's network
                // policy cannot be edited after a request is enqueued.
                if (!allowMetered || (existing != DownloadManager.STATUS_PENDING &&
                        existing != DownloadManager.STATUS_PAUSED)) return previous
            }
            manager.remove(previous)
        }
        val directory = context.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS)
            ?: error("App download directory is unavailable")
        require(directory.isDirectory || directory.mkdirs())
        val destination = File(directory, "NodexLM-" + checked.versionCode + ".apk")
        if (destination.exists()) require(destination.delete()) { "Cannot clear stale APK" }
        val request = DownloadManager.Request(Uri.parse(checked.apkUrl))
            .setTitle("NodexLM " + checked.versionName)
            .setDescription("Official GitHub Release APK; signature and SHA-256 verified before install")
            .setMimeType("application/vnd.android.package-archive")
            .setDestinationUri(Uri.fromFile(destination))
            .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            .setAllowedOverMetered(allowMetered)
            .setAllowedOverRoaming(false)
        val id = manager.enqueue(request)
        prefs.edit().putLong("download_id", id)
            .putString("sha256", checked.sha256)
            .putInt("version", checked.versionCode)
            .putString("file_path", destination.absolutePath)
            .remove("prompted_id").remove("download_error").apply()
        return id
    }

    fun maybeAutomaticDownload(context: Context, release: UpdateRelease?) {
        if (release == null) return
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        if (!prefs.getBoolean("auto_download", false)) return
        enqueue(context, release, allowMetered = false)
    }
}
