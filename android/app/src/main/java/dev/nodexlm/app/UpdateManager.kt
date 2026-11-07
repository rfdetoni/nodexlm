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
                tryInstallPending()
            }
        }
    }

    fun start() {
        if (!receiverRegistered) {
            val filter = IntentFilter(DownloadManager.ACTION_DOWNLOAD_COMPLETE)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                // DownloadManager broadcasts originate from a separate system app UID.
                // ACTION_DOWNLOAD_COMPLETE is not a protected system broadcast on
                // Android 14+, so an explicit receiver flag is mandatory.
                // Never trust its payload without checking the stored download ID
                // and re-querying DownloadManager for completion and SHA-256.
                activity.registerReceiver(receiver, filter, Context.RECEIVER_EXPORTED)
            } else {
                @Suppress("DEPRECATION")
                activity.registerReceiver(receiver, filter)
            }
            receiverRegistered = true
        }
        // Check at most once a day when the application is opened; never keep
        // polling in the foreground compute service or while the UI is closed.
        val now = System.currentTimeMillis()
        if (now - prefs.getLong("last_check", 0L) > 24L * 60 * 60 * 1000) check(manual = false)
    }

    fun stop() {
        if (receiverRegistered) {
            activity.unregisterReceiver(receiver)
            receiverRegistered = false
        }
    }

    fun onResume() = tryInstallPending()

    fun check(manual: Boolean) {
        if (!checking.compareAndSet(false, true)) return
        if (!manual) prefs.edit().putLong("last_check", System.currentTimeMillis()).apply()
        thread(name = "nodexlm-update-check") {
            try {
                val endpoint = "https://github.com/rfdetoni/nodexlm/releases/latest/download/update.json"
                val conn = URL(endpoint).openConnection() as HttpURLConnection
                conn.connectTimeout = 10_000
                conn.readTimeout = 15_000
                conn.setRequestProperty("Accept", "application/json")
                val metadata = try {
                    if (conn.responseCode != 200) throw IllegalStateException("releases HTTP ${conn.responseCode}")
                    val data = ByteArray(8193)
                    var read = 0
                    conn.inputStream.use { stream ->
                        while (read < data.size) {
                            val n = stream.read(data, read, data.size - read)
                            if (n < 0) break
                            read += n
                        }
                    }
                    require(read <= 8192) { "Update manifest too large" }
                    JSONObject(String(data, 0, read, Charsets.UTF_8))
                } finally { conn.disconnect() }
                val version = metadata.getInt("versionCode")
                val name = metadata.getString("versionName")
                val checksum = metadata.getString("sha256").lowercase()
                val url = metadata.getString("apkUrl")
                require(version > 0 && name.length in 1..48 && name.all { it.isLetterOrDigit() || it in ".-_+" })
                require(checksum.matches(Regex("[0-9a-f]{64}")))
                require(url.startsWith("https://github.com/rfdetoni/nodexlm/releases/download/"))
                // Preserve compatibility with early arm64-only releases while moving
                // to one APK for both ARMv7 and ARM64 Android 10+ devices.
                require(url.endsWith("/NodexLM-android-universal.apk") ||
                    url.endsWith("/NodexLM-android-arm64.apk"))
                val installed = activity.packageManager.getPackageInfo(activity.packageName, 0).longVersionCode
                prefs.edit().putLong("last_check", System.currentTimeMillis()).apply()
                if (version <= installed) {
                    if (manual) toast("NodexLM já está atualizado")
                    return@thread
                }
                val old = prefs.getLong("download_id", 0)
                if (prefs.getInt("version", -1) == version && old > 0) {
                    if (manual) toast("Atualização $name já está em download")
                    tryInstallPending()
                    return@thread
                }
                val destination = File(activity.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS), "NodexLM-$version.apk")
                if (destination.exists()) destination.delete()
                val request = DownloadManager.Request(Uri.parse(url))
                    .setTitle("NodexLM $name")
                    .setDescription("Atualização oficial verificada por SHA-256")
                    .setMimeType("application/vnd.android.package-archive")
                    .setDestinationUri(Uri.fromFile(destination))
                    .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
                    .setAllowedOverMetered(manual) // automatic download waits for unmetered Wi-Fi
                    .setAllowedOverRoaming(false)
                val id = downloads.enqueue(request)
                prefs.edit().putLong("download_id", id).putString("sha256", checksum)
                    .putInt("version", version).putString("file_path", destination.absolutePath)
                    .remove("prompted_id").apply()
                if (manual) toast("Baixando NodexLM $name")
            } catch (e: Exception) {
                Log.w("NodexLMUpdate", "Cannot check GitHub Releases", e)
                if (manual) toast("Não foi possível verificar atualizações: ${e.message}")
            } finally { checking.set(false) }
        }
    }

    private fun tryInstallPending() {
        val id = prefs.getLong("download_id", 0L)
        if (id <= 0 || prefs.getLong("prompted_id", -1L) == id ||
            !verifying.compareAndSet(false, true)) return
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
                    toast("Falha ao baixar a atualização")
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
                    "O SHA-256 do APK não confere"
                }
                verifyAPKSigner(verified, prefs.getInt("version", -1))

                // The callback (or its install worker) owns the staging
                // file now; the verifier must not delete it before it runs.
                staged = null
                activity.runOnUiThread {
                    if (!activity.packageManager.canRequestPackageInstalls()) {
                        verified.delete()
                        AlertDialog.Builder(activity)
                            .setTitle("Permitir atualização do NodexLM")
                            .setMessage("O Android exige autorização para instalar uma atualização assinada.")
                            .setPositiveButton("Abrir configurações") { _, _ ->
                                activity.startActivity(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
                                    Uri.parse("package:${activity.packageName}")))
                            }
                            .setNegativeButton("Depois", null)
                            .show()
                    } else {
                        thread(name = "nodexlm-update-install") {
                            try {
                                installVerified(verified, id)
                                prefs.edit().putLong("prompted_id", id).apply()
                            } catch (e: Exception) {
                                Log.e("NodexLMUpdate", "PackageInstaller session failed", e)
                                toast("Não foi possível instalar a atualização: ${e.message}")
                            } finally {
                                verified.delete()
                            }
                        }
                    }
                }
            } catch (e: Exception) {
                Log.e("NodexLMUpdate", "Update verification failed", e)
                prefs.edit().remove("download_id").apply()
                toast("Atualização rejeitada: ${e.message}")
            } finally {
                // When confirmation is not granted, onResume re-verifies the
                // download into a fresh private staging copy.
                if (staged != null) staged?.delete()
                verifying.set(false)
            }
        }
    }

    private fun verifyAPKSigner(apk: File, version: Int) {
        @Suppress("DEPRECATION")
        val candidate = activity.packageManager.getPackageArchiveInfo(
            apk.absolutePath, PackageManager.GET_SIGNING_CERTIFICATES)
            ?: error("Metadados inválidos do APK")
        require(candidate.packageName == activity.packageName) { "APK de outro aplicativo" }
        require(candidate.longVersionCode == version.toLong()) { "Versão do APK inesperada" }
        @Suppress("DEPRECATION")
        val installed = activity.packageManager.getPackageInfo(
            activity.packageName, PackageManager.GET_SIGNING_CERTIFICATES)
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
