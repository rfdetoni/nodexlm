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
        ?.filter { it.isFile && it.name.endsWith(".gguf",true) && validGGUF(it) }
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
        return file.takeIf { validGGUF(it) }
    }

    fun select(context: Context, file: File) {
        require(validGGUF(file))
        require(file.canonicalFile.parentFile == directory(context).canonicalFile)
        context.getSharedPreferences("nodexlm",Context.MODE_PRIVATE).edit()
            .putString("model_path",file.absolutePath).putString("model",file.name).apply()
    }

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

    fun import(context: Context, uri: Uri): File {
        val resolver = context.contentResolver
        val display = resolver.query(uri,arrayOf(android.provider.OpenableColumns.DISPLAY_NAME),null,null,null)?.use { cursor ->
            if (cursor.moveToFirst()) cursor.getString(0) else null
        } ?: "model.gguf"
        val name = safeFilename(display)
        val dir = directory(context)
        val target = File(dir,name)
        require(!target.exists()) { "Model $name already exists" }
        val staging = File.createTempFile(".import-",".part",dir)
        try {
            resolver.openInputStream(uri)?.use { input ->
                FileOutputStream(staging).use { out ->input.copyTo(out, 256*1024);out.fd.sync() }
            } ?: error("Cannot read selected file")
            require(validGGUF(staging)) { "Not a valid GGUF header" }
            require(staging.renameTo(target)) { "Could not save imported model" }
            select(context,target)
            return target
        } finally { staging.delete() }
    }
}
