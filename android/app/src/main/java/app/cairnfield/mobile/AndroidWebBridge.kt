package app.cairnfield.mobile

import android.webkit.JavascriptInterface

/**
 * Exposed to page scripts as the global `cairnfieldAndroid` object so the web
 * app can detect the native shell and read its version.
 */
class AndroidWebBridge {
    @JavascriptInterface
    fun getVersionName(): String = BuildConfig.VERSION_NAME

    @JavascriptInterface
    fun getVersionCode(): Int = BuildConfig.VERSION_CODE
}
