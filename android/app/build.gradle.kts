import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

fun signingProperty(name: String): String =
    System.getenv(name)?.takeIf { it.isNotBlank() }
        ?: (project.findProperty(name) as? String).orEmpty()

val cairnfieldVersionCode = System.getenv("CAIRNFIELD_ANDROID_VERSION_CODE")?.toIntOrNull() ?: 1
val cairnfieldVersionName = System.getenv("CAIRNFIELD_ANDROID_VERSION_NAME")?.takeIf { it.isNotBlank() } ?: "0.1.0"
val releaseStorePath = signingProperty("CAIRNFIELD_KEYSTORE_FILE")
val releaseStorePassword = signingProperty("CAIRNFIELD_KEYSTORE_PASSWORD")
val releaseKeyAlias = signingProperty("CAIRNFIELD_KEY_ALIAS")
val releaseKeyPassword = signingProperty("CAIRNFIELD_KEY_PASSWORD")
val hasReleaseSigning = listOf(releaseStorePath, releaseStorePassword, releaseKeyAlias, releaseKeyPassword).all { it.isNotBlank() }

android {
    namespace = "app.cairnfield.mobile"
    compileSdk = 35

    defaultConfig {
        applicationId = "app.cairnfield.mobile"
        minSdk = 26
        targetSdk = 35
        versionCode = cairnfieldVersionCode
        versionName = cairnfieldVersionName
    }

    signingConfigs {
        if (hasReleaseSigning) {
            create("cairnfieldRelease") {
                storeFile = file(releaseStorePath)
                storePassword = releaseStorePassword
                keyAlias = releaseKeyAlias
                keyPassword = releaseKeyPassword
            }
        }
    }

    buildTypes {
        getByName("release") {
            isMinifyEnabled = false
            signingConfig = if (hasReleaseSigning) {
                signingConfigs.getByName("cairnfieldRelease")
            } else {
                signingConfigs.getByName("debug")
            }
        }
    }

    buildFeatures {
        buildConfig = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_17)
    }
}

dependencies {
    implementation("androidx.activity:activity:1.10.1")
    implementation("androidx.core:core-ktx:1.15.0")
    implementation("androidx.webkit:webkit:1.12.1")
    implementation("androidx.work:work-runtime:2.11.2")
    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20240303")
}
