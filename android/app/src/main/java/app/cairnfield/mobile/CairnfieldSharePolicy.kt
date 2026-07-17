package app.cairnfield.mobile

internal object CairnfieldSharePolicy {
    const val SHARE_PATH = "/cairnfield-native-share/"

    fun acceptsScheme(scheme: String?): Boolean = scheme.equals("content", ignoreCase = true)

    // Browsers attach page-screenshot streams to URL shares; a share whose text
    // carries an http(s) URL is a page clip, so the text must win over streams.
    fun preferTextShare(text: String?): Boolean =
        !text.isNullOrBlank() &&
            (text.contains("https://", ignoreCase = true) || text.contains("http://", ignoreCase = true))

    fun shareUrl(serverOrigin: String, sessionID: String, token: String): String =
        serverOrigin.trimEnd('/') + SHARE_PATH + sessionID + "/" + token

    fun requestParts(serverOrigin: String, requestUrl: String, method: String = "GET"): List<String>? {
        if (!method.equals("GET", ignoreCase = true)) return null
        val location = CairnfieldPrefs.internalLocation(serverOrigin, requestUrl) ?: return null
        val path = location.substringBefore('?').substringBefore('#')
        // internalLocation keeps the server's base path, so a share URL for a
        // server at https://host/notes arrives as /notes/cairnfield-native-share/...
        val basePath = CairnfieldPrefs.basePath(serverOrigin)
        val relative = if (basePath.isNotEmpty()) path.removePrefix(basePath) else path
        if (!relative.startsWith(SHARE_PATH)) return null
        return relative.removePrefix(SHARE_PATH).split('/').takeIf { parts ->
            parts.size == 2 && parts.all { it.isNotBlank() }
        }
    }
}
