// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import java.net.URI
import java.security.MessageDigest
import java.util.Locale

internal data class UpdateRelease(
    val versionCode: Int,
    val versionName: String,
    val sha256: String,
    val apkUrl: String
)

enum class ReleaseChannel {
    STABLE,
    BETA
}

internal enum class VersionComparison {
    NEWER,
    CURRENT,
    DOWNGRADE,
    EXPECTED_MISMATCH
}

internal enum class SignatureCompatibility {
    COMPATIBLE,
    MIGRATION_REQUIRED_FROM_LEGACY_KEY,
    INCOMPATIBLE_UNTRUSTED
}

internal object ApkVerificationPolicy {
    private val stableVersionPattern = Regex("[0-9]+\\.[0-9]+\\.[0-9]+")
    private val sha256HexPattern = Regex("[0-9a-fA-F]{64}")

    fun classifyChannel(versionName: String): ReleaseChannel =
        if (stableVersionPattern.matches(versionName.trim())) ReleaseChannel.STABLE else ReleaseChannel.BETA

    fun compareVersion(
        installedCode: Long,
        candidateCode: Long,
        expectedCode: Long? = null
    ): VersionComparison {
        if (expectedCode != null && candidateCode != expectedCode) {
            return VersionComparison.EXPECTED_MISMATCH
        }
        return when {
            candidateCode > installedCode -> VersionComparison.NEWER
            candidateCode == installedCode -> VersionComparison.CURRENT
            else -> VersionComparison.DOWNGRADE
        }
    }

    fun sha256Hex(bytes: ByteArray): String =
        MessageDigest.getInstance("SHA-256").digest(bytes)
            .joinToString("") { "%02x".format(it.toInt() and 0xff) }

    fun verifySignatures(
        installedCertSha256s: List<String>,
        candidateCertSha256s: List<String>,
        expectedPermanentSha256: String
    ): SignatureCompatibility {
        if (installedCertSha256s.isEmpty() || candidateCertSha256s.isEmpty()) {
            return SignatureCompatibility.INCOMPATIBLE_UNTRUSTED
        }
        if (installedCertSha256s.any { !sha256HexPattern.matches(it) } ||
            candidateCertSha256s.any { !sha256HexPattern.matches(it) }) {
            return SignatureCompatibility.INCOMPATIBLE_UNTRUSTED
        }
        val installedNormalized = installedCertSha256s.map { it.lowercase(Locale.ROOT) }
        val candidateNormalized = candidateCertSha256s.map { it.lowercase(Locale.ROOT) }
        val installedSet = installedNormalized.toSet()
        val candidateSet = candidateNormalized.toSet()
        // Reject duplicate signer entries (e.g. [A, A] claiming parity with [A, B]).
        if (installedSet.size != installedNormalized.size || candidateSet.size != candidateNormalized.size) {
            return SignatureCompatibility.INCOMPATIBLE_UNTRUSTED
        }
        val expected = expectedPermanentSha256.trim().lowercase(Locale.ROOT)
        if (expected.isNotEmpty()) {
            if (!sha256HexPattern.matches(expected) || candidateSet != setOf(expected)) {
                return SignatureCompatibility.INCOMPATIBLE_UNTRUSTED
            }
            return if (installedSet == candidateSet) {
                SignatureCompatibility.COMPATIBLE
            } else {
                SignatureCompatibility.MIGRATION_REQUIRED_FROM_LEGACY_KEY
            }
        }
        return if (installedSet == candidateSet) {
            SignatureCompatibility.COMPATIBLE
        } else {
            SignatureCompatibility.INCOMPATIBLE_UNTRUSTED
        }
    }
}

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
