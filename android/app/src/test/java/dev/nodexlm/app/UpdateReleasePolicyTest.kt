package dev.nodexlm.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class UpdateReleasePolicyTest {
    private val official =
        "https://github.com/rfdetoni/nodexlm/releases/download/v1.2.3/NodexLM-android-universal.apk"
    private val checksum = "a".repeat(64)
    private val permanentCert = "2".repeat(64)
    private val legacyDebugCert = "9".repeat(64)

    @Test fun acceptsOnlyStableOfficialSignedRelease() {
        val release = UpdateReleasePolicy.validate(1, 12, "1.2.3", checksum.uppercase(), official)
        assertEquals(12, release.versionCode)
        assertEquals(checksum, release.sha256)
        assertEquals(official, release.apkUrl)
    }

    @Test fun rejectsUnknownOrMalformedRelease() {
        val invalid = listOf(
            "http://github.com/rfdetoni/nodexlm/releases/download/v1.2.3/NodexLM-android-universal.apk",
            "https://github.com.evil.invalid/rfdetoni/nodexlm/releases/download/v1.2.3/NodexLM-android-universal.apk",
            "https://github.com@attacker.invalid/rfdetoni/nodexlm/releases/download/v1.2.3/NodexLM-android-universal.apk",
            "https://github.com/rfdetoni/nodexlm/releases/download/v1.2.3-rc.1/NodexLM-android-universal.apk",
            "https://github.com/rfdetoni/nodexlm/releases/download/android-debug/NodexLM-android-universal.apk",
            "https://github.com/rfdetoni/nodexlm/releases/download/v1.2.3/NodexLM-android10-plus-debug.apk",
            "https://github.com/rfdetoni/nodexlm/releases/download/v1.2.3/NodexLM-android-universal.apk?redirect=evil",
            "https://github.com/rfdetoni/nodexlm/releases/download/v1.2.3/NodexLM-android-universal.apk#fragment"
        )
        for (url in invalid) {
            assertThrows("Unexpected accepted release: $url", IllegalArgumentException::class.java) {
                UpdateReleasePolicy.validate(1, 12, "1.2.3", checksum, url)
            }
        }
        assertThrows(IllegalArgumentException::class.java) {
            UpdateReleasePolicy.validate(2, 12, "1.2.3", checksum, official)
        }
        assertThrows(IllegalArgumentException::class.java) {
            UpdateReleasePolicy.validate(1, 0, "1.2.3", checksum, official)
        }
        assertThrows(IllegalArgumentException::class.java) {
            UpdateReleasePolicy.validate(1, 12, "../1.2", checksum, official)
        }
        assertThrows(IllegalArgumentException::class.java) {
            UpdateReleasePolicy.validate(1, 12, "1.2.3", "a".repeat(63), official)
        }
    }

    @Test fun classifiesStableAndBetaChannels() {
        assertEquals(ReleaseChannel.STABLE, ApkVerificationPolicy.classifyChannel("1.0.0"))
        assertEquals(ReleaseChannel.BETA, ApkVerificationPolicy.classifyChannel("0.1.0-ci.165"))
        assertEquals(ReleaseChannel.BETA, ApkVerificationPolicy.classifyChannel("1.0.0-rc.1"))
        assertEquals(ReleaseChannel.BETA, ApkVerificationPolicy.classifyChannel("0.1.0-alpha"))
    }

    @Test fun comparesVersionCodesStrictly() {
        assertEquals(VersionComparison.NEWER, ApkVerificationPolicy.compareVersion(165, 166))
        assertEquals(VersionComparison.CURRENT, ApkVerificationPolicy.compareVersion(165, 165))
        assertEquals(VersionComparison.DOWNGRADE, ApkVerificationPolicy.compareVersion(165, 164))
        assertEquals(VersionComparison.EXPECTED_MISMATCH, ApkVerificationPolicy.compareVersion(165, 167, 166))
    }

    @Test fun enforcesPermanentCertificateAndDetectsMigrationAndMultiSignerSpoofs() {
        assertEquals(
            SignatureCompatibility.COMPATIBLE,
            ApkVerificationPolicy.verifySignatures(
                listOf(permanentCert), listOf(permanentCert.uppercase()), permanentCert
            )
        )
        assertEquals(
            SignatureCompatibility.MIGRATION_REQUIRED_FROM_LEGACY_KEY,
            ApkVerificationPolicy.verifySignatures(
                listOf(legacyDebugCert), listOf(permanentCert), permanentCert
            )
        )
        assertEquals(
            SignatureCompatibility.INCOMPATIBLE_UNTRUSTED,
            ApkVerificationPolicy.verifySignatures(
                listOf(permanentCert), listOf(legacyDebugCert), permanentCert
            )
        )
        assertEquals(
            SignatureCompatibility.INCOMPATIBLE_UNTRUSTED,
            ApkVerificationPolicy.verifySignatures(
                listOf(permanentCert, legacyDebugCert), listOf(permanentCert, permanentCert), permanentCert
            )
        )
        assertEquals(
            SignatureCompatibility.INCOMPATIBLE_UNTRUSTED,
            ApkVerificationPolicy.verifySignatures(
                emptyList(), listOf(permanentCert), permanentCert
            )
        )
    }
}
