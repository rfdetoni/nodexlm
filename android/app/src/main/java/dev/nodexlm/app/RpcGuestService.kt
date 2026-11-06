// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.app.ActivityManager
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.os.Process
import android.util.Log
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.File
import java.net.HttpURLConnection
import java.net.InetSocketAddress
import java.net.Socket
import java.net.URI
import java.security.KeyStore
import java.security.cert.CertificateFactory
import java.util.Collections
import java.util.concurrent.atomic.AtomicBoolean
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocket
import javax.net.ssl.TrustManagerFactory
import kotlin.concurrent.thread

/**
 * True Android GGML RPC Guest. The Host owns the GGUF and distributes tensors,
 * buffers and compute graphs through the existing authenticated HTTPS relay.
 * No local model is required. Runs in a separate :rpc process because pinned
 * ggml_backend_rpc_start_server() is blocking and has no shutdown API.
 */
class RpcGuestService : Service() {
    companion object {
        const val START = "dev.nodexlm.RPC_START"
        const val STOP = "dev.nodexlm.RPC_STOP"
        const val EXTRA_ENDPOINT = "endpoint"
        const val EXTRA_NODE = "node"
        const val EXTRA_TOKEN = "token"
        const val EXTRA_NAME = "name"
        private const val CHANNEL = "nodexlm_rpc_guest"
        private const val LOG = "NodexLM-RpcGuest"
        private const val PORT = 50052
        private const val TUNNELS = 4
    }

    private val running = AtomicBoolean(false)
    private val stopping = AtomicBoolean(false)
    private val sockets = Collections.synchronizedSet(mutableSetOf<Socket>())
    private var cpuLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == STOP) {
            shutdown()
            return START_NOT_STICKY
        }
        if (intent?.action != START) {
            shutdown()
            return START_NOT_STICKY
        }
        if (!running.compareAndSet(false, true)) return START_NOT_STICKY
        val endpoint = intent.getStringExtra(EXTRA_ENDPOINT) ?: ""
        val node = intent.getStringExtra(EXTRA_NODE) ?: ""
        val token = intent.getStringExtra(EXTRA_TOKEN) ?: ""
        val name = intent.getStringExtra(EXTRA_NAME).orEmpty().ifBlank { Build.MODEL }
        val parsed = try { URI(endpoint) } catch (_: Exception) { null }
        if (parsed == null || parsed.scheme != "https" || parsed.host.isNullOrEmpty() || parsed.rawUserInfo != null ||
            parsed.rawQuery != null || parsed.rawFragment != null || endpoint.contains('\r') ||
            endpoint.contains('\n') || node.contains('\r') || node.contains('\n') ||
            token.contains('\r') || token.contains('\n') || node.length !in 32..128 ||
            token.length !in 32..512 || !File(filesDir, "coordinator.crt").isFile) {
            Log.e(LOG, "RPC Guest requires valid pinned Host enrollment")
            shutdown()
            return START_NOT_STICKY
        }
        val host = parsed.host
        val port = if (parsed.port > 0) parsed.port else 443
        createNotification()
        acquireLocks()
        thread(name = "nodexlm-rpc-supervisor", isDaemon = true) {
            try {
                val threads = (Runtime.getRuntime().availableProcessors() - 1).coerceIn(1, 6)
                val message = NativeRpc.start(threads)
                check(message.isEmpty()) { message }
                awaitNativeRPC()
                val sslContext = pinnedSSLContext()
                // Heartbeats report only measured Android RAM; there is no reliable
                // Android GPU VRAM number, so it is deliberately not advertised.
                thread(name = "nodexlm-rpc-heartbeat", isDaemon = true) {
                    while (running.get()) {
                        try { sendHeartbeat(endpoint, node, token, name, sslContext) }
                        catch (e: Exception) { if (running.get()) Log.w(LOG, "Heartbeat failed: ${e.message}") }
                        sleepWhileRunning(30_000)
                    }
                }
                repeat(TUNNELS) { slot ->
                    thread(name = "nodexlm-rpc-tunnel-$slot", isDaemon = true) {
                        var backoff = 1000L
                        while (running.get()) {
                            try {
                                connectAndRelay(host, port, node, token, sslContext)
                                backoff = 1000L
                            } catch (e: Exception) {
                                if (running.get()) Log.w(LOG, "RPC tunnel $slot: ${e.message}")
                                backoff = (backoff * 2).coerceAtMost(10_000)
                            }
                            sleepWhileRunning(backoff)
                        }
                    }
                }
            } catch (e: Throwable) {
                Log.e(LOG, "RPC Guest cannot start", e)
                shutdown()
            }
        }
        return START_NOT_STICKY
    }

    private fun createNotification() {
        val notifications = getSystemService(NotificationManager::class.java)
        notifications.createNotificationChannel(NotificationChannel(
            CHANNEL, "NodexLM RPC Guest", NotificationManager.IMPORTANCE_LOW))
        val stopIntent = Intent(this, RpcGuestService::class.java).setAction(STOP)
        val stop = PendingIntent.getService(this, 43, stopIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val notification = Notification.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setContentTitle("NodexLM · Guest RPC ativo")
            .setContentText("Compartilhando CPU/RAM com o Host · consome bateria")
            .setOngoing(true)
            .addAction(Notification.Action.Builder(null, "Parar", stop).build())
            .build()
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(43, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else startForeground(43, notification)
    }

    private fun acquireLocks() {
        try {
            cpuLock = (getSystemService(POWER_SERVICE) as PowerManager)
                .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "NodexLM:rpc-guest")
                .apply { setReferenceCounted(false); acquire() }
        } catch (e: Exception) { Log.w(LOG, "CPU wake lock unavailable", e) }
        try {
            val wifi = applicationContext.getSystemService(WIFI_SERVICE) as WifiManager
            @Suppress("DEPRECATION")
            wifiLock = wifi.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "NodexLM:rpc-wifi")
                .apply { setReferenceCounted(false); acquire() }
        } catch (e: Exception) { Log.w(LOG, "Wi-Fi wake lock unavailable", e) }
    }

    private fun sleepWhileRunning(duration: Long) {
        var remaining = duration
        while (running.get() && remaining > 0) {
            val step = minOf(remaining, 500L)
            try { Thread.sleep(step) } catch (_: InterruptedException) { return }
            remaining -= step
        }
    }

    private fun awaitNativeRPC() {
        repeat(60) {
            if (!running.get()) error("Guest stopped")
            try {
                Socket().use { it.connect(InetSocketAddress("127.0.0.1", PORT), 250) }
                return
            } catch (_: Exception) { sleepWhileRunning(200) }
        }
        error("Native Android GGML RPC server did not start on loopback")
    }

    private fun pinnedSSLContext(): SSLContext {
        val certificate = File(filesDir, "coordinator.crt").inputStream().use {
            CertificateFactory.getInstance("X.509").generateCertificate(it)
        }
        val store = KeyStore.getInstance(KeyStore.getDefaultType())
        store.load(null, null)
        store.setCertificateEntry("nodexlm-host-pinned", certificate)
        val managers = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm())
        managers.init(store)
        return SSLContext.getInstance("TLS").apply { init(null, managers.trustManagers, null) }
    }

    private fun sendHeartbeat(endpoint: String, node: String, token: String,
                              name: String, ssl: SSLContext) {
        val memory = ActivityManager.MemoryInfo()
        (getSystemService(ACTIVITY_SERVICE) as ActivityManager).getMemoryInfo(memory)
        // Address space on ARMv7 is smaller even on devices with more physical RAM.
        val available = if (Build.SUPPORTED_ABIS.firstOrNull()?.let { Build.SUPPORTED_32_BIT_ABIS.contains(it) } == true) {
            minOf(memory.availMem, 2L * 1024 * 1024 * 1024)
        } else memory.availMem
        val device = JSONObject()
            .put("name", name.take(80))
            .put("platform", "android")
            .put("architecture", Build.SUPPORTED_ABIS.firstOrNull() ?: "arm64-v8a")
            .put("cpu_count", Runtime.getRuntime().availableProcessors())
            .put("ram_bytes", memory.totalMem)
            .put("available_ram_bytes", available)
            .put("gpus", JSONArray())
            .put("runtime", JSONObject()
                .put("engine", "ggml-rpc-cpu")
                .put("version", NativeRpc.VERSION)
                .put("installed", true)
                .put("rpc_ready", false))
        val connection = java.net.URL(endpoint.trimEnd('/') + "/v1/nodes/heartbeat")
            .openConnection() as HttpsURLConnection
        connection.sslSocketFactory = ssl.socketFactory
        connection.requestMethod = "POST"
        connection.connectTimeout = 7000
        connection.readTimeout = 7000
        connection.doOutput = true
        connection.setRequestProperty("Content-Type", "application/json")
        connection.setRequestProperty("Authorization", "Bearer $token")
        connection.setRequestProperty("X-Nodex-Node", node)
        try {
            connection.outputStream.use { it.write(device.toString().toByteArray(Charsets.UTF_8)) }
            check(connection.responseCode == 200) { "Host heartbeat HTTP ${connection.responseCode}" }
            connection.inputStream.close()
        } finally { connection.disconnect() }
    }

    private fun <T : Socket> track(socket: T): T {
        sockets.add(socket)
        return socket
    }

    private fun readStatus(input: java.io.InputStream): String {
        val result = ByteArrayOutputStream()
        val tail = byteArrayOf(13, 10, 13, 10)
        while (result.size() < 8192) {
            val next = input.read()
            if (next < 0) error("Host closed CONNECT")
            result.write(next)
            val bytes = result.toByteArray()
            if (bytes.size >= 4 && bytes.takeLast(4).toByteArray().contentEquals(tail)) {
                return String(bytes, Charsets.US_ASCII).lineSequence().first()
            }
        }
        error("Oversized CONNECT response")
    }

    private fun connectAndRelay(host: String, port: Int, node: String,
                                token: String, ssl: SSLContext) {
        val remote = track(ssl.socketFactory.createSocket() as SSLSocket)
        try {
            remote.keepAlive = true
            remote.connect(InetSocketAddress(host, port), 7000)
            remote.soTimeout = 10_000
            remote.sslParameters = remote.sslParameters.apply { endpointIdentificationAlgorithm = "HTTPS" }
            remote.startHandshake()
            // The RPC binary stream is not exposed until the Host returns 200
            // and signals with a single byte that an authenticated relay is ready.
            val request = "CONNECT /v1/nodes/rpc/tunnel HTTP/1.1\r\n" +
                "Host: $host:$port\r\n" +
                "Authorization: Bearer $token\r\n" +
                "X-Nodex-Node: $node\r\n\r\n"
            remote.outputStream.write(request.toByteArray(Charsets.US_ASCII))
            remote.outputStream.flush()
            val status = readStatus(remote.inputStream)
            check(status.startsWith("HTTP/1.1 200 ") || status.startsWith("HTTP/1.0 200 ")) {
                "Host denied RPC tunnel: ${status.take(120)}"
            }
            remote.soTimeout = 0
            check(remote.inputStream.read() == 1) { "Missing Host RPC relay marker" }
            val local = track(Socket())
            try {
                local.keepAlive = true
                local.connect(InetSocketAddress("127.0.0.1", PORT), 3000)
                // The two copy loops do not parse or modify the llama.cpp RPC
                // protocol. The TLS session is pinned to the previously paired Host.
                val sender = thread(name = "nodexlm-rpc-stream-up", isDaemon = true) {
                    try { local.getInputStream().copyTo(remote.outputStream) }
                    catch (_: Exception) { }
                    finally { try { remote.close() } catch (_: Exception) {} }
                }
                try { remote.inputStream.copyTo(local.getOutputStream()) }
                catch (_: Exception) { }
                finally {
                    try { local.close() } catch (_: Exception) {}
                    try { remote.close() } catch (_: Exception) {}
                    sender.join(1000)
                }
            } finally {
                sockets.remove(local)
                try { local.close() } catch (_: Exception) {}
            }
        } finally {
            sockets.remove(remote)
            try { remote.close() } catch (_: Exception) {}
        }
    }

    private fun shutdown() {
        if (!stopping.compareAndSet(false, true)) return
        running.set(false)
        synchronized(sockets) {
            sockets.forEach { try { it.close() } catch (_: Exception) {} }
            sockets.clear()
        }
        try { if (wifiLock?.isHeld == true) wifiLock?.release() } catch (_: Exception) {}
        try { if (cpuLock?.isHeld == true) cpuLock?.release() } catch (_: Exception) {}
        try { stopForeground(STOP_FOREGROUND_REMOVE) } catch (_: Exception) {}
        stopSelf()
        // The native RPC server has no stop API in pinned llama.cpp. Only this
        // separate Android process exits; the app's UI and model library survive.
        thread(name = "nodexlm-rpc-exit", isDaemon = true) {
            try { Thread.sleep(250) } catch (_: InterruptedException) {}
            Process.killProcess(Process.myPid())
        }
    }

    override fun onDestroy() {
        shutdown()
        super.onDestroy()
    }
}
