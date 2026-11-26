// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.content.Context
import android.net.Uri
import android.provider.DocumentsContract
import java.io.File
import java.io.FileOutputStream
import java.io.RandomAccessFile

/** Storage shared by manual imports, DownloadManager and the native worker. */
internal object ModelFiles {
    fun directory(context: Context): File = File(context.getExternalFilesDir(null) ?: context.filesDir,"models").apply { mkdirs() }

    fun list(context: Context): List<File> = directory(context).listFiles()
        ?.filter { it.isFile && it.name.endsWith(".gguf",true) && validModel(it) }
        ?.sortedBy { it.name.lowercase() } ?: emptyList()

    fun selected(context: Context): File? {
        val prefs = context.getSharedPreferences("nodexlm", Context.MODE_PRIVATE)
        val saved = prefs.getString("model_path",null)
        val file = if (saved.isNullOrBlank()) File(context.filesDir,"models/active.gguf") else File(saved)
        val root = directory(context).canonicalFile
        // Support legacy imports stored in internal filesDir/models/active.gguf.
        val legacy = File(context.filesDir,"models/active.gguf").canonicalFile
        val canonical = file.canonicalFile
        if (canonical != legacy && canonical.parentFile != root) return null
        return file.takeIf { validModel(it) }
    }

    fun select(context: Context, file: File) {
        require(validModel(file))
        require(file.canonicalFile.parentFile == directory(context).canonicalFile)
        context.getSharedPreferences("nodexlm",Context.MODE_PRIVATE).edit()
            .putString("model_path",file.absolutePath).putString("model",file.name).apply()
    }

    fun shards(name: String): List<String> {
        val match = Regex("^(.*)-(\\d{5})-of-(\\d{5})(\\.gguf)$",RegexOption.IGNORE_CASE).matchEntire(name) ?: return listOf(name)
        val first = match.groupValues[2].toInt()
        val count = match.groupValues[3].toInt()
        require(first == 1 && count in 2..128) { "Select the first shard and the complete set (2–128 parts)" }
        return (1..count).map { match.groupValues[1]+"-%05d-of-%05d".format(java.util.Locale.US,it,count)+match.groupValues[4] }
    }
    fun validModel(file: File): Boolean = try { shards(file.name).all { validGGUF(File(file.parentFile,it)) } } catch (_:Exception) { false }
    fun modelBytes(file: File): Long = shards(file.name).sumOf { File(file.parentFile,it).length() }

    fun validGGUF(file: File): Boolean {
        if (!file.isFile || file.length() < 8) return false
        return try {
            RandomAccessFile(file,"r").use { f ->
                val magic = ByteArray(4)
                f.readFully(magic)
                magic.contentEquals(byteArrayOf(71, 71, 85, 70))
            }
        } catch (_:Exception) { false }
    }

    fun safeFilename(name: String): String {
        val clean = name.substringAfterLast('/').substringAfterLast('\\')
            .replace(Regex("[^a-zA-Z0-9._-]"),"_").trim('.','_').take(100)
        require(clean.isNotBlank() && clean.endsWith(".gguf",true)) { "Choose a .gguf file" }
        return clean
    }

    // SAF directory grants do not guarantee real filesystem paths, so use
    // the existing bounded streaming copy. No recursive traversal or links.
    @Synchronized fun importDirectory(context: Context, tree: Uri): Int {
        val resolver = context.contentResolver
        val root = DocumentsContract.getTreeDocumentId(tree)
        val children = DocumentsContract.buildChildDocumentsUriUsingTree(tree, root)
        val sources = linkedMapOf<String, Uri>()
        val columns = arrayOf(DocumentsContract.Document.COLUMN_DOCUMENT_ID,
            DocumentsContract.Document.COLUMN_DISPLAY_NAME, DocumentsContract.Document.COLUMN_MIME_TYPE)
        resolver.query(children, columns, null, null, null)?.use { cursor ->
            var visited = 0
            while (cursor.moveToNext()) {
                require(++visited <= 1024) { "Directory contains more than 1024 entries" }
                if (cursor.getString(2) == DocumentsContract.Document.MIME_TYPE_DIR) continue
                val original = cursor.getString(1) ?: continue
                if (!original.endsWith(".gguf", true)) continue
                val name = safeFilename(original)
                val uri = DocumentsContract.buildDocumentUriUsingTree(tree, cursor.getString(0))
                require(sources.put(name, uri) == null) { "Duplicate normalized filename: $name" }
            }
        } ?: error("Cannot read directory")
        val groups = planDirectoryImports(sources.keys)
        val dir = directory(context)
        var count = 0
        for (group in groups) {
            val targets = group.map { File(dir, it) }
            if (targets.all(::validGGUF)) continue
            require(targets.none { it.exists() }) { "Partial or invalid destination model: ${group.first()}" }
            try {
                importSources(context, group.associateWith { sources.getValue(it) }, selectModel = false)
                count++
            } catch (e: Exception) {
                throw IllegalStateException("Imported $count models; failed on ${group.first()}: ${e.message}", e)
            }
        }
        return count
    }

    internal fun planDirectoryImports(names: Set<String>): List<List<String>> {
        val remaining = names.toMutableSet()
        val groups = mutableListOf<List<String>>()
        val nonFirst = Regex("^(.*)-(\\d{5})-of-(\\d{5})\\.gguf$", RegexOption.IGNORE_CASE)
        for (name in names.sorted()) {
            if (name !in remaining) continue
            val match = nonFirst.matchEntire(name)
            if (match != null && match.groupValues[2] != "00001") continue
            val group = shards(name)
            require(group.all { it in remaining }) { "Incomplete model: $name" }
            remaining.removeAll(group.toSet())
            groups.add(group)
        }
        require(remaining.isEmpty()) { "Directory contains orphan GGUF shards" }
        return groups
    }

    fun import(context: Context, uri: Uri): File = importSet(context,listOf(uri))
    @Synchronized fun importSet(context: Context, uris: List<Uri>): File {
        require(uris.size in 1..128) { "Select up to 128 GGUF files" }
        val resolver=context.contentResolver
        val sources=uris.associateBy { uri ->
            val display=resolver.query(uri,arrayOf(android.provider.OpenableColumns.DISPLAY_NAME),null,null,null)?.use { cursor -> if(cursor.moveToFirst())cursor.getString(0) else null } ?: "model.gguf"
            safeFilename(display)
        }
        require(sources.size==uris.size) { "Duplicate filenames" }
        return importSources(context, sources, selectModel = true)
    }

    private fun importSources(context: Context, sources: Map<String, Uri>, selectModel: Boolean): File {
        val resolver = context.contentResolver
        val first=sources.keys.sorted().first()
        val expected=shards(first)
        require(expected.toSet()==sources.keys) { "Select every shard of one model" }
        val dir=directory(context)
        require(expected.none { File(dir,it).exists() }) { "Model or shard already exists" }
        val stage=File.createTempFile(".import-","",dir);check(stage.delete()&&stage.mkdir())
        val created=mutableListOf<File>()
        try {
            for(name in expected){
                val part=File(stage,name)
                resolver.openInputStream(sources.getValue(name))?.use { input -> FileOutputStream(part).use { out -> input.copyTo(out,256*1024);out.fd.sync() } } ?: error("Cannot read selected file")
                require(validGGUF(part)) { "Invalid GGUF shard: $name" }
            }
            for(name in expected.asReversed()) { val target=File(dir,name);require(!target.exists()&&File(stage,name).renameTo(target));created.add(target) }
            val result=File(dir,first);if (selectModel) select(context,result);return result
        } catch(e:Exception){created.forEach {it.delete()};throw e}
        finally {stage.deleteRecursively()}
    }
}
