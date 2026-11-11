package dev.nodexlm.app

import java.io.ByteArrayOutputStream
import java.io.InputStream
import java.time.Instant
import java.time.OffsetDateTime

internal fun parsePairingExpiry(value: String): Instant = OffsetDateTime.parse(value).toInstant()

/** Bound untrusted pairing JSON/certificate reads before allocating their bodies. */
internal fun boundedPairingBytes(input: InputStream, limit: Int = 8192): ByteArray {
    require(limit in 1..65536)
    val output = ByteArrayOutputStream()
    val buffer = ByteArray(1024)
    while (true) {
        val count = input.read(buffer)
        if (count < 0) break
        require(output.size() + count <= limit) { "Pairing response exceeds the size limit" }
        output.write(buffer, 0, count)
    }
    return output.toByteArray()
}
