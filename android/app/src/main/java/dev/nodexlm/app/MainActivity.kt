// Copyright (C) 2026 Rodrigo Felipe de Toni Grave
// SPDX-License-Identifier: AGPL-3.0-only
package dev.nodexlm.app

import android.Manifest
import android.app.Activity
import android.app.AlertDialog
import android.content.Intent
import android.content.pm.PackageManager
import android.content.res.ColorStateList
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.PowerManager
import android.provider.Settings
import android.util.Log
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.ArrayAdapter
import android.widget.Spinner
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.HorizontalScrollView
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
    private lateinit var contentScroll: ScrollView
    private var coordinatorDraft: String? = null
    private var pairingCodeDraft = ""
    private var deviceNameDraft: String? = null
    private lateinit var navigation: LinearLayout
    private lateinit var library: ModelLibraryView
    private lateinit var toastStatus: TextView
    private var tab = 0
    private val pickModel = 900
    private val pickCertificate = 901

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        window.statusBarColor = 0xff202327.toInt()
        window.navigationBarColor = 0xff202327.toInt()
        val base = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(0xff202327.toInt())
            setPadding(dp(16), dp(18), dp(16), dp(16))
        }
        val brand = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
        }
        brand.addView(ClusterMark(this), LinearLayout.LayoutParams(dp(38), dp(38)).apply { marginEnd = dp(12) })
        val titleBlock = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        titleBlock.addView(label(getString(R.string.ui_8792d2ba201f), 22, true))
        titleBlock.addView(label(getString(R.string.ui_37db6b658719), 12, false))
        brand.addView(titleBlock)
        base.addView(brand)
        space(base, 20)
        navigation = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        val tabs = HorizontalScrollView(this).apply {
            isHorizontalScrollBarEnabled = false
            isFillViewport = false
            addView(navigation)
        }
        base.addView(tabs)
        space(base, 16)
        content = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        contentScroll = ScrollView(this).apply {
            isFillViewport = true
            isVerticalScrollBarEnabled = true
            addView(content, ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        }
        base.addView(contentScroll, LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f))
        toastStatus = label(getString(R.string.ui_5c8c7f7835eb), 12, false)
        toastStatus.setPadding(dp(4), dp(12), dp(4), dp(8))
        base.addView(toastStatus)
        setContentView(base)
        library=ModelLibraryView(this,
            importPicker={
                val intent=Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                    type="*/*";addCategory(Intent.CATEGORY_OPENABLE);putExtra(Intent.EXTRA_ALLOW_MULTIPLE,true)
                }
                startActivityForResult(intent,pickModel)
            },
            onSelected={ file -> setStatus(getString(R.string.model_selected,file.name)) }
        )
        showTab(0)
        try { updateManager.start() } catch(e:Exception) {Log.w("NodexLMStartup","Updater not available",e)}
    }

    private fun showTab(index: Int) {
        if (tab == 1 && index != 1 && ::coordinator.isInitialized &&
            ::pairCode.isInitialized && ::deviceName.isInitialized) {
            coordinatorDraft = coordinator.text.toString()
            pairingCodeDraft = pairCode.text.toString()
            deviceNameDraft = deviceName.text.toString()
        }
        tab = index
        navigation.removeAllViews()
        val titles = listOf(
            getString(R.string.tab_overview),
            getString(R.string.tab_connection),
            getString(R.string.tab_processing),
            getString(R.string.models),
            getString(R.string.settings)
        )
        titles.forEachIndexed { i, title ->
            val active = i == index
            val button = Button(this).apply {
                text = title
                isAllCaps = false
                textSize = 13f
                minWidth = 0
                minHeight = 0
                stateListAnimator = null
                elevation = 0f
                setPadding(dp(16), 0, dp(16), 0)
                setTextColor(if (active) 0xffe3e8ed.toInt() else 0xffa8b0b8.toInt())
                backgroundTintList = null
                background = GradientDrawable().apply {
                    setColor(if (active) 0xff38414b.toInt() else 0xff262a2f.toInt())
                    cornerRadius = dp(9).toFloat()
                    setStroke(dp(1), if (active) 0xff4a5661.toInt() else 0xff353a40.toInt())
                }
                contentDescription = title
                setOnClickListener { if (tab != i) showTab(i) }
            }
            navigation.addView(button, LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT, dp(42)).apply { marginEnd = dp(8) })
        }
        content.removeAllViews()
        contentScroll.scrollTo(0, 0)
        when (index) {
            0 -> overviewScreen()
            1 -> connectionScreen()
            2 -> processingScreen()
            3 -> { (library.parent as? ViewGroup)?.removeView(library); content.addView(library) }
            else -> settingsScreen()
        }
    }

    private fun overviewScreen() {
        val overview=card()
        overview.addView(label(getString(R.string.ui_7681d877e8b1),18,true))
        val selected=ModelFiles.selected(this)
        overview.addView(label(getString(R.string.ui_6fc9111417fd),14,false))
        overview.addView(label(getString(R.string.model_optional,selected?.name ?: getString(R.string.none)),12,false))
        overview.addView(label(getString(R.string.system,Build.VERSION.RELEASE,Build.SUPPORTED_ABIS.firstOrNull() ?: "?"),12,false))
        overview.addView(label(getString(R.string.memory_budget,"%.2f".format(java.util.Locale.US,HuggingFaceHub(this).localBudgetBytes().toDouble()/(1L shl 30))),12,false))
        overview.addView(action(getString(R.string.ui_87125eeb01cb)) {showTab(3)})
        overview.addView(label(
            if (prefs.getString("node_id", "").isNullOrBlank())
                getString(R.string.node_not_paired)
            else getString(R.string.node_paired),
            12, false
        ))
        overview.addView(action(getString(R.string.tab_connection)) { showTab(1) })
        overview.addView(action(getString(R.string.tab_processing)) { showTab(2) })
        content.addView(overview)
    }

    private fun connectionScreen() {
        val pairing=card()
        pairing.addView(label(getString(R.string.ui_9220df143b25),18,true))
        pairing.addView(label(getString(R.string.ui_9ee19c2db1d5),13,false))
        coordinator=field(pairing,getString(R.string.host_url),
            coordinatorDraft ?: (prefs.getString("coordinator","") ?: ""))
        pairCode=field(pairing,getString(R.string.pairing_code),pairingCodeDraft)
        deviceName=field(pairing,getString(R.string.device_name),
            deviceNameDraft ?: (prefs.getString("name",Build.MODEL) ?: Build.MODEL))
        pairing.addView(action(getString(R.string.ui_c06cce69d22b)) {joinCluster()})
        pairing.addView(action(getString(R.string.ui_5701de13e132)) {
            startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
                type="*/*";addCategory(Intent.CATEGORY_OPENABLE)
            },pickCertificate)
        })
        content.addView(pairing)
    }

    private fun processingScreen() {
        val worker=card()
        worker.addView(label(getString(R.string.ui_5ac59a6efd23),18,true))
        worker.addView(label(getString(R.string.backend_help,NativeRpc.VERSION),13,false))
        val modes = listOf(getString(R.string.backend_auto), getString(R.string.backend_cpu), getString(R.string.backend_gpu), getString(R.string.backend_hybrid))
        val identifiers = listOf("auto", "cpu", "gpu", "hybrid")
        val selector = Spinner(this).apply {
            adapter = ArrayAdapter(this@MainActivity, android.R.layout.simple_spinner_dropdown_item, modes)
            setSelection(identifiers.indexOf(prefs.getString("rpc_mode", "auto")).coerceAtLeast(0))
            onItemSelectedListener = object: android.widget.AdapterView.OnItemSelectedListener {
                override fun onNothingSelected(parent: android.widget.AdapterView<*>?) {}
                override fun onItemSelected(parent: android.widget.AdapterView<*>?, view: View?, position: Int, id: Long) {
                    prefs.edit().putString("rpc_mode", identifiers[position]).apply()
                }
            }
        }
        worker.addView(label(getString(R.string.ui_e4c3a22567db),13,false))
        worker.addView(selector)
        worker.addView(action(getString(R.string.ui_2940dc16b87e)) {startRpcGuest()})
        val gpuStatus = label(getString(R.string.ui_54c9d56ac072),12,false)
        worker.addView(gpuStatus)
        thread(name="nodexlm-vulkan-probe") {
            val report = try {
                val devices = org.json.JSONObject(NativeRpc.inspect()).optJSONArray("gpus")
                if(devices == null || devices.length() == 0) getString(R.string.gpu_unavailable)
                else buildString {
                    append(getString(R.string.gpu_detected))
                    for(i in 0 until devices.length()) {
                        if(i>0)append(", ")
                        append(devices.optJSONObject(i)?.optString("name", "GPU") ?: "GPU")
                    }
                }
            } catch(e: Throwable) { getString(R.string.gpu_probe_failed) }
            runOnUiThread {gpuStatus.text = report}
        }
        worker.addView(action(getString(R.string.ui_586f2f6fc723)) {
            startService(Intent(this,RpcGuestService::class.java).setAction(RpcGuestService.STOP))
            setStatus(getString(R.string.ui_f54a84193c11))
        })
        worker.addView(label(getString(R.string.ui_4c9eb65e0bab),12,false))
        worker.addView(action(getString(R.string.ui_5949a199212d)) {startWorker()})
        worker.addView(action(getString(R.string.ui_942f9c4afc38)) {
            startService(Intent(this,ComputeService::class.java).setAction(ComputeService.STOP))
            setStatus(getString(R.string.ui_4630ba48f510))
        })
        content.addView(worker)
    }

    private fun settingsScreen() {
        val settings=card()
        settings.addView(label(getString(R.string.ui_14e59fc95d44),18,true))
        settings.addView(label(getString(R.string.ui_18ade3f42460),13,false))
        settings.addView(label(getString(R.string.ui_28c01454db70),13,false))
        settings.addView(action(getString(R.string.ui_474c3f7911a8)) {
            try {
                val manager = getSystemService(POWER_SERVICE) as PowerManager
                if (manager.isIgnoringBatteryOptimizations(packageName)) {
                    setStatus(getString(R.string.ui_1abbe7528f2e))
                } else {
                    startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
                }
            } catch (e: Exception) { setStatus(getString(R.string.ui_e7caf6ffa12d)) }
        })
        val onlyCharging = CheckBox(this).apply { text = getString(R.string.charging_only); isChecked = prefs.getBoolean("rpc_only_charging", false) }
        settings.addView(onlyCharging)
        val minimum = field(settings,getString(R.string.minimum_battery),prefs.getInt("rpc_min_battery",15).toString())
        val startHour = field(settings,getString(R.string.start_hour),prefs.getInt("rpc_start_hour",0).toString())
        val endHour = field(settings,getString(R.string.end_hour),prefs.getInt("rpc_end_hour",0).toString())
        val memoryCap = field(settings,getString(R.string.rpc_memory_cap),prefs.getInt("rpc_max_contribution_mib",0).toString())
        val memoryReserve = field(settings,getString(R.string.rpc_memory_reserve),prefs.getInt("rpc_memory_reserve_mib",1024).toString())
        val cpuThreads = field(settings,getString(R.string.rpc_cpu_threads),prefs.getInt("rpc_threads",0).toString())
        listOf(minimum,startHour,endHour,memoryCap,memoryReserve,cpuThreads).forEach {
            it.inputType = android.text.InputType.TYPE_CLASS_NUMBER
        }
        settings.addView(action(getString(R.string.ui_94c7583562e2)) {
            val min = minimum.text.toString().toIntOrNull()
            val from = startHour.text.toString().toIntOrNull()
            val until = endHour.text.toString().toIntOrNull()
            val cap = memoryCap.text.toString().toIntOrNull()
            val reserve = memoryReserve.text.toString().toIntOrNull()
            val threads = cpuThreads.text.toString().toIntOrNull()
            if (min == null || min !in 5..95 || from == null || from !in 0..23 ||
                until == null || until !in 0..23 || cap == null || cap !in 0..65536 ||
                reserve == null || reserve !in 1024..65536 || threads == null || threads !in 0..16) {
                setStatus(getString(R.string.ui_5f20f24e5efa))
            } else {
                prefs.edit().putBoolean("rpc_only_charging",onlyCharging.isChecked).putInt("rpc_min_battery",min)
                    .putInt("rpc_start_hour",from).putInt("rpc_end_hour",until)
                    .putInt("rpc_max_contribution_mib",cap).putInt("rpc_memory_reserve_mib",reserve)
                    .putInt("rpc_threads",threads).apply()
                setStatus(getString(R.string.ui_a0f818d41fea))
            }
        })
        settings.addView(action(getString(R.string.ui_fd732a2b0a67)) {updateManager.check(manual=true)})
        settings.addView(action(getString(R.string.ui_ff4f0c35cea3)) {
            try {
                val notice = assets.open("THIRD_PARTY_NOTICES.txt").bufferedReader().use { it.readText() }
                AlertDialog.Builder(this).setTitle(getString(R.string.ui_ff4f0c35cea3))
                    .setMessage(notice).setPositiveButton(getString(R.string.ui_0f2bd88ef0ac), null).show()
            } catch (e: Exception) { setStatus(getString(R.string.ui_6a31ec3b8a4d)) }
        })
        settings.addView(label(getString(R.string.version,packageManager.getPackageInfo(packageName,0).versionName),12,false))
        content.addView(settings)
    }

    private fun joinCluster() {
        val code=pairCode.text.toString().trim()
        if(code.isBlank()) {setStatus(getString(R.string.ui_becba7ac9722));return}
        val endpoint=coordinator.text.toString().trim()
        val name=deviceName.text.toString().trim()
        val model=ModelFiles.selected(this)?.name ?: ""
        setStatus(getString(R.string.ui_e794f9249e8b))
        thread(name="nodexlm-pairing") {
            try {
                val paired=PairingClient(this).join(code,endpoint,name,model)
                runOnUiThread {
                    if (!isFinishing && !isDestroyed) {
                        setStatus(getString(R.string.connected,paired))
                        pairingCodeDraft = ""
                        pairCode.setText("")
                        if(tab==1) showTab(0)
                        offerRpcGuestAfterPairing()
                    }
                }
            }catch(e:Exception) {runOnUiThread {setStatus(getString(R.string.pair_failed,e.message))}}
        }
    }

    private fun offerRpcGuestAfterPairing() {
        AlertDialog.Builder(this)
            .setTitle(R.string.rpc_guest_offer_title)
            .setMessage(R.string.rpc_guest_offer_message)
            .setPositiveButton(R.string.rpc_guest_offer_accept) { _, _ -> startRpcGuest() }
            .setNegativeButton(R.string.rpc_guest_offer_later, null)
            .show()
    }

    private fun startRpcGuest() {
        val token = runCatching { NodeSecrets.read(this) }.getOrDefault("")
        val id = prefs.getString("node_id","").orEmpty()
        val endpoint = prefs.getString("coordinator","").orEmpty()
        if (token.isBlank() || id.isBlank() || !endpoint.startsWith("https://")) {
            setStatus(getString(R.string.ui_2e207ce96075))
            return
        }
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 100)
        }
        try {
            // Prevent an old whole-model worker from competing for the same RAM.
            startService(Intent(this,ComputeService::class.java).setAction(ComputeService.STOP))
            val intent = Intent(this,RpcGuestService::class.java)
                .setAction(RpcGuestService.START)
                .putExtra(RpcGuestService.EXTRA_ENDPOINT,endpoint)
                .putExtra(RpcGuestService.EXTRA_NODE,id)
                .putExtra(RpcGuestService.EXTRA_NAME,prefs.getString("name",Build.MODEL) ?: Build.MODEL)
                .putExtra(RpcGuestService.EXTRA_MODE,prefs.getString("rpc_mode", "auto") ?: "auto")
            startForegroundService(intent)
            setStatus(getString(R.string.ui_69873d9d8387))
        } catch(e:Exception) {setStatus(getString(R.string.rpc_failed,e.message))}
    }

    private fun startWorker() {
        val file=ModelFiles.selected(this)
        val token=runCatching { NodeSecrets.read(this) }.getOrDefault("")
        if(file==null || token.isNullOrBlank()) {
            setStatus(getString(R.string.ui_ce1d54cb7b7a))
            return
        }
        val endpoint=prefs.getString("coordinator","") ?: ""
        if(!endpoint.startsWith("https://")) {setStatus(getString(R.string.ui_07a3785ba068));return}
        if(Build.VERSION.SDK_INT>=33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS)!=PackageManager.PERMISSION_GRANTED)
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS),100)
        try {
            startForegroundService(Intent(this,ComputeService::class.java).setAction(ComputeService.START))
            setStatus(getString(R.string.processing_started,file.name))
        }catch(e:Exception){setStatus(getString(R.string.start_failed,e.message))}
    }

    @Deprecated("Android file picker callback")
    override fun onActivityResult(requestCode:Int,resultCode:Int,data:Intent?) {
        super.onActivityResult(requestCode,resultCode,data)
        if(resultCode!=RESULT_OK)return
        val uris=data?.clipData?.let {clip -> (0 until clip.itemCount).map {clip.getItemAt(it).uri}} ?: listOfNotNull(data?.data)
        val uri=uris.firstOrNull()?:return
        when(requestCode) {
            pickModel -> {showTab(3);library.importFromUris(uris)}
            pickCertificate -> thread(name="nodexlm-cert-import") {
                try {
                    val bytes=contentResolver.openInputStream(uri)?.use { boundedPairingBytes(it, 65536) } ?: error("Certificate unreadable")
                    require(bytes.size<65_536)
                    java.security.cert.CertificateFactory.getInstance("X.509").generateCertificate(bytes.inputStream())
                    File(filesDir,"coordinator.crt").writeBytes(bytes)
                    runOnUiThread {setStatus(getString(R.string.ui_d98465fd9e86))}
                }catch(e:Exception){runOnUiThread {setStatus(getString(R.string.cert_rejected,e.message))}}
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
    private fun field(parent: LinearLayout, hint: String, value: String): EditText = EditText(this).apply {
        this.hint = hint
        setSingleLine(true)
        setText(value)
        textSize = 14f
        setTextColor(0xffd7dce1.toInt())
        setHintTextColor(0xff929ba5.toInt())
        setPadding(dp(14), 0, dp(14), 0)
        background = GradientDrawable().apply {
            setColor(0xff23272b.toInt())
            cornerRadius = dp(9).toFloat()
            setStroke(dp(1), 0xff3c434a.toInt())
        }
        parent.addView(this, LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.MATCH_PARENT, dp(52)).apply { bottomMargin = dp(10) })
    }

    private fun label(value: String, size: Int, bold: Boolean) = TextView(this).apply {
        text = value
        textSize = size.toFloat()
        setTextColor(if (bold) 0xffd9dee3.toInt() else 0xffa2abb4.toInt())
        if (bold) setTypeface(null, Typeface.BOLD)
        setPadding(0, dp(5), 0, dp(5))
    }

    private fun action(value: String, handler: () -> Unit) = Button(this).apply {
        text = value
        isAllCaps = false
        textSize = 13f
        minHeight = dp(44)
        stateListAnimator = null
        elevation = 0f
        setTextColor(0xffdce2e7.toInt())
        backgroundTintList = ColorStateList.valueOf(0xff394550.toInt())
        setOnClickListener { handler() }
    }

    private fun card(): LinearLayout = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        val p = dp(16)
        setPadding(p, p, p, p)
        background = GradientDrawable().apply {
            setColor(0xff292d32.toInt())
            cornerRadius = dp(12).toFloat()
            setStroke(dp(1), 0xff3a4047.toInt())
        }
        layoutParams = LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.MATCH_PARENT,
            LinearLayout.LayoutParams.WRAP_CONTENT).apply { bottomMargin = dp(12) }
    }

    private fun space(parent: LinearLayout, size: Int) =
        parent.addView(View(this), LinearLayout.LayoutParams(dp(1), dp(size)))

    private fun dp(value: Int) = (value * resources.displayMetrics.density).toInt()
}

/** Flat network mark: connected nodes represent the Host and its Guests. */
private class ClusterMark(context: android.content.Context) : View(context) {
    private val ink = Paint(Paint.ANTI_ALIAS_FLAG)

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        canvas.save()
        canvas.scale(width / 100f, height / 100f)
        ink.style = Paint.Style.STROKE
        ink.strokeCap = Paint.Cap.ROUND
        ink.strokeWidth = 5f
        ink.color = 0xff8194a6.toInt()
        for (segment in arrayOf(
            floatArrayOf(18f, 18f, 18f, 82f),
            floatArrayOf(18f, 18f, 50f, 50f),
            floatArrayOf(50f, 50f, 82f, 82f),
            floatArrayOf(82f, 18f, 82f, 82f)
        )) canvas.drawLine(segment[0], segment[1], segment[2], segment[3], ink)
        ink.style = Paint.Style.FILL
        for ((x, y) in arrayOf(
            18f to 18f, 18f to 82f, 50f to 50f, 82f to 18f, 82f to 82f
        )) {
            ink.color = if (x == 50f) 0xffa9b5c0.toInt() else 0xff8194a6.toInt()
            canvas.drawCircle(x, y, 8f, ink)
        }
        canvas.restore()
    }
}
