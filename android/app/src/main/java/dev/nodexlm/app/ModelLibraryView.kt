// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.app.Activity
import android.app.AlertDialog
import android.app.DownloadManager
import android.content.Context
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import java.io.File
import kotlin.concurrent.thread

/** Native, low-overhead Material-inspired library; heavyweight work is always off the UI thread. */
internal class ModelLibraryView(
    private val activity: Activity,
    private val importPicker: () -> Unit,
    private val onSelected: (File) -> Unit
) : LinearLayout(activity) {
    private val hub = HuggingFaceHub(activity)
    private val downloads = activity.getSystemService(DownloadManager::class.java)
    private val prefs = activity.getSharedPreferences("nodexlm_hf_download",Context.MODE_PRIVATE)
    private val main = Handler(Looper.getMainLooper())
    private val query = EditText(activity)
    private val results = LinearLayout(activity)
    private val library = LinearLayout(activity)
    private val status = TextView(activity)
    private val bar = ProgressBar(activity,null,android.R.attr.progressBarStyleHorizontal)
    private val downloadProgress = TextView(activity)
    private lateinit var cancel: Button
    private var finding = false
    private var verifying = false
    private val tick = object : Runnable {
        override fun run() {
            refreshProgress()
            main.postDelayed(this,1200)
        }
    }

    init {
        orientation = VERTICAL
        setPadding(0,12,0,28)
        label(activity.getString(R.string.ui_fe41cc165939),26,true)
        label(activity.getString(R.string.ui_96e8d213ee6c),13,false)
        space(14)
        val searchCard = card()
        query.setSingleLine(true)
        query.background = android.graphics.drawable.GradientDrawable().apply {
            setColor(0xff23272b.toInt())
            cornerRadius = 9 * resources.displayMetrics.density
            setStroke((1 * resources.displayMetrics.density).toInt(), 0xff3c434a.toInt())
        }
        query.setPadding(14 * resources.displayMetrics.density.toInt().coerceAtLeast(1), 0,
            14 * resources.displayMetrics.density.toInt().coerceAtLeast(1), 0)
        query.setText("Qwen")
        query.setTextColor(0xffd9dee3.toInt())
        query.setHintTextColor(0xff969faa.toInt())
        query.hint = activity.getString(R.string.query_hint)
        searchCard.addView(query)
        searchCard.addView(action(activity.getString(R.string.ui_76a328da1ce2)) { search() })
        addView(searchCard)
        space(12)
        val progressCard = card()
        status.text = activity.getString(R.string.ui_17b418d5924d)
        status.setTextColor(0xffacb5be.toInt())
        progressCard.addView(status)
        bar.max = 1000
        bar.visibility = GONE
        progressCard.addView(bar,LayoutParams(LayoutParams.MATCH_PARENT,10).apply {topMargin=10})
        downloadProgress.setTextColor(0xffaab8c5.toInt())
        progressCard.addView(downloadProgress)
        cancel = action(activity.getString(R.string.ui_cdd64d8d4333)) { cancelDownload() }
        cancel.visibility = GONE
        progressCard.addView(cancel)
        addView(progressCard)
        space(14)
        label(activity.getString(R.string.ui_608b33b7803e),19,true)
        results.orientation = VERTICAL
        addView(results)
        space(20)
        label(activity.getString(R.string.ui_308b5f1f24ae),19,true)
        val importButton = action(activity.getString(R.string.ui_18911140a84b)) { importPicker() }
        addView(importButton)
        library.orientation = VERTICAL
        addView(library)
        refreshLibrary()
    }

    override fun onAttachedToWindow() { super.onAttachedToWindow(); main.removeCallbacks(tick);main.post(tick) }
    override fun onDetachedFromWindow() { main.removeCallbacks(tick);super.onDetachedFromWindow() }

    fun importFromUri(uri: Uri) = importFromUris(listOf(uri))
    fun importFromUris(uris: List<Uri>) {
        status.text = activity.getString(R.string.ui_a8843f6c285c)
        thread(name="nodexlm-import") {
            try {
                val file = ModelFiles.importSet(activity,uris)
                activity.runOnUiThread {
                    status.text = activity.getString(R.string.import_done,file.name)
                    refreshLibrary()
                    onSelected(file)
                }
            } catch (e:Exception) {
                activity.runOnUiThread {status.text=activity.getString(R.string.import_failed,e.message)}
            }
        }
    }

    private fun search() {
        if (finding) return
        finding = true
        status.text=activity.getString(R.string.ui_5a0c39e51aa3)
        results.removeAllViews()
        val requested = query.text.toString()
        thread(name="nodexlm-hf-search") {
            try {
                val models = hub.search(requested)
                activity.runOnUiThread {
                    if (!isAttachedToWindow) { finding = false; return@runOnUiThread }
                    results.removeAllViews()
                    for (model in models) showCandidate(model)
                    status.text = activity.getString(R.string.search_count,models.size)
                    finding=false
                }
            } catch (e:Exception) {
                activity.runOnUiThread {status.text = activity.getString(R.string.search_failed,e.message);finding=false}
            }
        }
    }

    private fun showCandidate(model: HuggingFaceHub.Model) {
        val row=card()
        row.addView(text(model.filename,16,true))
        row.addView(text("${model.repository}  ·  ${gib(model.sizeBytes)}",13,false))
        val suitable=model.placement=="single-node-candidate"
        row.addView(text(when(model.placement) {
            "single-node-candidate" -> activity.getString(R.string.placement_single)
            "distributed-candidate" -> activity.getString(R.string.placement_distributed)
            "insufficient-memory" -> activity.getString(R.string.placement_memory)
            else -> activity.getString(R.string.placement_unknown)
        },12,false).apply {setTextColor(if(suitable)0xffaab8c5.toInt() else 0xffb6a99b.toInt())})
        row.addView(action(activity.getString(R.string.download_size,gib(model.sizeBytes))) { confirmDownload(model) })
        results.addView(row)
    }

    private fun confirmDownload(model: HuggingFaceHub.Model) {
        if(prefs.getLong("id",0L)>0L) {status.text=activity.getString(R.string.ui_c1234618156b);return}
        if(!model.url.startsWith("https://huggingface.co/") || !model.url.contains("/resolve/main/")) {
            status.text=activity.getString(R.string.ui_e226da7e5057);return
        }
        AlertDialog.Builder(activity)
            .setTitle(activity.getString(R.string.download_confirm,model.filename.substringAfterLast('/')))
            .setMessage(activity.getString(R.string.download_explanation,gib(model.sizeBytes)))
            .setPositiveButton(activity.getString(R.string.ui_b45ae836a031)) { _,_ -> download(model) }
            .setNegativeButton(activity.getString(R.string.ui_bb9dbb406dcb),null)
            .show()
    }

    private fun download(model: HuggingFaceHub.Model) {
        try {
            val fileName = ModelFiles.safeFilename(model.filename.substringAfterLast('/'))
            val root=ModelFiles.directory(activity)
            val target=File(root,fileName)
            require(!target.exists()) { activity.getString(R.string.model_exists) }
            require(root.usableSpace >= model.sizeBytes) { "Insufficient free storage" }
            val parts=ModelFiles.shards(fileName)
            prefs.edit().putString("parts",org.json.JSONArray(parts).toString()).putInt("part_index",0)
                .putString("source",model.url).putString("first",fileName).putLong("size",model.sizeBytes).commit()
            enqueuePart(model.url,parts.first(),model.repository)
            return
        } catch(e:Exception) {status.text=activity.getString(R.string.download_failed,e.message)}
    }
    private fun enqueuePart(source: String,fileName: String,repository: String) {
        val root=ModelFiles.directory(activity)
        val target=File(root,fileName)
        val staging=File(root,".${fileName}.download")
            require(!staging.exists()) { activity.getString(R.string.model_downloading) }
            val request=DownloadManager.Request(Uri.parse(source))
                .setTitle("NodexLM: $fileName")
                .setDescription(activity.getString(R.string.model_source,repository))
                .setMimeType("application/octet-stream")
                .setDestinationUri(Uri.fromFile(staging))
                .setAllowedOverRoaming(false)
                .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            val id=downloads.enqueue(request)
            prefs.edit().putLong("id",id).putString("staging",staging.absolutePath)
                .putString("target",target.absolutePath).putString("current_source",source).apply()
            status.text=activity.getString(R.string.download_started,fileName)
            refreshProgress()
    }

    private fun refreshProgress() {
        val id=prefs.getLong("id",0L)
        if (id<=0L) {bar.visibility=GONE;cancel.visibility=GONE;return}
        bar.visibility=VISIBLE
        cancel.visibility=VISIBLE
        try {
            val cursor=downloads.query(DownloadManager.Query().setFilterById(id)) ?: return
            cursor.use { c ->
                if(!c.moveToFirst()) {clearDownload();return}
                val state=c.getInt(c.getColumnIndexOrThrow(DownloadManager.COLUMN_STATUS))
                val bytes=c.getLong(c.getColumnIndexOrThrow(DownloadManager.COLUMN_BYTES_DOWNLOADED_SO_FAR))
                val total=c.getLong(c.getColumnIndexOrThrow(DownloadManager.COLUMN_TOTAL_SIZE_BYTES))
                if(total>0) {bar.isIndeterminate=false;bar.progress=(bytes*1000/total).toInt().coerceIn(0,1000)}
                else bar.isIndeterminate=true
                downloadProgress.text="${gib(bytes)} / ${if(total>0)gib(total) else "?"}"
                when(state) {
                    DownloadManager.STATUS_SUCCESSFUL -> completeDownload()
                    DownloadManager.STATUS_FAILED -> {status.text=activity.getString(R.string.ui_24184bb0704d);clearDownload()}
                    DownloadManager.STATUS_PAUSED -> status.text=activity.getString(R.string.ui_0168ed1ca821)
                }
            }
        }catch(e:Exception){status.text=activity.getString(R.string.download_status_failed,e.message)}
    }

    private fun completeDownload() {
        if(verifying)return
        verifying=true
        val downloadId=prefs.getLong("id",0L)
        val file=File(prefs.getString("staging","")?:"")
        val source=prefs.getString("current_source","")?:""
        thread(name="nodexlm-verify-shard") {
            try {
                require(ModelFiles.validGGUF(file)) { "Invalid GGUF shard" }
                val connection=java.net.URL(source).openConnection() as javax.net.ssl.HttpsURLConnection
                connection.requestMethod="HEAD";connection.instanceFollowRedirects=false
                connection.connectTimeout=8000;connection.readTimeout=15000
                val expected=try {connection.connect();connection.getHeaderField("X-Linked-Etag")?.trim('"')} finally {connection.disconnect()}
                if(expected!=null&&expected.matches(Regex("[0-9a-fA-F]{64}"))){
                    val digest=java.security.MessageDigest.getInstance("SHA-256")
                    file.inputStream().use {input -> val buffer=ByteArray(256*1024);while(true){val n=input.read(buffer);if(n<0)break;digest.update(buffer,0,n)}}
                    val actual=digest.digest().joinToString(""){"%02x".format(it.toInt() and 255)}
                    require(actual.equals(expected,true)) { "GGUF SHA-256 mismatch" }
                }
                activity.runOnUiThread {verifying=false;if(isAttachedToWindow&&prefs.getLong("id",0L)==downloadId)finishDownload()}
            }catch(e:Exception){activity.runOnUiThread {verifying=false;if(isAttachedToWindow&&prefs.getLong("id",0L)==downloadId){clearDownload();status.text=activity.getString(R.string.download_rejected,e.message)}}}
        }
    }
    private fun finishDownload() {
        val staging=File(prefs.getString("staging","") ?: "")
        val target=File(prefs.getString("target","") ?: "")
        val root=ModelFiles.directory(activity).canonicalFile
        try {
            require(staging.canonicalFile.parentFile==root && target.canonicalFile.parentFile==root)
            require(!target.exists() && ModelFiles.validGGUF(staging)) { activity.getString(R.string.gguf_invalid_exists) }
            val parts=org.json.JSONArray(prefs.getString("parts","[]"))
            val index=prefs.getInt("part_index",0)
            val ready=File(root,".${target.name}.ready")
            require(!ready.exists()&&staging.renameTo(ready)) { activity.getString(R.string.shard_prepare_failed) }
            if(index+1<parts.length()){
                prefs.edit().putInt("part_index",index+1).commit()
                val source=Uri.parse(prefs.getString("source","")).buildUpon().path(Uri.parse(prefs.getString("source","")).path!!.substringBeforeLast('/')+"/"+parts.getString(index+1)).build().toString()
                enqueuePart(source,parts.getString(index+1),"GGUF shards")
                return
            }
            val total=(0 until parts.length()).sumOf {File(root,".${parts.getString(it)}.ready").length()}
            val expected=prefs.getLong("size",0L)
            require(expected<=0L||expected==total) { activity.getString(R.string.download_incomplete) }
            val created=mutableListOf<File>()
            try{
                for(i in parts.length()-1 downTo 0){val dst=File(root,parts.getString(i));require(!dst.exists()&&File(root,".${dst.name}.ready").renameTo(dst));created.add(dst)}
                val first=File(root,prefs.getString("first",target.name)!!)
                ModelFiles.select(activity,first);status.text=activity.getString(R.string.model_ready,first.name);refreshLibrary();onSelected(first)
            }catch(e:Exception){created.forEach {it.delete()};throw e}
            clearDownload()
        }catch(e:Exception){staging.delete();clearDownload();status.text=activity.getString(R.string.download_rejected,e.message)}
        finally {if(prefs.getLong("id",0L)==0L)clearDownload()}
    }

    private fun cancelDownload() {
        val id=prefs.getLong("id",0L)
        if (id>0L) downloads.remove(id)
        File(prefs.getString("staging","") ?: "").takeIf { it.isFile }?.delete()
        status.text=activity.getString(R.string.ui_c48e80c1186c)
        clearDownload()
    }
    private fun clearDownload() {
        val root=ModelFiles.directory(activity).canonicalFile
        val parts=runCatching {org.json.JSONArray(prefs.getString("parts","[]"))}.getOrDefault(org.json.JSONArray())
        for(i in 0 until parts.length()){for(suffix in listOf("ready","download")){val file=File(root,".${parts.optString(i)}.$suffix");if(file.canonicalFile.parentFile==root)file.delete()}}
        prefs.edit().clear().apply()
        bar.visibility=GONE
        cancel.visibility=GONE
        downloadProgress.text=""
    }
    private fun refreshLibrary() {
        library.removeAllViews()
        val current=ModelFiles.selected(activity)?.canonicalPath
        for (file in ModelFiles.list(activity)) {
            val row=card()
            row.addView(text(file.name+(if(file.canonicalPath==current)activity.getString(R.string.selected_suffix) else ""),14,true))
            row.addView(text(gib(ModelFiles.modelBytes(file)),12,false))
            row.addView(action(activity.getString(R.string.ui_60b42e0b4454)) {
                ModelFiles.select(activity,file)
                status.text=activity.getString(R.string.selected,file.name)
                onSelected(file)
                refreshLibrary()
            })
            library.addView(row)
        }
        if(library.childCount==0) library.addView(text(activity.getString(R.string.ui_59cf2783d683),13,false))
    }

    private fun gib(bytes: Long) = "%.2f GiB".format(java.util.Locale.US, bytes.toDouble()/(1024*1024*1024))
    private fun space(dp: Int) = addView(View(activity), LayoutParams(1,(dp*resources.displayMetrics.density).toInt()))
    private fun label(s:String,size:Int,bold:Boolean) = addView(text(s,size,bold))
    private fun text(s:String,size:Int,bold:Boolean)=TextView(activity).apply {
        text=s;textSize=size.toFloat();setTextColor(if(bold) 0xffd9dee3.toInt() else 0xffa5afb8.toInt())
        setPadding(0,8,0,8)
        if(bold) setTypeface(null,android.graphics.Typeface.BOLD)
    }
    private fun card():LinearLayout = LinearLayout(activity).apply {
        orientation=VERTICAL
        background=android.graphics.drawable.GradientDrawable().apply {
            setColor(0xff292d32.toInt());cornerRadius=12*resources.displayMetrics.density
            setStroke((1*resources.displayMetrics.density).toInt(),0xff3a4047.toInt())
        }
        val p=(16*resources.displayMetrics.density).toInt();setPadding(p,p,p,p)
        layoutParams=LayoutParams(LayoutParams.MATCH_PARENT,LayoutParams.WRAP_CONTENT).apply {bottomMargin=(12*resources.displayMetrics.density).toInt()}
    }
    private fun action(label:String,handler:()->Unit) = Button(activity).apply {
        text=label;isAllCaps=false
        setTextColor(0xffdce2e7.toInt())
        backgroundTintList=android.content.res.ColorStateList.valueOf(0xff394550.toInt())
        stateListAnimator = null
        elevation = 0f
        setOnClickListener{handler()}
    }
}
