// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.content.Context
import android.os.Build
import org.bouncycastle.crypto.params.Ed25519PublicKeyParameters
import org.bouncycastle.crypto.signers.Ed25519Signer
import org.json.JSONObject
import java.io.InputStream
import java.net.URL
import java.util.Base64
import javax.net.ssl.HttpsURLConnection

internal object HostRuntimePolicy {
    private val prefix = "nodexlm/runtime-policy/v1\u0000".toByteArray(Charsets.UTF_8)
    private val hex64 = Regex("[0-9a-f]{64}")
    private val hex40 = Regex("[0-9a-f]{40}")
    private val hex128 = Regex("[0-9a-f]{128}")

    fun readBounded(input: InputStream): String {
        val output = java.io.ByteArrayOutputStream()
        val buf = ByteArray(4096)
        while (true) {
            val n = input.read(buf)
            if (n < 0) break
            require(output.size() + n <= 131072) { "Oversized Host response" }
            output.write(buf, 0, n)
        }
        return output.toString(Charsets.UTF_8.name())
    }

    private fun hex(s: String): ByteArray =
        ByteArray(s.length / 2) { s.substring(it * 2, it * 2 + 2).toInt(16).toByte() }

    // Consume the original bytes signed by Go rather than reserializing JSON.
    internal fun verifySigned(envelope: JSONObject, authority: String, cluster: String,
                              epoch: Long, node: String, now: Long): JSONObject {
        require(hex64.matches(authority) && cluster.isNotBlank() && epoch > 0)
        val signature = envelope.getString("signature")
        require(hex128.matches(signature))
        val encoded = envelope.getString("payload")
        require(encoded.length in 1..32768)
        val payload = Base64.getDecoder().decode(encoded)
        require(payload.size > prefix.size && payload.copyOfRange(0, prefix.size).contentEquals(prefix))
        val verifier = Ed25519Signer()
        verifier.init(false, Ed25519PublicKeyParameters(hex(authority), 0))
        verifier.update(payload, 0, payload.size)
        require(verifier.verifySignature(hex(signature))) { "Invalid Host runtime signature" }
        val p = JSONObject(String(payload, prefix.size, payload.size - prefix.size, Charsets.UTF_8))
        require(p.getString("cluster_id") == cluster && p.getLong("epoch") == epoch &&
            p.getString("node_id") == node) { "Runtime policy pairing mismatch" }
        val issued = p.getLong("issued_at")
        val expires = p.getLong("expires_at")
        require(issued > 0 && issued <= now + 30 && expires > now &&
            expires > issued && expires - issued <= 300) { "Expired runtime policy" }
        return p
    }

    // Call only after the enrolled TLS certificate and node credentials
    // have authenticated the response. Never accept a replacement authority.
    @Synchronized
    fun verifyResponse(context: Context, node: String, response: JSONObject) {
        try {
            val required = response.optString("required_build_commit")
            val envelope = response.optJSONObject("runtime_policy")
            if (envelope == null) {
                require(required.isEmpty() && BuildConfig.NODEX_BUILD_COMMIT.isEmpty()) {
                    "Release Host runtime policy missing"
                }
                return
            }
            require(hex40.matches(required))
            val prefs = context.getSharedPreferences("nodexlm", Context.MODE_PRIVATE)
            require(prefs.getString("node_id", "") == node)
            val storedKey = prefs.getString("runtime_authority_key", "").orEmpty()
            val storedCluster = prefs.getString("runtime_cluster_id", "").orEmpty()
            val storedEpoch = prefs.getLong("runtime_cluster_epoch", 0L)
            val bootstrap = storedKey.isEmpty() && storedCluster.isEmpty() && storedEpoch == 0L
            val key = if (bootstrap) response.getString("authority_pubkey") else storedKey
            val cluster = if (bootstrap) response.getString("cluster_id") else storedCluster
            val epoch = if (bootstrap) response.getLong("epoch") else storedEpoch
            val p = verifySigned(envelope, key, cluster, epoch, node, System.currentTimeMillis() / 1000L)
            require(p.getString("host_build_commit") == required)
            val needsUpdate = required != BuildConfig.NODEX_BUILD_COMMIT
            require(response.optBoolean("update_required", false) == needsUpdate)
            if (!needsUpdate) {
                require(p.getString("engine") == "llama.cpp" &&
                    p.getString("source_commit") == BuildConfig.LLAMA_SOURCE_COMMIT &&
                    p.getString("release") == BuildConfig.LLAMA_RPC_RELEASE &&
                    p.getInt("rpc_major") == BuildConfig.LLAMA_RPC_MAJOR &&
                    p.getInt("rpc_minor") == BuildConfig.LLAMA_RPC_MINOR &&
                    p.getInt("rpc_patch") == BuildConfig.LLAMA_RPC_PATCH) { "Incompatible native runtime policy" }
            }
            if (bootstrap) {
                check(prefs.edit().putString("runtime_authority_key", key)
                    .putString("runtime_cluster_id", cluster).putLong("runtime_cluster_epoch", epoch).commit())
            }
        } catch (e: Exception) {
            throw SecurityException("Host runtime policy validation failed", e)
        }
    }

    fun preflight(context: Context, endpoint: String, node: String, token: String,
                  name: String, cert: ByteArray): JSONObject {
        val device = JSONObject().put("name", name.take(80)).put("platform", "android")
            .put("architecture", Build.SUPPORTED_ABIS.firstOrNull() ?: "arm64-v8a")
            .put("cpu_count", Runtime.getRuntime().availableProcessors())
            .put("build_commit", BuildConfig.NODEX_BUILD_COMMIT)
            .put("app_version", BuildConfig.VERSION_NAME)
        val c = URL(endpoint.trimEnd('/') + "/v1/nodes/heartbeat").openConnection() as HttpsURLConnection
        try {
            PinnedHostTls.configure(c, cert)
            c.instanceFollowRedirects = false
            c.requestMethod = "POST"
            c.connectTimeout = 7000
            c.readTimeout = 7000
            c.doOutput = true
            c.setRequestProperty("Content-Type", "application/json")
            c.setRequestProperty("Authorization", "Bearer $token")
            c.setRequestProperty("X-Nodex-Node", node)
            c.outputStream.use { it.write(device.toString().toByteArray(Charsets.UTF_8)) }
            check(c.responseCode == 200) { "Host preflight HTTP ${c.responseCode}" }
            val response = JSONObject(c.inputStream.use(::readBounded))
            verifyResponse(context, node, response)
            return response
        } finally {
            c.disconnect()
        }
    }
}
