// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.content.res.ColorStateList
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.util.Log
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import java.io.File
import kotlin.concurrent.thread

/** Lightweight Android 10+ interface, inspired by the usability of PrivateLM.
 * Uses platform UI and native llama.cpp, no embedded browser or Flutter engine.
 */
class MainActivity : Activity() {
    private val prefs by lazy { getSharedPreferences("nodexlm", MODE_PRIVATE) }
    private val updateManager by lazy { UpdateManager(this) }
    private lateinit var coordinator: EditText
    private lateinit var pairCode: EditText
    private lateinit var deviceName: EditText
    private lateinit var content: LinearLayout
    private lateinit var navigation: LinearLayout
    private lateinit var library: ModelLibraryView
    private lateinit var toastStatus: TextView
    private var tab = 0
    private val pickModel = 900
    private val pickCertificate = 901

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.statusBarColor = 0xff101720.toInt()
        window.navigationBarColor = 0xff101720.toInt()
        val base = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(0xff101720.toInt())
            setPadding(dp(18),dp(18),dp(18),dp(8))
        }
        base.addView(label("NodexLM",29,true))
        base.addView(label("Uma rede. Uma inteligência.",14,false))
        space(base,14)
        navigation=LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        base.addView(navigation)
        space(base,12)
        content=LinearLayout(this).apply {orientation=LinearLayout.VERTICAL}
        base.addView(content)
        space(base,12)
        toastStatus=label("Pronto para configurar o cluster",12,false)
        base.addView(toastStatus)
        val scroll=ScrollView(this).apply {fillViewport=true;isVerticalScrollBarEnabled=false;addView(base,
            ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,ViewGroup.LayoutParams.WRAP_CONTENT))}
        setContentView(scroll)
        library=ModelLibraryView(this,
            importPicker={
                val intent=Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                    type="*/*";addCategory(Intent.CATEGORY_OPENABLE)
                }
                startActivityForResult(intent,pickModel)
            },
            onSelected={ file -> setStatus("Modelo selecionado: ${file.name}") }
        )
        showTab(0)
        try { updateManager.start() } catch(e:Exception) {Log.w("NodexLMStartup","Updater not available",e)}
    }

    private fun showTab(index:Int) {
        tab=index
        navigation.removeAllViews()
        listOf("Cluster","Modelos","Ajustes").forEachIndexed {i,title ->
            val button=Button(this).apply {
                text=title;textAllCaps=false;textSize=13f
                setTextColor(if(i==index)0xfff8fbff.toInt() else 0xffa9b7cb.toInt())
                backgroundTintList=ColorStateList.valueOf(if(i==index)0xff365885.toInt() else 0xff202b3b.toInt())
                setOnClickListener { if(tab!=i) showTab(i) }
            }
            navigation.addView(button,LinearLayout.LayoutParams(0,dp(48),1f).apply {marginEnd=dp(5)})
        }
        content.removeAllViews()
        when(index) {
            0 -> clusterScreen()
            1 -> { (library.parent as? ViewGroup)?.removeView(library);content.addView(library) }
            else -> settingsScreen()
        }
    }

    private fun clusterScreen() {
        val overview=card()
        overview.addView(label("Visão geral do dispositivo",18,true))
        val selected=ModelFiles.selected(this)
        overview.addView(label("Modelo: ${selected?.name ?: "Nenhum modelo selecionado"}",14,false))
        overview.addView(label("Sistema: Android ${Build.VERSION.RELEASE} · ${Build.SUPPORTED_ABIS.firstOrNull() ?: "?"}",12,false))
        overview.addView(label("Memória estimada para pesos: ${"%.2f".format(java.util.Locale.US,HuggingFaceHub(this).localBudgetBytes().toDouble()/(1L shl 30))} GiB",12,false))
        overview.addView(action("Explorar modelos GGUF") {showTab(1)})
        content.addView(overview)
        val pairing=card()
        pairing.addView(label("Conectar ao Host",18,true))
        pairing.addView(label("Emparelhe com o código de 8 caracteres mostrado no Host.",13,false))
        coordinator=field(pairing,"HTTPS do Host (opcional)",prefs.getString("coordinator","") ?: "")
        pairCode=field(pairing,"Código de emparelhamento","")
        deviceName=field(pairing,"Nome do dispositivo",prefs.getString("name",Build.MODEL) ?: Build.MODEL)
        pairing.addView(action("Conectar com segurança") {joinCluster()})
        pairing.addView(action("Importar certificado do Host") {
            startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                type="*/*";addCategory(Intent.CATEGORY_OPENABLE)
            },pickCertificate)
        })
        content.addView(pairing)
        val worker=card()
        worker.addView(label("Processamento",18,true))
        worker.addView(label("Processa solicitações completas usando um GGUF local. Participação RPC como parte de um único modelo permanece experimental e não está ativa neste APK.",13,false))
        worker.addView(action("Iniciar processamento em segundo plano") {startWorker()})
        worker.addView(action("Parar processamento") {
            startService(Intent(this,ComputeService::class.java).setAction(ComputeService.STOP))
            setStatus("Parando processamento")
        })
        content.addView(worker)
    }

    private fun settingsScreen() {
        val settings=card()
        settings.addView(label("Ajustes e manutenção",18,true))
        settings.addView(label("Modelos são armazenados no espaço privado do aplicativo. Downloads e importações não exigem Play Store.",13,false))
        settings.addView(action("Verificar atualizações") {updateManager.check(manual=true)})
        settings.addView(label("Versão: ${packageManager.getPackageInfo(packageName,0).versionName}",12,false))
        content.addView(settings)
    }

    private fun joinCluster() {
        val code=pairCode.text.toString().trim()
        if(code.isBlank()) {setStatus("Informe o código gerado no Host");return}
        val endpoint=coordinator.text.toString().trim()
        val name=deviceName.text.toString().trim()
        val model=ModelFiles.selected(this)?.name ?: ""
        setStatus("Procurando o Host na rede local…")
        thread(name="nodexlm-pairing") {
            try {
                val paired=PairingClient(this).join(code,endpoint,name,model)
                runOnUiThread {
                    setStatus("Conectado com segurança a $paired")
                    if(tab==0) showTab(0)
                }
            }catch(e:Exception) {runOnUiThread {setStatus("Falha no emparelhamento: ${e.message}")}}
        }
    }

    private fun startWorker() {
        val file=ModelFiles.selected(this)
        val token=prefs.getString("node_token","")
        if(file==null || token.isNullOrBlank()) {
            setStatus("Conecte ao Host e selecione um modelo GGUF na aba Modelos")
            return
        }
        val endpoint=prefs.getString("coordinator","") ?: ""
        if(!endpoint.startsWith("https://")) {setStatus("O Host precisa usar HTTPS");return}
        if(Build.VERSION.SDK_INT>=33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)!=PackageManager.PERMISSION_GRANTED)
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS),100)
        try {
            startForegroundService(Intent(this,ComputeService::class.java).setAction(ComputeService.START))
            setStatus("Processamento iniciado: ${file.name}")
        }catch(e:Exception){setStatus("Falha ao iniciar: ${e.message}")}
    }

    @Deprecated("Android file picker callback")
    override fun onActivityResult(requestCode:Int,resultCode:Int,data:Intent?) {
        super.onActivityResult(requestCode,resultCode,data)
        if(resultCode!=RESULT_OK)return
        val uri=data?.data?:return
        when(requestCode) {
            pickModel -> {showTab(1);library.importFromUri(uri)}
            pickCertificate -> thread(name="nodexlm-cert-import") {
                try {
                    val bytes=contentResolver.openInputStream(uri)?.use { it.readBytes() } ?: error("Certificate unreadable")
                    require(bytes.size<65_536)
                    java.security.cert.CertificateFactory.getInstance("X.509").generateCertificate(bytes.inputStream())
                    File(filesDir,"coordinator.crt").writeBytes(bytes)
                    runOnUiThread {setStatus("Certificado importado")}
                }catch(e:Exception){runOnUiThread {setStatus("Certificado rejeitado: ${e.message}")}}
            }
        }
    }

    override fun onResume() {
        super.onResume()
        try {updateManager.onResume()} catch(e:Exception) {Log.w("NodexLMStartup","Updater resume failed",e)}
    }
    override fun onDestroy() {
        try {updateManager.stop()} catch(_:Exception) {}
        super.onDestroy()
    }

    private fun setStatus(message:String) {
        toastStatus.text=message
        Toast.makeText(this,message,Toast.LENGTH_SHORT).show()
    }
    private fun field(parent:LinearLayout,hint:String,value:String):EditText = EditText(this).apply {
        this.hint=hint;setSingleLine(true);setText(value);setTextColor(0xfff1f5fc.toInt());setHintTextColor(0xff8194aa.toInt())
        parent.addView(this,LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT,dp(58)))
    }
    private fun label(value:String,size:Int,bold:Boolean)=TextView(this).apply {
        text=value;textSize=size.toFloat();setTextColor(if(bold)0xfff1f5ff.toInt() else 0xffadbccf.toInt())
        if(bold) setTypeface(null,Typeface.BOLD)
        setPadding(0,dp(5),0,dp(5))
    }
    private fun action(value:String,handler:()->Unit)=Button(this).apply {
        text=value;textAllCaps=false;textSize=13f
        setTextColor(0xffedf3fd.toInt())
        backgroundTintList=ColorStateList.valueOf(0xff30527b.toInt())
        setOnClickListener {handler()}
    }
    private fun card():LinearLayout=LinearLayout(this).apply {
        orientation=LinearLayout.VERTICAL
        val p=dp(16);setPadding(p,p,p,p)
        background=GradientDrawable().apply {
            setColor(0xff202b3a.toInt());cornerRadius=dp(18).toFloat();setStroke(dp(1),0xff384858.toInt())
        }
        layoutParams=LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT,LinearLayout.LayoutParams.WRAP_CONTENT).apply {bottomMargin=dp(12)}
    }
    private fun space(parent:LinearLayout,size:Int)=parent.addView(View(this),LinearLayout.LayoutParams(dp(1),dp(size)))
    private fun dp(value:Int)=(value*resources.displayMetrics.density).toInt()
}
