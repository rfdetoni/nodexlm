package dev.nodexlm.app

import android.content.Context
import java.io.Closeable
import java.io.File
import java.io.RandomAccessFile
import java.nio.channels.FileLock

/** An OS-released lock prevents independent app processes loading weights together. */
internal class MobileRuntimeLease private constructor(private val file: RandomAccessFile, private val lock: FileLock) : Closeable {
    override fun close() {
        try { lock.release() } finally { file.close() }
    }

    companion object {
        fun acquire(context: Context): MobileRuntimeLease? {
            val file = runCatching { RandomAccessFile(File(context.filesDir, "mobile-runtime.lock"), "rw") }.getOrNull() ?: return null
            try {
                val deadline = android.os.SystemClock.elapsedRealtime() + 3000
                do {
                    val lock = runCatching { file.channel.tryLock() }.getOrNull()
                    if (lock != null) return MobileRuntimeLease(file,lock)
                    Thread.sleep(100)
                } while (android.os.SystemClock.elapsedRealtime() < deadline)
            } catch (_: Exception) {}
            file.close()
            return null
        }
    }
}
