// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.InputStream
import java.io.OutputStream
import java.util.zip.Deflater
import java.util.zip.Inflater

/** Same independent raw-DEFLATE frames as the Go relay. No shared dictionary. */
internal object RpcFrameCodec {
    const val MAX_FRAME = 64 * 1024
    private const val MIN_COMPRESS = 16 * 1024

    // One codec per claimed direction; native zlib state released at EOF.
    class Reader(input: InputStream) : AutoCloseable {
        private val stream = DataInputStream(input)
        private val inflater = Inflater(true)
        private val encoded = ByteArray(MAX_FRAME)
        fun read(buffer: ByteArray): Int {
            require(buffer.size >= MAX_FRAME)
            val first = stream.read()
            if (first < 0) return -1
            val raw = (first shl 24) or (stream.readUnsignedByte() shl 16) or
                (stream.readUnsignedByte() shl 8) or stream.readUnsignedByte()
            val wire = stream.readInt()
            require(raw in 1..MAX_FRAME && wire in 1..raw) { "Invalid RPC frame length" }
            if (raw == wire) {
                stream.readFully(buffer, 0, raw)
                return raw
            }
            stream.readFully(encoded, 0, wire)
            inflater.reset()
            inflater.setInput(encoded, 0, wire)
            var offset = 0
            while (offset < raw && !inflater.finished()) {
                val n = inflater.inflate(buffer, offset, raw - offset)
                require(n > 0) { "Incomplete compressed RPC frame" }
                offset += n
            }
            // Some implementations consume the end marker only on a further
            // inflate call. Reject additional output or trailing wire bytes.
            if (!inflater.finished() && offset == raw) {
                require(inflater.inflate(ByteArray(1)) == 0) { "Oversized RPC frame" }
            }
            require(offset == raw && inflater.finished() && inflater.remaining == 0) {
                "Invalid compressed RPC frame"
            }
            return raw
        }
        override fun close() = inflater.end()
    }

    class Writer(output: OutputStream) : AutoCloseable {
        private val stream = DataOutputStream(output)
        private val deflater = Deflater(Deflater.BEST_SPEED, true)
        private val encoded = ByteArray(MAX_FRAME)
        fun write(buffer: ByteArray, length: Int) {
            require(length in 1..buffer.size)
            var offset = 0
            while (offset < length) {
                val raw = minOf(MAX_FRAME, length - offset)
                var wire = raw
                if (raw >= MIN_COMPRESS) {
                    deflater.reset()
                    deflater.setInput(buffer, offset, raw)
                    deflater.finish()
                    val n = deflater.deflate(encoded)
                    if (deflater.finished() && n > 0 && n + 8 < raw - raw / 8) wire = n
                }
                stream.writeInt(raw)
                stream.writeInt(wire)
                if (wire == raw) stream.write(buffer, offset, raw)
                else stream.write(encoded, 0, wire)
                offset += raw
            }
            stream.flush()
        }
        override fun close() = deflater.end()
    }

    fun directional(input: DataInputStream, output: OutputStream) {
        val payload = ByteArray(16 * 1024)
        repeat(64) {
            input.readFully(payload)
            require(payload.all { it == 0xa5.toByte() }) { "Invalid directional payload" }
        }
        output.write(0xa5)
        output.flush()
        require(input.readUnsignedByte() == 0x5a) { "Invalid directional request" }
        repeat(64) { output.write(payload) }
        output.flush()
    }
}
