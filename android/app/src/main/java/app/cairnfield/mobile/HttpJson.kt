package app.cairnfield.mobile

import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

internal data class HttpJsonResponse(val statusCode: Int, val body: JSONObject?)

object HttpJson {
    fun get(
        url: String,
        connectTimeoutMillis: Int = 5_000,
        readTimeoutMillis: Int = 5_000
    ): JSONObject? = getResponse(url, connectTimeoutMillis, readTimeoutMillis)
        ?.takeIf { it.statusCode in 200..299 }
        ?.body

    internal fun getResponse(
        url: String,
        connectTimeoutMillis: Int = 5_000,
        readTimeoutMillis: Int = 5_000
    ): HttpJsonResponse? {
        val conn = try {
            (URL(url).openConnection() as HttpURLConnection).apply {
                requestMethod = "GET"
                connectTimeout = connectTimeoutMillis
                readTimeout = readTimeoutMillis
                instanceFollowRedirects = true
                useCaches = false
                setRequestProperty("Accept", "application/json")
                setRequestProperty("Cache-Control", "no-cache")
                // The server may reject requests without a User-Agent.
                setRequestProperty("User-Agent", USER_AGENT)
            }
        } catch (_: Exception) {
            return null
        }
        return try {
            val status = conn.responseCode
            val stream = if (status in 200..299) conn.inputStream else conn.errorStream
            val raw = stream?.bufferedReader()?.use { it.readText() }.orEmpty()
            val parsed = try {
                raw.takeIf { it.isNotBlank() }?.let(::JSONObject)
            } catch (_: Exception) {
                null
            }
            HttpJsonResponse(status, parsed)
        } catch (_: Exception) {
            null
        } finally {
            conn.disconnect()
        }
    }

    private const val USER_AGENT = "cairnfield-android"
}
