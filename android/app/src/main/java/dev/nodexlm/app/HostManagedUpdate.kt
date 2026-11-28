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
    fun tick(context: Context, directive: JSONObject, idle: Boolean, role: String = "rpc") {
        val commit = directive.optString("required_build_commit")
        if (!Regex("[0-9a-f]{40}").matches(commit) ||
            commit == BuildConfig.NODEX_BUILD_COMMIT) return
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        try {
            val state = BetaApkDownload.state(context)
            if (prefs.getString("target", "") != commit ||
                state.id <= 0L || state.status == DownloadManager.STATUS_FAILED ||
                BetaApkDownload.expectedSourceCommit(context) != commit) {
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
            val since = System.currentTimeMillis() - prefs.getLong("submit_attempt", 0L)
            if (since in 0..599_999L) return
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
                    prefs.edit().putLong("submitted", state.id)
                        .putLong("submit_attempt", System.currentTimeMillis())
                        .putString("resume_role", role).commit()
                    session.commit(callback.intentSender)
                }
            } catch (e: Exception) {
                prefs.edit().remove("submitted").remove("resume_role").commit()
                installer.abandonSession(sessionId)
                throw e
            }
        } catch (e: Exception) {
            Log.w("NodexLMHostUpdate", "Waiting for compatible official APK", e)
        }
    }

    // Invoked only after a package replacement matching the Host's directive.
    // Preserve the saved pairing and operator's mobile resource policy.
    fun resume(context: Context) {
        val update = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        val role = update.getString("resume_role", null) ?: return
        if (update.getString("target", "") != BuildConfig.NODEX_BUILD_COMMIT) return
        val prefs = context.getSharedPreferences("nodexlm", Context.MODE_PRIVATE)
        val endpoint = prefs.getString("coordinator", "").orEmpty()
        val id = prefs.getString("node_id", "").orEmpty()
        if (endpoint.isBlank() || id.isBlank() || runCatching { NodeSecrets.read(context) }.getOrDefault("").isBlank()) return
        val intent = if (role == "rpc") {
            Intent(context, RpcGuestService::class.java).setAction(RpcGuestService.START)
                .putExtra(RpcGuestService.EXTRA_ENDPOINT, endpoint)
                .putExtra(RpcGuestService.EXTRA_NODE, id)
                .putExtra(RpcGuestService.EXTRA_NAME, prefs.getString("name", Build.MODEL))
                .putExtra(RpcGuestService.EXTRA_MODE, prefs.getString("rpc_mode", "auto"))
                .putExtra(RpcGuestService.EXTRA_RUST_TRANSPORT, BuildConfig.RUST_RPC_ENABLED && prefs.getBoolean("rpc_rust_transport", false))
                .putExtra(RpcGuestService.EXTRA_EFFICIENT_CORES, prefs.getBoolean("rpc_efficient_cores", false))
                .putExtra(RpcGuestService.EXTRA_COMPRESSION, prefs.getBoolean("rpc_compression", false))
                .putExtra(RpcGuestService.EXTRA_CPU_AFFINITY, prefs.getBoolean("rpc_cpu_affinity", false))
                .putExtra(RpcGuestService.EXTRA_WEIGHT_CACHE, prefs.getBoolean("rpc_weight_cache", false))
        } else {
            Intent(context, ComputeService::class.java).setAction(ComputeService.START)
                .putExtra(ComputeService.EXTRA_LITERT, role == "litert" || role == "litert-gpu")
                .putExtra(ComputeService.EXTRA_LITERT_GPU, role == "litert-gpu")
        }
        try {
            context.startForegroundService(intent)
            update.edit().remove("resume_role").remove("submitted").commit()
        } catch (e: Exception) {
            Log.w("NodexLMHostUpdate", "Android deferred Guest restart", e)
            notify(context, "Atualização concluída. Abra o NodexLM para reconectar.")
        }
    }

    fun cancelResume(context: Context) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().remove("resume_role").apply()
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
