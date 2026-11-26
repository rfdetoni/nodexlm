package dev.nodexlm.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class VerifiedHuggingFaceDownloadTest {
    private val revision = "a".repeat(40)
    private val hash = "b".repeat(64)

    @Test fun acceptsImmutableLfsChecksumAndSize() {
        val metadata = VerifiedHuggingFaceDownload.parseMetadata(
            revision, "\"$hash\"", "not-a-checksum", "10485760", -1
        )
        assertEquals(hash, metadata.sha256)
        assertEquals(revision, metadata.revision)
        assertEquals(10485760L, metadata.size)
    }

    @Test fun rejectsMissingAndWeakDigests() {
        assertThrows(IllegalArgumentException::class.java) {
            VerifiedHuggingFaceDownload.parseMetadata(revision, null, "\"opaque\"", "12345", -1)
        }
        assertThrows(IllegalArgumentException::class.java) {
            VerifiedHuggingFaceDownload.parseMetadata(revision, "W/\"$hash\"", null, "12345", -1)
        }
    }

    @Test fun rejectsUnknownRevisionAndInvalidSize() {
        assertThrows(IllegalArgumentException::class.java) {
            VerifiedHuggingFaceDownload.parseMetadata("main", hash, null, "20", -1)
        }
        assertThrows(IllegalArgumentException::class.java) {
            VerifiedHuggingFaceDownload.parseMetadata(revision, hash, null, "0", -1)
        }
    }

    @Test fun pinsAllShardsWithoutChangingRepositoryOrPath() {
        val urls = VerifiedHuggingFaceDownload.pinnedUrls(
            "https://huggingface.co/owner/model/resolve/main/gguf/model-00001-of-00002.gguf",
            revision,
            listOf("model-00001-of-00002.gguf", "model-00002-of-00002.gguf")
        )
        assertEquals(
            "https://huggingface.co/owner/model/resolve/$revision/gguf/model-00002-of-00002.gguf",
            urls.last()
        )
    }

    @Test fun rejectsUntrustedHostsAndRenamedShards() {
        assertThrows(IllegalArgumentException::class.java) {
            VerifiedHuggingFaceDownload.pinnedUrls(
                "https://huggingface.co.evil.example/owner/model/resolve/main/a.gguf",
                revision, listOf("a.gguf")
            )
        }
        assertThrows(IllegalArgumentException::class.java) {
            VerifiedHuggingFaceDownload.pinnedUrls(
                "https://huggingface.co/owner/model/resolve/main/old-00001-of-00002.gguf",
                revision, listOf("new-00001-of-00002.gguf", "new-00002-of-00002.gguf")
            )
        }
    }
}
