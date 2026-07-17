package app.cairnfield.mobile

import org.json.JSONObject

data class UpdateOffer(
    val versionName: String,
    val apkURL: String,
    val sha256: String
)

object UpdatePolicy {
    private val sha256Pattern = Regex("^[a-fA-F0-9]{64}$")

    fun normalizeVersionName(value: String): String =
        value.trim().removePrefix("v").removePrefix("V")

    fun shouldOffer(candidateVersionName: String, installedVersionName: String): Boolean =
        compareVersions(normalizeVersionName(candidateVersionName), normalizeVersionName(installedVersionName)) > 0

    fun needsAPKDownload(candidateVersionName: String, cachedVersionName: String?): Boolean =
        normalizeVersionName(candidateVersionName) != cachedVersionName

    fun validSHA256(value: String): Boolean = value.isBlank() || sha256Pattern.matches(value)

    /**
     * Compares dotted numeric versions ("0.10.2" > "0.9.9"). Non-numeric
     * suffixes ("1.0.0-beta") compare by their leading digits.
     */
    internal fun compareVersions(candidate: String, installed: String): Int {
        val left = versionParts(candidate)
        val right = versionParts(installed)
        val length = maxOf(left.size, right.size)
        for (index in 0 until length) {
            val a = left.getOrElse(index) { 0 }
            val b = right.getOrElse(index) { 0 }
            if (a != b) return a.compareTo(b)
        }
        return 0
    }

    private fun versionParts(value: String): List<Int> =
        value.split('.').map { part -> part.takeWhile { it.isDigit() }.toIntOrNull() ?: 0 }

    /**
     * Builds an installable offer from a GitHub `releases/latest` payload,
     * preferring an asset named `cairnfield-android.apk` and otherwise taking
     * the first `.apk` asset.
     */
    fun parseReleaseOffer(installedVersionName: String, release: JSONObject): UpdateOffer? {
        val versionName = normalizeVersionName(release.optString("tag_name", ""))
        if (versionName.isBlank() || !shouldOffer(versionName, installedVersionName)) return null
        val assets = release.optJSONArray("assets") ?: return null

        var apkURL = ""
        var sha256 = ""
        var fallbackURL = ""
        var fallbackSha256 = ""
        for (index in 0 until assets.length()) {
            val asset = assets.optJSONObject(index) ?: continue
            val name = asset.optString("name", "")
            if (!name.endsWith(".apk", ignoreCase = true)) continue
            val url = asset.optString("browser_download_url", "").trim()
            if (!url.startsWith("https://")) continue
            val digest = asset.optString("digest", "").trim()
                .takeIf { it.startsWith("sha256:") }
                ?.removePrefix("sha256:")
                .orEmpty()
            if (!validSHA256(digest)) continue
            if (name == PREFERRED_APK_NAME) {
                apkURL = url
                sha256 = digest
                break
            }
            if (fallbackURL.isBlank()) {
                fallbackURL = url
                fallbackSha256 = digest
            }
        }
        val resolvedURL = apkURL.ifBlank { fallbackURL }
        if (resolvedURL.isBlank()) return null
        val resolvedSha256 = if (apkURL.isNotBlank()) sha256 else fallbackSha256
        return UpdateOffer(versionName, resolvedURL, resolvedSha256)
    }

    private const val PREFERRED_APK_NAME = "cairnfield-android.apk"
}
