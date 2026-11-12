// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.app.Activity
import android.app.AlertDialog
import android.app.DownloadManager
import android.app.PendingIntent
import android.content.pm.PackageInstaller
import android.content.pm.PackageManager
import android.os.Build
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.Uri
import android.os.Environment
import android.provider.Settings
import android.util.Log
import android.widget.Toast
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.concurrent.thread

/** Sideload updates exclusively from the official signed GitHub Release APK.
 * Downloads are checked against the published SHA-256 and Android's installer
 * validates package signatures. Unprivileged apps cannot silently install APKs.
 */
class UpdateManager(private val activity: Activity) {
    private val prefs = activity.getSharedPreferences("nodexlm_updates", Context.MODE_PRIVATE)
    private val downloads = activity.getSystemService(DownloadManager::class.java)
    private val verifying = AtomicBoolean(false)
    private val checking = AtomicBoolean(false)
    private var receiverRegistered = false
    private val receiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            if (intent?.action != DownloadManager.ACTION_DOWNLOAD_COMPLETE) return
            if (intent.getLongExtra(DownloadManager.EXTRA_DOWNLOAD_ID, -1L) == prefs.getLong("download_id", -2L)) {
                val status = status().downloadStatus
                if (status == DownloadManager.STATUS_SUCCESSFUL || status == DownloadManager.STATUS_FAILED) {
                    RuntimeLogs.append(activity, RuntimeRole.UPDATES,
                        if (status == DownloadManager.STATUS_SUCCESSFUL) RuntimeLevel.INFO else RuntimeLevel.WARN,
                        if (status == DownloadManager.STATUS_SUCCESSFUL) RuntimeEvent.UPDATE_DOWNLOADED
                        else RuntimeEvent.UPDATE_DOWNLOAD_FAILED)
                }
                notifyChanged()
            }
        }
    }

    var onStateChanged: (() -> Unit)? = null

    private fun notifyChanged() {
        activity.runOnUiThread {
            if (!activity.isFinishing && !activity.isDestroyed) onStateChanged?.invoke()
        }
    }

    fun start() {
        if (!receiverRegistered) {
            val filter = IntentFilter(DownloadManager.ACTION_DOWNLOAD_COMPLETE)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                // Only installed DownloadManager broadcasts are expected. Never
                // trust the broadcast alone: verify stored ID, status, SHA
                // and the installed app's signing certificate separately.
                activity.registerReceiver(receiver, filter, Context.RECEIVER_EXPORTED)
            } else {
                @Suppress("DEPRECATION")
                activity.registerReceiver(receiver, filter)
            }
            receiverRegistered = true
        }
        // The UI checks while opened, not on a headless RPC loop.
        val now = System.currentTimeMillis()
        val last = prefs.getLong("last_attempt", 0L)
        if (last <= 0L || now < last || now - last >= 24L * 60 * 60 * 1000) {
            check(manual = false)
        }
    }

    fun stop() {
        if (receiverRegistered) {
            activity.unregisterReceiver(receiver)
            receiverRegistered = false
        }
        onStateChanged = null
    }

    fun onResume() { notifyChanged() }

    fun check(manual: Boolean) {
        if (!checking.compareAndSet(false, true)) return
        RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.INFO, RuntimeEvent.UPDATE_CHECK)
        notifyChanged()
        thread(name = "nodexlm-update-check") {
            try {
                val available = UpdateFeed.check(activity)
                when (prefs.getString("feed_status", "")) {
                    "available" -> {
                        RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.INFO, RuntimeEvent.UPDATE_AVAILABLE)
                        if (manual) toast(activity.getString(R.string.update_found, available?.versionName ?: "?"))
                    }
                    "unpublished" -> {
                        RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.INFO, RuntimeEvent.UPDATE_UNPUBLISHED)
                        if (manual) toast(activity.getString(R.string.update_unpublished))
                    }
                    else -> {
                        RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.INFO, RuntimeEvent.UPDATE_CURRENT)
                        if (manual) toast(activity.getString(R.string.update_current))
                    }
                }
                // The opt-in mode downloads on unmetered connections, but
                // NEVER invokes the package installer without a user click.
                if (available != null) {
                    try {
                        val currentId = prefs.getLong("download_id", 0L)
                        UpdateFeed.maybeAutomaticDownload(activity, available)
                        if (prefs.getLong("download_id", 0L) != currentId) {
                            RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.INFO, RuntimeEvent.UPDATE_DOWNLOAD)
                        }
                    } catch (e: Exception) {
                        RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.WARN, RuntimeEvent.UPDATE_DOWNLOAD_FAILED)
                        Log.w("NodexLMUpdate", "Automatic update download unavailable", e)
                    }
                }
            } catch (e: Exception) {
                RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.WARN, RuntimeEvent.UPDATE_CHECK_FAILED)
                Log.w("NodexLMUpdate", "Cannot check GitHub Releases", e)
                if (manual) toast(activity.getString(R.string.update_check_failed, e.message))
            } finally {
                checking.set(false)
                notifyChanged()
            }
        }
    }

    fun downloadAvailable() {
        thread(name = "nodexlm-update-download") {
            try {
                val available = UpdateFeed.available(activity)
                    ?: error(activity.getString(R.string.update_not_available))
                val old = prefs.getLong("download_id", 0L)
                val id = UpdateFeed.enqueue(activity, available, allowMetered = true)
                if (id != old) RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.INFO, RuntimeEvent.UPDATE_DOWNLOAD)
                toast(activity.getString(R.string.update_started, available.versionName))
            } catch (e: Exception) {
                RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.WARN, RuntimeEvent.UPDATE_DOWNLOAD_FAILED)
                toast(activity.getString(R.string.update_download_failed))
                Log.w("NodexLMUpdate", "Cannot queue verified update download", e)
            } finally { notifyChanged() }
        }
    }

    fun installDownloaded() { tryInstallPending() }

    data class Status(
        val feed: String,
        val currentVersion: String,
        val availableVersion: String?,
        val lastSuccess: Long,
        val checking: Boolean,
        val verifying: Boolean,
        val error: String?,
        val downloadStatus: Int,
        val downloaded: Long,
        val total: Long,
        val installRequested: Boolean
    )

    fun status(): Status {
        val id = prefs.getLong("download_id", 0L)
        var status = -1
        var downloaded = 0L
        var total = 0L
        if (id > 0) try {
            downloads.query(DownloadManager.Query().setFilterById(id))?.use { cursor ->
                if (cursor.moveToFirst()) {
                    status = cursor.getInt(cursor.getColumnIndexOrThrow(DownloadManager.COLUMN_STATUS))
                    downloaded = cursor.getLong(cursor.getColumnIndexOrThrow(DownloadManager.COLUMN_BYTES_DOWNLOADED_SO_FAR)).coerceAtLeast(0)
                    total = cursor.getLong(cursor.getColumnIndexOrThrow(DownloadManager.COLUMN_TOTAL_SIZE_BYTES)).coerceAtLeast(0)
                }
            }
        } catch (e: Exception) { Log.w("NodexLMUpdate", "Cannot query DownloadManager", e) }
        val current = activity.packageManager.getPackageInfo(activity.packageName, 0).versionName ?: "?"
        return Status(
            prefs.getString("feed_status", "") ?: "",
            current,
            prefs.getString("available_name", null),
            prefs.getLong("last_success", 0L),
            checking.get(),
            verifying.get(),
            prefs.getString("check_error", null),
            status, downloaded, total,
            id > 0 && id == prefs.getLong("prompted_id", -1L)
        )
    }

    private fun tryInstallPending() {
        val id = prefs.getLong("download_id", 0L)
        if (id <= 0 || prefs.getLong("prompted_id", -1L) == id ||
            !verifying.compareAndSet(false, true)) return
        RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.INFO, RuntimeEvent.UPDATE_VERIFY)
        notifyChanged()
        thread(name = "nodexlm-update-verify") {
            var staged: File? = null
            try {
                val cursor = downloads.query(DownloadManager.Query().setFilterById(id)) ?: return@thread
                val status = cursor.use {
                    if (!it.moveToFirst()) return@thread
                    it.getInt(it.getColumnIndexOrThrow(DownloadManager.COLUMN_STATUS))
                }
                if (status == DownloadManager.STATUS_FAILED) {
                    prefs.edit().remove("download_id").apply()
                    RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.WARN, RuntimeEvent.UPDATE_DOWNLOAD_FAILED)
                    toast(activity.getString(R.string.update_download_failed))
                    return@thread
                }
                if (status != DownloadManager.STATUS_SUCCESSFUL) return@thread

                val original = File(prefs.getString("file_path", "") ?: "")
                val downloadsDir = activity.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS)
                    ?: error("Download directory unavailable")
                require(original.isFile && original.length() > 0L &&
                    original.canonicalPath.startsWith(downloadsDir.canonicalPath + File.separator)) {
                    "Update file outside the private download directory"
                }
                // Verify exactly the bytes that will be installed. Never use
                // DownloadManager's external URI after hash verification.
                val verified = File(activity.cacheDir, "nodexlm-verified-$id.apk")
                if (verified.exists()) require(verified.delete())
                staged = verified
                val digest = MessageDigest.getInstance("SHA-256")
                original.inputStream().buffered().use { input ->
                    verified.outputStream().buffered().use { output ->
                        val buffer = ByteArray(64 * 1024)
                        while (true) {
                            val n = input.read(buffer)
                            if (n < 0) break
                            digest.update(buffer, 0, n)
                            output.write(buffer, 0, n)
                        }
                    }
                }
                val hash = digest.digest().joinToString("") { "%02x".format(it) }
                require(hash == prefs.getString("sha256", "")) {
                    activity.getString(R.string.update_sha_failed)
                }
                verifyAPKSigner(verified, prefs.getInt("version", -1))
                RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.INFO, RuntimeEvent.UPDATE_VERIFIED)

                // The callback (or its install worker) owns the staging
                // file now; the verifier must not delete it before it runs.
                staged = null
                activity.runOnUiThread {
                    if (!activity.packageManager.canRequestPackageInstalls()) {
                        verified.delete()
                        AlertDialog.Builder(activity)
                            .setTitle(activity.getString(R.string.ui_a2334f70f759))
                            .setMessage(activity.getString(R.string.ui_83c6621ca708))
                            .setPositiveButton(activity.getString(R.string.ui_4dc25299c9e2)) { _, _ ->
                                activity.startActivity(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                                    Uri.parse("package:${activity.packageName}")))
                            }
                            .setNegativeButton(activity.getString(R.string.ui_b8db71c3cce7), null)
                            .show()
                    } else {
                        thread(name = "nodexlm-update-install") {
                            try {
                                installVerified(verified, id)
                                prefs.edit().putLong("prompted_id", id).apply()
                                RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.INFO, RuntimeEvent.UPDATE_INSTALL)
                            } catch (e: Exception) {
                                RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.ERROR, RuntimeEvent.UPDATE_INSTALL_FAILED)
                                Log.e("NodexLMUpdate", "PackageInstaller session failed", e)
                                toast(activity.getString(R.string.update_install_failed,e.message))
                                notifyChanged()
                            } finally {
                                verified.delete()
                            }
                        }
                    }
                }
            } catch (e: Exception) {
                RuntimeLogs.append(activity, RuntimeRole.UPDATES, RuntimeLevel.ERROR, RuntimeEvent.UPDATE_REJECTED)
                Log.e("NodexLMUpdate", "Update verification failed", e)
                prefs.edit().remove("download_id").apply()
                toast(activity.getString(R.string.update_rejected,e.message))
            } finally {
                // When confirmation is not granted, onResume re-verifies the
                // download into a fresh private staging copy.
                if (staged != null) staged?.delete()
                verifying.set(false)
                notifyChanged()
            }
        }
    }

    private fun verifyAPKSigner(apk: File, version: Int) {
        @Suppress("DEPRECATION")
        val candidate = activity.packageManager.getPackageArchiveInfo(
            apk.absolutePath, PackageManager.GET_SIGNING_CERTIFICATES)
            ?: error(activity.getString(R.string.apk_metadata_invalid))
        require(candidate.packageName == activity.packageName) { "APK de outro aplicativo" }
        require(candidate.longVersionCode == version.toLong()) { activity.getString(R.string.apk_version_invalid) }
        @Suppress("DEPRECATION")
        val installed = activity.packageManager.getPackageInfo(
            activity.packageName, PackageManager.GET_SIGNING_CERTIFICATES)
        require(candidate.longVersionCode > installed.longVersionCode) { "APK is not newer than installed version" }
        val currentSigners = installed.signingInfo?.apkContentsSigners
            ?: error("Current APK signature unavailable")
        val nextSigners = candidate.signingInfo?.apkContentsSigners
            ?: error("Update APK signature unavailable")
        require(currentSigners.isNotEmpty() && currentSigners.size == nextSigners.size &&
            currentSigners.indices.all { i ->
                currentSigners.any { old ->
                    MessageDigest.isEqual(old.toByteArray(), nextSigners[i].toByteArray())
                }
            }) { "Update APK signing certificate does not match installed application" }
    }

    private fun installVerified(apk: File, id: Long) {
        val installer = activity.packageManager.packageInstaller
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL)
        params.setAppPackageName(activity.packageName)
        val sessionId = installer.createSession(params)
        try {
            installer.openSession(sessionId).use { session ->
                session.openWrite("base.apk", 0, apk.length()).use { output ->
                    apk.inputStream().use { input -> input.copyTo(output, 64 * 1024) }
                    session.fsync(output)
                }
                val flags = PendingIntent.FLAG_UPDATE_CURRENT or
                    (if (Build.VERSION.SDK_INT >= 31) PendingIntent.FLAG_MUTABLE else 0)
                val callback = PendingIntent.getBroadcast(activity, sessionId,
                    Intent(activity, UpdateInstallReceiver::class.java)
                        .setAction(UpdateInstallReceiver.ACTION), flags)
                session.commit(callback.intentSender)
            }
        } catch (e: Exception) {
            installer.abandonSession(sessionId)
            throw e
        }
    }

    private fun toast(message: String) {
        activity.runOnUiThread { Toast.makeText(activity, message, Toast.LENGTH_LONG).show() }
    }
}
