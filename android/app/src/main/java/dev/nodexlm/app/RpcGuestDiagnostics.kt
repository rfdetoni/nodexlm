package dev.nodexlm.app

import android.app.ActivityManager
import android.content.Context
import android.os.BatteryManager
import android.os.Build
import android.os.PowerManager
import android.util.AtomicFile
import org.json.JSONObject
import java.io.File

/**
 * Small, opt-in diagnostic export. Never records the coordinator address,
 * enrollment ID, certificate, token, prompts, model or tensor contents.
 * State is the last *recorded* event, not a live process-health guarantee.
 */
internal object RpcGuestDiagnostics {
    private const val FILE = "rpc-guest-state.json"

    fun record(context: Context, state: String, reason: String = "") {
        // This is written only at lifecycle transitions, not every heartbeat.
        val status = JSONObject()
            .put("state", state.take(32))
            .put("reason", reason.take(64))
            .put("timestamp_ms", System.currentTimeMillis())
        val atomic = AtomicFile(File(context.filesDir, FILE))
        val output = atomic.startWrite()
        try {
            output.write(status.toString().toByteArray(Charsets.UTF_8))
            atomic.finishWrite(output)
        } catch (e: Exception) {
            atomic.failWrite(output)
            throw e
        }
    }

    fun report(context: Context): String {
        val manager = context.getSystemService(Context.ACTIVITY_SERVICE) as ActivityManager
        val memory = ActivityManager.MemoryInfo()
        manager.getMemoryInfo(memory)
        val power = context.getSystemService(Context.POWER_SERVICE) as PowerManager
        val battery = context.getSystemService(Context.BATTERY_SERVICE) as BatteryManager
        val percent = battery.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY)
        val prefs = context.getSharedPreferences("nodexlm", Context.MODE_PRIVATE)
        val last = try {
            AtomicFile(File(context.filesDir, FILE)).openRead()
                .bufferedReader().use { JSONObject(it.readText()) }
        } catch (_: Exception) {
            JSONObject().put("state", "unknown")
        }
        val report = JSONObject()
            .put("schema", 1)
            .put("physical_acceptance", false)
            .put("platform", "android")
            .put("manufacturer", Build.MANUFACTURER.take(64))
            .put("model", Build.MODEL.take(80))
            .put("sdk", Build.VERSION.SDK_INT)
            .put("architecture", Build.SUPPORTED_ABIS.firstOrNull() ?: "unknown")
            .put("runtime_revision", BuildConfig.LLAMA_VERSION)
            .put("available_ram_bytes", memory.availMem)
            .put("physical_ram_bytes", memory.totalMem)
            .put("cpu_count", Runtime.getRuntime().availableProcessors())
            .put("thermal_status", power.currentThermalStatus)
            .put("battery_percent", if (percent in 0..100) percent else JSONObject.NULL)
            .put("configured_backend", prefs.getString("rpc_mode", "auto"))
            .put("configured_threads", prefs.getInt("rpc_threads", 0))
            .put("max_contribution_mib", prefs.getInt("rpc_max_contribution_mib", 0))
            .put("memory_reserve_mib", prefs.getInt("rpc_memory_reserve_mib", 1024))
            .put("last_recorded_guest_event", last)
        return report.toString(2)
    }
}
