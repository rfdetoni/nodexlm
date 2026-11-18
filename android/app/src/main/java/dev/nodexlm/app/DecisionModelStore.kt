// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import org.json.JSONObject
import java.io.File
import java.io.FileOutputStream

/**
 * Self-contained Laya ONNX bundles. Weights are not shipped with the APK:
 * users import a verified conversion, while the runtime is native to the APK.
 * File names and model layouts are restricted rather than trusting filenames
 * received from the coordinator.
 */
internal object DecisionModelStore {
    const val DEFAULT_ALIAS = "ollaya/laya:en"
    private val namePattern = Regex("[A-Za-z0-9._-]{1,90}")
    private val allowed = setOf(
        "model.onnx", "model.onnx.data", "model-fp32.onnx",
        "model-fp32.onnx.data", "model-fp16.onnx",
        "model-fp16.onnx.data", "laya-q8.onnx", "tokenizer.json",
        "decision.json", "calibration.json", "rl_agent_config.json"
    )
    private val graphNames = listOf("model.onnx", "model-fp32.onnx", "model-fp16.onnx", "laya-q8.onnx")

    data class Bundle(val alias: String, val directory: File, val graph: File) {
        val tokenizer get() = File(directory, "tokenizer.json")
        val decision get() = File(directory, "decision.json")
        val calibration get() = File(directory, "calibration.json")
    }

    fun directory(context: Context): File =
        File(context.getExternalFilesDir(null) ?: context.filesDir, "decision-models").apply { mkdirs() }

    private fun folder(alias: String): String {
        require(alias.length in 1..256 && !alias.contains('/') ||
            alias.startsWith("ollaya/") && alias.drop(7).isNotEmpty()) { "Invalid decision alias" }
        val normalized = alias.removePrefix("ollaya/").replace(':', '-')
        require(namePattern.matches(normalized) && normalized != "." && normalized != "..") {
            "Invalid decision alias"
        }
        return normalized
    }

    fun valid(bundle: Bundle): Boolean = try {
        require(bundle.directory.canonicalFile.parentFile == directoryPath(bundle.directory))
        require(bundle.graph.isFile && bundle.graph.length() > 1024L)
        require(bundle.tokenizer.isFile && bundle.tokenizer.length() in 100L..(20L shl 20))
        require(bundle.decision.isFile && bundle.decision.length() in 10L..(64L shl 10))
        require(bundle.calibration.isFile && bundle.calibration.length() in 5L..(64L shl 10))
        val config = JSONObject(bundle.decision.readText(Charsets.UTF_8))
        config.getString("engine") == "onnx" && config.getString("layout") == "laya-markers-v1" &&
            config.getInt("max_len") in 64..2048 && config.getInt("head_max_len") in 16..1024
    } catch (_: Exception) { false }

    private fun directoryPath(dir: File) = dir.parentFile!!.canonicalFile

    /** Finds a locally imported model by its *exact* advertised alias. */
    fun find(context: Context, alias: String): Bundle? = try {
        val root = directory(context).canonicalFile
        val dir = File(root, folder(alias)).canonicalFile
        if (dir.parentFile != root) return null
        val graph = graphNames.asSequence().map { File(dir, it) }.firstOrNull { it.isFile } ?: return null
        Bundle(alias, dir, graph).takeIf(::valid)
    } catch (_: Exception) { null }

    fun selected(context: Context): Bundle? {
        val name = context.getSharedPreferences("nodexlm", Context.MODE_PRIVATE)
            .getString("decision_model", "").orEmpty()
        return find(context, name)
    }

    fun list(context: Context): List<Bundle> {
        val root = directory(context)
        return root.listFiles()?.filter { it.isDirectory }?.mapNotNull { folder ->
            val alias = "ollaya/" + folder.name.replace('-', ':')
            find(context, alias)
        } ?: emptyList()
    }

    private fun displayName(context: Context, uri: Uri): String {
        val name = context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME),
            null, null, null)?.use { c -> if (c.moveToFirst()) c.getString(0) else null }
        return name ?: error("Missing model filename")
    }

    /**
     * Atomic import: no installed model is replaced and no half-copied bundle
     * becomes discoverable. Supports Ollaya's bundle and laya-mobile's q8 files.
     */
    @Synchronized
    fun importBundle(context: Context, alias: String, uris: List<Uri>): Bundle {
        val name = folder(alias)
        require(uris.size in 3..5) { "Select 3-5 ONNX model, tokenizer and configuration files" }
        val byName = uris.associateBy { uri -> displayName(context, uri) }
        require(byName.size == uris.size && byName.keys.all { it in allowed }) { "Invalid/duplicate ONNX bundle file" }
        require(byName.keys.count { it.endsWith(".onnx") } == 1)
        require("tokenizer.json" in byName)
        val mobile = "rl_agent_config.json" in byName
        require(if (mobile) byName.keys.contains("laya-q8.onnx") && byName.size == 3 else
            "decision.json" in byName && "calibration.json" in byName) {
            "Incomplete Laya model bundle"
        }

        val root = directory(context).canonicalFile
        val target = File(root, name)
        require(!target.exists()) { "Decision model is already installed" }
        val staging = File.createTempFile(".decision-", "", root)
        check(staging.delete() && staging.mkdir()) { "Cannot stage decision bundle" }
        try {
            var total = 0L
            for ((fileName, uri) in byName) {
                val output = File(staging, fileName)
                val maxBytes = when {
                    fileName.endsWith(".onnx") || fileName.endsWith(".onnx.data") -> 4L shl 30
                    fileName == "tokenizer.json" -> 20L shl 20
                    else -> 64L shl 10
                }
                context.contentResolver.openInputStream(uri)?.use { input ->
                    FileOutputStream(output).use { destination ->
                        val buffer = ByteArray(256 * 1024)
                        while (true) {
                            val n = input.read(buffer)
                            if (n < 0) break
                            total += n
                            require(output.length() + n <= maxBytes && total <= (4L shl 30)) { "Model too large" }
                            destination.write(buffer, 0, n)
                        }
                        destination.fd.sync()
                    }
                } ?: error("Cannot read ONNX model file: $fileName")
                require(output.length() > 0L) { "Empty model file: $fileName" }
            }
            if (mobile) {
                val config = JSONObject(File(staging, "rl_agent_config.json").readText())
                require(config.getInt("max_len") == 512 && config.getInt("head_max_len") == 192)
                val layer = JSONObject()
                    .put("engine", "onnx").put("layout", "laya-markers-v1")
                    .put("max_len", 512).put("head_max_len", 192).put("min_markers", 1)
                    .put("special_tokens", JSONObject().put("cls", 50281).put("sep", 50282)
                        .put("mask", 50284).put("pad", 50283).put("mask_text", "[MASK]"))
                File(staging, "decision.json").writeText(layer.toString())
                File(staging, "calibration.json").writeText(config.toString())
            }
            val graph = graphNames.asSequence().map { File(staging, it) }.first { it.isFile }
            val staged = Bundle(alias, staging, graph)
            require(valid(staged)) { "Model configuration is invalid or unsupported" }
            require(staging.renameTo(target)) { "Failed to install decision model" }
            val installed = Bundle(alias, target, File(target, graph.name))
            context.getSharedPreferences("nodexlm", Context.MODE_PRIVATE).edit()
                .putString("decision_model", alias).apply()
            return installed
        } finally {
            if (staging.exists()) staging.deleteRecursively()
        }
    }
}
