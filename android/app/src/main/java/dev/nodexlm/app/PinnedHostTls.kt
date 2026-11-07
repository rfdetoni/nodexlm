// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.content.Context
import java.io.File
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSession
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

/**
 * All paired Android connections pin the EXACT certificate authenticated
 * during the pairing proof. This intentionally does not rely on DNS SAN or
 * CA expiry, because LAN IPs change; replacing the certificate requires a
 * fresh authenticated pairing. Never use for arbitrary HTTPS destinations.
 */
internal object PinnedHostTls {
    fun certificate(context: Context): ByteArray =
        File(context.filesDir, "coordinator.crt").readBytes().also {
            require(it.size in 1..65536) { "Invalid enrolled Host certificate" }
        }

    private fun matches(expected: ByteArray, peer: X509Certificate): Boolean =
        MessageDigest.isEqual(expected, MessageDigest.getInstance("SHA-256").digest(peer.encoded))

    fun context(certDER: ByteArray): SSLContext {
        val pin = MessageDigest.getInstance("SHA-256").digest(certDER)
        val trust = object : X509TrustManager {
            override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) {
                throw CertificateException("Client certificates are unsupported")
            }

            override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
                if (chain.isEmpty() || !matches(pin, chain[0])) {
                    throw CertificateException("Host certificate pin mismatch")
                }
            }

            override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
        }
        return SSLContext.getInstance("TLS").apply {
            init(null, arrayOf<TrustManager>(trust), SecureRandom())
        }
    }

    fun verify(session: SSLSession, certDER: ByteArray): Boolean = try {
        val expected = MessageDigest.getInstance("SHA-256").digest(certDER)
        matches(expected, session.peerCertificates.first() as X509Certificate)
    } catch (_: Exception) {
        false
    }

    fun configure(connection: HttpsURLConnection, certDER: ByteArray) {
        connection.sslSocketFactory = context(certDER).socketFactory
        // The same previously authenticated pin is checked during handshake
        // and hostname verification. A DHCP change does not invalidate it.
        connection.hostnameVerifier = javax.net.ssl.HostnameVerifier { _, session ->
            verify(session, certDER)
        }
    }

    fun configure(connection: HttpsURLConnection, context: Context) =
        configure(connection, certificate(context))
}
