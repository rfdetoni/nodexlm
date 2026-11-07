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
        if (intent.action != ACTION) return
        val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS,
            PackageInstaller.STATUS_FAILURE)
        when (status) {
            PackageInstaller.STATUS_PENDING_USER_ACTION -> {
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
                    context.startActivity(confirmation)
                } catch (e: Exception) {
                    Log.e("NodexLMUpdate", "Cannot start Android installer confirmation", e)
                }
            }
            PackageInstaller.STATUS_SUCCESS -> {
                context.getSharedPreferences("nodexlm_updates", Context.MODE_PRIVATE).edit()
                    .remove("download_id").remove("prompted_id").apply()
            }
            else -> {
                val detail = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE)
                    ?: "Installer error $status"
                Log.e("NodexLMUpdate", "APK installation rejected: $detail")
                context.getSharedPreferences("nodexlm_updates", Context.MODE_PRIVATE).edit()
                    .remove("download_id").remove("prompted_id").apply()
                Toast.makeText(context, context.getString(R.string.update_not_installed,detail), Toast.LENGTH_LONG).show()
            }
        }
    }
}
