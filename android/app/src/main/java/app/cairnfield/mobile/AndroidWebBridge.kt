package app.cairnfield.mobile

import android.webkit.JavascriptInterface

/**
 * Exposed to page scripts as the global `cairnfieldAndroid` object so the web
 * app can detect the native shell, read its version, and pull native share
 * sessions captured from ACTION_SEND intents.
 */
class AndroidWebBridge(
    private val shareStore: CairnfieldShareStore,
    private val serverOrigin: String,
    private val onClipInApp: (url: String, folderPath: String, title: String) -> Unit = { _, _, _ -> }
) {
    @JavascriptInterface
    fun getVersionName(): String = BuildConfig.VERSION_NAME

    @JavascriptInterface
    fun getVersionCode(): Int = BuildConfig.VERSION_CODE

    @JavascriptInterface
    fun getSharedFilesManifest(shareId: String): String =
        shareStore.manifest(shareId, serverOrigin)?.toString().orEmpty()

    @JavascriptInterface
    fun releaseShare(shareId: String) {
        shareStore.release(shareId)
    }

    /**
     * Called by the web app to clip [url] from the rendered page: the shell
     * loads it in the WebView with a clip bar overlay. Invoked on a
     * JavascriptInterface thread, so the callback must hop to the UI thread.
     */
    @JavascriptInterface
    fun clipInApp(url: String, folderPath: String, title: String) {
        onClipInApp(url, folderPath, title)
    }
}
