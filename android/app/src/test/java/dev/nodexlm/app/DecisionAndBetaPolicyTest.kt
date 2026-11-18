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
}
