package app.cairnfield.mobile

import android.content.Context
import android.webkit.WebResourceResponse
import androidx.webkit.WebViewAssetLoader
import java.io.ByteArrayInputStream

/**
 * Serves the app's bundled web assets (the vendored SingleFile library) to the
 * WebView over https://appassets.androidplatform.net with CORS enabled, so an
 * ES module `import()` from any clipped page's origin can load them.
 */
internal object CairnfieldAssetServer {
    private const val ASSETS_PREFIX = "/assets/"

    fun buildLoader(context: Context): WebViewAssetLoader =
        WebViewAssetLoader.Builder()
            .addPathHandler(ASSETS_PREFIX) { path ->
                serveAsset(context, path)
            }
            .build()

    internal fun serveAsset(context: Context, path: String): WebResourceResponse? {
        val clean = path.substringBefore('?').substringBefore('#')
        if (clean.isEmpty() || clean.startsWith("/") || clean.split('/').any { it == ".." }) return null
        val input = try {
            context.assets.open(clean)
        } catch (_: Exception) {
            return null
        }
        return WebResourceResponse(
            mimeTypeFor(clean),
            null,
            200,
            "OK",
            mapOf(
                "Access-Control-Allow-Origin" to "*",
                "Cache-Control" to "no-cache",
                "X-Content-Type-Options" to "nosniff"
            ),
            input
        )
    }

    internal fun mimeTypeFor(path: String): String = when (path.substringAfterLast('.', "").lowercase()) {
        "js", "mjs" -> "text/javascript"
        "css" -> "text/css"
        "html" -> "text/html"
        "json" -> "application/json"
        "svg" -> "image/svg+xml"
        "png" -> "image/png"
        "jpg", "jpeg" -> "image/jpeg"
        "gif" -> "image/gif"
        "webp" -> "image/webp"
        "woff" -> "font/woff"
        "woff2" -> "font/woff2"
        "ttf" -> "font/ttf"
        "wasm" -> "application/wasm"
        "txt" -> "text/plain"
        "zip" -> "application/zip"
        else -> "application/octet-stream"
    }

    /** 404 helper for requests outside the bundled asset tree. */
    fun notFound(): WebResourceResponse = WebResourceResponse(
        "text/plain",
        "UTF-8",
        404,
        "Not Found",
        mapOf("Access-Control-Allow-Origin" to "*"),
        ByteArrayInputStream("not found".toByteArray())
    )
}
