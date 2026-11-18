// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import org.json.JSONArray
import org.json.JSONObject
import java.util.Locale
import kotlin.math.exp
import kotlin.math.max

/**
 * Deterministic System One Laya layout and TypeSafe answer contract.
 * No hand-written tokenizer or fabricated scores: token IDs and raw logits
 * always come from the model/tokenizer shipped with the selected bundle.
 */
internal object LayaDecisionProtocol {
    data class Config(
        val maxLen: Int, val headMaxLen: Int, val minMarkers: Int,
        val cls: Long, val sep: Long, val mask: Long, val pad: Long, val maskText: String
    ) {
        companion object {
            fun parse(json: JSONObject): Config {
                require(json.getString("engine") == "onnx" &&
                    json.getString("layout") == "laya-markers-v1") { "Unsupported ONNX decision architecture" }
                val special = json.getJSONObject("special_tokens")
                val result = Config(
                    json.getInt("max_len"), json.getInt("head_max_len"),
                    json.optInt("min_markers", 1),
                    special.getLong("cls"), special.getLong("sep"),
                    special.getLong("mask"), special.getLong("pad"),
                    special.getString("mask_text"))
                require(result.maxLen in 64..2048 && result.headMaxLen in 16..1024 &&
                    result.minMarkers in 1..32 && result.maskText.isNotEmpty()) {
                    "Unsupported decision layout limits"
                }
                return result
            }
        }
    }

    data class Question(
        val id: String, val type: String, val instructions: String,
        val labels: List<String>, val options: List<String>, val scoreValues: List<Any?>
    ) {
        val typeIndex: Long get() = when(type) { "choice" -> 0L; "score" -> 1L; else -> 2L }
    }

    data class Encoded(val ids: LongArray, val markers: LongArray, val stateTokens: Int)

    fun questions(root: JSONObject): List<Question> {
        require(root.length() in 1..32) { "System One needs 1-32 questions" }
        val names = root.keys()
        val result = ArrayList<Question>()
        while (names.hasNext()) {
            val id = names.next()
            require(id.isNotEmpty() && id.length <= 128)
            val definition = root.getJSONObject(id)
            val kind = definition.getString("type")
            require(kind == "choice" || kind == "score" || kind == "noul") {
                "Unsupported System One question type"
            }
            require(!definition.optBoolean("ordinal", false)) { "Ordinal questions require another decision head" }
            val instructions = if (definition.has("instructions")) stringify(definition.get("instructions"))
                else id
            val criteria = definition.opt("criteria")
            val labels = ArrayList<String>()
            val options = ArrayList<String>()
            val values = ArrayList<Any?>()
            when (kind) {
                "choice" -> when (criteria) {
                    is JSONObject -> {
                        val keys = criteria.keys()
                        while (keys.hasNext()) {
                            val label = keys.next()
                            require(label.isNotEmpty() && label.length <= 128)
                            val value = criteria.get(label)
                            labels.add(label)
                            options.add(if (value == JSONObject.NULL || value == "") label
                                else "$label: ${stringify(value)}")
                        }
                    }
                    is JSONArray -> {
                        for (i in 0 until criteria.length()) {
                            val label = criteria.getString(i)
                            if (label !in labels) {
                                require(label.isNotEmpty() && label.length <= 128)
                                labels.add(label)
                                options.add(label)
                            }
                        }
                    }
                    else -> error("choice criteria must be a map or list")
                }
                "score" -> {
                    require(criteria is JSONArray) { "score criteria must be a list" }
                    for (i in 0 until criteria.length()) {
                        val v = criteria.get(i)
                        values.add(v)
                        labels.add(i.toString())
                        options.add("level $i: ${stringify(v)}")
                    }
                }
                "noul" -> {
                    require(criteria == null || criteria == JSONObject.NULL || criteria is JSONObject) {
                        "noul criteria must be a map"
                    }
                    val c = criteria as? JSONObject
                    fun description(key: String, fallback: String): String {
                        val keys = c?.keys() ?: return fallback
                        while (keys.hasNext()) {
                            val next = keys.next()
                            if (next.equals(key, true)) {
                                val value = c.get(next)
                                if (value != JSONObject.NULL && value != "") return stringify(value)
                            }
                        }
                        return fallback
                    }
                    labels.addAll(listOf("false", "true"))
                    options.add("false: " + description("false", "no, the statement does not hold"))
                    options.add("true: " + description("true", "yes, the statement holds"))
                }
            }
            require(labels.size in 2..32 && options.all { it.length <= 4096 }) {
                "Decision questions need between 2 and 32 bounded options"
            }
            result.add(Question(id, kind, instructions.take(16384), labels, options, values))
        }
        return result
    }

    // Python json.dumps default separators, with UTF-8 preserved, as Laya reads
    // non-string states. This does not implicitly add tokenizer special tokens.
    fun stringify(value: Any?): String = when (value) {
        null, JSONObject.NULL -> "null"
        is String -> value
        is JSONObject -> {
            val keys = value.keys()
            val pairs = ArrayList<String>()
            while (keys.hasNext()) {
                val key = keys.next()
                pairs.add(JSONObject.quote(key) + ": " + literal(value.get(key)))
            }
            "{"+pairs.joinToString(", ")+"}"
        }
        is JSONArray -> {
            val parts = (0 until value.length()).map { literal(value.get(it)) }
            "["+parts.joinToString(", ")+"]"
        }
        else -> value.toString()
    }

    private fun literal(value: Any?): String =
        if (value is String) JSONObject.quote(value) else stringify(value)

    fun encode(
        config: Config, state: String, question: Question, keepRecentState: Boolean = false,
        tokenize: (String) -> LongArray
    ): Encoded {
        val safeState = state.replace(config.maskText, " ")
        val stateIDs = tokenize(safeState)
        val head = tokenize("${question.type} question: " +
            question.instructions.replace(config.maskText, " ")).toMutableList()
        val options = question.options.map { option ->
            val items = ArrayList<Long>()
            items.add(config.mask)
            items.addAll(tokenize(" " + option.replace(config.maskText, " "))
                .take(48))
            items
        }.toMutableList()

        val optionTokens = options.sumOf { it.size }
        var budget = config.headMaxLen - optionTokens
        if (budget < 16) {
            val per = max(4, (config.headMaxLen - 16) / max(options.size, 1))
            for (i in options.indices) {
                options[i] = ArrayList(options[i].take(per))
            }
            budget = config.headMaxLen - options.sumOf { it.size }
        }
        while (head.size > max(8, budget)) head.removeAt(head.lastIndex)
        val ids = ArrayList<Long>(config.maxLen)
        ids.add(config.cls)
        ids.addAll(head)
        ids.add(config.sep)
        val markers = ArrayList<Long>(options.size)
        for (option in options) {
            markers.add(ids.size.toLong())
            ids.addAll(option)
        }
        ids.add(config.sep)
        require(ids.size + 1 <= config.maxLen && markers.all { it < config.maxLen }) {
            "Question exceeded the Laya sequence budget"
        }
        val stateRoom = (config.maxLen - ids.size - 1).coerceAtLeast(0)
        // Laya treats array states as conversations (latest messages last).
        // Non-array states retain the oldest prefix, exactly as the reference.
        ids.addAll(if (keepRecentState) stateIDs.takeLast(stateRoom) else stateIDs.take(stateRoom))
        ids.add(config.sep)
        return Encoded(ids.toLongArray(), markers.toLongArray(), stateIDs.size)
    }

    fun probabilities(logits: FloatArray, temperature: Double): DoubleArray {
        require(logits.isNotEmpty() && logits.all { it.isFinite() } && temperature.isFinite() &&
            temperature > 0.0) { "Invalid ONNX decision logits" }
        val z = logits.map { it.toDouble() / temperature }
        val maximum = z.maxOrNull()!!
        val values = z.map { exp(it - maximum) }
        val sum = values.sum()
        require(sum.isFinite() && sum > 0.0) { "Invalid ONNX probability mass" }
        return values.map { it / sum }.toDoubleArray()
    }

    fun temperature(calibration: JSONObject, kind: String, count: Int): Double {
        require(!calibration.has("temperature_map")) { "Input-dependent calibration is not supported" }
        val temperatures = calibration.getJSONArray("temperature")
        val index = when (kind) { "choice" -> 0; "score" -> 1; else -> 2 }
        val bucket = when {
            count <= 2 -> "2"
            count <= 5 -> "3-5"
            count <= 10 -> "6-10"
            else -> "11+"
        }
        val byOptions = calibration.optJSONObject("temperature_by_options")
        val value = byOptions?.optDouble("$kind:$bucket")?.takeIf { it.isFinite() }
            ?: temperatures.getDouble(index)
        val range = calibration.optJSONArray("temperature_range")
        val minValue = range?.optDouble(0, 0.5) ?: 0.5
        val maxValue = range?.optDouble(1, 5.0) ?: 5.0
        require(minValue > 0.0 && maxValue >= minValue && value.isFinite())
        return value.coerceIn(minValue, maxValue)
    }

    fun answer(q: Question, p: DoubleArray): JSONObject {
        require(p.size == q.labels.size && p.all { it.isFinite() })
        val maxIndex = p.indices.maxByOrNull { p[it] }!!
        val confidence = ((p.size * p[maxIndex] - 1.0) / (p.size - 1)).coerceIn(0.0, 1.0)
        val probabilities = JSONObject()
        for (i in p.indices) probabilities.put(q.labels[i], round4(p[i]))
        return when (q.type) {
            "choice" -> JSONObject().put("type", "choice").put("choice", q.labels[maxIndex])
                .put("confidence", round4(confidence)).put("probabilities", probabilities)
            "score" -> {
                val legend = JSONObject()
                q.scoreValues.forEachIndexed { i, value -> legend.put(i.toString(), value) }
                JSONObject().put("type", "score").put("score", round4(p.indices.sumOf { it * p[it] }))
                    .put("confidence", round4(confidence)).put("legend", legend)
                    .put("probabilities", probabilities)
            }
            else -> JSONObject().put("type", "noul").put("noul", round4(p[1]))
        }
    }

    private fun round4(value: Double): Double = String.format(Locale.US, "%.4f", value).toDouble()
}
