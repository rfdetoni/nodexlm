package dev.nodexlm.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class UpdateReleasePolicyTest {
    private val official =
        "https://github.com/rfdetoni/nodexlm/releases/download/v1.2.3/NodexLM-android-universal.apk"
    private val checksum = "a".repeat(64)

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
}
