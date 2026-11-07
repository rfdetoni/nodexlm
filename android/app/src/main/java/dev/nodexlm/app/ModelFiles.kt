// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.content.Context
import android.net.Uri
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

    fun import(context: Context, uri: Uri): File = importSet(context,listOf(uri))
    @Synchronized fun importSet(context: Context, uris: List<Uri>): File {
        require(uris.size in 1..128) { "Select up to 128 GGUF files" }
        val resolver=context.contentResolver
        val sources=uris.associateBy { uri ->
            val display=resolver.query(uri,arrayOf(android.provider.OpenableColumns.DISPLAY_NAME),null,null,null)?.use { cursor -> if(cursor.moveToFirst())cursor.getString(0) else null } ?: "model.gguf"
            safeFilename(display)
        }
        require(sources.size==uris.size) { "Duplicate filenames" }
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
            val result=File(dir,first);select(context,result);return result
        } catch(e:Exception){created.forEach {it.delete()};throw e}
        finally {stage.deleteRecursively()}
    }
}
