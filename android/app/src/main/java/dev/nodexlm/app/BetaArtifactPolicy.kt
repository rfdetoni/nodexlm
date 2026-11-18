// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import java.net.URI

internal data class BetaArtifact(val url: String, val sha256: String, val sizeBytes: Long)

/** Public debug APK: downloadable for testing, never an automatic in-place update. */
internal object BetaArtifactPolicy {
    const val TAG = "android-debug"
    const val FILE = "NodexLM-android10-plus-debug.apk"
    const val URL = "https://github.com/rfdetoni/nodexlm/releases/download/$TAG/$FILE"
    private val shaPattern = Regex("sha256:[0-9a-fA-F]{64}")
    
    fun validate(
        tag: String, prerelease: Boolean, draft: Boolean, assetName: String,
        url: String, digest: String, bytes: Long
    ): BetaArtifact {
        require(tag == TAG && prerelease && !draft) { "Not the official Android test prerelease" }
        require(assetName == FILE && url == URL) { "Untrusted Android beta APK URL" }
        val parsed = URI(url)
        require(parsed.scheme == "https" && parsed.host == "github.com" &&
            parsed.port == -1 && parsed.rawUserInfo == null &&
            parsed.rawQuery == null && parsed.rawFragment == null) { "Invalid APK URL" }
        require(shaPattern.matches(digest)) { "Missing GitHub SHA-256 for beta APK" }
        require(bytes in 1..(512L * 1024 * 1024)) { "Invalid Android beta APK size" }
        return BetaArtifact(url, digest.substringAfter(':').lowercase(java.util.Locale.ROOT), bytes)
    }
}
