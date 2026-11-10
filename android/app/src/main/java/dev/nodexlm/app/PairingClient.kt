// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.content.Context
import android.app.ActivityManager
import android.os.Build
import android.util.Log
import android.net.wifi.WifiManager
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.NetworkInterface
import java.net.URL
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.util.Base64
import javax.crypto.Mac
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.PBEKeySpec
import javax.crypto.spec.SecretKeySpec
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocket
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

/** Local-only enrollment. No secret is sent before pinning host TLS identity. */
internal class PairingClient(private val context: Context) {
    companion object {
        private const val DISCOVERY_PORT = 46465
        private const val DISCOVERY_MESSAGE = "NODEXLM_DISCOVER_V1"
        private const val ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
    }

    fun join(inputCode: String, inputEndpoint: String, name: String, modelName: String): String {
        val code = inputCode.uppercase().replace("-", "").replace(" ", "").trim()
        require(code.length == 8 && code.all { ALPHABET.contains(it) }) { "Enter an 8-character alphanumeric host code" }
        val endpoints = if (inputEndpoint.isBlank()) discover() else listOf(inputEndpoint.trimEnd('/'))
        if (endpoints.isEmpty()) error("No host found; ensure both devices are on the same Wi-Fi or enter the host's HTTPS URL")
        // An explicitly imported or previously enrolled certificate remains an
        // additional trust constraint. A new Host requires a deliberate import.
        val trustedFile = File(context.filesDir, "coordinator.crt")
        val trusted = if (trustedFile.exists()) trustedFile.inputStream().use {
            java.security.cert.CertificateFactory.getInstance("X.509")
                .generateCertificate(boundedPairingBytes(it, 65536).inputStream()).encoded
        } else null
        val matches = mutableListOf<Pair<String, ByteArray>>()
        val failures = mutableListOf<String>()
        for (endpoint in endpoints) {
            var stage = "URL validation"
            try {
                val remote = URL(endpoint)
                if (remote.protocol != "https" || remote.userInfo != null || remote.query != null || remote.ref != null) continue
                stage = "TLS handshake"
                val certDER = fetchCertificate(remote)
                stage = "Host certificate identity"
                if (trusted != null && !MessageDigest.isEqual(trusted, certDER)) {
                    val reason = context.getString(R.string.pair_host_certificate_changed)
                    failures.add(reason)
                    Log.w("NodexLM-Pairing", reason)
                    continue
                }
                stage = "pairing offer"
                val offer = requestOffer(endpoint, certDER)
                if (offer.getString("endpoint") != endpoint) continue
                val expires = java.time.Instant.parse(offer.getString("expires"))
                val now = java.time.Instant.now()
                if (expires.isBefore(now) || expires.isAfter(now.plusSeconds(31 * 60L))) {
                    failures.add(context.getString(R.string.pair_clock_or_code_expired))
                    continue
                }
                val salt = Base64.getUrlDecoder().decode(offer.getString("salt"))
                val nonce = Base64.getUrlDecoder().decode(offer.getString("nonce"))
                if (salt.size != 16 || nonce.size != 16) continue
                val proof = hexToBytes(offer.getString("proof"))
                if (proof.size != 32) continue
                stage = "pairing proof (PBKDF2)"
                val key = SecretKeyFactory.getInstance("PBKDF2WithHmacSHA256").generateSecret(
                    PBEKeySpec(code.toCharArray(), salt, 200_000, 256)
                ).encoded
                val mac = Mac.getInstance("HmacSHA256")
                mac.init(SecretKeySpec(key, "HmacSHA256"))
                val expected = mac.doFinal(
                    "nodexlm-pairing-v1\u0000".toByteArray(Charsets.UTF_8) +
                        MessageDigest.getInstance("SHA-256").digest(certDER) + nonce
                )
                key.fill(0)
                if (MessageDigest.isEqual(proof, expected)) {
                    if (matches.none { MessageDigest.isEqual(it.second, certDER) }) matches.add(Pair(endpoint, certDER))
                } else {
                    failures.add(context.getString(R.string.pair_code_mismatch))
                }
            } catch (e: Exception) {
                // No codes, tokens or certificate contents are written to logs.
                Log.w("NodexLM-Pairing", "Candidate rejected during $stage: ${e.javaClass.simpleName}", e)
                failures.add("$stage: ${e.message?.take(160) ?: e.javaClass.simpleName}")
            }
        }
        if (matches.size != 1) {
            if (matches.isEmpty() && inputEndpoint.isNotBlank() && failures.isNotEmpty()) {
                error(failures.first())
            }
            error(if (matches.isEmpty()) "No host matches that code; use the Host HTTPS URL to diagnose" else "Multiple matching hosts; enter the host URL")
        }
        val (endpoint, certificate) = matches.single()
        val memory = ActivityManager.MemoryInfo()
        (context.getSystemService(Context.ACTIVITY_SERVICE) as ActivityManager).getMemoryInfo(memory)
        val device = JSONObject().put("ram_bytes",memory.totalMem)
            .put("available_ram_bytes",memory.availMem)
            .put("runtime",JSONObject().put("engine","ggml-rpc-cpu").put("installed",false).put("rpc_ready",false))
            .put("name", name.take(80).ifBlank { Build.MODEL })
            .put("platform", "android")
            .put("architecture", Build.SUPPORTED_ABIS.firstOrNull() ?: "arm64-v8a")
            .put("cpu_count", Runtime.getRuntime().availableProcessors())
            .put("models", JSONArray().apply { if (modelName.isNotBlank()) put(modelName) })
        val payload = JSONObject().put("code", code).put("device", device)
        val c = openPinnedConnection(endpoint + "/v1/pairing/enroll", certificate)
        val response = try {
            c.requestMethod = "POST"
            c.connectTimeout = 7_000
            c.readTimeout = 10_000
            c.doOutput = true
            c.setRequestProperty("Content-Type", "application/json")
            c.outputStream.use { it.write(payload.toString().toByteArray(Charsets.UTF_8)) }
            if (c.responseCode != 201) error("Host refused enrollment (HTTP ${c.responseCode})")
            JSONObject(c.inputStream.use { boundedPairingBytes(it).toString(Charsets.UTF_8) })
        } finally { c.disconnect() }
        val nodeID = response.getString("node_id")
        val token = response.getString("token")
        require(nodeID.length >= 32 && token.length >= 32) { "Host returned invalid node credentials" }
        File(context.filesDir, "coordinator.crt").writeBytes(certificate)
        val saved = context.getSharedPreferences("nodexlm", Context.MODE_PRIVATE).edit()
            .putString("coordinator", endpoint).putString("node_id", nodeID)
            .putString("node_token_encrypted", NodeSecrets.encrypt(token)).remove("node_token")
            .putString("name", name).remove("join")
            .commit()
        if (!saved) error("Could not persist pairing credentials")
        return endpoint
    }

    private fun discover(): List<String> {
        val endpoints = linkedSetOf<String>()
        // Some older Android Wi-Fi stacks suppress local multicast discovery.
        // Request only a short-lived lock, and still allow manual HTTPS Host URL.
        val wifi = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as? WifiManager
        val lock = wifi?.createMulticastLock("NodexLM:pairing").apply {
            this?.setReferenceCounted(false)
        }
        try {
            try { lock?.acquire() } catch (e: Exception) {
                Log.w("NodexLM-Pairing", "Wi-Fi multicast lock unavailable", e)
            }
            DatagramSocket().use { socket ->
            socket.broadcast = true
            socket.soTimeout = 250
            val bytes = DISCOVERY_MESSAGE.toByteArray(Charsets.US_ASCII)
            val destinations = linkedSetOf(InetAddress.getByName("127.0.0.1"), InetAddress.getByName("255.255.255.255"))
            val interfaces = NetworkInterface.getNetworkInterfaces()
            while (interfaces.hasMoreElements()) {
                val iface = interfaces.nextElement()
                if (!iface.isUp || iface.isLoopback) continue
                for (addr in iface.interfaceAddresses) addr.broadcast?.let { destinations.add(it) }
            }
            for (ip in destinations) try { socket.send(DatagramPacket(bytes, bytes.size, ip, DISCOVERY_PORT)) } catch (_: Exception) {}
            val deadline = System.currentTimeMillis() + 3000L
            val buf = ByteArray(1500)
            while (System.currentTimeMillis() < deadline && endpoints.size < 20) {
                val packet = DatagramPacket(buf, buf.size)
                try {
                    socket.receive(packet)
                    val offer = JSONObject(String(buf, 0, packet.length, Charsets.UTF_8))
                    val url = URL(offer.getString("endpoint"))
                    if (url.protocol == "https" && url.port > 0 && url.host.isNotBlank()) endpoints.add(url.toString())
                } catch (_: Exception) { /* Continue listening until the deadline. */ }
            }
            }
        } finally {
            try { if (lock?.isHeld == true) lock.release() } catch (_: Exception) {}
        }
        return endpoints.toList()
    }

    private fun fetchCertificate(url: URL): ByteArray {
        // This socket only reads the public certificate: it sends no HTTP request,
        // code or credential. The certificate is authenticated against the code
        // proof before opening a new connection capable of sending secrets.
        val untrusted = object : X509TrustManager {
            override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) = Unit
            override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) = Unit
            override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
        }
        val tls = SSLContext.getInstance("TLS")
        tls.init(null, arrayOf<TrustManager>(untrusted), SecureRandom())
        val address = InetAddress.getByName(url.host)
        require(address.isSiteLocalAddress || address.isLoopbackAddress) { "Pairing requires a private LAN address" }
        val raw = java.net.Socket()
        raw.connect(java.net.InetSocketAddress(address, if (url.port > 0) url.port else 443), 5000)
        val socket = try {
            tls.socketFactory.createSocket(raw, url.host, if (url.port > 0) url.port else 443, true) as SSLSocket
        } catch (e: Exception) {
            raw.close()
            throw e
        }
        return socket.use { s ->
            s.soTimeout = 6000
            s.startHandshake()
            (s.session.peerCertificates.first() as X509Certificate).encoded
        }
    }

    private fun openPinnedConnection(url: String, certDER: ByteArray): HttpsURLConnection =
        (URL(url).openConnection() as HttpsURLConnection).apply {
            PinnedHostTls.configure(this, certDER)
        }

    private fun requestOffer(endpoint: String, certDER: ByteArray): JSONObject {
        val c = openPinnedConnection(endpoint + "/v1/pairing/offer", certDER)
        return try {
            c.connectTimeout = 5000
            c.readTimeout = 5000
            if (c.responseCode != 200) error("Pairing session unavailable")
            JSONObject(c.inputStream.use { boundedPairingBytes(it).toString(Charsets.UTF_8) })
        } finally { c.disconnect() }
    }

    private fun hexToBytes(hex: String): ByteArray {
        if (hex.length % 2 != 0) return byteArrayOf()
        return try { ByteArray(hex.length / 2) { hex.substring(it * 2, it * 2 + 2).toInt(16).toByte() } }
        catch (_: Exception) { byteArrayOf() }
    }
}
