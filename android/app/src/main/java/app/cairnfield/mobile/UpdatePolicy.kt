package app.cairnfield.mobile

import org.json.JSONObject
import java.net.URL

data class UpdateOffer(
    val versionCode: Int,
    val versionName: String,
    val apkURL: String,
    val sha256: String
)

object UpdatePolicy {
    private val sha256Pattern = Regex("^[a-fA-F0-9]{64}$")

    fun normalizeVersionName(value: String): String =
        value.trim().removePrefix("v").removePrefix("V")

    fun shouldOffer(candidateVersionCode: Int, installedVersionCode: Int): Boolean =
        candidateVersionCode > installedVersionCode

    fun needsAPKDownload(candidateVersionCode: Int, cachedVersionCode: Int?): Boolean =
        candidateVersionCode != cachedVersionCode

    fun validSHA256(value: String): Boolean = value.isBlank() || sha256Pattern.matches(value)

    /**
     * Builds an installable offer from the cairnfield server's `/android/latest.json`
     * metadata. Relative `apkUrl` values are resolved against [serverBaseURL]. Only
     * HTTPS URLs are accepted.
     */
    fun parseServerOffer(
        installedVersionCode: Int,
        serverBaseURL: String,
        metadata: JSONObject
    ): UpdateOffer? {
        val versionCode = metadata.optInt("versionCode", 0)
        if (versionCode == 0 || !shouldOffer(versionCode, installedVersionCode)) return null

        val versionName = metadata.optString("versionName", "").trim()
        if (versionName.isBlank()) return null

        val apkUrl = metadata.optString("apkUrl", "").trim()
        if (apkUrl.isBlank()) return null
        val resolvedURL = resolveApkURL(serverBaseURL, apkUrl) ?: return null

        val sha256 = metadata.optString("sha256", "").trim()
        if (!validSHA256(sha256)) return null

        return UpdateOffer(versionCode, versionName, resolvedURL, sha256)
    }

    private fun resolveApkURL(serverBaseURL: String, apkUrl: String): String? {
        if (apkUrl.isBlank()) return null

        return try {
            val direct = URL(apkUrl)
            if (direct.protocol != "https") return null
            direct.toString()
        } catch (_: Exception) {
            try {
                val resolved = URL(URL(serverBaseURL), apkUrl)
                if (resolved.protocol != "https") return null
                resolved.toString()
            } catch (_: Exception) {
                null
            }
        }
    }
}
