// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.app.ActivityManager
import android.content.Context
import ai.djl.huggingface.tokenizers.HuggingFaceTokenizer
import ai.onnxruntime.OnnxTensor
import ai.onnxruntime.OrtEnvironment
import ai.onnxruntime.OrtSession
import org.json.JSONObject
import java.io.ByteArrayInputStream
import java.nio.LongBuffer
import kotlin.math.max

/**
 * Runs a complete Laya ONNX decision in-process on the Android CPU.
 * A single resident session is reused for successive cluster tasks. There is
 * no local HTTP server, so other applications cannot access this model.
 */
internal object EmbeddedDecisionRuntime {
    private data class Loaded(
        val key: List<String>,
        val config: LayaDecisionProtocol.Config,
        val calibration: JSONObject,
        val session: OrtSession,
        val tokenizer: HuggingFaceTokenizer
    ) : AutoCloseable {
        override fun close() {
            try { session.close() } finally { tokenizer.close() }
        }
    }

    private val environment: OrtEnvironment by lazy { OrtEnvironment.getEnvironment() }
    private var resident: Loaded? = null
    // Installed bundles are immutable within the app; invalidate when their
    // metadata changes (including external ONNX weights).
    private var validated: List<String>? = null

    @Synchronized
    fun close() {
        val previous = resident
        resident = null
        previous?.close()
    }

    private fun key(bundle: DecisionModelStore.Bundle): List<String> =
        (listOf(bundle.graph, bundle.tokenizer, bundle.decision, bundle.calibration) +
            java.io.File(bundle.graph.path + ".data")).map {
            "${it.canonicalPath}:${it.length()}:${it.lastModified()}"
        }

    private fun enoughMemory(context: Context, bundle: DecisionModelStore.Bundle): Boolean {
        val memory = ActivityManager.MemoryInfo()
        (context.getSystemService(Context.ACTIVITY_SERVICE) as ActivityManager).getMemoryInfo(memory)
        val weights = bundle.graph.length() + java.io.File(bundle.graph.path + ".data").length()
        val reserve = GuestResourceBudget.reserveBytes(0, memory.totalMem, memory.threshold)
        return !memory.lowMemory && memory.availMem > reserve &&
            weights < memory.availMem - reserve
    }

    private fun load(context: Context, alias: String): Loaded {
        val bundle = DecisionModelStore.find(context, alias) ?: error("No installed ONNX decision bundle for $alias")
        val key = key(bundle)
        resident?.takeIf { it.key == key }?.let { return it }
        close()
        require(enoughMemory(context, bundle)) { "Insufficient free RAM for ONNX decision weights" }
        val path = bundle.graph.canonicalPath
        val config = LayaDecisionProtocol.Config.parse(JSONObject(bundle.decision.readText()))
        val calibration = JSONObject(bundle.calibration.readText())
        val rawTokenizer = JSONObject(bundle.tokenizer.readText())
        require(rawTokenizer.getJSONObject("model").getString("type") == "BPE") {
            "This Laya tokenizer requires byte-level BPE"
        }
        // Ollaya disables tokenizer-level truncation/padding; the Laya layout
        // applies its own budgets and inserts exactly its own special tokens.
        rawTokenizer.put("truncation", JSONObject.NULL)
        rawTokenizer.put("padding", JSONObject.NULL)
        val tokenizer = HuggingFaceTokenizer.newInstance(
            ByteArrayInputStream(rawTokenizer.toString().toByteArray(Charsets.UTF_8)),
            mapOf("addSpecialTokens" to "false"))
        try {
            val options = OrtSession.SessionOptions()
            val session = try {
                options.setIntraOpNumThreads(max(1, minOf(4, Runtime.getRuntime().availableProcessors() - 1)))
                options.setOptimizationLevel(OrtSession.SessionOptions.OptLevel.ALL_OPT)
                environment.createSession(path, options)
            } finally {
                options.close()
            }
            try {
                val inputs = session.inputNames
                require(inputs.containsAll(setOf(
                    "input_ids", "attention_mask", "marker_pos", "marker_mask", "qtype"
                )) && session.outputNames.contains("logits")) {
                    "ONNX model is not a Laya System One graph"
                }
                return Loaded(key, config, calibration, session, tokenizer)
                    .also { resident = it; validated = key }
            } catch (e: Exception) {
                session.close()
                throw e
            }
        } catch (e: Exception) {
            tokenizer.close()
            throw e
        }
    }

    /** Validate once, then release a probe-only session. Do not allocate
     * weights on every lease or keep a model resident merely to advertise it.
     * Actual decision execution reopens and validates the native session.
     */
    @Synchronized
    fun available(context: Context, alias: String, allowLoad: Boolean = true): Boolean {
        return try {
            val bundle = DecisionModelStore.find(context, alias) ?: return false
            val key = key(bundle)
            if (resident?.key == key) return true
            if (!enoughMemory(context, bundle)) return false
            if (validated == key) return true
            if (!allowLoad || resident != null) return false
            try {
                load(context, alias)
                true
            } finally {
                close()
            }
        } catch (_: Exception) {
            false
        }
    }

    @Synchronized
    fun decide(context: Context, alias: String, request: JSONObject): JSONObject {
        val loaded = load(context, alias)
        require(request.has("state")) { "System One state is required" }
        val questions = LayaDecisionProtocol.questions(request.getJSONObject("questions"))
        val rawState = request.get("state")
        val state = LayaDecisionProtocol.stringify(rawState)
        val answers = JSONObject()
        var tokenCount = 0
        for (q in questions) {
            if (Thread.currentThread().isInterrupted) error("Decision interrupted")
            val encoded = LayaDecisionProtocol.encode(
                loaded.config, state, q, rawState is org.json.JSONArray
            ) { input ->
                loaded.tokenizer.encode(input, false, false).ids
            }
            val slots = max(q.labels.size, loaded.config.minMarkers)
            val tokenIDs = encoded.ids
            val attention = LongArray(tokenIDs.size) { 1L }
            val pos = LongArray(slots) { i -> encoded.markers.getOrElse(i) { 0L } }
            val mask = BooleanArray(slots) { it < encoded.markers.size }
            val inputs = LinkedHashMap<String, OnnxTensor>()
            try {
                inputs["input_ids"] = OnnxTensor.createTensor(
                    environment, LongBuffer.wrap(tokenIDs), longArrayOf(1L, tokenIDs.size.toLong()))
                inputs["attention_mask"] = OnnxTensor.createTensor(
                    environment, LongBuffer.wrap(attention), longArrayOf(1L, attention.size.toLong()))
                inputs["marker_pos"] = OnnxTensor.createTensor(
                    environment, LongBuffer.wrap(pos), longArrayOf(1L, pos.size.toLong()))
                inputs["marker_mask"] = OnnxTensor.createTensor(environment, arrayOf(mask))
                inputs["qtype"] = OnnxTensor.createTensor(
                    environment, LongBuffer.wrap(longArrayOf(q.typeIndex)), longArrayOf(1L))

                loaded.session.run(inputs, setOf("logits")).use { output ->
                    val logits = (output[0].value as Array<*>)[0] as FloatArray
                    require(logits.size >= q.labels.size) { "ONNX output has too few decision slots" }
                    val temperature = LayaDecisionProtocol.temperature(
                        loaded.calibration, q.type, q.labels.size)
                    val probabilities = LayaDecisionProtocol.probabilities(
                        logits.copyOf(q.labels.size), temperature)
                    answers.put(q.id, LayaDecisionProtocol.answer(q, probabilities))
                }
            } finally {
                inputs.values.forEach { it.close() }
            }
            tokenCount += tokenIDs.size
        }
        return JSONObject().put("model", alias).put("answers", answers)
            .put("usage", JSONObject().put("input_tokens", tokenCount).put("output_tokens", 0))
    }
}
