// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.os.Build
import android.util.Log
import android.widget.Toast

/** Receives the explicit immutable-target callback from PackageInstaller.
 * User confirmation is still handled by Android's trusted system UI.
 */
class UpdateInstallReceiver : BroadcastReceiver() {
    companion object {
        const val ACTION = "dev.nodexlm.app.UPDATE_INSTALL_STATUS"
    }

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_MY_PACKAGE_REPLACED) {
            HostManagedUpdate.resume(context)
            return
        }
        if (intent.action != ACTION) return
        val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS,
            PackageInstaller.STATUS_FAILURE)
        if (status != PackageInstaller.STATUS_PENDING_USER_ACTION && status != PackageInstaller.STATUS_SUCCESS) {
            context.getSharedPreferences("nodexlm_host_update", Context.MODE_PRIVATE).edit().remove("submitted").remove("resume_role").apply()
        }
        when (status) {
            PackageInstaller.STATUS_PENDING_USER_ACTION -> {
                RuntimeLogs.append(context, RuntimeRole.UPDATES, RuntimeLevel.INFO, RuntimeEvent.UPDATE_INSTALL)
                @Suppress("DEPRECATION")
                val confirmation = if (Build.VERSION.SDK_INT >= 33) {
                    intent.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)
                } else {
                    intent.getParcelableExtra(Intent.EXTRA_INTENT) as? Intent
                }
                if (confirmation == null) {
                    Log.e("NodexLMUpdate", "Installer did not provide confirmation intent")
                    return
                }
                try {
                    confirmation.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                    HostManagedUpdate.notify(context, "Confirme a atualização assinada do NodexLM", confirmation)
                    context.startActivity(confirmation)
                } catch (e: Exception) {
                    Log.e("NodexLMUpdate", "Cannot start Android installer confirmation", e)
                }
            }
            PackageInstaller.STATUS_SUCCESS -> {
                RuntimeLogs.append(context, RuntimeRole.UPDATES, RuntimeLevel.INFO, RuntimeEvent.UPDATE_INSTALLED)
                context.getSharedPreferences("nodexlm_updates", Context.MODE_PRIVATE).edit()
                    .remove("download_id").remove("prompted_id").apply()
                BetaApkDownload.clearStaged(context)
            }
            PackageInstaller.STATUS_FAILURE_ABORTED -> {
                RuntimeLogs.append(context, RuntimeRole.UPDATES, RuntimeLevel.INFO, RuntimeEvent.UPDATE_INSTALL_FAILED)
                context.getSharedPreferences("nodexlm_updates", Context.MODE_PRIVATE).edit()
                    .remove("prompted_id").apply()
                Toast.makeText(context, context.getString(R.string.update_install_cancelled), Toast.LENGTH_SHORT).show()
            }
            PackageInstaller.STATUS_FAILURE_CONFLICT,
            PackageInstaller.STATUS_FAILURE_INCOMPATIBLE -> {
                RuntimeLogs.append(context, RuntimeRole.UPDATES, RuntimeLevel.WARN, RuntimeEvent.UPDATE_INSTALL_FAILED)
                val detail = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE)
                    ?: "Installer error $status"
                Log.e("NodexLMUpdate", "APK installation signature/version conflict: $detail")
                context.getSharedPreferences("nodexlm_updates", Context.MODE_PRIVATE).edit()
                    .remove("download_id").remove("prompted_id").apply()
                Toast.makeText(context, context.getString(R.string.update_migration_required), Toast.LENGTH_LONG).show()
            }
            else -> {
                RuntimeLogs.append(context, RuntimeRole.UPDATES, RuntimeLevel.WARN, RuntimeEvent.UPDATE_INSTALL_FAILED)
                val detail = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE)
                    ?: "Installer error $status"
                Log.e("NodexLMUpdate", "APK installation rejected: $detail")
                context.getSharedPreferences("nodexlm_updates", Context.MODE_PRIVATE).edit()
                    .remove("download_id").remove("prompted_id").apply()
                Toast.makeText(context, context.getString(R.string.update_not_installed, detail), Toast.LENGTH_LONG).show()
            }
        }
    }
}
