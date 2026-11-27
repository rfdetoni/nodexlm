// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.io.FileOutputStream
import java.net.InetAddress
import java.net.URL
import java.security.KeyFactory
import java.security.KeyPairGenerator
import java.security.MessageDigest
import java.security.PrivateKey
import java.security.SecureRandom
import java.security.Signature
import java.security.spec.ECGenParameterSpec
import java.security.spec.PKCS8EncodedKeySpec
import java.util.concurrent.ConcurrentHashMap
import javax.net.ssl.HttpsURLConnection

/**
 * Authenticated direct Guest <-> Guest GGUF cache client.
 * Queries the pinned Coordinator for eligible LAN peers holding the exact
 * SHA-256, then connects directly to the peer with Coordinator-attested TLS
 * certificate pinning, signed PeerGrant, and per-request ECDSA P-256 PoP.
 */
internal object PeerModelCache {
    private const val KEY_PRIV_FILE = "peer_identity_ec.pk8"
    private const val KEY_PUB_FILE = "peer_identity_ec.pub"
    private val quarantinedUntil = ConcurrentHashMap<String, Long>()
    private val random = SecureRandom()

    @Synchronized
    fun publicKeyHex(context: Context): String {
        val pubFile = File(context.filesDir, KEY_PUB_FILE)
        val privFile = File(context.filesDir, KEY_PRIV_FILE)
        if (pubFile.isFile && privFile.isFile) {
            val existing = runCatching { pubFile.readText().trim() }.getOrDefault("")
            if (existing.matches(Regex("[0-9a-f]{40,256}"))) return existing
        }
        val gen = KeyPairGenerator.getInstance("EC")
        gen.initialize(ECGenParameterSpec("secp256r1"), random)
        val pair = gen.generateKeyPair()
        val pubHex = pair.public.encoded.joinToString("") { "%02x".format(it.toInt() and 255) }
        context.openFileOutput(KEY_PRIV_FILE, Context.MODE_PRIVATE).use { it.write(pair.private.encoded) }
        context.openFileOutput(KEY_PUB_FILE, Context.MODE_PRIVATE).use { it.write(pubHex.toByteArray()) }
        return pubHex
    }

    private fun privateKey(context: Context): PrivateKey {
        publicKeyHex(context)
        val pkcs8 = File(context.filesDir, KEY_PRIV_FILE).readBytes()
        return KeyFactory.getInstance("EC").generatePrivate(PKCS8EncodedKeySpec(pkcs8))
    }

    internal fun signProof(priv: PrivateKey, method: String, path: String, grantNonce: String, nowMs: Long = System.currentTimeMillis()): String {
        val nonce = ByteArray(12)
        random.nextBytes(nonce)
        val nonceHex = nonce.joinToString("") { "%02x".format(it.toInt() and 255) }
        val payload = "$method\n$path\n$nowMs\n$nonceHex\n$grantNonce".toByteArray(Charsets.UTF_8)
        val sig = Signature.getInstance("SHA256withECDSA").apply {
            initSign(priv, random)
            update(payload)
        }.sign()
        val sigHex = sig.joinToString("") { "%02x".format(it.toInt() and 255) }
        return "$nowMs.$nonceHex.$sigHex"
    }

    internal fun isPrivatePeerAddress(address: String): Boolean {
        val colon = address.lastIndexOf(':')
        if (colon <= 0 || colon >= address.length - 1) return false
        val host = address.substring(0, colon).removePrefix("[").removeSuffix("]")
        val port = address.substring(colon + 1).toIntOrNull() ?: return false
        if (port !in 1..65535) return false
        // Must be IP literal, never a DNS hostname.
        val ipLiteral = (host.contains('.') && host.all { it.isDigit() || it == '.' }) ||
            (host.contains(':') && host.all { it.isDigit() || it == ':' || it in 'a'..'f' || it in 'A'..'F' })
        if (!ipLiteral) return false
        val addr = runCatching { InetAddress.getByName(host) }.getOrNull() ?: return false
        return !addr.isAnyLocalAddress && !addr.isMulticastAddress && !addr.isLinkLocalAddress &&
            !addr.isLoopbackAddress && addr.isSiteLocalAddress
    }

    fun fetch(
        context: Context,
        checksum: String,
        size: Long,
        temporary: File,
        cancelled: () -> Boolean,
        progress: (Long) -> Unit
    ): Boolean {
        if (!checksum.matches(Regex("[0-9a-f]{64}")) || size !in 8L..(150L shl 30) ||
            !HostModelCache.available(context)) return false
        val prefs = context.getSharedPreferences("nodexlm", Context.MODE_PRIVATE)
        val base = prefs.getString("coordinator", "").orEmpty().trimEnd('/')
        val nodeId = prefs.getString("node_id", "").orEmpty()
        val token = runCatching { NodeSecrets.read(context) }.getOrDefault("")
        val hostCert = runCatching { PinnedHostTls.certificate(context) }.getOrNull() ?: return false
        val priv = runCatching { privateKey(context) }.getOrNull() ?: return false

        val peers = queryPeers(base, hostCert, token, nodeId, checksum)
        if (peers.length() == 0) return false

        val now = System.currentTimeMillis()
        for (i in 0 until peers.length()) {
            if (cancelled()) return false
            val peer = peers.optJSONObject(i) ?: continue
            val peerId = peer.optString("node_id")
            val addr = peer.optString("address")
            val certSha = peer.optString("cert_sha256")
            val peerSize = peer.optLong("size", -1L)
            val grant = peer.optJSONObject("grant") ?: continue
            val grantNonce = grant.optString("nonce")
            if (peerId.isBlank() || peerSize != size || !isPrivatePeerAddress(addr) ||
                !certSha.matches(Regex("[0-9a-f]{64}")) || grantNonce.length != 32) continue
            if ((quarantinedUntil[peerId] ?: 0L) > now) continue

            val ok = try {
                fetchFromSinglePeer(addr, certSha, nodeId, priv, grant.toString(), grantNonce, checksum, size, temporary, cancelled, progress)
            } catch (_: Exception) {
                if (quarantinedUntil.size >= 256) {
                    val expiryCutoff = System.currentTimeMillis()
                    quarantinedUntil.entries.removeIf { it.value <= expiryCutoff }
                }
                quarantinedUntil[peerId] = System.currentTimeMillis() + 5 * 60_000L
                false
            }
            if (ok) return true
        }
        return false
    }

    private fun queryPeers(base: String, hostCert: ByteArray, token: String, nodeId: String, checksum: String): JSONArray {
        val conn = (URL("$base/v1/nodes/cache/peers/$checksum").openConnection() as? HttpsURLConnection) ?: return JSONArray()
        return try {
            conn.requestMethod = "GET"
            conn.instanceFollowRedirects = false
            conn.connectTimeout = 5_000
            conn.readTimeout = 10_000
            PinnedHostTls.configure(conn, hostCert)
            conn.setRequestProperty("Authorization", "Bearer $token")
            conn.setRequestProperty("X-Nodex-Node", nodeId)
            if (conn.responseCode != 200) return JSONArray()
            val body = conn.inputStream.bufferedReader().use { it.readText() }
            JSONObject(body).optJSONArray("peers") ?: JSONArray()
        } catch (_: Exception) {
            JSONArray()
        } finally {
            conn.disconnect()
        }
    }

    private fun fetchFromSinglePeer(
        addr: String, certSha: String, nodeId: String, priv: PrivateKey,
        grantJson: String, grantNonce: String, checksum: String, size: Long,
        temporary: File, cancelled: () -> Boolean, progress: (Long) -> Unit
    ): Boolean {
        val path = "/v1/peer/cache/$checksum"
        val url = "https://$addr$path"
        fun openPeer(method: String): HttpsURLConnection {
            val conn = URL(url).openConnection() as HttpsURLConnection
            conn.requestMethod = method
            conn.instanceFollowRedirects = false
            conn.connectTimeout = 5_000
            conn.readTimeout = 20_000
            PinnedHostTls.configureSha256(conn, certSha)
            conn.setRequestProperty("X-Nodex-Node", nodeId)
            conn.setRequestProperty("X-Nodex-Peer-Grant", grantJson)
            conn.setRequestProperty("X-Nodex-Peer-Proof", signProof(priv, method, path, grantNonce))
            conn.setRequestProperty("Accept-Encoding", "identity")
            return conn
        }
        val head = openPeer("HEAD")
        val accepted = try {
            head.responseCode == 200 && head.contentLengthLong == size &&
                head.getHeaderField("ETag") == "\"$checksum\""
        } finally {
            head.disconnect()
        }
        if (!accepted || cancelled()) return false
        if (size >= HostCacheRanges.MIN_PARALLEL_BYTES) {
            HostCacheRanges.transfer(temporary, checksum, size, cancelled, progress) { range ->
                val conn = openPeer("GET")
                try {
                    conn.setRequestProperty("Range", "bytes=${range.first}-${range.last}")
                    conn.setRequestProperty("If-Range", "\"$checksum\"")
                    val code = conn.responseCode
                    if (code == 200 || code == 416) throw HostCacheRanges.UnsupportedRange()
                    if (code != 206 ||
                        conn.getHeaderField("ETag") != "\"$checksum\"" ||
                        conn.getHeaderField("Content-Range") != "bytes ${range.first}-${range.last}/$size" ||
                        conn.contentLengthLong != range.length
                    ) throw java.io.IOException("Invalid peer byte range metadata")
                    object : java.io.FilterInputStream(conn.inputStream) {
                        override fun close() {
                            try { super.close() } finally { conn.disconnect() }
                        }
                    }
                } catch (e: Exception) {
                    conn.disconnect()
                    throw e
                }
            }
            return true
        }
        val conn = openPeer("GET")
        try {
            if (conn.responseCode != 200 || conn.contentLengthLong != size ||
                conn.getHeaderField("ETag") != "\"$checksum\"") return false
            val digest = MessageDigest.getInstance("SHA-256")
            var written = 0L
            FileOutputStream(temporary).use { out ->
                conn.inputStream.use { input ->
                    val buf = ByteArray(128 * 1024)
                    while (true) {
                        if (cancelled()) return false
                        val n = input.read(buf)
                        if (n < 0) break
                        written += n
                        if (written > size) return false
                        digest.update(buf, 0, n)
                        out.write(buf, 0, n)
                        progress(written)
                    }
                }
                out.fd.sync()
            }
            if (cancelled() || written != size) return false
            val actual = digest.digest().joinToString("") { "%02x".format(it.toInt() and 255) }
            return actual == checksum
        } finally {
            conn.disconnect()
        }
    }
}
