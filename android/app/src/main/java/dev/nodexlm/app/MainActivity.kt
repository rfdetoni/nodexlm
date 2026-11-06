// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.view.Gravity
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import java.io.File
import java.io.FileOutputStream
import kotlin.concurrent.thread

class MainActivity : Activity() {
    private val prefs by lazy { getSharedPreferences("nodexlm", MODE_PRIVATE) }
    private lateinit var coordinator: EditText
    private lateinit var pairCode: EditText
    private lateinit var name: EditText
    private lateinit var model: TextView
    private var modelName: String = ""
    private val pickModel = 900
    private val pickCertificate = 901

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(32, 40, 32, 24)
        }
        val header = TextView(this).apply { text = "NodexLM"; textSize = 28f }
        root.addView(header)
        root.addView(TextView(this).apply {
            text = "Low-memory Android cluster worker. Runs without the screen on after you start the service."
            textSize = 14f
        })
        coordinator = field(root, "Coordinator HTTPS URL", prefs.getString("coordinator", "") ?: "")
        pairCode = field(root, "8-character host pairing code", "")
        name = field(root, "Device name", prefs.getString("name", Build.MODEL) ?: Build.MODEL)
        modelName = prefs.getString("model", "") ?: ""
        model = TextView(this).apply { text = if (modelName.isEmpty()) "No GGUF selected" else modelName }
        root.addView(model)
        button(root,"Join local cluster securely") {
            val code=pairCode.text.toString()
            val endpoint=coordinator.text.toString().trim()
            val deviceName=name.text.toString().trim()
            if(code.isBlank()){toast("Enter the host's 8-character code");return@button}
            toast("Looking for the host on your local network...")
            thread(name="nodexlm-pairing") {
                try {
                    val paired=PairingClient(this).join(code,endpoint,deviceName,modelName)
                    runOnUiThread {coordinator.setText(paired);pairCode.setText("");toast("Paired securely. You can now start the worker.")}
                } catch(e:Exception){runOnUiThread {toast("Pairing failed: ${e.message}")}}
            }
        }
        button(root,"Import coordinator TLS certificate") {
            val intent=Intent(Intent.ACTION_OPEN_DOCUMENT).apply {type="*/*";addCategory(Intent.CATEGORY_OPENABLE)}
            startActivityForResult(intent,pickCertificate)
        }
        button(root,"Choose GGUF model") {
            val intent = Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                type = "application/octet-stream"
                addCategory(Intent.CATEGORY_OPENABLE)
            }
            startActivityForResult(intent, pickModel)
        }
        button(root,"Start background worker") {
            val url=coordinator.text.toString().trimEnd('/')
            if (!url.startsWith("https://") || modelName.isBlank() || prefs.getString("node_token","").isNullOrEmpty()) {
                toast("Pair with the host and select a local GGUF model first");return@button
            }
            prefs.edit().putString("coordinator",url)
                .putString("name",name.text.toString().take(80))
                .apply()
            if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)!= PackageManager.PERMISSION_GRANTED) {
                requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 100)
            }
            try { startForegroundService(Intent(this,ComputeService::class.java).setAction(ComputeService.START));toast("Starting worker") }
            catch (e: Exception) {toast("Cannot start: ${e.message}")}
        }
        button(root,"Stop background worker") {startService(Intent(this,ComputeService::class.java).setAction(ComputeService.STOP))}
        val scroller = android.widget.ScrollView(this)
        scroller.addView(root,ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT,ViewGroup.LayoutParams.WRAP_CONTENT))
        setContentView(scroller)
    }
    private fun field(root:LinearLayout,hint:String, value:String):EditText {
        val input=EditText(this).apply { this.hint=hint;setSingleLine(true);setText(value) }
        root.addView(input);return input
    }
    private fun button(root:LinearLayout,title:String,clicked:()->Unit) {
        root.addView(Button(this).apply {text=title;setOnClickListener {clicked()} })
    }
    private fun toast(msg:String)=Toast.makeText(this,msg,Toast.LENGTH_LONG).show()
    @Deprecated("Android document picker result")
    override fun onActivityResult(requestCode:Int,resultCode:Int,data:Intent?) {
        super.onActivityResult(requestCode,resultCode,data)
        if (resultCode!=RESULT_OK || (requestCode!=pickModel && requestCode!=pickCertificate)) return
        val uri=data?.data?:return
        if(requestCode==pickCertificate){
            thread {
                try {
                    val bytes=contentResolver.openInputStream(uri)!!.use { it.readBytes() }
                    if(bytes.size>65536)throw IllegalArgumentException("certificate too large")
                    java.security.cert.CertificateFactory.getInstance("X.509")
                        .generateCertificate(bytes.inputStream())
                    File(filesDir,"coordinator.crt").writeBytes(bytes)
                    runOnUiThread {toast("Coordinator certificate imported")}
                } catch(e:Exception){runOnUiThread {toast("Certificate rejected: ${e.message}")}}
            }
            return
        }
        val filename=android.provider.OpenableColumns.DISPLAY_NAME.let {column ->
            contentResolver.query(uri,arrayOf(column),null,null,null)?.use {cursor ->
                if(cursor.moveToFirst()) cursor.getString(0) else null
            }
        } ?: "model.gguf"
        if (!filename.lowercase().endsWith(".gguf")) {toast("Choose a GGUF file");return}
        model.text="Importing model into private storage..."
        thread {
            try {
                val target=File(filesDir,"models").apply {mkdirs()}
                val final=File(target,"active.gguf")
                val tmp=File(target,"active.tmp")
                contentResolver.openInputStream(uri)!!.use {input ->
                    FileOutputStream(tmp).use {out ->input.copyTo(out,bufferSize=1024*1024)}
                }
                if (final.exists()) final.delete()
                if (!tmp.renameTo(final)) throw IllegalStateException("model import could not be saved")
                modelName=filename
                prefs.edit().putString("model",filename).apply()
                runOnUiThread {model.text="Model: $filename";toast("Model ready")}
            } catch(e:Exception) {runOnUiThread {model.text="Import failed: ${e.message}"}}
        }
    }
}
