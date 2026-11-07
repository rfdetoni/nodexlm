// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.app.ActivityManager
import android.content.Context
import android.net.Uri
import android.os.Build
import org.json.JSONArray
import java.io.ByteArrayOutputStream
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.security.KeyStore
import java.security.cert.CertificateFactory
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManagerFactory

/** Small Android Hugging Face Hub API client. No Python runtime or browser.
 * It shares the model contract with the Go internal/catalog Hub client.
 * Only public single-file GGUF weights are listed; estimates are advisory.
 */
internal class HuggingFaceHub(private val context: Context) {
    data class Model(
        val repository: String,
        val filename: String,
        val sizeBytes: Long,
        val url: String,
        val placement: String,
        val reason: String
    )

    private fun getJson(url: String, auth: Boolean = false): String {
        require(url.startsWith("https://"))
        val conn = URL(url).openConnection() as HttpsURLConnection
        conn.connectTimeout = 8_000
        conn.readTimeout = 18_000
        conn.setRequestProperty("Accept", "application/json")
        if (auth) {
            val prefs = context.getSharedPreferences("nodexlm", Context.MODE_PRIVATE)
            val token = NodeSecrets.read(context).ifBlank { error("Device not paired") }
            conn.setRequestProperty("Authorization", "Bearer $token")
            conn.setRequestProperty("X-Nodex-Node", prefs.getString("node_id", "") ?: "")
            PinnedHostTls.configure(conn, context)
        }
        try {
            if (conn.responseCode != 200) error("HTTP ${conn.responseCode}")
            val output = ByteArrayOutputStream()
            conn.inputStream.use { stream ->
                val buffer = ByteArray(8192)
                while (true) {
                    val n = stream.read(buffer)
                    if (n < 0) break
                    require(output.size() + n <= 3 * 1024 * 1024) { "Catalog response too large" }
                    output.write(buffer, 0, n)
                }
            }
            return output.toString("UTF-8")
        } finally { conn.disconnect() }
    }

    fun search(query: String): List<Model> {
        val q = query.trim()
        require(q.length in 2..64) { "Search needs 2–64 characters" }
        val prefs = context.getSharedPreferences("nodexlm", Context.MODE_PRIVATE)
        val host = prefs.getString("coordinator", "")?.trimEnd('/') ?: ""
        if (host.startsWith("https://") && runCatching { NodeSecrets.read(context) }.getOrDefault("").isNotEmpty()) {
            // Pairing's TLS credential authenticates this request, not the admin API.
            // The Host only includes ready RPC nodes in its memory estimate.
            try {
                val result = JSONArray(getJson("$host/v1/nodes/catalog?q=${Uri.encode(q)}", auth = true))
                return (0 until result.length()).mapNotNull { index ->
                    val row = result.optJSONObject(index) ?: return@mapNotNull null
                    val size = row.optLong("size_bytes", 0)
                    if (size <= 0) return@mapNotNull null
                    Model(row.optString("repository"), row.optString("filename"), size,
                        row.optString("url"), row.optString("placement"), row.optString("reason"))
                }
            } catch (_: Exception) { /* Host unavailable; fall back to local estimates. */ }
        }
        val results = mutableListOf<Model>()
        val queryUrl = "https://huggingface.co/api/models?search=${Uri.encode(q)}&filter=gguf&sort=downloads&direction=-1&limit=8"
        val repositories = JSONArray(getJson(queryUrl))
        val budget = localBudgetBytes()
        for (index in 0 until repositories.length()) {
            if (results.size >= 20) break
            val entry = repositories.optJSONObject(index) ?: continue
            if (entry.opt("gated") != null && entry.opt("gated") != false) continue
            val repository = entry.optString("id")
            val parts = repository.split('/')
            if (parts.size != 2 || parts.any { !safeSegment(it) }) continue
            try {
                val tree = JSONArray(getJson("https://huggingface.co/api/models/${Uri.encode(parts[0])}/${Uri.encode(parts[1])}/tree/main?recursive=true&limit=1000"))
                var found = 0
                for (j in 0 until tree.length()) {
                    if (results.size >= 20 || found >= 3) break
                    val file = tree.optJSONObject(j) ?: continue
                    if (file.optString("type") != "file") continue
                    val filename = file.optString("path")
                    val size = file.optLong("size", 0L)
                    if (!filename.endsWith(".gguf", ignoreCase = true) || size <= 0 || filename.contains("-of-", true) || filename.contains("mmproj",true)) continue
                    val segments = filename.split('/')
                    if (segments.size > 20 || segments.any { !safeSegment(it) }) continue
                    val url = "https://huggingface.co/${Uri.encode(parts[0])}/${Uri.encode(parts[1])}/resolve/main/" +
                        segments.joinToString("/") { Uri.encode(it) }
                    val fit = size <= budget && size <= Long.MAX_VALUE / 115 && size * 115 / 100 <= budget
                    results.add(Model(repository,filename,size,url,
                        if (fit) "single-node-candidate" else "unknown",
                        if (fit) "Estimated fit on this phone" else "Not confirmed for this phone; pair with the Host to check cluster capacity"))
                    found++
                }
            } catch (_: Exception) { /* Inaccessible repo is not an application error. */ }
        }
        return results.sortedWith(compareBy<Model> { it.placement != "single-node-candidate" }.thenBy { it.sizeBytes })
    }

    fun localBudgetBytes(): Long {
        val info = ActivityManager.MemoryInfo()
        (context.getSystemService(Context.ACTIVITY_SERVICE) as ActivityManager).getMemoryInfo(info)
        val headroom = 1L shl 30
        val available = (info.availMem - headroom).coerceAtLeast(0)
        // A 32-bit userspace cannot map multi-GiB GGUF files reliably.
        val limit = if (Build.SUPPORTED_64_BIT_ABIS.isEmpty()) 896L shl 20 else Long.MAX_VALUE
        return (available * 80 / 100).coerceAtMost(limit)
    }

    private fun safeSegment(s: String): Boolean = s.isNotBlank() && s != "." && s != ".." && s.length < 160 &&
        s.none { it == '/' || it == '\\' || it == ':' || it == '?' || it == '#' || it == '%' || it.code < 32 }
}
