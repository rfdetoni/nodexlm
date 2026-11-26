package dev.nodexlm.app

import android.app.Activity
import android.app.ActivityManager
import android.content.Intent
import android.os.Bundle
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.concurrent.thread

/** Standalone GGUF chat: selected local model, explicit attachments, no network. */
class LocalChatActivity : Activity() {
    private val busy=AtomicBoolean(false)
    private val cancelled=AtomicBoolean(false)
    private val nativeTouched=AtomicBoolean(false)
    private var document=""
    private lateinit var status: TextView
    private lateinit var prompt: EditText
    private lateinit var gpu: CheckBox
    private lateinit var generate: Button
    private lateinit var attach: Button

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        title=getString(R.string.local_document_chat)
        val panel=LinearLayout(this).apply { orientation=LinearLayout.VERTICAL;setPadding(24,24,24,24) }
        panel.addView(TextView(this).apply { text=getString(R.string.document_help) })
        status=TextView(this)
        panel.addView(status)
        prompt=EditText(this).apply { hint=getString(R.string.litert_prompt) }
        panel.addView(prompt)
        gpu=CheckBox(this).apply { text=getString(R.string.litert_gpu) }
        panel.addView(gpu)
        attach=Button(this).apply {
            text=getString(R.string.document_attach)
            setOnClickListener { startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).apply { type="*/*";addCategory(Intent.CATEGORY_OPENABLE) },71) }
        }
        panel.addView(attach)
        panel.addView(Button(this).apply {
            text=getString(R.string.document_clear)
            setOnClickListener { if(!busy.get()) { document="";status.text="" } }
        })
        generate=Button(this).apply { text=getString(R.string.litert_run);setOnClickListener { runGeneration() } }
        panel.addView(generate)
        panel.addView(Button(this).apply { text=getString(R.string.litert_cancel);setOnClickListener { cancel() } })
        setContentView(ScrollView(this).apply { addView(panel) })
    }

    private fun runTask(action:()->String) {
        if(!busy.compareAndSet(false,true))return
        cancelled.set(false)
        generate.isEnabled=false;attach.isEnabled=false
        status.text=getString(R.string.litert_busy)
        thread(name="nodex-local-chat") {
            val answer=try { action() } catch(e:Exception) { e.message ?: "Operation failed" }
            catch(e:LinkageError) { "Native runtime unavailable" }
            finally { busy.set(false) }
            runOnUiThread {
                if(!isDestroyed&&!isFinishing) { status.text=answer;generate.isEnabled=true;attach.isEnabled=true }
            }
        }
    }

    @Deprecated("Android file picker callback")
    override fun onActivityResult(requestCode:Int,resultCode:Int,data:Intent?) {
        super.onActivityResult(requestCode,resultCode,data)
        if(requestCode!=71 || resultCode!=RESULT_OK)return
        val uri=data?.data?:return
        runTask {
            val text=MobileDocuments.read(this,uri)
            check(!cancelled.get()) { "Cancelled" }
            document=text
            getString(R.string.document_attached,text.toByteArray(Charsets.UTF_8).size)
        }
    }

    private fun runGeneration() {
        val text=prompt.text.toString().trim()
        val useGpu=gpu.isChecked
        if(text.isEmpty())return
        runTask {
            val file=ModelFiles.selected(this) ?: error("Select a GGUF model first")
            val input=MobileDocuments.prompt(text,document)
            val lease=MobileRuntimeLease.acquire(this) ?: error("Another mobile runtime is active")
            lease.use {
                val memory=ActivityManager.MemoryInfo()
                getSystemService(ActivityManager::class.java).getMemoryInfo(memory)
                val reserve=GuestResourceBudget.reserveBytes(0,memory.totalMem,memory.threshold)
                require(MobileModelAdvice.fits(ModelFiles.modelBytes(file),MobileModelAdvice.budget(memory.availMem,reserve,android.os.Process.is64Bit()))) { "Insufficient estimated memory" }
                check(!cancelled.get()) { "Cancelled" }
                NativeRpc.initBackends(applicationInfo.nativeLibraryDir)
                nativeTouched.set(true)
                try {
                    val cores=Runtime.getRuntime().availableProcessors()
                    val threads=NativeRpc.recommendedThreads(cores)
                    val err=NativeEntry.load(file.absolutePath,2048,threads,useGpu)
                    check(err.isEmpty()) { err }
                    check(!cancelled.get()) { "Cancelled" }
                    val answer=String(NativeEntry.generate(arrayOf("user"),arrayOf(input),256,0.7f,1f,emptyArray(),null),Charsets.UTF_8)
                    check(!cancelled.get()) { "Cancelled" }
                    answer
                } finally { NativeEntry.unload();nativeTouched.set(false) }
            }
        }
    }

    private fun cancel() {
        cancelled.set(true)
        if(nativeTouched.get()) runCatching { NativeEntry.cancel() }
    }
    override fun onStop() { super.onStop();if(busy.get())cancel() }
}
