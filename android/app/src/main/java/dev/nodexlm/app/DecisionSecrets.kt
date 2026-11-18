// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.content.Context

/** Optional local-only runtime credential, independent of the Host node token. */
internal object DecisionSecrets {
    private const val PREFS = "nodexlm"
    private const val KEY = "decision_api_key_encrypted"

    fun save(context: Context, value: String) {
        val edit = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit()
        if (value.isBlank()) edit.remove(KEY)
        else edit.putString(KEY, NodeSecrets.encrypt(value))
        check(edit.commit()) { "Cannot save local decision credential" }
    }

    fun read(context: Context): String {
        val value = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .getString(KEY, null).orEmpty()
        return if (value.isEmpty()) "" else NodeSecrets.decrypt(value)
    }
}
