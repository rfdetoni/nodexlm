package dev.nodexlm.app

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import kotlin.math.abs

class LayaDecisionProtocolTest {
    private val config = LayaDecisionProtocol.Config(
        maxLen = 512, headMaxLen = 192, minMarkers = 1,
        cls = 50281, sep = 50282, mask = 50284, pad = 50283, maskText = "[MASK]"
    )

    @Test fun enforcesModelFamilyAndSequenceLimits() {
        val parsed = LayaDecisionProtocol.Config.parse(JSONObject("""
            {"engine":"onnx","layout":"laya-markers-v1","max_len":512,"head_max_len":192,
             "special_tokens":{"cls":50281,"sep":50282,"mask":50284,"pad":50283,"mask_text":"[MASK]"}}
        """.trimIndent()))
        assertEquals(config, parsed)
        assertThrows(IllegalArgumentException::class.java) {
            LayaDecisionProtocol.Config.parse(JSONObject("""
                {"engine":"onnx","layout":"gliclass-uni-v1","max_len":512,"head_max_len":192,
                 "special_tokens":{"cls":50281,"sep":50282,"mask":50284,"pad":50283,"mask_text":"[MASK]"}}
            """.trimIndent()))
        }
    }

    @Test fun encodesTypedOptionsAndMaskMarkers() {
        val questions = LayaDecisionProtocol.questions(JSONObject("""
            {"route":{"type":"choice","instructions":"Who handles this?","criteria":{
              "shipping":"delivery problems","billing":"money"}}, 
             "urgent":{"type":"noul","instructions":"Urgent?"}}
        """.trimIndent()))
        assertEquals(2, questions.size)
        val tokenizer: (String) -> LongArray = { text ->
            text.split(" ").filter { it.isNotEmpty() }
                .map { it.length.toLong() + 10 }.toLongArray()
        }
        val state = "My parcel arrived late"
        for (q in questions) {
            val encoded = LayaDecisionProtocol.encode(config, state, q, tokenizer)
            assertEquals(config.cls, encoded.ids.first())
            assertEquals(config.sep, encoded.ids.last())
            assertEquals(q.options.size, encoded.markers.size)
            encoded.markers.forEach { assertEquals(config.mask, encoded.ids[it.toInt()]) }
            assertEquals(4, encoded.stateTokens)
            assertTrue(encoded.ids.size <= 512)
        }
    }

    @Test fun calibrationClampsExtremeTemperatures() {
        val calibration = JSONObject("""
            {"temperature":[1.5,2.0,3.0],"temperature_by_options":
                {"choice:2":1.90636,"choice:11+":0.1005}}
        """.trimIndent())
        assertEquals(1.90636, LayaDecisionProtocol.temperature(calibration,"choice",2),1e-6)
        assertEquals(0.5, LayaDecisionProtocol.temperature(calibration,"choice",12),1e-6)
        assertEquals(3.0, LayaDecisionProtocol.temperature(calibration,"noul",2),1e-6)
    }

    @Test fun rendersTypeSafeProbabilitiesInsteadOfInventedScores() {
        val questions = LayaDecisionProtocol.questions(JSONObject("""
            {"choose":{"type":"choice","criteria":["a","b"],"instructions":"Pick"},
             "score":{"type":"score","criteria":["bad","okay","good"],"instructions":"Rate"},
             "yes":{"type":"noul","instructions":"Confirm"}}
        """.trimIndent()))
        val byType = questions.associateBy { it.type }
        val choice = LayaDecisionProtocol.answer(byType.getValue("choice"), doubleArrayOf(0.2, 0.8))
        assertEquals("b", choice.getString("choice"))
        assertEquals(0.6, choice.getDouble("confidence"), 1e-5)
        val score = LayaDecisionProtocol.answer(byType.getValue("score"), doubleArrayOf(0.1,0.2,0.7))
        assertEquals(1.6, score.getDouble("score"),1e-5)
        assertEquals("good", score.getJSONObject("legend").getString("2"))
        val noul = LayaDecisionProtocol.answer(byType.getValue("noul"),doubleArrayOf(0.15,0.85))
        assertEquals(0.85, noul.getDouble("noul"),1e-5)
        assertFalse(noul.has("confidence"))
        assertEquals("noul", noul.getString("type"))
    }

    @Test fun stableSoftmaxNeverReturnsInvalidProbabilities() {
        val result = LayaDecisionProtocol.probabilities(floatArrayOf(1100f,1101f),1.0)
        assertEquals(1.0,result.sum(),1e-12)
        assertTrue(result.all { it.isFinite() && it >= 0.0 })
        assertTrue(abs(result[1]-0.73105858) < 0.000001)
        assertThrows(IllegalArgumentException::class.java) {
            LayaDecisionProtocol.probabilities(floatArrayOf(Float.NaN,0f),1.0)
        }
    }

    @Test fun rejectsMalformedAndUnsafeQuestions() {
        assertThrows(IllegalArgumentException::class.java) {
            LayaDecisionProtocol.questions(JSONObject("""
                {"a":{"type":"choice","instructions":"bad","criteria":[]}}
            """.trimIndent()))
        }
        assertThrows(IllegalArgumentException::class.java) {
            LayaDecisionProtocol.questions(JSONObject("""
                {"a":{"type":"choice","ordinal":true,"instructions":"bad","criteria":["a","b"]}}
            """.trimIndent()))
        }
    }
}
