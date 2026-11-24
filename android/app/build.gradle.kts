plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}
val rustRpcEnabled = providers.gradleProperty("nodexRustRpc").map { it == "true" }.orElse(false).get()
val rustRpcOutput = layout.buildDirectory.dir("generated/rustRpc/jniLibs")
val llamaRevision = groovy.json.JsonSlurper().parse(file("../../internal/llamaruntime/revision.json")) as Map<*, *>
val releaseKeystore = System.getenv("NODEXLM_ANDROID_KEYSTORE_FILE")
val releaseStorePassword = System.getenv("NODEXLM_ANDROID_STORE_PASSWORD")
val releaseKeyAlias = System.getenv("NODEXLM_ANDROID_KEY_ALIAS")
val releaseKeyPassword = System.getenv("NODEXLM_ANDROID_KEY_PASSWORD")
val hasReleaseSigning = listOf(
    releaseKeystore, releaseStorePassword, releaseKeyAlias, releaseKeyPassword
).all { !it.isNullOrBlank() }

android {
    buildFeatures { buildConfig = true }
    namespace = "dev.nodexlm.app"
    compileSdk = 35
    ndkVersion = "27.2.12479018"
    defaultConfig {
        buildConfigField("boolean", "RUST_RPC_ENABLED", rustRpcEnabled.toString())
        buildConfigField("String", "LLAMA_VERSION", "\"${llamaRevision["version"]}\"")
        applicationId = "dev.nodexlm.app"
        minSdk = 29 // Android 10 (API 29) and newer
        targetSdk = 35
        versionCode = providers.gradleProperty("nodexVersionCode").orNull?.toIntOrNull() ?: 1
        versionName = providers.gradleProperty("nodexVersionName").orNull ?: "0.1.0-alpha"
        // Both common smartphone ABIs: 32-bit Android 10 and 64-bit Android 10+.
        // Android loads only its matching library from the universal APK.
        ndk { abiFilters += listOf("armeabi-v7a", "arm64-v8a") }
        externalNativeBuild { cmake { arguments += "-DANDROID_SUPPORT_FLEXIBLE_PAGE_SIZES=ON"; cppFlags += listOf("-std=c++17", "-O3", "-fvisibility=hidden") } }
    }
    externalNativeBuild { cmake { path = file("src/main/cpp/CMakeLists.txt") } }
    signingConfigs {
        if (hasReleaseSigning) {
            create("nodexRelease") {
                storeFile = file(requireNotNull(releaseKeystore))
                storePassword = releaseStorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseKeyPassword
            }
        }
    }
    buildTypes {
        getByName("release") {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            if (hasReleaseSigning) signingConfig = signingConfigs.getByName("nodexRelease")
        }
    }
    compileOptions { sourceCompatibility = JavaVersion.VERSION_17; targetCompatibility = JavaVersion.VERSION_17 }
    kotlinOptions { jvmTarget = "17" }
    packaging { jniLibs { useLegacyPackaging = false } }
    if (rustRpcEnabled) sourceSets.getByName("main").jniLibs.srcDir(rustRpcOutput)
}

dependencies {
    // ONNX inference executes inside ComputeService; no external Ollaya server.
    implementation("com.microsoft.onnxruntime:onnxruntime-android:1.32.0")
    // Match Android JNI tokenizer and Java API versions. Both ABIs are packaged
    // by the Android variant; HF tokenizer.json is not reimplemented approximately.
    implementation("ai.djl.huggingface:tokenizers:0.40.0")
    testImplementation("junit:junit:4.13.2")
    // Real JSONObject behavior for pure Laya protocol tests on the JVM.
    testImplementation("org.json:json:20250517")
}

// DJL has not published a 16-KiB-page-aligned Android Maven AAR yet.
// Fetch the official published 0.40.0 JNI with pinned hashes. The checksum
// script caches correct bytes and does not download mutable snapshot artifacts.
val prepareDecisionTokenizerNative by tasks.registering(Exec::class) {
    commandLine(
        "python3",
        file("../../scripts/fetch-android-tokenizer.py").absolutePath,
        file("src/main/jniLibs").absolutePath
    )
}
tasks.named("preBuild") {
    dependsOn(prepareDecisionTokenizerNative)
}

if (rustRpcEnabled) {
    val buildRustRpc by tasks.registering(Exec::class) {
        inputs.dir(file("../rpc-rust/src"))
        inputs.files(file("../rpc-rust/Cargo.toml"), file("../rpc-rust/Cargo.lock"), file("../rpc-rust/build-android.sh"))
        outputs.dir(rustRpcOutput)
        commandLine("bash", file("../rpc-rust/build-android.sh").absolutePath, rustRpcOutput.get().asFile.absolutePath)
    }
    tasks.named("preBuild") { dependsOn(buildRustRpc) }
    tasks.configureEach {
        if (name.startsWith("merge") && name.endsWith("NativeLibs")) dependsOn(buildRustRpc)
        if (name.startsWith("merge") && name.endsWith("JniLibFolders")) dependsOn(buildRustRpc)
    }
}
