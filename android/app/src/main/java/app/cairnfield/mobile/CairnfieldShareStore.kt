package app.cairnfield.mobile

import android.content.Context
import android.content.Intent
import android.database.Cursor
import android.net.Uri
import android.provider.OpenableColumns
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import androidx.core.content.IntentCompat
import org.json.JSONObject
import java.io.ByteArrayInputStream
import java.util.UUID

/**
 * Captures content:// streams from ACTION_SEND / ACTION_SEND_MULTIPLE intents
 * and serves them back to the web app over the synthetic
 * [CairnfieldSharePolicy.SHARE_PATH] path. Sessions live in a process-wide
 * [CairnfieldShareSessions] instance so they survive Activity recreation.
 */
class CairnfieldShareStore(private val context: Context) {
    fun capture(intent: Intent): String? {
        if (intent.action != Intent.ACTION_SEND && intent.action != Intent.ACTION_SEND_MULTIPLE) return null
        val uris = linkedSetOf<Uri>()
        intent.clipData?.let { clip ->
            for (index in 0 until clip.itemCount) clip.getItemAt(index).uri?.let(uris::add)
        }
        IntentCompat.getParcelableExtra(intent, Intent.EXTRA_STREAM, Uri::class.java)?.let(uris::add)
        IntentCompat.getParcelableArrayListExtra(intent, Intent.EXTRA_STREAM, Uri::class.java)?.let(uris::addAll)
        if (uris.isEmpty()) return null

        val items = uris.mapNotNull { uri -> itemFor(uri, intent.type) }
        if (items.isEmpty()) return null
        return sharedSessions.create(items)
    }

    fun manifest(sessionID: String, serverOrigin: String): JSONObject? =
        sharedSessions.manifest(sessionID, serverOrigin)

    fun release(sessionID: String) = sharedSessions.release(sessionID)

    fun intercept(request: WebResourceRequest, allowedOrigin: String): WebResourceResponse? {
        val parts = CairnfieldSharePolicy.requestParts(allowedOrigin, request.url.toString(), request.method)
            ?: return null
        val item = sharedSessions.lookup(parts[0], parts[1]) ?: return notFound(allowedOrigin)
        val input = try {
            context.contentResolver.openInputStream(Uri.parse(item.uri))
        } catch (_: Exception) {
            null
        } ?: return notFound(allowedOrigin)
        return WebResourceResponse(
            item.type,
            null,
            200,
            "OK",
            responseHeaders(allowedOrigin),
            input
        )
    }

    private fun itemFor(uri: Uri, fallbackMimeType: String?): CairnfieldShareSessions.Entry? {
        if (!CairnfieldSharePolicy.acceptsScheme(uri.scheme)) return null
        if (uri.authority == "${BuildConfig.APPLICATION_ID}.files") return null
        var displayName = "attachment"
        var size = -1L
        try {
            context.contentResolver.query(
                uri,
                arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE),
                null,
                null,
                null
            )?.use { cursor ->
                if (cursor.moveToFirst()) {
                    displayName = cursor.stringValue(OpenableColumns.DISPLAY_NAME).orEmpty().trim().ifBlank { "attachment" }
                    size = cursor.longValue(OpenableColumns.SIZE) ?: -1L
                }
            }
        } catch (_: Exception) {
            // Some providers expose a stream but not metadata.
        }
        val resolvedMimeType = try {
            context.contentResolver.getType(uri)
        } catch (_: Exception) {
            null
        }
        val mimeType = resolvedMimeType
            ?.substringBefore(';')
            ?.trim()
            ?.takeIf { it.contains('/') }
            ?: fallbackMimeType?.substringBefore(';')?.trim()?.takeIf { it.contains('/') }
            ?: "application/octet-stream"
        return CairnfieldShareSessions.Entry(UUID.randomUUID().toString(), uri.toString(), displayName, mimeType, size)
    }

    private fun notFound(allowedOrigin: String) = WebResourceResponse(
        "application/json",
        "UTF-8",
        404,
        "Not Found",
        responseHeaders(allowedOrigin),
        ByteArrayInputStream("{\"error\":\"not_found\"}".toByteArray())
    )

    private fun responseHeaders(allowedOrigin: String) = mapOf(
        "Access-Control-Allow-Origin" to allowedOrigin,
        "Cache-Control" to "no-store",
        "Vary" to "*",
        "X-Content-Type-Options" to "nosniff"
    )

    private fun Cursor.stringValue(column: String): String? {
        val index = getColumnIndex(column)
        return if (index >= 0 && !isNull(index)) getString(index) else null
    }

    private fun Cursor.longValue(column: String): Long? {
        val index = getColumnIndex(column)
        return if (index >= 0 && !isNull(index)) getLong(index) else null
    }

    companion object {
        private val sharedSessions = CairnfieldShareSessions()
    }
}
