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
        val path: String,
        val config: LayaDecisionProtocol.Config,
        val calibration: JSONObject,
        val session: OrtSession,
        val tokenizer: HuggingFaceTokenizer
    ) : AutoCloseable {
        override fun close() {
            session.close()
            tokenizer.close()
        }
    }

    private val environment: OrtEnvironment by lazy { OrtEnvironment.getEnvironment() }
    private var resident: Loaded? = null

    @Synchronized
    fun close() {
        resident?.close()
        resident = null
    }

    private fun load(context: Context, alias: String): Loaded {
        val bundle = DecisionModelStore.find(context, alias) ?: error("No installed ONNX decision bundle for $alias")
        val path = bundle.graph.canonicalPath
        resident?.takeIf { it.path == path }?.let { return it }
        close()
        val memory = ActivityManager.MemoryInfo()
        (context.getSystemService(Context.ACTIVITY_SERVICE) as ActivityManager).getMemoryInfo(memory)
        // A large ONNX encoder graph needs weight buffers and activation memory.
        // Don't trigger low-memory eviction on phones with limited free RAM.
        require(memory.availMem > bundle.graph.length() + (512L shl 20)) {
            "Insufficient free RAM for ONNX decision weights"
        }
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
                return Loaded(path, config, calibration, session, tokenizer)
                    .also { resident = it }
            } catch (e: Exception) {
                session.close()
                throw e
            }
        } catch (e: Exception) {
            tokenizer.close()
            throw e
        }
    }

    /** Adverts only *loaded and validated* models, never a guessed HF filename. */
    @Synchronized
    fun available(context: Context, alias: String): Boolean = try {
        load(context, alias)
        true
    } catch (_: Exception) {
        false
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
