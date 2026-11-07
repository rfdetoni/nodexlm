// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** Encrypted node credentials, available to the UI and the isolated :rpc
 * process under the same application UID. No token is passed through Intents.
 */
internal object NodeSecrets {
    private const val ALIAS = "nodexlm-node-token-v1"
    private const val PREFERENCES = "nodexlm"
    private const val ENCRYPTED = "node_token_encrypted"

    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(ALIAS, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        generator.init(
            KeyGenParameterSpec.Builder(ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build()
        )
        return generator.generateKey()
    }

    fun encrypt(token: String): String {
        require(token.isNotBlank()) { "Node token is empty" }
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val encrypted = cipher.doFinal(token.toByteArray(Charsets.UTF_8))
        val packed = cipher.iv + encrypted
        return Base64.encodeToString(packed, Base64.NO_WRAP)
    }

    fun read(context: Context): String {
        val prefs = context.getSharedPreferences(PREFERENCES, Context.MODE_PRIVATE)
        val encoded = prefs.getString(ENCRYPTED, null)
        if (!encoded.isNullOrEmpty()) {
            try {
                val packed = Base64.decode(encoded, Base64.DEFAULT)
                require(packed.size > 12 + 16) { "Truncated node credentials" }
                val cipher = Cipher.getInstance("AES/GCM/NoPadding")
                cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, packed.copyOfRange(0, 12)))
                return String(cipher.doFinal(packed.copyOfRange(12, packed.size)), Charsets.UTF_8)
            } catch (e: Exception) {
                throw IllegalStateException("Node credentials cannot be decrypted; pair with the Host again", e)
            }
        }
        // One-time migration of old debug APKs; commit encrypted value before
        // removing plaintext so service restarts do not lose the pairing.
        val legacy = prefs.getString("node_token", "").orEmpty()
        if (legacy.isNotEmpty()) {
            val secure = encrypt(legacy)
            check(prefs.edit().putString(ENCRYPTED, secure).remove("node_token").commit()) {
                "Could not migrate node credentials"
            }
        }
        return legacy
    }
}
