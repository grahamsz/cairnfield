package app.cairnfield.mobile

import java.net.URI

internal object AndroidBackNavigation {
    /**
     * When the WebView history is exhausted on a note page, back should return
     * to the notes root instead of closing the activity.
     */
    fun noteFallbackUrl(serverOrigin: String, currentUrl: String?): String? {
        if (currentUrl.isNullOrBlank()) return null
        val location = CairnfieldPrefs.internalLocation(serverOrigin, currentUrl) ?: return null
        val basePath = CairnfieldPrefs.basePath(serverOrigin)
        val appPath = if (basePath.isEmpty()) location else location.removePrefix(basePath)
        if (!appPath.startsWith("/notes/")) return null
        return CairnfieldPrefs.resolveInternalUrl(serverOrigin, "/")
    }
}
