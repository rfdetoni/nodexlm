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
import android.widget.CheckBox
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
    private val importDirectoryPicker: () -> Unit,
    private val onSelected: (File) -> Unit
) : LinearLayout(activity) {
    private val hub = HuggingFaceHub(activity)
    private val downloads = activity.getSystemService(DownloadManager::class.java)
    private val prefs = activity.getSharedPreferences("nodexlm_hf_download",Context.MODE_PRIVATE)
    private val main = Handler(Looper.getMainLooper())
    private val query = EditText(activity)
    private val onlyDecisions = CheckBox(activity)
    private var catalogResults = emptyList<HuggingFaceHub.Model>()
    private val results = LinearLayout(activity)
    private val library = LinearLayout(activity)
    private val status = TextView(activity)
    private val bar = ProgressBar(activity,null,android.R.attr.progressBarStyleHorizontal)
    private val downloadProgress = TextView(activity)
    private lateinit var cancel: Button
    private var finding = false
    private var verifying = false
    private var preparing = false
    @Volatile private var cacheEpoch = 0L
    @Volatile private var cacheCancelled = false
    @Volatile private var cacheReceived = 0L
    private var cacheExpected = 0L
    private var cacheTransfer = false
    private var sampledDownload = 0L
    private var sampledAt = 0L
    private var sampledBytes = 0L
    private var bytesPerSecond = 0.0
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
        val horizontalPadding = (14 * resources.displayMetrics.density).toInt()
        query.setPadding(horizontalPadding, 0, horizontalPadding, 0)
        query.setText("Qwen")
        query.setTextColor(0xffd9dee3.toInt())
        query.setHintTextColor(0xff969faa.toInt())
        query.hint = activity.getString(R.string.query_hint)
        searchCard.addView(query)
        onlyDecisions.text = activity.getString(R.string.model_filter_decisions)
        onlyDecisions.setOnCheckedChangeListener { _, enabled ->
            if (enabled && query.text.toString().trim() == "Qwen") query.setText("decision")
            renderSearchResults()
        }
        searchCard.addView(onlyDecisions)
        searchCard.addView(text(activity.getString(R.string.model_filter_decisions_note), 12, false))
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
        addView(action(activity.getString(R.string.import_directory)) { importDirectoryPicker() })
        library.orientation = VERTICAL
        addView(library)
        refreshLibrary()
        // An unowned Host-cache transfer cannot be resumed after Activity re-creation.
        if (prefs.getLong("id", 0L) in listOf(-1L, -2L)) clearDownload()
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

    fun importDirectory(uri: Uri) {
        status.text = activity.getString(R.string.ui_a8843f6c285c)
        thread(name = "nodexlm-import-directory") {
            try {
                val count = ModelFiles.importDirectory(activity, uri)
                activity.runOnUiThread {
                    status.text = activity.getString(R.string.import_directory_done, count)
                    refreshLibrary()
                }
            } catch (e: Exception) {
                activity.runOnUiThread {
                    status.text = activity.getString(R.string.import_failed, e.message)
                    refreshLibrary()
                }
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
                    catalogResults = models
                    renderSearchResults()
                    finding=false
                }
            } catch (e:Exception) {
                activity.runOnUiThread {status.text = activity.getString(R.string.search_failed,e.message);finding=false}
            }
        }
    }

    private fun renderSearchResults() {
        results.removeAllViews()
        val shown = if (onlyDecisions.isChecked) catalogResults.filter { it.decisionModel } else catalogResults
        shown.forEach(::showCandidate)
        status.text = activity.getString(R.string.search_count, shown.size)
    }

    private fun showCandidate(model: HuggingFaceHub.Model) {
        val row=card()
        row.addView(text(model.filename,16,true))
        row.addView(text("${model.repository}  ·  ${gib(model.sizeBytes)}",13,false))
        if (model.decisionModel) row.addView(text(activity.getString(R.string.model_decision_candidate), 12, false))
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
        if(preparing || prefs.getLong("id",0L)!=0L) {status.text=activity.getString(R.string.ui_c1234618156b);return}
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
        if (preparing || prefs.getLong("id", 0L) != 0L) return
        preparing = true
        status.text = activity.getString(R.string.model_downloading)
        thread(name = "nodexlm-verify-hf-metadata") {
            try {
                val fileName = ModelFiles.safeFilename(model.filename.substringAfterLast('/'))
                val parts = ModelFiles.shards(fileName)
                val plan = VerifiedHuggingFaceDownload.prepare(model.url, parts, model.sizeBytes)
                activity.runOnUiThread {
                    preparing = false
                    if (!isAttachedToWindow) return@runOnUiThread
                    try {
                        val root = ModelFiles.directory(activity)
                        require(prefs.getLong("id", 0L) == 0L) { activity.getString(R.string.model_downloading) }
                        require(parts.none { File(root, it).exists() }) { activity.getString(R.string.model_exists) }
                        require(root.usableSpace >= plan.totalBytes) { "Insufficient free storage" }
                        val names = org.json.JSONArray()
                        val urls = org.json.JSONArray()
                        val digests = org.json.JSONArray()
                        val sizes = org.json.JSONArray()
                        plan.parts.forEach { part ->
                            names.put(part.name)
                            urls.put(part.url)
                            digests.put(part.sha256)
                            sizes.put(part.size)
                        }
                        check(prefs.edit().putString("parts", names.toString())
                            .putString("urls", urls.toString())
                            .putString("shas", digests.toString())
                            .putString("sizes", sizes.toString())
                            .putInt("part_index", 0)
                            .putString("first", fileName)
                            .putLong("size", plan.totalBytes).commit()) { "Cannot persist download metadata" }
                        enqueuePart(plan.parts.first().url, parts.first(), model.repository)
                    } catch (e: Exception) {
                        clearDownload()
                        status.text = activity.getString(R.string.download_failed, e.message)
                    }
                }
            } catch (e: Exception) {
                activity.runOnUiThread {
                    preparing = false
                    if (isAttachedToWindow) status.text = activity.getString(R.string.download_failed, e.message)
                }
            }
        }
    }


    private fun enqueuePart(source: String, fileName: String, repository: String) {
        val root = ModelFiles.directory(activity)
        val target = File(root, fileName)
        val staging = File(root, ".$fileName.download")
        require(!staging.exists()) { activity.getString(R.string.model_downloading) }
        val index = prefs.getInt("part_index", 0)
        val checksum = org.json.JSONArray(prefs.getString("shas", "[]")).optString(index)
        val expected = org.json.JSONArray(prefs.getString("sizes", "[]")).optLong(index, -1L)
        if (!HostModelCache.available(activity) || expected <= 0L) {
            enqueueNetworkPart(source, fileName, repository)
            return
        }
        val epoch = ++cacheEpoch
        cacheCancelled = false
        cacheReceived = 0L
        cacheExpected = expected
        cacheTransfer = true
        check(prefs.edit().putLong("id", -1L).putString("staging", staging.absolutePath)
            .putString("target", target.absolutePath).putString("current_source", source).commit())
        status.text = activity.getString(R.string.download_started, fileName)
        val scratch = File(root, ".$fileName.p2p-$epoch")
        thread(name = "nodexlm-host-content-cache") {
            val available = HostModelCache.fetch(activity, checksum, expected, scratch,
                { cacheCancelled || epoch != cacheEpoch }, { cacheReceived = it })
            activity.runOnUiThread {
                if (epoch != cacheEpoch || prefs.getLong("id", 0L) != -1L) {
                    scratch.delete()
                    return@runOnUiThread
                }
                cacheTransfer = false
                if (available && scratch.renameTo(staging)) {
                    // Host cache has already verified the entire SHA-256.
                    // Avoid reading multi-GB model weights a second time.
                    finishDownload()
                } else {
                    scratch.delete()
                    prefs.edit().putLong("id", 0L).commit()
                    try {
                        enqueueNetworkPart(source, fileName, repository)
                    } catch (e: Exception) {
                        clearDownload()
                        status.text = activity.getString(R.string.download_failed, e.message)
                    }
                }
            }
        }
    }

    private fun enqueueNetworkPart(source: String, fileName: String, repository: String) {
        if (BuildConfig.RUST_RPC_ENABLED) {
            val index = prefs.getInt("part_index", 0)
            val checksum = org.json.JSONArray(prefs.getString("shas", "[]")).optString(index)
            val expected = org.json.JSONArray(prefs.getString("sizes", "[]")).optLong(index, -1L)
            if (checksum.matches(Regex("[0-9a-f]{64}")) && expected >= 8) {
                enqueueRustPart(source, fileName, repository, checksum, expected)
                return
            }
        }
        enqueueDownloadManager(source, fileName, repository)
    }

    private fun enqueueRustPart(source: String, fileName: String, repository: String, checksum: String, expected: Long) {
        val root = ModelFiles.directory(activity)
        val staging = File(root, ".$fileName.download")
        val target = File(root, fileName)
        require(!staging.exists()) { activity.getString(R.string.model_downloading) }
        val epoch = ++cacheEpoch
        cacheCancelled = false
        cacheReceived = 0
        cacheExpected = expected
        cacheTransfer = true
        check(prefs.edit().putLong("id", -2L).putString("staging", staging.absolutePath)
            .putString("target", target.absolutePath).putString("current_source", source).commit())
        status.text = activity.getString(R.string.download_started, fileName)
        // Stable private stage lets Rust resume only fsync-committed ranges after process death.
        val scratch = File(root, ".$fileName.native.partial")
        thread(name = "nodexlm-rust-model-download") {
            val error = runCatching {
                NativeRustDownload.fetch(source, scratch.absolutePath, checksum, expected)
            }.getOrElse { it.message ?: "Native download unavailable" }
            activity.runOnUiThread {
                if (epoch != cacheEpoch || prefs.getLong("id", 0L) != -2L) {
                    scratch.delete()
                    return@runOnUiThread
                }
                cacheTransfer = false
                if (error.isBlank() && scratch.renameTo(staging)) {
                    // Rust streamed, hashed and fsynced the verified bytes.
                    finishDownload()
                } else {
                    scratch.delete()
                    prefs.edit().putLong("id", 0L).commit()
                    try {
                        enqueueDownloadManager(source, fileName, repository)
                    } catch (e: Exception) {
                        clearDownload()
                        status.text = activity.getString(R.string.download_failed, e.message)
                    }
                }
            }
        }
    }

    private fun enqueueDownloadManager(source: String, fileName: String, repository: String) {
        val root = ModelFiles.directory(activity)
        val target = File(root, fileName)
        val staging = File(root, ".$fileName.download")
        require(!staging.exists()) { activity.getString(R.string.model_downloading) }
        val request = DownloadManager.Request(Uri.parse(source))
            .setTitle("NodexLM: $fileName")
            .setDescription(activity.getString(R.string.model_source, repository))
            .setMimeType("application/octet-stream")
            .setDestinationUri(Uri.fromFile(staging))
            .setAllowedOverRoaming(false)
            .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
        val id = downloads.enqueue(request)
        if (!prefs.edit().putLong("id", id).putString("staging", staging.absolutePath)
            .putString("target", target.absolutePath).putString("current_source", source).commit()) {
            downloads.remove(id)
            error("Cannot persist active download")
        }
        status.text = activity.getString(R.string.download_started, fileName)
        refreshProgress()
    }
    private fun refreshProgress() {
        val id=prefs.getLong("id",0L)
        if ((id == -1L || id == -2L) && cacheTransfer) {
            bar.visibility=VISIBLE;cancel.visibility=VISIBLE
            val received=if(id == -2L)runCatching { NativeRustDownload.transferred() }.getOrDefault(0L) else cacheReceived
            bar.isIndeterminate=cacheExpected<=0
            if(cacheExpected>0)bar.progress=(received*1000/cacheExpected).toInt().coerceIn(0,1000)
            val nativeSpeed=if(id == -2L)runCatching { NativeRustDownload.bytesPerSecond() }.getOrDefault(0L) else 0L
            val rate=if(nativeSpeed>0) " · %.2f MiB/s".format(java.util.Locale.US,nativeSpeed.toDouble()/(1024*1024)) else ""
            val details = if (id == -2L) runCatching {
                val rows = org.json.JSONArray(NativeRustDownload.connectionStats())
                (0 until rows.length()).mapNotNull { index ->
                    val c = rows.optJSONObject(index) ?: return@mapNotNull null
                    val bytes = c.optLong("bytes", 0L)
                    if (bytes <= 0L) return@mapNotNull null
                    val speed = c.optLong("bytesPerSecond", 0L)
                    "C${c.optInt("id", index+1)}: %.1f MiB/s".format(
                        java.util.Locale.US, speed.toDouble() / (1024 * 1024))
                }.joinToString(" · ")
            }.getOrDefault("") else ""
            downloadProgress.text="${if(id == -2L) "Rust HTTPS" else "Host cache"} · ${gib(received)} / ${gib(cacheExpected)}$rate" +
                if (details.isNotEmpty()) "\n$details" else ""
            return
        }
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
                val now=android.os.SystemClock.elapsedRealtime()
                if(id!=sampledDownload){
                    sampledDownload=id;sampledAt=now;sampledBytes=bytes;bytesPerSecond=0.0
                }else if(state==DownloadManager.STATUS_RUNNING && now-sampledAt>=1000){
                    val elapsed=(now-sampledAt).coerceAtLeast(1)
                    val instant=(bytes-sampledBytes).coerceAtLeast(0)*1000.0/elapsed
                    bytesPerSecond=if(bytesPerSecond==0.0)instant else bytesPerSecond*0.7+instant*0.3
                    sampledAt=now;sampledBytes=bytes
                }else if(state!=DownloadManager.STATUS_RUNNING){
                    bytesPerSecond=0.0;sampledAt=now;sampledBytes=bytes
                }
                val rate=if(bytesPerSecond>0 && state==DownloadManager.STATUS_RUNNING)
                    " · %.2f MiB/s".format(java.util.Locale.US,bytesPerSecond/(1024*1024)) else ""
                val eta=if(total>bytes && bytesPerSecond>0)
                    " · ~%ds".format(java.util.Locale.US,((total-bytes)/bytesPerSecond).toLong()) else ""
                downloadProgress.text="${gib(bytes)} / ${if(total>0)gib(total) else "?"}$rate$eta"
                when(state) {
                    DownloadManager.STATUS_SUCCESSFUL -> completeDownload()
                    DownloadManager.STATUS_FAILED -> {status.text=activity.getString(R.string.ui_24184bb0704d);clearDownload()}
                    DownloadManager.STATUS_PAUSED -> status.text=activity.getString(R.string.ui_0168ed1ca821)
                }
            }
        }catch(e:Exception){status.text=activity.getString(R.string.download_status_failed,e.message)}
    }

    private fun completeDownload() {
        if (verifying) return
        verifying = true
        val downloadId = prefs.getLong("id", 0L)
        val file = File(prefs.getString("staging", "") ?: "")
        val index = prefs.getInt("part_index", 0)
        val digests = org.json.JSONArray(prefs.getString("shas", "[]"))
        val sizes = org.json.JSONArray(prefs.getString("sizes", "[]"))
        val expectedSha = digests.optString(index)
        val expectedSize = sizes.optLong(index, -1L)
        thread(name = "nodexlm-verify-shard") {
            try {
                require(expectedSha.matches(Regex("[0-9a-f]{64}")) && expectedSize >= 8L) {
                    "Missing immutable model checksum or size"
                }
                require(file.length() == expectedSize && ModelFiles.validGGUF(file)) {
                    "Incomplete or invalid GGUF shard"
                }
                val digest = java.security.MessageDigest.getInstance("SHA-256")
                file.inputStream().use { input ->
                    val buffer = ByteArray(256 * 1024)
                    while (true) {
                        val n = input.read(buffer)
                        if (n < 0) break
                        digest.update(buffer, 0, n)
                    }
                }
                val actual = digest.digest().joinToString("") { "%02x".format(it.toInt() and 255) }
                require(actual == expectedSha) { "GGUF SHA-256 mismatch" }
                activity.runOnUiThread {
                    verifying = false
                    if (isAttachedToWindow && prefs.getLong("id", 0L) == downloadId) finishDownload()
                }
            } catch (e: Exception) {
                activity.runOnUiThread {
                    verifying = false
                    if (isAttachedToWindow && prefs.getLong("id", 0L) == downloadId) {
                        clearDownload()
                        status.text = activity.getString(R.string.download_rejected, e.message)
                    }
                }
            }
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
                val urls=org.json.JSONArray(prefs.getString("urls","[]"))
                enqueuePart(urls.getString(index+1),parts.getString(index+1),"GGUF shards")
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
        if (id == -1L || id == -2L) {
            cacheCancelled = true; cacheEpoch++; cacheTransfer = false
            if (id == -2L) runCatching { NativeRustDownload.cancel() }
        }
        if (id>0L) downloads.remove(id)
        File(prefs.getString("staging","") ?: "").takeIf { it.isFile }?.delete()
        status.text=activity.getString(R.string.ui_c48e80c1186c)
        clearDownload()
    }
    private fun clearDownload() {
        cacheCancelled = true; cacheEpoch++; cacheTransfer = false
        if (prefs.getLong("id", 0L) == -2L) runCatching { NativeRustDownload.cancel() }
        val root=ModelFiles.directory(activity).canonicalFile
        val parts=runCatching {org.json.JSONArray(prefs.getString("parts","[]"))}.getOrDefault(org.json.JSONArray())
        for(i in 0 until parts.length()){for(suffix in listOf("ready","download")){val file=File(root,".${parts.optString(i)}.$suffix");if(file.canonicalFile.parentFile==root)file.delete()}}
        prefs.edit().clear().apply()
        bar.visibility=GONE
        cancel.visibility=GONE
        downloadProgress.text=""
        sampledDownload=0L;sampledAt=0L;sampledBytes=0L;bytesPerSecond=0.0
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
