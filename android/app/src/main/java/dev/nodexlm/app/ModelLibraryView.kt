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
    private val tick = object : Runnable {
        override fun run() {
            refreshProgress()
            main.postDelayed(this,1200)
        }
    }

    init {
        orientation = VERTICAL
        setPadding(0,12,0,28)
        label("Explore modelos",26,true)
        label("Hugging Face · GGUF · download e importação local",13,false)
        space(14)
        val searchCard = card()
        query.setSingleLine(true)
        query.setText("Qwen")
        query.setTextColor(0xffeef4ff.toInt())
        query.setHintTextColor(0xff8f9bab.toInt())
        query.hint = "Buscar modelo ou organização"
        searchCard.addView(query)
        searchCard.addView(action("Pesquisar no Hugging Face") { search() })
        addView(searchCard)
        space(12)
        val progressCard = card()
        status.text = "Selecione um GGUF compatível com os recursos disponíveis."
        status.setTextColor(0xffbccadd.toInt())
        progressCard.addView(status)
        bar.max = 1000
        bar.visibility = GONE
        progressCard.addView(bar,LayoutParams(LayoutParams.MATCH_PARENT,10).apply {topMargin=10})
        downloadProgress.setTextColor(0xff82b9ff.toInt())
        progressCard.addView(downloadProgress)
        cancel = action("Cancelar download") { cancelDownload() }
        cancel.visibility = GONE
        progressCard.addView(cancel)
        addView(progressCard)
        space(14)
        label("Modelos disponíveis",19,true)
        results.orientation = VERTICAL
        addView(results)
        space(20)
        label("Minha biblioteca",19,true)
        val importButton = action("Importar GGUF do dispositivo") { importPicker() }
        addView(importButton)
        library.orientation = VERTICAL
        addView(library)
        refreshLibrary()
    }

    override fun onAttachedToWindow() { super.onAttachedToWindow(); main.removeCallbacks(tick);main.post(tick) }
    override fun onDetachedFromWindow() { main.removeCallbacks(tick);super.onDetachedFromWindow() }

    fun importFromUri(uri: Uri) {
        status.text = "Importando arquivo sem carregar tudo na memória…"
        thread(name="nodexlm-import") {
            try {
                val file = ModelFiles.import(activity,uri)
                activity.runOnUiThread {
                    status.text = "Modelo importado: ${file.name}"
                    refreshLibrary()
                    onSelected(file)
                }
            } catch (e:Exception) {
                activity.runOnUiThread {status.text="Falha ao importar: ${e.message}"}
            }
        }
    }

    private fun search() {
        if (finding) return
        finding = true
        status.text="Consultando catálogo oficial do Hugging Face…"
        results.removeAllViews()
        val requested = query.text.toString()
        thread(name="nodexlm-hf-search") {
            try {
                val models = hub.search(requested)
                activity.runOnUiThread {
                    if (!isAttachedToWindow) { finding = false; return@runOnUiThread }
                    results.removeAllViews()
                    for (model in models) showCandidate(model)
                    status.text = "${models.size} arquivos GGUF encontrados · capacidade estimada, não garantida"
                    finding=false
                }
            } catch (e:Exception) {
                activity.runOnUiThread {status.text = "Busca indisponível: ${e.message}";finding=false}
            }
        }
    }

    private fun showCandidate(model: HuggingFaceHub.Model) {
        val row=card()
        row.addView(text(model.filename,16,true))
        row.addView(text("${model.repository}  ·  ${gib(model.sizeBytes)}",13,false))
        val suitable=model.placement=="single-node-candidate"
        row.addView(text(when(model.placement) {
            "single-node-candidate" -> "Estimativa: compatível com um nó"
            "distributed-candidate" -> "Candidato distribuído (requer Guests RPC prontos)"
            "insufficient-memory" -> "Memória agregada insuficiente"
            else -> "Compatibilidade não confirmada"
        },12,false).apply {setTextColor(if(suitable)0xff80d6b4.toInt() else 0xffe6ba78.toInt())})
        row.addView(action("Baixar ${gib(model.sizeBytes)}") { confirmDownload(model) })
        results.addView(row)
    }

    private fun confirmDownload(model: HuggingFaceHub.Model) {
        if(prefs.getLong("id",0L)>0L) {status.text="Já existe um download ativo";return}
        if(!model.url.startsWith("https://huggingface.co/") || !model.url.contains("/resolve/main/")) {
            status.text="URL de modelo não autorizada";return
        }
        AlertDialog.Builder(activity)
            .setTitle("Baixar ${model.filename.substringAfterLast('/')}?")
            .setMessage("Tamanho: ${gib(model.sizeBytes)}. Verifique o armazenamento livre. A compatibilidade é estimada e modelos grandes podem não caber na RAM.\n\nO download também poderá usar dados móveis.")
            .setPositiveButton("Baixar") { _,_ -> download(model) }
            .setNegativeButton("Cancelar",null)
            .show()
    }

    private fun download(model: HuggingFaceHub.Model) {
        try {
            val fileName = ModelFiles.safeFilename(model.filename.substringAfterLast('/'))
            val root=ModelFiles.directory(activity)
            val target=File(root,fileName)
            require(!target.exists()) { "Este modelo já está na biblioteca" }
            val staging=File(root,".${fileName}.download")
            require(!staging.exists()) { "Este modelo já está sendo baixado" }
            val request=DownloadManager.Request(Uri.parse(model.url))
                .setTitle("NodexLM: $fileName")
                .setDescription("Modelo GGUF de ${model.repository}")
                .setMimeType("application/octet-stream")
                .setDestinationUri(Uri.fromFile(staging))
                .setAllowedOverRoaming(false)
                .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            val id=downloads.enqueue(request)
            prefs.edit().putLong("id",id).putString("staging",staging.absolutePath)
                .putString("target",target.absolutePath).putLong("size",model.sizeBytes).apply()
            status.text="Download iniciado: $fileName"
            refreshProgress()
        } catch(e:Exception) {status.text="Não foi possível baixar: ${e.message}"}
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
                    DownloadManager.STATUS_FAILED -> {status.text="Download falhou; tente novamente";clearDownload()}
                    DownloadManager.STATUS_PAUSED -> status.text="Download pausado, aguardando rede…"
                }
            }
        }catch(e:Exception){status.text="Falha ao consultar download: ${e.message}"}
    }

    private fun completeDownload() {
        val staging=File(prefs.getString("staging","") ?: "")
        val target=File(prefs.getString("target","") ?: "")
        val root=ModelFiles.directory(activity).canonicalFile
        try {
            require(staging.canonicalFile.parentFile==root && target.canonicalFile.parentFile==root)
            require(!target.exists() && ModelFiles.validGGUF(staging)) { "Arquivo GGUF inválido ou já existente" }
            val expected=prefs.getLong("size",0L)
            require(expected<=0L || staging.length()==expected) { "Download incompleto" }
            require(staging.renameTo(target)) { "Falha ao salvar modelo" }
            ModelFiles.select(activity,target)
            status.text="Modelo pronto: ${target.name}"
            refreshLibrary()
            onSelected(target)
        }catch(e:Exception){staging.delete();status.text="Download rejeitado: ${e.message}"}
        finally {clearDownload()}
    }

    private fun cancelDownload() {
        val id=prefs.getLong("id",0L)
        if (id>0L) downloads.remove(id)
        File(prefs.getString("staging","") ?: "").takeIf { it.isFile }?.delete()
        status.text="Download cancelado"
        clearDownload()
    }
    private fun clearDownload() {
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
            row.addView(text(file.name+(if(file.canonicalPath==current)"  •  selecionado" else ""),14,true))
            row.addView(text(gib(file.length()),12,false))
            row.addView(action("Usar este modelo") {
                ModelFiles.select(activity,file)
                status.text="Selecionado: ${file.name}"
                onSelected(file)
                refreshLibrary()
            })
            library.addView(row)
        }
        if(library.childCount==0) library.addView(text("Nenhum GGUF salvo neste aparelho.",13,false))
    }

    private fun gib(bytes: Long) = "%.2f GiB".format(java.util.Locale.US, bytes.toDouble()/(1024*1024*1024))
    private fun space(dp: Int) = addView(View(activity), LayoutParams(1,(dp*resources.displayMetrics.density).toInt()))
    private fun label(s:String,size:Int,bold:Boolean) = addView(text(s,size,bold))
    private fun text(s:String,size:Int,bold:Boolean)=TextView(activity).apply {
        text=s;textSize=size.toFloat();setTextColor(if(bold) 0xffeff4ff.toInt() else 0xffacb8c9.toInt())
        setPadding(0,8,0,8)
        if(bold) setTypeface(null,android.graphics.Typeface.BOLD)
    }
    private fun card():LinearLayout = LinearLayout(activity).apply {
        orientation=VERTICAL
        background=android.graphics.drawable.GradientDrawable().apply {
            setColor(0xff202a38.toInt());cornerRadius=18*resources.displayMetrics.density
            setStroke((1*resources.displayMetrics.density).toInt(),0xff354557.toInt())
        }
        val p=(16*resources.displayMetrics.density).toInt();setPadding(p,p,p,p)
        layoutParams=LayoutParams(LayoutParams.MATCH_PARENT,LayoutParams.WRAP_CONTENT).apply {bottomMargin=(12*resources.displayMetrics.density).toInt()}
    }
    private fun action(label:String,handler:()->Unit) = Button(activity).apply {
        text=label;textAllCaps=false
        setTextColor(0xffdceaff.toInt())
        backgroundTintList=android.content.res.ColorStateList.valueOf(0xff30476b.toInt())
        setOnClickListener{handler()}
    }
}
