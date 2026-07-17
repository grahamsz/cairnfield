package app.cairnfield.mobile

import android.app.Activity
import android.app.AlertDialog
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest
import java.util.concurrent.atomic.AtomicBoolean

object UpdateChecker {
    private const val RELEASES_API_URL = "https://api.github.com/repos/grahamsz/cairnfield/releases/latest"
    private const val UPDATE_APK_FILENAME = "cairnfield-update.apk"
    private const val CHECK_INTERVAL_MS = 6 * 60 * 60_000L
    private const val MAX_APK_BYTES = 250L * 1024 * 1024
    private val checking = AtomicBoolean(false)

    fun check(context: Context, force: Boolean = false) {
        if (!checking.compareAndSet(false, true)) return
        try {
            readyUpdate(context, force)?.let { notifyInstall(context, it) }
        } finally {
            checking.set(false)
        }
    }

    fun checkInForeground(
        activity: Activity,
        force: Boolean = false,
        shouldPrompt: (ReadyUpdate) -> Boolean = { true }
    ) {
        if (!checking.compareAndSet(false, true)) return
        Thread {
            val update = try {
                readyUpdate(activity.applicationContext, force)
            } finally {
                checking.set(false)
            }
            if (update == null || activity.isFinishing || activity.isDestroyed) return@Thread
            notifyInstall(activity, update)
            activity.runOnUiThread {
                if (activity.isFinishing || activity.isDestroyed) return@runOnUiThread
                val lifecycle = (activity as? LifecycleOwner)?.lifecycle
                if (lifecycle != null && !lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) {
                    return@runOnUiThread
                }
                if (!shouldPrompt(update)) return@runOnUiThread
                AlertDialog.Builder(activity)
                    .setTitle("cairnfield update ready")
                    .setMessage("Version ${update.versionName} has been downloaded from GitHub.")
                    .setNegativeButton("Later", null)
                    .setPositiveButton("Install") { _, _ -> openInstaller(activity) }
                    .show()
            }
        }.start()
    }

    private fun readyUpdate(context: Context, force: Boolean): ReadyUpdate? {
        val cached = cachedReadyUpdate(context)
        val now = System.currentTimeMillis()
        if (!force && now - CairnfieldPrefs.lastUpdateCheck(context) < CHECK_INTERVAL_MS) return cached

        val release = HttpJson.get(RELEASES_API_URL) ?: return cached
        val tagName = release.optString("tag_name", "")
        if (tagName.isNotBlank() && !UpdatePolicy.shouldOffer(tagName, BuildConfig.VERSION_NAME)) {
            // The latest release is not newer than the installed build.
            discardReadyUpdate(context)
            CairnfieldPrefs.setLastUpdateCheck(context, now)
            return null
        }
        val offer = UpdatePolicy.parseReleaseOffer(BuildConfig.VERSION_NAME, release) ?: return cached
        if (!UpdatePolicy.needsAPKDownload(offer.versionName, cached?.versionName)) {
            CairnfieldPrefs.setReadyUpdate(context, offer.versionName)
            CairnfieldPrefs.setLastUpdateCheck(context, now)
            return ReadyUpdate(offer.versionName)
        }

        val apk = download(context, offer.apkURL) ?: return cachedReadyUpdate(context)
        if (offer.sha256.isNotBlank() && !offer.sha256.equals(fileSHA256(apk), ignoreCase = true)) {
            discardReadyUpdate(context)
            return null
        }
        if (!UpdateAPKValidator.validate(context, apk, offer.versionName)) {
            discardReadyUpdate(context)
            return null
        }
        CairnfieldPrefs.setReadyUpdate(context, offer.versionName)
        CairnfieldPrefs.setLastUpdateCheck(context, now)
        return ReadyUpdate(offer.versionName)
    }

    private fun cachedReadyUpdate(context: Context): ReadyUpdate? {
        val stored = CairnfieldPrefs.readyUpdate(context) ?: return null
        val apk = updateAPK(context)
        if (
            !UpdatePolicy.shouldOffer(stored.versionName, BuildConfig.VERSION_NAME) ||
            !UpdateAPKValidator.validate(context, apk, stored.versionName)
        ) {
            CairnfieldPrefs.clearReadyUpdate(context)
            apk.delete()
            return null
        }
        return ReadyUpdate(stored.versionName)
    }

    private fun download(context: Context, rawURL: String): File? {
        val connection = (URL(rawURL).openConnection() as HttpURLConnection).apply {
            connectTimeout = 10_000
            readTimeout = 30_000
            setRequestProperty("Accept", "application/vnd.android.package-archive")
            setRequestProperty("User-Agent", "cairnfield-android")
            instanceFollowRedirects = true
            useCaches = false
        }
        val target = updateAPK(context)
        val partial = File(target.parentFile, "$UPDATE_APK_FILENAME.part")
        return try {
            if (connection.responseCode !in 200..299) return null
            val declaredLength = connection.contentLengthLong
            if (declaredLength > MAX_APK_BYTES) return null
            var copied = 0L
            connection.inputStream.use { input ->
                partial.outputStream().use { output ->
                    val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
                    while (true) {
                        val read = input.read(buffer)
                        if (read <= 0) break
                        copied += read
                        if (copied > MAX_APK_BYTES) return null
                        output.write(buffer, 0, read)
                    }
                }
            }
            if (target.exists() && !target.delete()) return null
            if (!partial.renameTo(target)) return null
            target
        } catch (_: Exception) {
            null
        } finally {
            partial.delete()
            connection.disconnect()
        }
    }

    private fun notifyInstall(context: Context, update: ReadyUpdate) {
        NotificationChannels.ensure(context)
        val pending = PendingIntent.getActivity(
            context,
            400,
            Intent(context, UpdateInstallActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val notification = NotificationCompat.Builder(context, NotificationChannels.UPDATES)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("cairnfield update ready")
            .setContentText("Install ${update.versionName}")
            .setContentIntent(pending)
            .setAutoCancel(true)
            .build()
        try {
            context.getSystemService(NotificationManager::class.java).notify(2001, notification)
        } catch (_: SecurityException) {
            // The foreground prompt still works when notification permission is denied.
        }
    }

    private fun openInstaller(context: Context) {
        context.startActivity(Intent(context, UpdateInstallActivity::class.java))
    }

    fun updateAPK(context: Context): File {
        val directory = File(context.cacheDir, "updates")
        if (!directory.exists()) directory.mkdirs()
        return File(directory, UPDATE_APK_FILENAME)
    }

    fun validatedUpdateAPK(context: Context): File? {
        val stored = CairnfieldPrefs.readyUpdate(context) ?: return null
        val apk = updateAPK(context)
        if (!UpdatePolicy.shouldOffer(stored.versionName, BuildConfig.VERSION_NAME) ||
            !UpdateAPKValidator.validate(context, apk, stored.versionName)
        ) {
            CairnfieldPrefs.clearReadyUpdate(context)
            apk.delete()
            return null
        }
        return apk
    }

    private fun discardReadyUpdate(context: Context) {
        CairnfieldPrefs.clearReadyUpdate(context)
        updateAPK(context).delete()
    }

    private fun fileSHA256(file: File): String {
        val digest = MessageDigest.getInstance("SHA-256")
        file.inputStream().use { input ->
            val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
            while (true) {
                val read = input.read(buffer)
                if (read <= 0) break
                digest.update(buffer, 0, read)
            }
        }
        return digest.digest().joinToString("") { "%02x".format(it) }
    }

    data class ReadyUpdate(val versionName: String)
}
