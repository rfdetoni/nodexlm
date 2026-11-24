package dev.nodexlm.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class DecisionAndBetaPolicyTest {
    @Test fun detectsDecisionModelCandidatesWithoutBroadSubstringMatches() {
        assertTrue(DecisionModelNames.likely("user/Winnow-E4B", "model-Q8_0.gguf"))
        assertTrue(DecisionModelNames.likely("user/pplx-decider-flash", "model.gguf"))
        assertTrue(DecisionModelNames.likely("user/JevK5", "model.gguf"))
        assertFalse(DecisionModelNames.likely("user/Qwen3-Instruct", "model.gguf"))
        assertFalse(DecisionModelNames.likely("user/decisionless-chat", "model.gguf"))
    }

    @Test fun betaDownloadsOnlyFromTheOfficialPrerelease() {
        val digest = "sha256:" + "A".repeat(64)
        val valid = BetaArtifactPolicy.validate(
            "android-debug", true, false, BetaArtifactPolicy.FILE,
            BetaArtifactPolicy.URL, digest, 12000000L
        )
        assertEquals("a".repeat(64), valid.sha256)
        assertEquals(BetaArtifactPolicy.URL, valid.url)
        assertEquals(12000000L, valid.sizeBytes)

        val invalid = listOf(
            listOf("v1.0.0", "true", BetaArtifactPolicy.FILE, BetaArtifactPolicy.URL),
            listOf("android-debug", "false", BetaArtifactPolicy.FILE, BetaArtifactPolicy.URL),
            listOf("android-debug", "true", "other.apk", BetaArtifactPolicy.URL),
            listOf("android-debug", "true", BetaArtifactPolicy.FILE,
                "https://github.com.evil.invalid/rfdetoni/nodexlm/releases/download/android-debug/NodexLM-android10-plus-debug.apk"),
            listOf("android-debug", "true", BetaArtifactPolicy.FILE, BetaArtifactPolicy.URL + "?evil=yes")
        )
        for (row in invalid) {
            assertThrows(IllegalArgumentException::class.java) {
                BetaArtifactPolicy.validate(
                    row[0], row[1] == "true", false, row[2], row[3], digest, 12000000L
                )
            }
        }
        assertThrows(IllegalArgumentException::class.java) {
            BetaArtifactPolicy.validate(
                "android-debug", true, false, BetaArtifactPolicy.FILE,
                BetaArtifactPolicy.URL, "sha256:bad", 12000000L
            )
        }
        assertThrows(IllegalArgumentException::class.java) {
            BetaArtifactPolicy.validate(
                "android-debug", true, false, BetaArtifactPolicy.FILE,
                BetaArtifactPolicy.URL, digest, 0L
            )
        }
    }

    @Test fun betaBuildInfoAcceptsPermanentReleaseAndRejectsDebug() {
        val cert = "20fe35971bfdd2ef83cfdaf449e19fd5c8828b93f7595f7657a65730cd9cc4ee"
        val parsed = BetaArtifactPolicy.parseBuildInfo(
            """
            source_commit=0123456789abcdef0123456789abcdef01234567
            android_workflow_run=12345
            package=NodexLM-android10-plus-debug.apk
            build_variant=release
            signature=permanent
            version_code=168
            version_name=0.1.0-ci.168
            certificate_sha256=${cert.uppercase()}
            """.trimIndent()
        )
        assertEquals(168L, parsed.versionCode)
        assertEquals("0.1.0-ci.168", parsed.versionName)
        assertEquals("release", parsed.buildVariant)
        assertEquals("permanent", parsed.signature)
        assertEquals(cert, parsed.certificateSha256)

        assertThrows(IllegalArgumentException::class.java) {
            BetaArtifactPolicy.parseBuildInfo(
                "build_variant=debug\nsignature=ephemeral-debug\nversion_code=159\n"
            )
        }
        assertThrows(IllegalArgumentException::class.java) {
            BetaArtifactPolicy.parseBuildInfo(
                "build_variant=release\nsignature=ephemeral-debug\nversion_code=160\n"
            )
        }
        assertThrows(IllegalArgumentException::class.java) {
            BetaArtifactPolicy.parseBuildInfo(
                "build_variant=release\nsignature=permanent\nversion_code=0\n"
            )
        }
    }
}
