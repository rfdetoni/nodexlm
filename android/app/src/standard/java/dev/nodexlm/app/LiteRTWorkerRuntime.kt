package dev.nodexlm.app

import android.content.Context
import java.io.File

@Suppress("UNUSED_PARAMETER")
internal object LiteRTWorkerRuntime {
    const val ALIAS = "litert/active.litertlm"
    fun modelFile(context: Context) = File(context.filesDir,"litert/active.litertlm")
    fun load(context: Context, file: File, gpu: Boolean, threads: Int): Unit = error("LiteRT SDK not bundled")
    fun generate(roles: Array<String>, contents: Array<String>, maxTokens: Int, temperature: Double, topP: Double,
                 stops: Array<String>, effort: String, budget: Int): String = error("LiteRT SDK not bundled")
    fun cancel() = Unit
    fun unload() = Unit
}
