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
        val uri = try {
            URI(location)
        } catch (_: Exception) {
            return null
        }
        if (!uri.path.orEmpty().startsWith("/notes/")) return null
        return CairnfieldPrefs.resolveInternalUrl(serverOrigin, "/")
    }
}
