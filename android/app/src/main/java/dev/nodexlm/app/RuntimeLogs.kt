// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.content.Context
import android.util.Log
import java.io.File
import java.io.FileOutputStream
import java.text.SimpleDateFormat
import java.util.ArrayDeque
import java.util.Date
import java.util.Locale

/** Only explicit, bounded, structured operational events are recorded.
 * Never persist URLs, node IDs, pairing codes, tokens, prompts, responses,
 * model paths or arbitrary exception messages.
 */
internal enum class RuntimeRole { GUEST, HOST, APP, UPDATES }
internal enum class RuntimeMode { RPC_GUEST, WORKER_GUEST, INACTIVE }
internal enum class RuntimeLevel { INFO, WARN, ERROR }

internal enum class RuntimeEvent(val pt: String, val en: String) {
    APP_OPEN("Aplicativo iniciado", "Application started"),
    PAIR_OK("Pareamento com Host concluído", "Host pairing succeeded"),
    PAIR_FAILED("Pareamento com Host falhou", "Host pairing failed"),
    GUEST_START("Guest RPC iniciando", "RPC Guest starting"),
    GUEST_READY("Servidor RPC nativo pronto", "Native RPC server ready"),
    GUEST_RECOVERED("Guest retomado após resfriamento", "Guest recovered after cooling"),
    GUEST_DRAIN_THERMAL("Guest pausado: temperatura", "Guest draining: thermal"),
    GUEST_DRAIN_MEMORY("Guest pausado: RAM baixa", "Guest draining: low RAM"),
    GUEST_DRAIN_POWER("Guest pausado: bateria/energia", "Guest draining: battery/power"),
    GUEST_DRAIN_POLICY("Guest pausado: política", "Guest draining: policy"),
    GUEST_STOP("Guest RPC encerrado", "RPC Guest stopped"),
    GUEST_FAILED("Falha no serviço RPC nativo", "Native RPC service failed"),
    GUEST_LOWMEM_PREVIOUS("Android encerrou Guest anterior por baixa RAM", "Previous Guest killed by Android low memory"),
    HOST_HEARTBEAT_OK("Heartbeat enviado ao Host", "Heartbeat sent to Host"),
    HOST_HEARTBEAT_FAILED("Falha no heartbeat do Host", "Host heartbeat failed"),
    HOST_TLS_FAILED("Conexão segura com Host falhou", "Secure Host connection failed"),
    HOST_AUTH_FAILED("Host rejeitou credenciais do Guest", "Host rejected Guest credentials"),
    HOST_NET_OFFLINE("Rede indisponível para RPC", "Network unavailable for RPC"),
    HOST_TUNNEL_CLAIMED("Host selecionou túnel RPC", "Host claimed RPC tunnel"),
    HOST_NATIVE_CONNECTED("Túnel conectado ao RPC nativo", "Tunnel connected to native RPC"),
    HOST_FIRST_REQUEST("Primeira solicitação recebida do Host", "First Host request received"),
    HOST_FIRST_RESPONSE("Primeira resposta do RPC nativo", "First native RPC response"),
    HOST_TUNNEL_CLOSED("Túnel RPC encerrado", "RPC tunnel closed"),
    HOST_RETRY("Túnel RPC reconectando", "RPC tunnel retrying"),
    WORKER_START("Worker de inferência iniciado", "Inference worker started"),
    WORKER_STOP("Worker de inferência encerrado", "Inference worker stopped"),
    WORKER_FAILED("Worker de inferência falhou", "Inference worker failed"),
    WORKER_RETRY("Worker tentando reconectar", "Worker reconnecting"),
    WORKER_JOB_RECEIVED("Tarefa recebida do Host", "Job received from Host"),
    WORKER_MODEL_LOADED("Modelo carregado no worker", "Worker model loaded"),
    WORKER_JOB_DONE("Inferência concluída", "Inference completed"),
    WORKER_JOB_FAILED("Inferência falhou", "Inference failed"),
    UPDATE_CHECK("Consulta ao GitHub iniciada", "GitHub update check started"),
    UPDATE_CURRENT("Nenhuma atualização mais recente", "No newer update"),
    UPDATE_AVAILABLE("Atualização oficial disponível", "Official update available"),
    UPDATE_UNPUBLISHED("Sem release estável oficial", "No official stable release"),
    UPDATE_CHECK_FAILED("Falha na consulta de atualizações", "Update check failed"),
    UPDATE_DOWNLOAD("Download do APK solicitado", "APK download requested"),
    UPDATE_DOWNLOADED("APK baixado", "APK downloaded"),
    UPDATE_DOWNLOAD_FAILED("Download do APK falhou", "APK download failed"),
    UPDATE_VERIFY("Verificação do APK iniciada", "APK verification started"),
    UPDATE_VERIFIED("Assinatura e SHA-256 conferidos", "Signature and SHA-256 verified"),
    UPDATE_REJECTED("APK rejeitado na verificação", "APK verification rejected"),
    UPDATE_INSTALL("Instalação solicitada ao Android", "Installation requested from Android"),
    UPDATE_INSTALLED("Instalação concluída", "Installation completed"),
    UPDATE_INSTALL_FAILED("Instalação não concluída", "Installation failed")
}

internal data class RuntimeLogEntry(
    val timestamp: Long,
    val role: RuntimeRole,
    val level: RuntimeLevel,
    val event: RuntimeEvent,
    val number: Long = 0L
)

internal object RuntimeLogFormat {
    fun encode(event: RuntimeLogEntry): String =
        "${event.timestamp}\t${event.role.name}\t${event.level.name}\t${event.event.name}\t${event.number}\n"

    fun decode(line: String): RuntimeLogEntry? {
        val parts = line.split('\t')
        if (parts.size != 5) return null
        val time = parts[0].toLongOrNull() ?: return null
        if (time <= 0) return null
        val role = enumValues<RuntimeRole>().firstOrNull { it.name == parts[1] } ?: return null
        val level = enumValues<RuntimeLevel>().firstOrNull { it.name == parts[2] } ?: return null
        val event = enumValues<RuntimeEvent>().firstOrNull { it.name == parts[3] } ?: return null
        val number = parts[4].toLongOrNull() ?: return null
        return RuntimeLogEntry(time, role, level, event, number)
    }

    // Only the latest *recorded* lifecycle transition; not an OS liveness check.
    fun lastMode(entries: List<RuntimeLogEntry>): RuntimeMode {
        for (entry in entries.asReversed()) {
            when (entry.event) {
                RuntimeEvent.GUEST_START, RuntimeEvent.GUEST_READY, RuntimeEvent.GUEST_RECOVERED ->
                    return RuntimeMode.RPC_GUEST
                RuntimeEvent.WORKER_START -> return RuntimeMode.WORKER_GUEST
                RuntimeEvent.GUEST_STOP, RuntimeEvent.GUEST_FAILED,
                RuntimeEvent.WORKER_STOP, RuntimeEvent.WORKER_FAILED ->
                    return RuntimeMode.INACTIVE
                else -> continue
            }
        }
        return RuntimeMode.INACTIVE
    }

    fun readable(entries: List<RuntimeLogEntry>, english: Boolean): String {
        val format = SimpleDateFormat("MM-dd HH:mm:ss", Locale.getDefault())
        return entries.joinToString("\n") { item ->
            val label = if (english) item.event.en else item.event.pt
            val metric = if (item.number != 0L) " #${item.number}" else ""
            "${format.format(Date(item.timestamp))} [${item.role}] [${item.level}] $label$metric"
        }
    }
}

/** Cross-process rolling file (UI/worker and isolated :rpc process).
 * All access uses the same OS file lock, plus a local mutex for threads.
 * Rotating at 128 KiB keeps at most 3 small segments, including screen-off.
 */
internal object RuntimeLogs {
    private const val TAG = "NodexLMLogs"
    private const val MAX_SEGMENT = 128 * 1024L
    private const val SEGMENTS = 3
    private const val MAX_ENTRIES = 250
    private const val MAX_READ_SEGMENT = 256 * 1024L
    private const val FOLDER = "operational-logs"
    private const val FILE = "runtime.log"
    private const val LOCK = "runtime.lock"

    private fun directory(context: Context): File =
        File(context.filesDir, FOLDER).also { require(it.isDirectory || it.mkdirs()) }

    private fun logFile(dir: File, index: Int): File =
        File(dir, if (index == 0) FILE else "$FILE.$index")

    private inline fun <T> locked(context: Context, work: (File) -> T): T {
        val dir = directory(context)
        synchronized(this) {
            FileOutputStream(File(dir, LOCK), true).channel.use { channel ->
                val lock = channel.lock()
                try { return work(dir) } finally { lock.release() }
            }
        }
    }

    fun append(
        context: Context,
        role: RuntimeRole,
        level: RuntimeLevel,
        event: RuntimeEvent,
        number: Long = 0L
    ) {
        val encoded = RuntimeLogFormat.encode(RuntimeLogEntry(
            System.currentTimeMillis(), role, level, event, number
        )).toByteArray(Charsets.UTF_8)
        try {
            locked(context) { dir ->
                val main = logFile(dir, 0)
                if (main.length() + encoded.size > MAX_SEGMENT) {
                    logFile(dir, SEGMENTS - 1).delete()
                    for (index in SEGMENTS - 2 downTo 0) {
                        val old = logFile(dir, index)
                        if (old.isFile) require(old.renameTo(logFile(dir, index + 1))) {
                            "Cannot rotate diagnostic log"
                        }
                    }
                }
                FileOutputStream(main, true).use { it.write(encoded) }
            }
        } catch (e: Exception) {
            Log.w(TAG, "Cannot store bounded diagnostic event", e)
        }
    }

    fun read(context: Context, limit: Int = MAX_ENTRIES): List<RuntimeLogEntry> =
        locked(context) { dir ->
            val tail = ArrayDeque<RuntimeLogEntry>()
            val capped = limit.coerceIn(1, MAX_ENTRIES)
            for (index in SEGMENTS - 1 downTo 0) {
                val file = logFile(dir, index)
                if (!file.isFile || file.length() > MAX_READ_SEGMENT) continue
                file.bufferedReader(Charsets.UTF_8).useLines { lines ->
                    lines.forEach { line ->
                        val event = RuntimeLogFormat.decode(line) ?: return@forEach
                        tail.addLast(event)
                        if (tail.size > capped) tail.removeFirst()
                    }
                }
            }
            tail.toList()
        }

    fun clear(context: Context) {
        locked(context) { dir ->
            for (index in 0 until SEGMENTS) logFile(dir, index).delete()
        }
    }
}
