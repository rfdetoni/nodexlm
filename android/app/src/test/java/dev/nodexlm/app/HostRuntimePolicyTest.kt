package dev.nodexlm.app

import org.bouncycastle.crypto.params.Ed25519PrivateKeyParameters
import org.bouncycastle.crypto.signers.Ed25519Signer
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Base64

class HostRuntimePolicyTest {
    private fun hex(b: ByteArray) = b.joinToString("") { "%02x".format(it.toInt() and 255) }

    @Test fun verifiesOriginalBytesAndRejectsTampering() {
        val key = Ed25519PrivateKeyParameters(ByteArray(32) { it.toByte() }, 0)
        val payload = ("nodexlm/runtime-policy/v1\u0000" +
            """{"cluster_id":"cluster","epoch":2,"node_id":"guest","issued_at":100,"expires_at":220}""").toByteArray()
        val signer = Ed25519Signer()
        signer.init(true, key)
        signer.update(payload, 0, payload.size)
        val envelope = JSONObject().put("payload", Base64.getEncoder().encodeToString(payload))
            .put("signature", hex(signer.generateSignature()))
        val authority = hex(key.generatePublicKey().encoded)
        assertEquals("guest", HostRuntimePolicy.verifySigned(envelope, authority, "cluster", 2, "guest", 150).getString("node_id"))
        assertTrue(runCatching { HostRuntimePolicy.verifySigned(envelope, authority, "cluster", 2, "other", 150) }.isFailure)
        assertTrue(runCatching { HostRuntimePolicy.verifySigned(envelope, authority, "cluster", 2, "guest", 220) }.isFailure)
        payload[payload.lastIndex] = 32
        envelope.put("payload", Base64.getEncoder().encodeToString(payload))
        assertTrue(runCatching { HostRuntimePolicy.verifySigned(envelope, authority, "cluster", 2, "guest", 150) }.isFailure)
    }
}
