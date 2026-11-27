// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import java.net.URI
import java.util.Locale

internal data class BetaArtifact(
    val url: String,
    val sha256: String,
    val sizeBytes: Long,
    val versionCode: Long = 0L,
    val versionName: String? = null,
    val sourceCommit: String? = null
)

internal data class BetaBuildInfo(
    val versionCode: Long,
    val versionName: String?,
    val buildVariant: String,
    val signature: String,
    val certificateSha256: String?,
    val sourceCommit: String? = null
)

/** Public beta built as non-debuggable release with permanent signing. Legacy asset name retained for older clients. */
internal object BetaArtifactPolicy {
    const val TAG = "android-debug"
    const val FILE = "NodexLM-android10-plus-debug.apk"
    const val BUILD_INFO_FILE = "BUILD_INFO.txt"
    const val URL = "https://github.com/rfdetoni/nodexlm/releases/download/$TAG/$FILE"
    const val BUILD_INFO_URL = "https://github.com/rfdetoni/nodexlm/releases/download/$TAG/$BUILD_INFO_FILE"
    private val shaPattern = Regex("sha256:[0-9a-fA-F]{64}")
    private val certShaPattern = Regex("[0-9a-fA-F]{64}")

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
        return BetaArtifact(url, digest.substringAfter(':').lowercase(Locale.ROOT), bytes)
    }

    fun parseBuildInfo(text: String): BetaBuildInfo {
        val values = LinkedHashMap<String, String>()
        for (rawLine in text.lineSequence()) {
            val line = rawLine.trim()
            if (line.isEmpty()) continue
            val idx = line.indexOf('=')
            require(idx > 0) { "Invalid BUILD_INFO line" }
            values[line.substring(0, idx).trim()] = line.substring(idx + 1).trim()
        }
        val variant = values["build_variant"] ?: error("Missing build_variant")
        val signature = values["signature"] ?: error("Missing signature")
        require(variant == "release") { "Beta APK must be a non-debuggable release build" }
        require(signature == "permanent") { "Beta APK must use the permanent signing identity" }
        val versionCode = values["version_code"]?.toLongOrNull() ?: 0L
        require(versionCode > 0L) { "Invalid beta version_code" }
        val versionName = values["version_name"]?.takeIf { it.isNotBlank() }
        val certSha = values["certificate_sha256"]?.takeIf { it.isNotBlank() }?.also {
            require(certShaPattern.matches(it)) { "Invalid beta certificate_sha256" }
        }?.lowercase(Locale.ROOT)
        return BetaBuildInfo(versionCode, versionName, variant, signature, certSha,
            values["source_commit"]?.takeIf { Regex("[0-9a-f]{40}").matches(it) })
    }
}
