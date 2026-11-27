// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.app.DownloadManager
import android.content.Context
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Environment
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest
import java.util.Locale

/**
 * Opt-in testing artifact from the repository's permanently signed public prerelease.
 * Never mixes feeds with the stable updater, and enforces SHA-256, versionCode,
 * and permanent certificate checks before invoking Android's PackageInstaller.
 */
internal object BetaApkDownload {
    private const val PREFS = "nodexlm_beta_apk"
    private const val API = "https://api.github.com/repos/rfdetoni/nodexlm/releases/tags/android-debug"
    private const val MAX_METADATA = 256 * 1024
    private const val MAX_BUILD_INFO = 4096

    internal data class State(
        val id: Long,
        val status: Int,
        val received: Long,
        val total: Long,
        val verified: Boolean,
        val error: String?,
        val installable: Boolean = false,
        val migrationRequired: Boolean = false,
        val versionCode: Long = 0L,
        val versionName: String? = null
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
            var apkArtifact: BetaArtifact? = null
            var hasBuildInfo = false
            for (i in 0 until assets.length()) {
                val item = assets.optJSONObject(i) ?: continue
                when (item.optString("name")) {
                    BetaArtifactPolicy.FILE -> {
                        apkArtifact = BetaArtifactPolicy.validate(
                            release.optString("tag_name"),
                            release.optBoolean("prerelease"),
                            release.optBoolean("draft"),
                            item.optString("name"),
                            item.optString("browser_download_url"),
                            item.optString("digest"),
                            item.optLong("size")
                        )
                    }
                    BetaArtifactPolicy.BUILD_INFO_FILE -> {
                        if (item.optString("browser_download_url") == BetaArtifactPolicy.BUILD_INFO_URL) {
                            hasBuildInfo = true
                        }
                    }
                }
            }
            val base = apkArtifact ?: error("The public prerelease has no Android beta APK")
            if (!hasBuildInfo) return base
            val info = fetchBuildInfo()
            if (info.certificateSha256 != null) {
                require(info.certificateSha256 == BuildConfig.PERMANENT_SIGNING_CERT_SHA256) {
                    "Beta APK certificate metadata does not match pinned permanent certificate"
                }
            }
            return base.copy(versionCode = info.versionCode, versionName = info.versionName, sourceCommit = info.sourceCommit)
        } finally {
            connection.disconnect()
        }
    }

    private fun fetchBuildInfo(): BetaBuildInfo {
        val connection = URL(BetaArtifactPolicy.BUILD_INFO_URL).openConnection() as HttpURLConnection
        connection.connectTimeout = 10_000
        connection.readTimeout = 15_000
        connection.setRequestProperty("User-Agent", "NodexLM-Android-Beta")
        try {
            check(connection.responseCode == HttpURLConnection.HTTP_OK) {
                "GitHub BUILD_INFO HTTP ${connection.responseCode}"
            }
            val output = ByteArrayOutputStream()
            connection.inputStream.use { stream ->
                val buffer = ByteArray(1024)
                while (true) {
                    val n = stream.read(buffer)
                    if (n < 0) break
                    require(output.size() + n <= MAX_BUILD_INFO) { "BUILD_INFO.txt too large" }
                    output.write(buffer, 0, n)
                }
            }
            return BetaArtifactPolicy.parseBuildInfo(output.toString(Charsets.UTF_8.name()))
        } finally {
            connection.disconnect()
        }
    }

    @Synchronized
    fun enqueue(context: Context, artifact: BetaArtifact): Long {
        val valid = BetaArtifactPolicy.validate(
            BetaArtifactPolicy.TAG, true, false, BetaArtifactPolicy.FILE,
            artifact.url, "sha256:" + artifact.sha256, artifact.sizeBytes
        )
        val manager = context.getSystemService(DownloadManager::class.java)
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val previous = prefs.getLong("id", 0L)
        if (previous > 0L) manager.remove(previous)
        clearStagedFile(context, prefs.getString("staged_path", null))
        prefs.edit().clear().commit()

        // Public Downloads, not the app-private model directory: the user can
        // find and manage the APK during one-time migration from legacy debug builds.
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
            .putLong("expected_version_code", artifact.versionCode)
            .putString("version_name", artifact.versionName)
            .putBoolean("verified", false)
            .putBoolean("installable", false)
            .putBoolean("migration_required", false)
            .remove("error")
            .remove("staged_path")
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
        val verified = status == DownloadManager.STATUS_SUCCESSFUL && prefs.getBoolean("verified", false)
        val stagedPath = prefs.getString("staged_path", null)
        val installable = verified && prefs.getBoolean("installable", false) &&
            !stagedPath.isNullOrBlank() && File(stagedPath).isFile
        return State(
            id = id,
            status = status,
            received = received,
            total = total,
            verified = verified,
            error = prefs.getString("error", null),
            installable = installable,
            migrationRequired = verified && prefs.getBoolean("migration_required", false),
            versionCode = prefs.getLong("version_code", prefs.getLong("expected_version_code", 0L)),
            versionName = prefs.getString("version_name", null)
        )
    }

    fun expectedSha256(context: Context): String? =
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString("sha256", null)

    fun stagedFile(context: Context): File? {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val raw = prefs.getString("staged_path", null) ?: return null
        val file = File(raw)
        if (!file.isFile) return null
        val cacheCanonical = context.cacheDir.canonicalPath + File.separator
        if (!file.canonicalPath.startsWith(cacheCanonical)) return null
        return file
    }

    fun clearStaged(context: Context) {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        clearStagedFile(context, prefs.getString("staged_path", null))
        prefs.edit().putBoolean("installable", false).remove("staged_path").apply()
    }

    private fun clearStagedFile(context: Context, rawPath: String?) {
        if (rawPath.isNullOrBlank()) return
        try {
            val file = File(rawPath)
            val cacheCanonical = context.cacheDir.canonicalPath + File.separator
            if (file.canonicalPath.startsWith(cacheCanonical) && file.isFile) {
                file.delete()
            }
        } catch (_: Exception) {}
    }

    /**
     * Called on a worker thread only. Copies DownloadManager bytes into private cache
     * while hashing, then validates package name, versionCode and permanent signing certificate.
     */
    fun verify(context: Context): Boolean {
        val manager = context.getSystemService(DownloadManager::class.java)
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val id = prefs.getLong("id", 0L)
        if (id <= 0 || state(context).status != DownloadManager.STATUS_SUCCESSFUL) return false
        if (prefs.getBoolean("verified", false)) {
            if (!prefs.getBoolean("installable", false) || stagedFile(context) != null) {
                return true
            }
        }
        val staged = File(context.cacheDir, "nodexlm-beta-verified-$id.apk")
        var keepStaged = false
        return try {
            if (staged.exists()) require(staged.delete()) { "Cannot clear stale beta APK" }
            val digest = MessageDigest.getInstance("SHA-256")
            var count = 0L
            val uri = manager.getUriForDownloadedFile(id) ?: error("APK download URI missing")
            context.contentResolver.openInputStream(uri)?.buffered()?.use { input ->
                staged.outputStream().buffered().use { output ->
                    val buffer = ByteArray(64 * 1024)
                    while (true) {
                        val n = input.read(buffer)
                        if (n < 0) break
                        digest.update(buffer, 0, n)
                        output.write(buffer, 0, n)
                        count += n
                    }
                }
            } ?: error("Cannot read the downloaded APK")
            val actual = digest.digest().joinToString("") { "%02x".format(it.toInt() and 255) }
            require(count == prefs.getLong("expected_bytes", -1L) &&
                actual == prefs.getString("sha256", null)) {
                "APK beta failed SHA-256/size verification"
            }

            @Suppress("DEPRECATION")
            val candidate = context.packageManager.getPackageArchiveInfo(
                staged.absolutePath, PackageManager.GET_SIGNING_CERTIFICATES)
                ?: error(context.getString(R.string.apk_metadata_invalid))
            require(candidate.packageName == context.packageName) {
                context.getString(R.string.update_wrong_package)
            }
            @Suppress("DEPRECATION")
            val installed = context.packageManager.getPackageInfo(
                context.packageName, PackageManager.GET_SIGNING_CERTIFICATES)
            val expectedVersion = prefs.getLong("expected_version_code", 0L).takeIf { it > 0L }
            val versionCmp = ApkVerificationPolicy.compareVersion(
                installed.longVersionCode, candidate.longVersionCode, expectedVersion)
            if (versionCmp == VersionComparison.EXPECTED_MISMATCH) {
                error(context.getString(R.string.apk_version_invalid))
            }

            val installedCerts = installed.signingInfo?.apkContentsSigners
                ?.map { ApkVerificationPolicy.sha256Hex(it.toByteArray()) } ?: emptyList()
            val candidateCerts = candidate.signingInfo?.apkContentsSigners
                ?.map { ApkVerificationPolicy.sha256Hex(it.toByteArray()) } ?: emptyList()
            val sigCompat = ApkVerificationPolicy.verifySignatures(
                installedCerts, candidateCerts, BuildConfig.PERMANENT_SIGNING_CERT_SHA256)
            if (sigCompat == SignatureCompatibility.INCOMPATIBLE_UNTRUSTED) {
                error(context.getString(R.string.update_signature_untrusted))
            }
            if (prefs.getLong("id", 0L) != id) return false

            val canInstall = sigCompat == SignatureCompatibility.COMPATIBLE &&
                versionCmp == VersionComparison.NEWER
            val migration = sigCompat == SignatureCompatibility.MIGRATION_REQUIRED_FROM_LEGACY_KEY
            val statusMessage = when {
                migration -> null
                versionCmp == VersionComparison.CURRENT -> context.getString(R.string.updates_beta_already_current)
                versionCmp == VersionComparison.DOWNGRADE -> context.getString(R.string.update_not_newer)
                else -> null
            }
            keepStaged = canInstall
            prefs.edit()
                .putBoolean("verified", true)
                .putBoolean("installable", canInstall)
                .putBoolean("migration_required", migration)
                .putLong("version_code", candidate.longVersionCode)
                .putString("version_name", candidate.versionName?.lowercase(Locale.ROOT)?.let { candidate.versionName })
                .apply {
                    if (canInstall) putString("staged_path", staged.absolutePath) else remove("staged_path")
                    if (statusMessage != null) putString("error", statusMessage) else remove("error")
                }
                .apply()
            true
        } catch (e: Exception) {
            if (prefs.getLong("id", 0L) == id) {
                manager.remove(id)
                prefs.edit().putLong("id", 0L).putBoolean("verified", false)
                    .putBoolean("installable", false).putBoolean("migration_required", false)
                    .remove("staged_path")
                    .putString("error", (e.message ?: "APK verification failed").take(180)).apply()
            }
            false
        } finally {
            if (!keepStaged && staged.exists()) staged.delete()
        }
    }
}
