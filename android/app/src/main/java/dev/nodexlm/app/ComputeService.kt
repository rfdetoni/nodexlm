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
    companion object { const val START="dev.nodexlm.START";const val STOP="dev.nodexlm.STOP";private const val CHANNEL="nodexlm_worker" }
    private val running = AtomicBoolean(false)
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
        thread(name="nodexlm-worker",isDaemon=false) {workerLoop()}
        return START_STICKY
    }

    private fun stopWorker() {
        running.set(false)
        releaseWorkerWakeLock()
        try{NativeEntry.cancel()}catch (_:Throwable){}
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }
    override fun onDestroy() {
        running.set(false)
        releaseWorkerWakeLock()
        super.onDestroy()
    }

    private fun workerLoop() {
        var backoff=1000L
        val model=ModelFiles.selected(this)
        val coordinator=(prefs.getString("coordinator","")?:"").trimEnd('/')
        if (!coordinator.startsWith("https://")||model == null || !model.isFile){Log.e(logTag,"Invalid worker configuration");stopWorker();return}
        try {
            var id=prefs.getString("node_id","")?:""
            val token=NodeSecrets.read(this)
            if (id.isEmpty()||token.isEmpty()) error("Device not paired; enter the host's pairing code in the app first")
            var loaded=false
            val cpuSampler = CpuUsageSampler()
            var lastInference=android.os.SystemClock.elapsedRealtime()
            while(running.get()) {
                try {
                    val memory = ActivityManager.MemoryInfo()
                    (getSystemService(ACTIVITY_SERVICE) as ActivityManager).getMemoryInfo(memory)
                    val payload=JSONObject().put("models",JSONArray().put(prefs.getString("model","model.gguf")))
                        .put("ram_bytes",memory.totalMem).put("available_ram_bytes",memory.availMem)
                        .put("ram_used_bytes",(memory.totalMem-memory.availMem).coerceAtLeast(0L))
                    cpuSampler.sample()?.let { payload.put("cpu_usage_percent", it) }
                    val job=request(coordinator+"/v1/nodes/lease",token,id,payload)
                    if(job.length()==0) {
                        if(loaded && android.os.SystemClock.elapsedRealtime()-lastInference>120_000L) {
                            NativeEntry.unload()
                            loaded=false
                        }
                        continue
                    }
                    val jid=job.getString("id")
                    var answer="";var failure=""
                    val pm=getSystemService(POWER_SERVICE) as PowerManager
                    val wake=pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK,"NodexLM:inference")
                    // Token generation is time-bounded; never hold the lock while idle/polling.
                    wake.acquire(180_000L)
                    try {
                        if (!loaded) {
                            val init=NativeEntry.load(model!!.absolutePath,2048,maxOf(1,Runtime.getRuntime().availableProcessors()-2))
                            if(init.isNotEmpty())throw IllegalStateException(init)
                            loaded=true
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
                                    if(state.optString("status")!="running"||android.os.SystemClock.elapsedRealtime()>deadline){NativeEntry.cancel();break}
                                }catch(_:Exception){NativeEntry.cancel();break}
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
                            answer=String(NativeEntry.generate(roles,contents,(input.optInt("max_tokens",256)).coerceIn(1,1024),input.optDouble("temperature",0.7).toFloat(),input.optDouble("top_p",1.0).toFloat(),stops,sink),Charsets.UTF_8)
                            if(streaming){flushChunk(true);require(undecoded.isEmpty()){ "Incomplete UTF-8 output" }}
                        }finally{monitorDone.set(true);monitor.interrupt();monitor.join(1000)}
                        if(answer.startsWith("NODEXLM_ERROR:")){failure=answer;answer=""}
                    }catch(e:Exception){failure=e.message?:"inference error"}
                    finally {if(wake.isHeld)wake.release()}
                    lastInference=android.os.SystemClock.elapsedRealtime()
                    val result=JSONObject().put("result",answer).put("error",failure.take(1000))
                    request(coordinator+"/v1/nodes/result/$jid",token,id,result)
                    backoff=1000L
                }catch(e:Exception) {
                    Log.w(logTag,"Worker request failed (will retry): ${e.message}")
                    Thread.sleep(backoff)
                    backoff=min(backoff*2,30_000L)
                }
            }
        }catch(e:Exception){Log.e(logTag,"Worker stopped",e)}
        finally {
            try { NativeEntry.unload() } catch (_: Throwable) {}
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
