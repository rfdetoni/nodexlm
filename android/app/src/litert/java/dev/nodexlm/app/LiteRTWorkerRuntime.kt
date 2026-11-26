package dev.nodexlm.app

import android.app.ActivityManager
import android.content.Context
import com.google.ai.edge.litertlm.Backend
import com.google.ai.edge.litertlm.Contents
import com.google.ai.edge.litertlm.Conversation
import com.google.ai.edge.litertlm.ConversationConfig
import com.google.ai.edge.litertlm.Engine
import com.google.ai.edge.litertlm.EngineConfig
import com.google.ai.edge.litertlm.Message
import com.google.ai.edge.litertlm.SamplerConfig
import com.google.ai.edge.litertlm.ThinkingConfig
import java.io.File
import java.util.concurrent.atomic.AtomicBoolean

/** A whole-model worker, never a GGML tensor RPC provider. The service owns
 * its lifetime and the same cancellation/lease protocol as GGUF workers.
 */
internal object LiteRTWorkerRuntime {
    const val ALIAS = "litert/active.litertlm"
    private var engine: Engine? = null
    private val guard = Any()
    private var active: Conversation? = null
    private val cancelled = AtomicBoolean(false)
    fun modelFile(context: Context) = File(context.filesDir,"litert/active.litertlm")

    fun load(context: Context, file: File, gpu: Boolean, threads: Int) {
        unload()
        cancelled.set(false)
        val info = ActivityManager.MemoryInfo()
        context.getSystemService(ActivityManager::class.java).getMemoryInfo(info)
        val reserve = GuestResourceBudget.reserveBytes(0,info.totalMem,info.threshold)
        require(MobileModelAdvice.fits(file.length(),MobileModelAdvice.budget(info.availMem,reserve,true))) { "Insufficient LiteRT memory budget" }
        val candidate = Engine(EngineConfig(modelPath=file.absolutePath,
            backend=if (gpu) Backend.GPU() else Backend.CPU(threadCount=threads),
            maxNumTokens=2048,cacheDir=context.cacheDir.absolutePath))
        try {
            candidate.initialize()
            check(!cancelled.get()) { "Worker stopped" }
            engine = candidate
        } catch (e: Throwable) { runCatching { candidate.close() };throw e }
    }

    fun generate(roles: Array<String>, contents: Array<String>, maxTokens: Int, temperature: Double, topP: Double,
                 stops: Array<String>, effort: String, budget: Int): String {
        require(roles.isNotEmpty() && roles.size==contents.size && roles.size<=64 && roles.last()=="user") { "LiteRT requires final user message" }
        require(contents.all { it.toByteArray(Charsets.UTF_8).size<=32768 }) { "Message too large" }
        cancelled.set(false)
        val history = roles.indices.dropLast(1).map { index ->
            when (roles[index]) {
                "system" -> Message.system(contents[index])
                "user" -> Message.user(contents[index])
                "assistant" -> Message.model(contents[index])
                else -> error("Unsupported role")
            }
        }
        val thinking = if (effort=="none") ThinkingConfig(enableThinking=false)
                       else if (budget>0) ThinkingConfig(thinkingTokenBudget=budget) else null
        val config = ConversationConfig(initialMessages=history,automaticToolCalling=false,
            samplerConfig=SamplerConfig(topK=40,topP=topP.coerceIn(0.0,1.0),temperature=temperature.coerceIn(0.0,2.0)),
            thinkingConfig=thinking)
        requireNotNull(engine).createConversation(config).use { conversation ->
            synchronized(guard) {
                check(!cancelled.get()) { "Cancelled" }
                active = conversation
            }
            try {
                val text = conversation.sendMessage(contents.last(),maxOutputToken=maxTokens,thinkingConfig=thinking).toString()
                check(!cancelled.get()) { "Cancelled" }
                val boundary = stops.filter { it.isNotEmpty() }.mapNotNull { text.indexOf(it).takeIf { i -> i>=0 } }.minOrNull() ?: text.length
                return text.substring(0,boundary)
            } finally { synchronized(guard) { active = null } }
        }
    }

    fun cancel() {
        cancelled.set(true)
        synchronized(guard) { active?.cancelProcess() }
    }

    fun unload() {
        engine?.close()
        engine = null
    }
}
