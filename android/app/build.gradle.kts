plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}
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
}

dependencies { testImplementation("junit:junit:4.13.2") }
