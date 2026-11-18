// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.net.HttpURLConnection
import java.net.Proxy
import java.net.URL

/**
 * On-device System One endpoint adapter. This is a local process on the
 * Android phone (for example, a separately installed Android-compatible
 * decision runtime), not a proxy to a remote server or a fake score generator.
 * Absence of the runtime means the device advertises no decision capability.
 */
internal object MobileDecisionClient {
    const val DEFAULT_PORT = 11435
    private const val MAX_BODY = 512 * 1024

    private fun connect(port: Int, path: String, method: String): HttpURLConnection {
        require(port in 1024..65535) { "Invalid local decision port" }
        val connection = URL("http://127.0.0.1:$port$path")
            .openConnection(Proxy.NO_PROXY) as HttpURLConnection
        connection.instanceFollowRedirects = false
        connection.requestMethod = method
        connection.connectTimeout = 2_000
        connection.readTimeout = if (method == "GET") 3_000 else 60_000
        connection.setRequestProperty("Accept", "application/json")
        return connection
    }

    private fun bounded(response: HttpURLConnection): JSONObject {
        check(response.responseCode == 200) { "Local decision runtime HTTP ${response.responseCode}" }
        val output = ByteArrayOutputStream()
        response.inputStream.use { stream ->
            val buffer = ByteArray(8192)
            while (true) {
                val count = stream.read(buffer)
                if (count < 0) break
                require(output.size() + count <= MAX_BODY) { "Decision response too large" }
                output.write(buffer, 0, count)
            }
        }
        return JSONObject(output.toString(Charsets.UTF_8.name()))
    }

    fun available(port: Int, model: String, apiKey: String = ""): Boolean {
        if (model.isBlank() || model.length > 256) return false
        return try {
            val connection = connect(port, "/v1/models", "GET")
            if (apiKey.isNotEmpty()) connection.setRequestProperty("Authorization", "Bearer $apiKey")
            try {
                val body = bounded(connection)
                val name = model.removePrefix("ollaya/")
                val models = body.optJSONArray("models") ?: body.optJSONArray("data") ?: return false
                (0 until models.length()).any { index ->
                    val entry = models.optJSONObject(index)
                    entry?.optString("name") == name || entry?.optString("id") == name
                }
            } finally {
                connection.disconnect()
            }
        } catch (_: Exception) {
            false
        }
    }

    fun decide(port: Int, model: String, request: JSONObject, apiKey: String = ""): JSONObject {
        require(model.isNotBlank() && model.length <= 256)
        val input = JSONObject(request.toString()).put("model", model.removePrefix("ollaya/"))
        val payload = input.toString().toByteArray(Charsets.UTF_8)
        require(payload.size in 1..MAX_BODY) { "Decision request too large" }
        val connection = connect(port, "/v1/systemone", "POST")
        connection.setRequestProperty("Content-Type", "application/json")
        if (apiKey.isNotEmpty()) connection.setRequestProperty("Authorization", "Bearer $apiKey")
        connection.doOutput = true
        return try {
            connection.outputStream.use { it.write(payload) }
            bounded(connection)
        } finally {
            connection.disconnect()
        }
    }
}
