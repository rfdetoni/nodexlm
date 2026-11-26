// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.app.Notification
import android.app.ActivityManager
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.util.Log
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.security.KeyStore
import java.security.cert.CertificateFactory
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManagerFactory
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.concurrent.thread
import kotlin.math.min

/** Explicit user-started foreground worker; the UI process can leave the screen. */
class ComputeService : Service() {
    companion object { const val START="dev.nodexlm.START";const val STOP="dev.nodexlm.STOP";const val EXTRA_LITERT="litert";const val EXTRA_LITERT_GPU="litert_gpu";private const val CHANNEL="nodexlm_worker" }
    private val running = AtomicBoolean(false)
    // A STOP intent sent before any whole-model job must not load GGML/Vulkan
    // into the UI process merely to cancel a nonexistent inference.
    private val nativeTouched = AtomicBoolean(false)
    private val liteTouched = AtomicBoolean(false)
    private fun cancelInference() {
        if (liteTouched.get()) runCatching { LiteRTWorkerRuntime.cancel() }
        if (nativeTouched.get()) runCatching { NativeEntry.cancel() }
    }
    // This is an explicitly user-started foreground worker. Keep CPU/network
    // polling running through screen-off Doze, but never outside worker lifetime.
    private var workerWakeLock: PowerManager.WakeLock? = null
    private fun releaseWorkerWakeLock() {
        synchronized(this) {
            try { if (workerWakeLock?.isHeld == true) workerWakeLock?.release() }
            catch (e: Exception) { Log.w(logTag, "Wake lock release failed", e) }
            workerWakeLock = null
        }
    }
    private val prefs by lazy {getSharedPreferences("nodexlm",MODE_PRIVATE)}
    private val logTag="NodexLMWorker"
    override fun onBind(intent:Intent?):IBinder?=null

    override fun onStartCommand(intent:Intent?,flags:Int,startId:Int):Int {
        if (intent?.action==STOP) {stopWorker();return START_NOT_STICKY}
        if (!running.compareAndSet(false,true))return START_STICKY
        val useLiteRT = BuildConfig.LITERT_ENABLED && intent?.getBooleanExtra(EXTRA_LITERT,false) == true
        val useLiteGPU = intent?.getBooleanExtra(EXTRA_LITERT_GPU,false) == true
        RuntimeLogs.append(this, RuntimeRole.GUEST, RuntimeLevel.INFO, RuntimeEvent.WORKER_START)
        val manager=getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(NotificationChannel(CHANNEL,"NodexLM background inference",NotificationManager.IMPORTANCE_LOW))
        val stop=PendingIntent.getService(this,11,Intent(this,ComputeService::class.java).setAction(STOP),PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE)
        val notification=Notification.Builder(this,CHANNEL)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setContentTitle(getString(R.string.ui_7235bff56ab7))
            .setContentText(getString(R.string.ui_99e7969791c0))
            .setOngoing(true)
            .addAction(Notification.Action.Builder(null,"Stop",stop).build())
            .build()
        if (Build.VERSION.SDK_INT>=34) startForeground(42,notification,ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        else startForeground(42,notification)
        try {
            synchronized(this) {
                releaseWorkerWakeLock()
                workerWakeLock = (getSystemService(POWER_SERVICE) as PowerManager)
                    .newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "NodexLM:background-worker")
                    .apply { setReferenceCounted(false); acquire() }
            }
        } catch (e: Exception) {
            Log.e(logTag, "Cannot keep cluster polling active while screen is locked", e)
        }
        thread(name="nodexlm-worker",isDaemon=false) {
            val lease = MobileRuntimeLease.acquire(this)
            if (lease == null) { stopWorker(); return@thread }
            lease.use { if (running.get()) workerLoop(useLiteRT, useLiteGPU) }
        }
        return if (useLiteRT) START_NOT_STICKY else START_STICKY
    }

    private fun stopWorker() {
        running.set(false)
        RuntimeLogs.append(this, RuntimeRole.GUEST, RuntimeLevel.INFO, RuntimeEvent.WORKER_STOP)
        releaseWorkerWakeLock()
        Thread { cancelInference() }.start()
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }
    override fun onDestroy() {
        running.set(false)
        releaseWorkerWakeLock()
        super.onDestroy()
    }

    private fun workerLoop(useLiteRT: Boolean, useLiteGPU: Boolean) {
        var backoff=1000L
        val model=if (useLiteRT) LiteRTWorkerRuntime.modelFile(this) else ModelFiles.selected(this)
        val modelAlias=if (useLiteRT) LiteRTWorkerRuntime.ALIAS else model?.name.orEmpty()
        fun unloadModel() { if (useLiteRT) LiteRTWorkerRuntime.unload() else NativeEntry.unload() }
        val decisionModel = if (useLiteRT) "" else (prefs.getString("decision_model", "") ?: "").trim()
        val decisionPort = prefs.getInt("decision_port", MobileDecisionClient.DEFAULT_PORT)
        val decisionKey = try {
            if (decisionModel.isNotEmpty()) DecisionSecrets.read(this) else ""
        } catch (e: Exception) {
            Log.e(logTag, "Cannot decrypt local decision runtime credential", e)
            stopWorker()
            return
        }
        val coordinator=(prefs.getString("coordinator","")?:"").trimEnd('/')
        if (!coordinator.startsWith("https://") ||
            ((model == null || !model.isFile) && decisionModel.isEmpty())) {
            RuntimeLogs.append(this, RuntimeRole.GUEST, RuntimeLevel.ERROR, RuntimeEvent.WORKER_FAILED)
            Log.e(logTag,"Invalid worker configuration");stopWorker();return
        }
        try {
            var id=prefs.getString("node_id","")?:""
            val token=NodeSecrets.read(this)
            if (id.isEmpty()||token.isEmpty()) error("Device not paired; enter the host's pairing code in the app first")
            var loaded=false
            val cpuSampler = CpuUsageSampler()
            var lastInference=android.os.SystemClock.elapsedRealtime()
            while(running.get()) {
                try {
                    if (android.os.SystemClock.elapsedRealtime() - lastInference > 120_000L) {
                        if (loaded) {
                            unloadModel()
                            loaded = false
                        }
                        EmbeddedDecisionRuntime.close()
                    }
                    val memory = ActivityManager.MemoryInfo()
                    (getSystemService(ACTIVITY_SERVICE) as ActivityManager).getMemoryInfo(memory)
                    val embedded = decisionModel.isNotEmpty() &&
                        DecisionModelStore.find(this, decisionModel) != null
                    val decisionReady = decisionModel.isNotEmpty() && if (embedded) {
                        EmbeddedDecisionRuntime.available(this, decisionModel, allowLoad = !loaded)
                    } else {
                        MobileDecisionClient.available(decisionPort, decisionModel, decisionKey)
                    }
                    val models = JSONArray()
                    if (model?.isFile == true) models.put(modelAlias)
                    val payload=JSONObject().put("models", models)
                        .put("runtime",JSONObject().put("engine",if (useLiteRT) "litertlm" else "llama.cpp").put("installed",true).put("rpc_ready",false))
                        .put("decision_models", if (decisionReady) JSONArray().put(decisionModel) else JSONArray())
                        .put("ram_bytes",memory.totalMem).put("available_ram_bytes",memory.availMem)
                        .put("ram_used_bytes",(memory.totalMem-memory.availMem).coerceAtLeast(0L))
                    cpuSampler.sample()?.let { payload.put("cpu_usage_percent", it) }
                    val battery = (getSystemService(BATTERY_SERVICE) as android.os.BatteryManager)
                        .getIntProperty(android.os.BatteryManager.BATTERY_PROPERTY_CAPACITY)
                    if (battery in 0..100) payload.put("battery_percent", battery)
                    val batteryState = registerReceiver(null, android.content.IntentFilter(Intent.ACTION_BATTERY_CHANGED))
                    val powerStatus = batteryState?.getIntExtra(android.os.BatteryManager.EXTRA_STATUS, -1) ?: -1
                    if (powerStatus >= 0) payload.put("charging", powerStatus ==
                        android.os.BatteryManager.BATTERY_STATUS_CHARGING ||
                        powerStatus == android.os.BatteryManager.BATTERY_STATUS_FULL)
                    val thermal = (getSystemService(POWER_SERVICE) as PowerManager).currentThermalStatus
                    if (thermal in 0..6) payload.put("thermal_status", thermal)
                    val job=request(coordinator+"/v1/nodes/lease",token,id,payload)
                    if(job.length()==0) {
                        continue
                    }
                    val jid=job.getString("id")
                    RuntimeLogs.append(this, RuntimeRole.HOST, RuntimeLevel.INFO, RuntimeEvent.WORKER_JOB_RECEIVED)
                    var answer="";var failure=""
                    val pm=getSystemService(POWER_SERVICE) as PowerManager
                    val wake=pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK,"NodexLM:inference")
                    // Token generation is time-bounded; never hold the lock while idle/polling.
                    wake.acquire(180_000L)
                    try {
                        if (job.optString("kind") == "decision") {
                            require(decisionReady && job.getJSONObject("request").getString("model") == decisionModel) {
                                "Decision model not available on Android"
                            }
                            if (loaded) {
                                unloadModel()
                                loaded = false
                            }
                            answer = if (embedded) {
                                EmbeddedDecisionRuntime.decide(this, decisionModel,
                                    job.getJSONObject("decision")).toString()
                            } else {
                                MobileDecisionClient.decide(decisionPort, decisionModel,
                                    job.getJSONObject("decision"), decisionKey).toString()
                            }
                        } else {
                        require(model?.isFile == true) { "Chat model not selected" }
                        require(job.getJSONObject("request").getString("model")==modelAlias) { "Requested model not available" }
                        if (!loaded) {
                            EmbeddedDecisionRuntime.close()
                            check(running.get()) { "Worker stopped" }
                            val cores = Runtime.getRuntime().availableProcessors().coerceAtLeast(1)
                            val requestedThreads = prefs.getInt("rpc_threads", 0)
                            if (useLiteRT) {
                                liteTouched.set(true)
                                val threads = if (requestedThreads == 0) minOf(4,cores) else requestedThreads.coerceIn(1,minOf(8,cores))
                                LiteRTWorkerRuntime.load(this,requireNotNull(model),useLiteGPU,threads)
                            } else {
                                nativeTouched.set(true)
                                NativeRpc.initBackends(applicationInfo.nativeLibraryDir)
                                val threads = if (requestedThreads == 0) NativeRpc.recommendedThreads(cores)
                                              else requestedThreads.coerceIn(1, minOf(8, cores))
                                val useGpu = prefs.getString("rpc_mode", "auto") == "gpu"
                                val init=NativeEntry.load(model!!.absolutePath,2048,threads,useGpu)
                                if(init.isNotEmpty())throw IllegalStateException(init)
                            }
                            loaded=true
                            RuntimeLogs.append(this, RuntimeRole.GUEST, RuntimeLevel.INFO, RuntimeEvent.WORKER_MODEL_LOADED)
                        }
                        val input=job.getJSONObject("request")
                        val messages=input.getJSONArray("messages")
                        val roles=Array(messages.length()) { idx -> messages.getJSONObject(idx).getString("role") }
                        val contents=Array(messages.length()) { idx -> messages.getJSONObject(idx).getString("content") }
                        val monitorDone=java.util.concurrent.atomic.AtomicBoolean(false)
                        val monitor=thread(name="nodexlm-generation-lease",isDaemon=true){
                            val deadline=android.os.SystemClock.elapsedRealtime()+180_000L
                            while(!monitorDone.get()&&running.get()){
                                try{
                                    val state=request(coordinator+"/v1/nodes/jobs/$jid",token,id,null,"GET")
                                    if(state.optString("status")!="running"||android.os.SystemClock.elapsedRealtime()>deadline){cancelInference();break}
                                }catch(_:Exception){cancelInference();break}
                                try{Thread.sleep(1000)}catch(_:InterruptedException){break}
                            }
                        }
                        val pending=java.io.ByteArrayOutputStream()
                        val decoder=Charsets.UTF_8.newDecoder()
                        var undecoded=ByteArray(0)
                        var lastChunk=0L
                        val streaming=input.optBoolean("stream",false)
                        fun flushChunk(force:Boolean=false){
                            if(pending.size()==0)return
                            val now=android.os.SystemClock.elapsedRealtime()
                            if(!force&&now-lastChunk<50)return
                            val bytes=java.nio.ByteBuffer.wrap(undecoded+pending.toByteArray());pending.reset()
                            val chars=java.nio.CharBuffer.allocate(bytes.remaining()*2+2)
                            val decoded=decoder.decode(bytes,chars,false)
                            if(decoded.isError)decoded.throwException()
                            undecoded=ByteArray(bytes.remaining());bytes.get(undecoded);chars.flip()
                            val text=chars.toString()
                            if(text.isNotEmpty()){
                                val data=JSONObject().put("id",jid).put("object","chat.completion.chunk").put("model",input.getString("model"))
                                    .put("choices",JSONArray().put(JSONObject().put("index",0).put("delta",JSONObject().put("content",text))))
                                request(coordinator+"/v1/nodes/chunks/$jid",token,id,JSONObject().put("data",data));lastChunk=now
                            }
                        }
                        val stopValue=input.opt("stop")
                        val stops=when(stopValue){is String->arrayOf(stopValue);is JSONArray->Array(stopValue.length()){stopValue.getString(it)};else->emptyArray()}
                        try{
                            val sink=if(streaming)NativeEntry.TokenSink {piece -> pending.write(piece);flushChunk()} else null
                            val maxTokens = input.optInt("max_tokens",256).coerceIn(1,1024)
                            if (useLiteRT) {
                                answer = LiteRTWorkerRuntime.generate(roles,contents,maxTokens,input.optDouble("temperature",0.7),input.optDouble("top_p",1.0),stops,
                                    input.optString("reasoning_effort"),input.optInt("reasoning_budget_tokens"))
                                if (streaming && answer.isNotEmpty()) { pending.write(answer.toByteArray(Charsets.UTF_8));flushChunk(true) }
                            } else {
                                answer=String(NativeEntry.generate(roles,contents,maxTokens,input.optDouble("temperature",0.7).toFloat(),input.optDouble("top_p",1.0).toFloat(),stops,sink),Charsets.UTF_8)
                            }
                            if(streaming){flushChunk(true);require(undecoded.isEmpty()){ "Incomplete UTF-8 output" }}
                        }finally{monitorDone.set(true);monitor.interrupt();monitor.join(1000)}
                        if(answer.startsWith("NODEXLM_ERROR:")){failure=answer;answer=""}
                        }
                    }catch(e:Exception){failure=e.message?:"inference error"}
                    finally {if(wake.isHeld)wake.release()}
                    lastInference=android.os.SystemClock.elapsedRealtime()
                    val result=JSONObject().put("result",answer).put("error",failure.take(1000))
                    request(coordinator+"/v1/nodes/result/$jid",token,id,result)
                    RuntimeLogs.append(this, RuntimeRole.GUEST,
                        if (failure.isEmpty()) RuntimeLevel.INFO else RuntimeLevel.WARN,
                        if (failure.isEmpty()) RuntimeEvent.WORKER_JOB_DONE else RuntimeEvent.WORKER_JOB_FAILED)
                    backoff=1000L
                }catch(e:Exception) {
                    if (backoff == 1000L || backoff >= 30_000L) {
                        RuntimeLogs.append(this, RuntimeRole.HOST, RuntimeLevel.WARN, RuntimeEvent.WORKER_RETRY)
                    }
                    Log.w(logTag,"Worker request failed (will retry): ${e.message}")
                    Thread.sleep(backoff)
                    backoff=min(backoff*2,30_000L)
                }
            }
        }catch(e:Exception){
            RuntimeLogs.append(this, RuntimeRole.GUEST, RuntimeLevel.ERROR, RuntimeEvent.WORKER_FAILED)
            Log.e(logTag,"Worker stopped",e)
        }
        finally {
            if (nativeTouched.get()) try { NativeEntry.unload() } catch (_: Throwable) {}
            if (liteTouched.get()) runCatching { LiteRTWorkerRuntime.unload() }
            try { EmbeddedDecisionRuntime.close() } catch (e: Exception) {
                Log.w(logTag, "Could not close local ONNX decision session", e)
            }
            releaseWorkerWakeLock()
            stopSelf()
        }
    }

    private fun request(url:String,token:String,node:String,body:JSONObject?,method:String="POST"):JSONObject {
        val c=(URL(url).openConnection() as HttpURLConnection)
        if(c is HttpsURLConnection) {
            PinnedHostTls.configure(c, this)
        }
        c.requestMethod=method
        c.connectTimeout=7_000
        c.readTimeout=35_000
        c.doOutput=body!=null
        c.setRequestProperty("Content-Type","application/json")
        c.setRequestProperty("Authorization","Bearer $token")
        if(node.isNotEmpty())c.setRequestProperty("X-Nodex-Node",node)
        return try {
            if(body!=null)c.outputStream.use {out ->out.write(body.toString().toByteArray(Charsets.UTF_8))}
            if(c.responseCode==204) JSONObject()
            else if(c.responseCode in 200..299) c.inputStream.bufferedReader(Charsets.UTF_8).use {reader->JSONObject(reader.readText())}
            else throw IllegalStateException("coordinator HTTP ${c.responseCode}")
        } finally {c.disconnect()}
    }
}
