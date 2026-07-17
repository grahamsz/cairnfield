package app.cairnfield.mobile

import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

object CairnfieldServerValidator {
    data class Result(val valid: Boolean, val message: String = "")

    fun validate(baseUrl: String): Result = validateWith(baseUrl) { url ->
        url.openConnection() as HttpURLConnection
    }

    internal fun validateWith(
        baseUrl: String,
        connectionFactory: (URL) -> HttpURLConnection
    ): Result {
        val endpoint = try {
            URL(baseUrl.trimEnd('/') + "/api/bootstrap")
        } catch (_: Exception) {
            return Result(false, "Enter a valid HTTPS cairnfield server URL.")
        }
        val connection = try {
            connectionFactory(endpoint).apply {
                requestMethod = "GET"
                connectTimeout = 7_000
                readTimeout = 7_000
                instanceFollowRedirects = true
                setRequestProperty("Accept", "application/json")
                setRequestProperty("Cache-Control", "no-cache")
            }
        } catch (_: Exception) {
            return Result(false, CONNECTION_ERROR)
        }

        return try {
            val status = connection.responseCode
            if (status !in 200..299) {
                return Result(false, "The server returned HTTP $status instead of a cairnfield setup response.")
            }
            if (CairnfieldPrefs.internalLocation(baseUrl, connection.url.toString()) == null) {
                return Result(false, "The server redirected outside the configured cairnfield address.")
            }
            if (!isJsonContentType(connection.contentType)) {
                return Result(false, NOT_CAIRNFIELD_ERROR)
            }

            val body = connection.inputStream.bufferedReader(Charsets.UTF_8).use { reader ->
                val result = StringBuilder()
                val buffer = CharArray(4_096)
                while (true) {
                    val count = reader.read(buffer)
                    if (count < 0) break
                    result.append(buffer, 0, count)
                    if (result.length > MAX_BOOTSTRAP_CHARS) return Result(false, NOT_CAIRNFIELD_ERROR)
                }
                result.toString()
            }
            val payload = try {
                JSONObject(body)
            } catch (_: Exception) {
                return Result(false, NOT_CAIRNFIELD_ERROR)
            }
            if (!hasBootstrapMarkers(payload)) Result(false, NOT_CAIRNFIELD_ERROR) else Result(true)
        } catch (_: Exception) {
            Result(false, CONNECTION_ERROR)
        } finally {
            connection.disconnect()
        }
    }

    internal fun hasBootstrapMarkers(payload: JSONObject): Boolean =
        payload.opt("users_exist") is Boolean

    internal fun isJsonContentType(value: String?): Boolean =
        value?.substringBefore(';')?.trim()?.equals("application/json", ignoreCase = true) == true

    private const val MAX_BOOTSTRAP_CHARS = 1_000_000
    private const val CONNECTION_ERROR = "Could not connect to this cairnfield server. Check the address and try again."
    private const val NOT_CAIRNFIELD_ERROR = "This address did not return a cairnfield server response. Check the URL and try again."
}
