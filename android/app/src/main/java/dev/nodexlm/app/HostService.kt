package dev.nodexlm.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import java.io.File
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.concurrent.thread

/** The bundled Go Host is the existing coordinator, dashboard and RPC planner.
 * Its executable and pinned llama-server are read-only APK-native payloads.
 */
class HostService : Service() {
    companion object {
        const val START = "dev.nodexlm.HOST_START"
        const val STOP = "dev.nodexlm.HOST_STOP"
        private const val CHANNEL = "nodexlm_host"
        fun status(context: Context): String = runCatching { File(context.filesDir,"host/status").readText().take(256) }.getOrDefault("")
    }
    private val running = AtomicBoolean(false)
    @Volatile private var child: java.lang.Process? = null
    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == STOP) {
            running.set(false)
            child?.destroy()
            stopSelf()
            return START_NOT_STICKY
        }
        if (intent?.action != START || !BuildConfig.HOST_ENABLED || !android.os.Process.is64Bit()) {
            stopSelf()
            return START_NOT_STICKY
        }
        if (!running.compareAndSet(false,true)) return START_NOT_STICKY
        getSystemService(NotificationManager::class.java).createNotificationChannel(NotificationChannel(CHANNEL,"NodexLM Host",NotificationManager.IMPORTANCE_LOW))
        val stop = PendingIntent.getService(this,61,Intent(this,HostService::class.java).setAction(STOP),PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val notification = Notification.Builder(this,CHANNEL).setSmallIcon(android.R.drawable.stat_notify_sync)
            .setContentTitle(getString(R.string.android_host_title)).setOngoing(true)
            .addAction(Notification.Action.Builder(null,getString(R.string.android_host_stop),stop).build()).build()
        if (Build.VERSION.SDK_INT >= 34) startForeground(61,notification,ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        else startForeground(61,notification)
        thread(name="nodexlm-host") { runHost() }
        return START_NOT_STICKY
    }

    private fun runHost() {
        val home = File(filesDir,"host").apply { mkdirs() }
        val status = File(home,"status")
        val telemetry = File(home,"memory.json")
        fun publishMemory() {
            val memory=android.app.ActivityManager.MemoryInfo()
            getSystemService(android.app.ActivityManager::class.java).getMemoryInfo(memory)
            val reserve=GuestResourceBudget.reserveBytes(0,memory.totalMem,memory.threshold)
            val stage=File(home,"memory.tmp")
            stage.writeText(org.json.JSONObject().put("total",memory.totalMem).put("available",memory.availMem).put("reserve",reserve).toString())
            android.system.Os.rename(stage.absolutePath,telemetry.absolutePath)
        }
        fun record(text: String) { runCatching { status.writeText(text) } }
        val lease = MobileRuntimeLease.acquire(this)
        if (lease == null) {
            record("Host not started: another mobile runtime is active")
            running.set(false);stopSelf();return
        }
        lease.use {
            val lock = getSystemService(PowerManager::class.java).newWakeLock(PowerManager.PARTIAL_WAKE_LOCK,"NodexLM:host")
            try {
                if (!running.get()) return
                val native = File(applicationInfo.nativeLibraryDir)
                val executable = File(native,"libnodexlm_host.so")
                val llama = File(native,"libnodexlm_llama_server.so")
                require(executable.canExecute() && llama.canExecute()) { "Host binaries not available in this APK" }
                lock.setReferenceCounted(false)
                lock.acquire()
                publishMemory()
                var lastMemory=android.os.SystemClock.elapsedRealtime()
                val command = ProcessBuilder(executable.absolutePath,"host","--dir",home.absolutePath,
                    "--model-dir",ModelFiles.directory(this).absolutePath,"--llama-bin",llama.absolutePath,
                    "--rpc-transport","tcp","--web-addr","127.0.0.1:8787")
                command.environment().apply {
                    put("HOME",home.absolutePath);put("NODEXLM_HOME",home.absolutePath);put("TMPDIR",cacheDir.absolutePath)
                    put("NODEXLM_ANDROID_PARENT_PID",android.os.Process.myPid().toString())
                    put("NODEXLM_ANDROID_MEMORY_FILE",telemetry.absolutePath)
                    remove("NODEXLM_ADMIN_TOKEN");remove("NODEXLM_JOIN_TOKEN")
                }
                // Startup output can contain temporary pairing credentials. Never put it in Logcat.
                command.redirectErrorStream(true).redirectOutput(ProcessBuilder.Redirect.to(File("/dev/null")))
                val process = command.start()
                child = process
                record("Host starting: http://127.0.0.1:8787")
                if (!running.get()) process.destroy()
                while (running.get() && !process.waitFor(1,TimeUnit.SECONDS)) {
                    val now=android.os.SystemClock.elapsedRealtime()
                    if(now-lastMemory>=15_000) { runCatching { publishMemory() };lastMemory=now }
                }
                if (process.isAlive) {
                    process.destroy()
                    if (!process.waitFor(12,TimeUnit.SECONDS)) { process.destroyForcibly();process.waitFor() }
                }
                record(if (running.get()) "Host exited (${process.exitValue()})" else "Host stopped")
            } catch (e: Exception) {
                record("Host error: ${e.javaClass.simpleName}")
            } finally {
                child?.let { if (it.isAlive) { it.destroyForcibly();runCatching { it.waitFor() } } }
                child = null
                if (lock.isHeld) lock.release()
                running.set(false)
                stopSelf()
            }
        }
    }

    override fun onDestroy() {
        running.set(false)
        child?.destroy()
        stopForeground(STOP_FOREGROUND_REMOVE)
        super.onDestroy()
    }
}
