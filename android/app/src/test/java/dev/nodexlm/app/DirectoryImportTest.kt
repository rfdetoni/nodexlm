package dev.nodexlm.app

import org.junit.Assert.assertEquals
import org.junit.Test

class DirectoryImportTest {
    @Test fun groupsCompleteSets() {
        assertEquals(listOf(listOf("a-00001-of-00002.gguf", "a-00002-of-00002.gguf"), listOf("b.gguf")),
            ModelFiles.planDirectoryImports(setOf("b.gguf", "a-00002-of-00002.gguf", "a-00001-of-00002.gguf")))
    }

    @Test(expected = IllegalArgumentException::class)
    fun rejectsIncompleteSet() {
        ModelFiles.planDirectoryImports(setOf("a-00001-of-00002.gguf"))
    }

    @Test(expected = IllegalArgumentException::class)
    fun rejectsOrphanShard() {
        ModelFiles.planDirectoryImports(setOf("a-00002-of-00002.gguf"))
    }
}
