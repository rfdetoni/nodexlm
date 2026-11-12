// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import java.net.URI

internal data class UpdateRelease(
    val versionCode: Int,
    val versionName: String,
    val sha256: String,
    val apkUrl: String
)

/**
 * Only stable, officially signed GitHub Release APKs are update candidates.
 * Debug APKs use CI signing keys which are not stable across workflow runs.
 * Manifest and APK both originate from the pinned repository; PackageInstaller
 * still checks that the APK signer is the already installed app's signer.
 */
internal object UpdateReleasePolicy {
    private val namePattern = Regex("[A-Za-z0-9][A-Za-z0-9._+-]{0,47}")
    private val checksumPattern = Regex("[0-9a-fA-F]{64}")
    private val signedReleasePath = Regex(
        "/rfdetoni/nodexlm/releases/download/v[0-9]+\\.[0-9]+\\.[0-9]+/NodexLM-android-universal\\.apk"
    )

    fun validate(
        schema: Int,
        versionCode: Int,
        versionName: String,
        sha256: String,
        apkUrl: String
    ): UpdateRelease {
        require(schema == 1) { "Unsupported update manifest schema" }
        require(versionCode > 0) { "Invalid update versionCode" }
        require(namePattern.matches(versionName)) { "Invalid update versionName" }
        require(checksumPattern.matches(sha256)) { "Invalid update SHA-256" }
        val uri = URI(apkUrl)
        require(
            uri.scheme == "https" && uri.host == "github.com" &&
                uri.port == -1 && uri.rawUserInfo == null &&
                uri.rawQuery == null && uri.rawFragment == null &&
                signedReleasePath.matches(uri.rawPath ?: "") &&
                apkUrl == "https://github.com" + uri.rawPath
        ) { "APK URL is not an official stable NodexLM GitHub Release" }
        return UpdateRelease(versionCode, versionName, sha256.lowercase(), apkUrl)
    }
}
