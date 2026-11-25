// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import java.io.ByteArrayOutputStream
import java.io.DataInputStream
import java.io.DataOutputStream
import kotlin.random.Random
import org.junit.Assert.*
import org.junit.Test

class RpcFrameCodecTest {
    @Test fun compressedAndRawFramesPreserveBytes() {
        val data = ByteArray(64 * 1024) { 42 } + Random(7).nextBytes(64 * 1024) + byteArrayOf(0, -1, 42)
        val wire = ByteArrayOutputStream()
        RpcFrameCodec.Writer(wire).use { it.write(data, data.size) }
        assertTrue(wire.size() < data.size)
        val restored = ByteArrayOutputStream()
        RpcFrameCodec.Reader(wire.toByteArray().inputStream()).use { reader ->
            val buffer = ByteArray(RpcFrameCodec.MAX_FRAME)
            while (true) {
                val n = reader.read(buffer)
                if (n < 0) break
                restored.write(buffer, 0, n)
            }
        }
        assertArrayEquals(data, restored.toByteArray())
    }

    @Test(expected = IllegalArgumentException::class)
    fun rejectsOversizedFrame() {
        val wire = ByteArrayOutputStream()
        DataOutputStream(wire).apply { writeInt(RpcFrameCodec.MAX_FRAME + 1); writeInt(1) }
        RpcFrameCodec.Reader(wire.toByteArray().inputStream()).use { it.read(ByteArray(RpcFrameCodec.MAX_FRAME)) }
    }

    @Test fun directionalProbeUsesExactWireFormat() {
        val input = ByteArray(1 shl 20) { 0xa5.toByte() } + byteArrayOf(0x5a)
        val output = ByteArrayOutputStream()
        RpcFrameCodec.directional(DataInputStream(input.inputStream()), output)
        assertArrayEquals(ByteArray((1 shl 20) + 1) { 0xa5.toByte() }, output.toByteArray())
    }
}
