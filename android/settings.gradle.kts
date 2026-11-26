pluginManagement {
    // Optional LiteRT SDK uses newer Kotlin metadata; keep the universal build unchanged.
    if (providers.gradleProperty("nodexLiteRT").orNull == "true") {
        resolutionStrategy.eachPlugin {
            when (requested.id.id) {
                "com.android.application" -> useVersion("8.10.1")
                "org.jetbrains.kotlin.android" -> useVersion("2.2.21")
            }
        }
    }
    repositories { google(); mavenCentral(); gradlePluginPortal() }
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories { google(); mavenCentral() }
}
rootProject.name = "NodexLM"
include(":app")
