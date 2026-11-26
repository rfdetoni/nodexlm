package dev.nodexlm.app

import android.app.Activity
import android.app.ActivityManager
import android.content.Intent
import android.os.Bundle
import android.provider.OpenableColumns
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import com.google.ai.edge.litertlm.Backend
import com.google.ai.edge.litertlm.Conversation
import com.google.ai.edge.litertlm.Engine
import com.google.ai.edge.litertlm.EngineConfig
import java.io.File
import java.io.FileOutputStream
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.concurrent.thread

/** Explicit standalone SDK execution. Never advertises LiteRT as a GGML RPC device. */
class LiteRTActivity : Activity() {
    private val model by lazy { File(filesDir, "litert/active.litertlm") }
    private val busy = AtomicBoolean(false)
    private val cancelled = AtomicBoolean(false)
    private val conversationGuard = Any()
    private var conversation: Conversation? = null
    private lateinit var status: TextView
    private lateinit var prompt: EditText
    private lateinit var gpu: CheckBox
    private lateinit var generate: Button
    private lateinit var importButton: Button

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (!android.os.Process.is64Bit()) { finish(); return }
        title = getString(R.string.litert_title)
        val panel = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(24, 24, 24, 24)
        }
        panel.addView(TextView(this).apply { text = getString(R.string.litert_help) })
        status = TextView(this)
        panel.addView(status)
        importButton = Button(this).apply {
            text = getString(R.string.litert_import)
            setOnClickListener {
                startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                    type = "application/octet-stream"
                    addCategory(Intent.CATEGORY_OPENABLE)
                }, 41)
            }
        }
        panel.addView(importButton)
        gpu = CheckBox(this).apply { text = getString(R.string.litert_gpu) }
        panel.addView(gpu)
        prompt = EditText(this).apply { hint = getString(R.string.litert_prompt) }
        panel.addView(prompt)
        generate = Button(this).apply {
            text = getString(R.string.litert_run)
            setOnClickListener { runInference() }
        }
        panel.addView(generate)
        panel.addView(Button(this).apply {
            text = getString(R.string.litert_cancel)
            setOnClickListener { cancelInference() }
        })
        setContentView(ScrollView(this).apply { addView(panel) })
        idle()
    }

    private fun idle() {
        status.text = getString(if (model.isFile) R.string.litert_ready else R.string.litert_missing)
        generate.isEnabled = model.isFile
        importButton.isEnabled = true
    }

    private fun launch(action: () -> String) {
        if (!busy.compareAndSet(false, true)) return
        cancelled.set(false)
        generate.isEnabled = false
        importButton.isEnabled = false
        status.text = getString(R.string.litert_busy)
        thread(name = "nodexlm-litert") {
            val result = try { action() } catch (e: Exception) {
                getString(R.string.litert_error, e.message ?: "error")
            } catch (e: LinkageError) {
                getString(R.string.litert_error, "Native SDK unavailable")
            } finally { busy.set(false) }
            runOnUiThread {
                if (!isDestroyed && !isFinishing) {
                    idle()
                    status.text = result
                }
            }
        }
    }

    @Deprecated("Android file picker callback")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != 41 || resultCode != RESULT_OK) return
        val uri = data?.data ?: return
        launch {
            val name = contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use {
                if (it.moveToFirst()) it.getString(0) else null
            } ?: error("Missing filename")
            require(name.endsWith(".litertlm", true)) { "Choose a .litertlm model" }
            val parent = requireNotNull(model.parentFile)
            check(parent.isDirectory || parent.mkdirs())
            val stage = File.createTempFile(".import-", ".tmp", parent)
            try {
                val limit = 8L * 1024 * 1024 * 1024
                val buffer = ByteArray(256 * 1024)
                var bytes = 0L
                contentResolver.openInputStream(uri)?.use { input ->
                    FileOutputStream(stage).use { output ->
                        while (true) {
                            check(!cancelled.get()) { "Cancelled" }
                            val n = input.read(buffer)
                            if (n < 0) break
                            bytes += n
                            require(bytes <= limit) { "Model exceeds 8 GiB" }
                            output.write(buffer, 0, n)
                        }
                        output.fd.sync()
                    }
                } ?: error("Cannot read model")
                require(bytes > 0 && !cancelled.get()) { "Empty or cancelled import" }
                // Same-directory rename publishes only a complete copy. SDK validates format on load.
                android.system.Os.rename(stage.absolutePath, model.absolutePath)
                getString(R.string.litert_ready)
            } finally { stage.delete() }
        }
    }

    private fun runInference() {
        val text = prompt.text.toString().trim()
        val useGpu = gpu.isChecked
        if (text.isEmpty() || text.length > 8192 || !model.isFile) return
        launch {
            val memory = ActivityManager.MemoryInfo()
            getSystemService(ActivityManager::class.java).getMemoryInfo(memory)
            val reserve = GuestResourceBudget.reserveBytes(0, memory.totalMem, memory.threshold)
            require(MobileModelAdvice.fits(model.length(), MobileModelAdvice.budget(memory.availMem, reserve, true))) {
                "Insufficient estimated available RAM"
            }
            val config = EngineConfig(
                modelPath = model.absolutePath,
                backend = if (useGpu) Backend.GPU() else Backend.CPU(),
                maxNumTokens = 2048,
                cacheDir = cacheDir.absolutePath
            )
            Engine(config).use { engine ->
                check(!cancelled.get()) { "Cancelled" }
                engine.initialize()
                check(!cancelled.get()) { "Cancelled" }
                engine.createConversation().use { session ->
                    synchronized(conversationGuard) {
                        check(!cancelled.get()) { "Cancelled" }
                        conversation = session
                    }
                    try {
                        session.sendMessage(text, maxOutputToken = 256).toString()
                    } finally {
                        synchronized(conversationGuard) { conversation = null }
                    }
                }
            }
        }
    }

    private fun cancelInference() {
        cancelled.set(true)
        // Native cancellation may block; never execute it on the UI thread.
        thread(name = "nodexlm-litert-cancel") {
            synchronized(conversationGuard) { runCatching { conversation?.cancelProcess() } }
        }
    }

    override fun onStop() {
        super.onStop()
        if (busy.get()) cancelInference()
    }
}
