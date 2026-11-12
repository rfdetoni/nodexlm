// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.app.ActivityManager
import android.app.ApplicationExitInfo
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.os.BatteryManager
import android.content.IntentFilter
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.net.ConnectivityManager
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
import java.util.concurrent.ThreadLocalRandom
import java.util.concurrent.ScheduledThreadPoolExecutor
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicLong
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
        const val EXTRA_NAME = "name"
        const val EXTRA_MODE = "mode"
        private const val CHANNEL = "nodexlm_rpc_guest"
        private const val LOG = "NodexLM-RpcGuest"

        private const val TUNNELS = 8
    }

    private lateinit var rpcSocket: String
    private val localSockets = Collections.synchronizedSet(mutableSetOf<android.net.LocalSocket>())
    private lateinit var energyPolicy: EnergyPolicy
    private val reconnectFailures=AtomicLong()
    // A single shared daemon handles diagnostics for claimed (not idle) tunnels.
    private val diagnostics = ScheduledThreadPoolExecutor(1) { task ->
        Thread(task, "nodexlm-rpc-watchdog").apply { isDaemon = true }
    }.apply { removeOnCancelPolicy = true }
    private val running = AtomicBoolean(false)
    private val stopping = AtomicBoolean(false)
    private val draining = AtomicBoolean(false)
    @Volatile private var drainReason = ""
    @Volatile private var drainStartedMs = 0L
    private val claimedNativeRelays = AtomicLong()
    private val cpuSampler = CpuUsageSampler()
    private val sockets = Collections.synchronizedSet(mutableSetOf<Socket>())
    private var cpuLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == STOP) {
            shutdown("stopped", "manual")
            return START_NOT_STICKY
        }
        if (intent?.action != START) {
            shutdown()
            return START_NOT_STICKY
        }
        if (!running.compareAndSet(false, true)) return START_NOT_STICKY
        val energy = getSharedPreferences("nodexlm", MODE_PRIVATE)
        energyPolicy = EnergyPolicy(energy.getBoolean("rpc_only_charging", false),energy.getInt("rpc_min_battery",15),energy.getInt("rpc_start_hour",0),energy.getInt("rpc_end_hour",0))
        val endpoint = intent.getStringExtra(EXTRA_ENDPOINT) ?: ""
        val node = intent.getStringExtra(EXTRA_NODE) ?: ""
        val token = try { NodeSecrets.read(this) } catch (e: Exception) {
            Log.e(LOG, "Cannot read protected node token", e)
            ""
        }
        val name = intent.getStringExtra(EXTRA_NAME).orEmpty().ifBlank { Build.MODEL }
        val mode = intent.getStringExtra(EXTRA_MODE).orEmpty().ifBlank { "auto" }
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
        try {
            createNotification()
            acquireLocks()
            recordGuestEvent("starting")
        } catch (e: Exception) {
            Log.e(LOG, "Cannot start Android RPC foreground service", e)
            shutdown()
            return START_NOT_STICKY
        }
        thread(name = "nodexlm-rpc-supervisor", isDaemon = true) {
            try {
                logPreviousLowMemoryExit()
                val cores = Runtime.getRuntime().availableProcessors().coerceAtLeast(1)
                val requestedThreads = energy.getInt("rpc_threads", 0)
                val threads = if (requestedThreads == 0) (cores - 1).coerceIn(1, 6)
                              else requestedThreads.coerceIn(1, minOf(8, cores))
                check(checkThermalsAndBattery() == null) {
                    "RPC Guest cannot start outside its thermal/energy policy"
                }
                val socketFile=File(filesDir,"rpc.sock")
                check(!socketFile.exists()||socketFile.delete()) { "Cannot remove stale RPC socket" }
                rpcSocket=socketFile.absolutePath
                val message = NativeRpc.start(threads, mode, rpcSocket)
                check(message.isEmpty()) { message }
                awaitNativeRPC()
                recordGuestEvent("ready")
                Log.i(LOG, "Native GGML RPC socket ready; mode=$mode, threads=$threads")
                val sslContext = pinnedSSLContext()
                // Share measured Vulkan heap counters; on mobile GPUs memory can
                // be shared with RAM and must never be counted twice by the Host.
                thread(name = "nodexlm-rpc-heartbeat", isDaemon = true) {
                    while (running.get()) {
                        try { sendHeartbeat(endpoint, node, token, name, mode, sslContext) }
                        catch (e: Exception) { if (running.get()) Log.w(LOG, "Heartbeat failed: ${e.message}") }
                        sleepWhileRunning(15_000)
                    }
                }
                repeat(TUNNELS) { slot ->
                    thread(name = "nodexlm-rpc-tunnel-$slot", isDaemon = true) {
                        var backoff = 1000L
                        while (running.get() && !draining.get()) {
                            // Avoid a TLS reconnect storm while offline. Preserve
                            // pinned identity when the active network changes.
                            val connectivity = getSystemService(CONNECTIVITY_SERVICE) as ConnectivityManager
                            if (connectivity.activeNetwork == null) {
                                sleepWhileRunning(2_000L)
                                continue
                            }
                            var retryCeiling = 10_000L
                            var failed = false
                            try {
                                connectAndRelay(slot, host, port, node, token, sslContext)
                                backoff = 1000L
                            } catch (e: SecurityException) {
                                Log.e(LOG, "RPC Host rejected credentials; stopping Guest", e)
                                shutdown()
                                break
                            } catch (e: Exception) {
                                failed = true
                                reconnectFailures.incrementAndGet()
                                // Saturated Host relays need slower retries, not
                                // eight synchronized attempts every second.
                                if (e.message?.contains(" 503 ") == true) retryCeiling = 30_000L
                                if (running.get()) Log.w(LOG, "RPC tunnel $slot: ${e.message}")
                            }
                            val jitter = ThreadLocalRandom.current().nextLong(0L, (backoff / 4).coerceAtLeast(250L) + 1L)
                            sleepWhileRunning(backoff + jitter)
                            if (failed) backoff = (backoff * 2).coerceAtMost(retryCeiling)
                        }
                    }
                }
                // After startup the detached GGML thread may still exit.
                // Do not keep publishing heartbeat or reconnecting to a dead worker.
                while (running.get()) {
                    check(NativeRpc.isRunning()) { "Native GGML RPC server exited unexpectedly" }
                    // SEVERE thermal status still stops immediately. Other policy
                    // exits withdraw from new Host placements before closing
                    // any active native RPC sessions.
                    val reason = checkThermalsAndBattery()
                    if (reason != null && !draining.get()) {
                        drainReason = reason
                        drainStartedMs = android.os.SystemClock.elapsedRealtime()
                        if (draining.compareAndSet(false, true)) {
                            recordGuestEvent("draining", reason)
                            Log.w(LOG, "RPC Guest draining because of $reason; " +
                                "no automatic restart. Current claimed tunnels=${claimedNativeRelays.get()}")
                            // Publish withdrawal immediately, not only at the
                            // next scheduled heartbeat (up to 15s later).
                            try { sendHeartbeat(endpoint, node, token, name, mode, sslContext) }
                            catch (e: Exception) { Log.w(LOG, "Cannot announce drain to Host", e) }
                        }
                    }
                    if (draining.get()) {
                        val elapsed = android.os.SystemClock.elapsedRealtime() - drainStartedMs
                        if ((elapsed >= 5_000L && claimedNativeRelays.get() == 0L) ||
                            elapsed >= 90_000L) {
                            Log.w(LOG, "RPC Guest drain finished: $drainReason, " +
                                "claimed=${claimedNativeRelays.get()}, elapsed_ms=$elapsed")
                            shutdown("stopped", "drained_$drainReason")
                            break
                        }
                    }
                    sleepWhileRunning(1_000L)
                }
            } catch (e: Throwable) {
                Log.e(LOG, "RPC Guest cannot start", e)
                val reason = e.message ?: "Falha ao iniciar Guest RPC"
                shutdown("failed", "runtime")
                getSystemService(NotificationManager::class.java).notify(44,
                    Notification.Builder(this, CHANNEL).setSmallIcon(android.R.drawable.stat_notify_error)
                        .setContentTitle(getString(R.string.ui_8ea70b71b55f))
                        .setContentText(reason).setStyle(Notification.BigTextStyle().bigText(reason)).build())
            }
        }
        return START_NOT_STICKY
    }

    private fun logPreviousLowMemoryExit() {
        if (Build.VERSION.SDK_INT < 30) return
        try {
            val manager = getSystemService(ACTIVITY_SERVICE) as ActivityManager
            val previous = manager.getHistoricalProcessExitReasons(packageName, 0, 8)
                .filter { it.processName == "$packageName:rpc" }
                .maxByOrNull { it.timestamp }
            if (previous?.reason == ApplicationExitInfo.REASON_LOW_MEMORY) {
                Log.w(LOG, "Previous RPC process terminated by Android low-memory killer; " +
                    "timestamp_ms=${previous.timestamp}. Reduce Guest RAM contribution.")
            }
        } catch (e: RuntimeException) {
            Log.w(LOG, "Prior native process exit information unavailable", e)
        }
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
            .setContentTitle(getString(R.string.ui_fc46b31da822))
            .setContentText(getString(R.string.ui_343d89ad1a83))
            .setOngoing(true)
            .addAction(Notification.Action.Builder(null, "Parar", stop).build())
            .build()
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(43, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE)
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
            // HIGH_PERF can assist screen-off Wi-Fi on API 29-33; on API 34+
            // Android converts it to LOW_LATENCY, which is ineffective with
            // screen off. This is an advisory lock, not a Doze exemption.
            @Suppress("DEPRECATION")
            val mode = if (Build.VERSION.SDK_INT < 34) WifiManager.WIFI_MODE_FULL_HIGH_PERF
                       else WifiManager.WIFI_MODE_FULL_LOW_LATENCY
            wifiLock = wifi.createWifiLock(mode, "NodexLM:rpc-wifi")
                .apply { setReferenceCounted(false); acquire() }
            Log.i(LOG, "Wi-Fi lock acquired; API=${Build.VERSION.SDK_INT}, " +
                "screen_off_supported_by_mode=${Build.VERSION.SDK_INT < 34}; " +
                "device power policy and OEM may still suspend connectivity")
        } catch (e: Exception) { Log.w(LOG, "Wi-Fi wake lock unavailable", e) }
    }

    private fun checkThermalsAndBattery(): String? {
        val power = getSystemService(POWER_SERVICE) as PowerManager
        val thermal = power.currentThermalStatus
        check(thermal < PowerManager.THERMAL_STATUS_SEVERE) {
            getString(R.string.energy_thermal)
        }
        // Native GGML does not support live thread-count reduction. Withdraw
        // instead of claiming a thermal throttle we cannot implement.
        if (thermal >= PowerManager.THERMAL_STATUS_MODERATE) return "thermal"
        val battery = getSystemService(BATTERY_SERVICE) as BatteryManager
        val percent = battery.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY)
        val state = registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
            ?.getIntExtra(BatteryManager.EXTRA_STATUS, -1) ?: -1
        val charging = state == BatteryManager.BATTERY_STATUS_CHARGING || state == BatteryManager.BATTERY_STATUS_FULL
        return energyPolicy.pauseReason(percent, charging,
            java.util.Calendar.getInstance().get(java.util.Calendar.HOUR_OF_DAY))
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
                android.net.LocalSocket().use { it.connect(android.net.LocalSocketAddress(rpcSocket,android.net.LocalSocketAddress.Namespace.FILESYSTEM)) }
                return
            } catch (_: Exception) { sleepWhileRunning(200) }
        }
        error("Native Android GGML RPC server did not start on loopback")
    }

    private fun pinnedSSLContext(): SSLContext =
        PinnedHostTls.context(PinnedHostTls.certificate(this))

    private fun sendHeartbeat(endpoint: String, node: String, token: String,
                              name: String, mode: String, ssl: SSLContext) {
        val memory = ActivityManager.MemoryInfo()
        (getSystemService(ACTIVITY_SERVICE) as ActivityManager).getMemoryInfo(memory)
        // Address space on ARMv7 is smaller even on devices with more physical RAM.
        val available = if (Build.SUPPORTED_ABIS.firstOrNull()?.let { Build.SUPPORTED_32_BIT_ABIS.contains(it) } == true) {
            minOf(memory.availMem, 2L * 1024 * 1024 * 1024)
        } else memory.availMem
        val power = getSystemService(POWER_SERVICE) as PowerManager
        val battery = getSystemService(BATTERY_SERVICE) as BatteryManager
        val percent = battery.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY)
        val batteryStatus = registerReceiver(null, IntentFilter(Intent.ACTION_BATTERY_CHANGED))
            ?.getIntExtra(BatteryManager.EXTRA_STATUS, -1) ?: -1
        val charging = batteryStatus == BatteryManager.BATTERY_STATUS_CHARGING ||
            batteryStatus == BatteryManager.BATTERY_STATUS_FULL
        val device = JSONObject()
            .put("status", if (draining.get()) "draining" else "online")
            .put("unavailable_reason", if (draining.get()) drainReason else "")
            .put("name", name.take(80))
            .put("platform", "android")
            .put("architecture", Build.SUPPORTED_ABIS.firstOrNull() ?: "arm64-v8a")
            .put("cpu_count", Runtime.getRuntime().availableProcessors())
            .put("ram_bytes", memory.totalMem)
            .put("ram_used_bytes", (memory.totalMem - memory.availMem).coerceAtLeast(0L))
            // Physical RAM is still available when compute is GPU-only;
            // disabling CPU does not make physical memory disappear.
            .put("available_ram_bytes", available)
            .put("max_contribution_bytes", getSharedPreferences("nodexlm", MODE_PRIVATE)
                .getInt("rpc_max_contribution_mib", 0).coerceIn(0, 65536).toLong() * 1024L * 1024L)
            .put("memory_reserve_bytes", getSharedPreferences("nodexlm", MODE_PRIVATE)
                .getInt("rpc_memory_reserve_mib", 1024).coerceIn(1024, 65536).toLong() * 1024L * 1024L)
            .put("thermal_status", power.currentThermalStatus)
            .put("charging", charging)
            // Only expose GPU memory when this worker allows Vulkan offload.
            // A CPU-only Guest must not make the Host allocate layers to GPU.
            .put("gpus", if (mode == "cpu") JSONArray() else try {
                val gpus = JSONObject(NativeRpc.inspect()).optJSONArray("gpus") ?: JSONArray()
                for (i in 0 until gpus.length()) {
                    val gpu = gpus.optJSONObject(i) ?: continue
                    // Vulkan heap counters on Android refer to shared physical RAM.
                    gpu.put("memory_type", "shared")
                    val total = gpu.optLong("vram_bytes", 0L)
                    val free = gpu.optLong("free_vram_bytes", -1L)
                    if (total > 0L && free >= 0L && free <= total) {
                        gpu.put("vram_used_bytes", total - free)
                    }
                    // Vulkan does not expose measured engine busy percentage here.
                }
                gpus
            } catch (e: Exception) {
                Log.w(LOG, "Vulkan metrics unavailable", e)
                JSONArray()
            })
            .put("runtime", JSONObject()
                .put("engine", when(mode) {
                    "cpu" -> "ggml-rpc-cpu"
                    "gpu" -> "ggml-rpc-vulkan"
                    else -> "ggml-rpc-cpu-vulkan"
                })
                .put("version", NativeRpc.VERSION)
                .put("installed", true)
                .put("benchmark", true)
                .put("reconnect_failures",reconnectFailures.get())
                .put("rpc_ready", false))
        if (percent in 0..100) device.put("battery_percent", percent)
        if (Build.VERSION.SDK_INT >= 30) {
            // Android limits polling of thermal headroom. Heartbeats are 15s apart.
            val headroom = power.getThermalHeadroom(10)
            if (headroom.isFinite() && headroom >= 0f && headroom <= 10f) {
                device.put("thermal_headroom", headroom.toDouble())
            }
        }
        cpuSampler.sample()?.let { device.put("cpu_usage_percent", it) }
        val connection = java.net.URL(endpoint.trimEnd('/') + "/v1/nodes/heartbeat")
            .openConnection() as HttpsURLConnection
        PinnedHostTls.configure(connection, this)
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
        var trailer = 0
        while (result.size() < 8192) {
            val next = input.read()
            if (next < 0) error("Host closed CONNECT")
            result.write(next)
            // Track CRLFCRLF without repeatedly copying the entire header buffer.
            trailer = (trailer shl 8) or next
            if (result.size() >= 4 && trailer == 0x0D0A0D0A) {
                return result.toString(Charsets.US_ASCII.name()).lineSequence().first()
            }
        }
        error("Oversized CONNECT response")
    }

    private fun connectAndRelay(slot: Int, host: String, port: Int, node: String,
                                token: String, ssl: SSLContext) {
        // Keep the concrete SSLSocket type: the generic socket tracker returns
        // Socket and would erase SSL parameters/handshake APIs at compile time.
        val remote = ssl.socketFactory.createSocket() as SSLSocket
        track(remote)
        try {
            remote.keepAlive = true
            remote.connect(InetSocketAddress(host, port), 7000)
            remote.soTimeout = 10_000
            val params = remote.sslParameters
            // Paired certificate pin is verified in PinnedHostTls.
            // Do not require the original IP in the self-signed SAN.
            params.endpointIdentificationAlgorithm = null
            try {
                // The native relay uses HTTP/1.1 CONNECT. Some older OEM
                // TLS providers lack ALPN; with no ALPN HTTP/1.1 is the fallback.
                params.applicationProtocols = arrayOf("http/1.1")
            } catch (e: UnsupportedOperationException) {
                Log.w(LOG, "TLS provider has no ALPN support; using HTTP/1.1 without ALPN", e)
            }
            remote.sslParameters = params
            remote.startHandshake()
            val negotiated = try { remote.applicationProtocol } catch (e: UnsupportedOperationException) {
                Log.w(LOG, "TLS provider cannot report ALPN protocol; using HTTP/1.1", e)
                null
            }
            check(negotiated.isNullOrEmpty() || negotiated == "http/1.1") {
                "RPC requires HTTP/1.1; Host negotiated $negotiated"
            }
            // The RPC binary stream is not exposed until the Host returns 200
            // and signals with a single byte that an authenticated relay is ready.
            val request = "CONNECT /v1/nodes/rpc/tunnel HTTP/1.1\r\n" +
                "Host: $host:$port\r\n" +
                "Authorization: Bearer $token\r\n" +
                "X-Nodex-Node: $node\r\n\r\n"
            remote.outputStream.write(request.toByteArray(Charsets.US_ASCII))
            remote.outputStream.flush()
            val status = readStatus(remote.inputStream)
            if (status.contains(" 401 ") || status.contains(" 403 ")) {
                throw SecurityException("Host denied authenticated RPC tunnel: ${status.take(80)}")
            }
            check(status.startsWith("HTTP/1.1 200 ") || status.startsWith("HTTP/1.0 200 ")) {
                "Host denied RPC tunnel: ${status.take(120)}"
            }
            remote.soTimeout = 0
            val marker = remote.inputStream.read()
            if (marker == 2) {
                remote.soTimeout = 15_000
                val input = java.io.DataInputStream(remote.inputStream)
                repeat(2) {
                    val length = input.readInt()
                    require(length in 1..(1 shl 20)) { "Invalid benchmark length" }
                    val buffer = ByteArray(16 * 1024)
                    var remaining = length
                    while (remaining > 0) {
                        val n = input.read(buffer, 0, minOf(remaining, buffer.size))
                        check(n > 0) { "Incomplete benchmark" }
                        remote.outputStream.write(buffer, 0, n)
                        remaining -= n
                    }
                    remote.outputStream.flush()
                }
                return
            }
            check(marker == 1) { "Missing Host RPC relay marker" }
            claimedNativeRelays.incrementAndGet()
            try {
            Log.i(LOG, "RPC tunnel $slot claimed by Host; opening private native RPC socket")
            val local = android.net.LocalSocket()
            localSockets.add(local)
            try {
                local.connect(android.net.LocalSocketAddress(rpcSocket,android.net.LocalSocketAddress.Namespace.FILESYSTEM))
                Log.i(LOG, "RPC tunnel $slot connected to native GGML RPC server")
                val toNative = AtomicLong(0)
                val fromNative = AtomicLong(0)
                val activatedAt = android.os.SystemClock.elapsedRealtime()
                // This timer observes only claimed RPC sessions. It never blocks,
                // disconnects, or retries a legitimate slow model initialization.
                // Report the actual first request/response bytes so a stuck HELLO
                // can be distinguished from TLS CONNECT and an idle tunnel.
                val watchdog = diagnostics.scheduleAtFixedRate({
                    val sent = toNative.get()
                    val received = fromNative.get()
                    if (running.get() && sent > 0L && received == 0L) {
                        Log.w(LOG, "RPC tunnel $slot: native server has not replied " +
                            "after ${(android.os.SystemClock.elapsedRealtime() - activatedAt) / 1000}s; " +
                            "host_to_native_bytes=$sent, native_to_host_bytes=$received")
                    }
                }, 20L, 30L, TimeUnit.SECONDS)
                try {
                    val sender = thread(name = "nodexlm-rpc-stream-up-$slot", isDaemon = true) {
                        try {
                            val input = local.getInputStream()
                            val output = remote.outputStream
                            val buffer = ByteArray(32 * 1024)
                            while (running.get()) {
                                val n = input.read(buffer)
                                if (n < 0) break
                                if (n == 0) continue
                                output.write(buffer, 0, n)
                                if (fromNative.getAndAdd(n.toLong()) == 0L) {
                                    Log.i(LOG, "RPC tunnel $slot received first native response ($n bytes)")
                                }
                            }
                        } catch (e: Exception) {
                            if (running.get()) Log.w(LOG, "RPC tunnel $slot native->Host relay failed", e)
                        } finally {
                            try { remote.close() } catch (_: Exception) { }
                        }
                    }
                    try {
                        val input = remote.inputStream
                        val output = local.getOutputStream()
                        val buffer = ByteArray(32 * 1024)
                        while (running.get()) {
                            val n = input.read(buffer)
                            if (n < 0) break
                            if (n == 0) continue
                            output.write(buffer, 0, n)
                            if (toNative.getAndAdd(n.toLong()) == 0L) {
                                Log.i(LOG, "RPC tunnel $slot delivered first Host request to native server ($n bytes)")
                            }
                        }
                    } catch (e: Exception) {
                        if (running.get()) Log.w(LOG, "RPC tunnel $slot Host->native relay failed", e)
                    } finally {
                        try { local.close() } catch (_: Exception) { }
                        try { remote.close() } catch (_: Exception) { }
                        sender.join(1000)
                    }
                } finally {
                    watchdog.cancel(false)
                    val sent = toNative.get()
                    val received = fromNative.get()
                    if (sent > 0L || received > 0L) {
                        Log.i(LOG, "RPC tunnel $slot closed; host_to_native_bytes=$sent, native_to_host_bytes=$received")
                    }
                }
            } finally {
                localSockets.remove(local)
                try { local.close() } catch (_: Exception) { }
            }
            } finally {
                claimedNativeRelays.decrementAndGet()
            }
        } finally {
            sockets.remove(remote)
            try { remote.close() } catch (_: Exception) {}
        }
    }

    private fun recordGuestEvent(state: String, reason: String = "") {
        try { RpcGuestDiagnostics.record(this, state, reason) }
        catch (e: Exception) { Log.w(LOG, "Cannot persist sanitized RPC Guest state", e) }
    }

    private fun shutdown(state: String = "stopped", reason: String = "service") {
        if (!stopping.compareAndSet(false, true)) return
        recordGuestEvent(state, reason)
        diagnostics.shutdownNow()
        running.set(false)
        synchronized(localSockets){localSockets.forEach {try{it.close()}catch(_:Exception){}};localSockets.clear()}
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
