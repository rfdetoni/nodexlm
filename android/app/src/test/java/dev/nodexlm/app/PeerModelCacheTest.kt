// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.security.KeyPairGenerator
import java.security.Signature
import java.security.spec.ECGenParameterSpec

class PeerModelCacheTest {
    @Test
    fun rejectsUnsafeOrNonPrivatePeerAddresses() {
        assertTrue(PeerModelCache.isPrivatePeerAddress("192.168.1.42:9443"))
        assertTrue(PeerModelCache.isPrivatePeerAddress("10.0.0.5:8443"))
        assertFalse(PeerModelCache.isPrivatePeerAddress("127.0.0.1:9443"))
        assertFalse(PeerModelCache.isPrivatePeerAddress("169.254.169.254:80"))
        assertFalse(PeerModelCache.isPrivatePeerAddress("8.8.8.8:443"))
        assertFalse(PeerModelCache.isPrivatePeerAddress("evil.local:8443"))
        assertFalse(PeerModelCache.isPrivatePeerAddress("cafe:8443"))
        assertFalse(PeerModelCache.isPrivatePeerAddress("192.168.1.1:0"))
    }

    @Test
    fun signsVerifiableEcdsaPeerRequestProof() {
        val gen = KeyPairGenerator.getInstance("EC")
        gen.initialize(ECGenParameterSpec("secp256r1"))
        val pair = gen.generateKeyPair()
        val grantNonce = "a".repeat(32)
        val path = "/v1/peer/cache/" + "b".repeat(64)
        val nowMs = 1700000000000L
        val proof = PeerModelCache.signProof(pair.private, "GET", path, grantNonce, nowMs)
        val parts = proof.split(".")
        assertEquals(3, parts.size)
        assertEquals(nowMs.toString(), parts[0])
        assertEquals(24, parts[1].length)
        val sigBytes = parts[2].chunked(2).map { it.toInt(16).toByte() }.toByteArray()
        val verifier = Signature.getInstance("SHA256withECDSA")
        verifier.initVerify(pair.public)
        verifier.update("GET\n$path\n${parts[0]}\n${parts[1]}\n$grantNonce".toByteArray(Charsets.UTF_8))
        assertTrue(verifier.verify(sigBytes))
    }
}
