package dev.nodexlm.app

import android.app.DownloadManager
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.content.pm.PackageManager
import android.os.Build
import android.util.Log
import org.json.JSONObject

/** Only authenticated Host directives reach this method. The Host cannot
 * supply URLs, APK files or commands. The official permanent-signed feed,
 * exact source identity and monotonically increasing version are mandatory. */
internal object HostManagedUpdate {
    private const val PREFS = "nodexlm_host_update"
    private const val CHANNEL = "nodexlm_host_update"
    private const val NOTIFICATION = 6018

    @Synchronized
    fun tick(context: Context, directive: JSONObject, idle: Boolean) {
        val commit = directive.optString("required_build_commit")
        if (!Regex("[0-9a-f]{40}").matches(commit) ||
            commit == BuildConfig.NODEX_BUILD_COMMIT) return
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        try {
            val state = BetaApkDownload.state(context)
            if (prefs.getString("target", "") != commit ||
                state.id <= 0L || state.status == DownloadManager.STATUS_FAILED) {
                val now = System.currentTimeMillis()
                if (prefs.getString("target", "") == commit &&
                    now - prefs.getLong("attempt", 0L) in 0..59_999L) return
                prefs.edit().putString("target", commit).putLong("attempt", now).remove("submitted").commit()
                val artifact = BetaApkDownload.fetchArtifact()
                require(artifact.sourceCommit == commit) { "Matching signed Android build is not published yet" }
                require(artifact.versionCode > UpdateFeed.installedVersionCode(context)) {
                    "Host requests an older or same-version APK; update the Host"
                }
                BetaApkDownload.enqueue(context, artifact)
                return
            }
            if (!idle || state.status != DownloadManager.STATUS_SUCCESSFUL) return
            if (!BetaApkDownload.verify(context)) return
            val checked = BetaApkDownload.state(context)
            val apk = BetaApkDownload.stagedFile(context) ?: return
            if (!checked.verified || !checked.installable) return
            // The source identity is embedded in the signed APK itself, not
            // merely asserted by the mutable release metadata.
            @Suppress("DEPRECATION")
            val info = context.packageManager.getPackageArchiveInfo(apk.absolutePath, PackageManager.GET_META_DATA)
                ?: error("Missing APK manifest")
            require(info.applicationInfo?.metaData?.getString("dev.nodexlm.BUILD_COMMIT") == commit) {
                "Signed APK source commit does not match Host"
            }
            if (!context.packageManager.canRequestPackageInstalls()) {
                notify(context, "Autorize a instalação de atualizações do NodexLM")
                return
            }
            if (prefs.getLong("submitted", 0L) == state.id) return
            val installer = context.packageManager.packageInstaller
            val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL)
            params.setAppPackageName(context.packageName)
            if (Build.VERSION.SDK_INT >= 31) {
                params.setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED)
            }
            val sessionId = installer.createSession(params)
            try {
                installer.openSession(sessionId).use { session ->
                    session.openWrite("base.apk", 0, apk.length()).use { output ->
                        apk.inputStream().use { it.copyTo(output, 64 * 1024) }
                        session.fsync(output)
                    }
                    val flags = PendingIntent.FLAG_UPDATE_CURRENT or
                        (if (Build.VERSION.SDK_INT >= 31) PendingIntent.FLAG_MUTABLE else 0)
                    val callback = PendingIntent.getBroadcast(context, sessionId,
                        Intent(context, UpdateInstallReceiver::class.java).setAction(UpdateInstallReceiver.ACTION), flags)
                    prefs.edit().putLong("submitted", state.id).commit()
                    session.commit(callback.intentSender)
                }
            } catch (e: Exception) {
                prefs.edit().remove("submitted").commit()
                installer.abandonSession(sessionId)
                throw e
            }
        } catch (e: Exception) {
            Log.w("NodexLMHostUpdate", "Waiting for compatible official APK", e)
        }
    }

    fun notify(context: Context, message: String, action: Intent = Intent(context, MainActivity::class.java)) {
        val manager = context.getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel(CHANNEL, "NodexLM updates", NotificationManager.IMPORTANCE_HIGH))
        val pending = PendingIntent.getActivity(context, NOTIFICATION, action,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        manager.notify(NOTIFICATION, Notification.Builder(context, CHANNEL)
            .setSmallIcon(android.R.drawable.stat_sys_download_done)
            .setContentTitle("NodexLM · atualização do Host")
            .setContentText(message).setContentIntent(pending).setAutoCancel(true).build())
    }
}
