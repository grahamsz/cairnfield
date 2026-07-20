package app.cairnfield.mobile

import android.Manifest
import android.app.AlertDialog
import android.app.DownloadManager
import android.content.ActivityNotFoundException
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.Color
import android.net.Uri
import android.net.http.SslError
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.JavascriptInterface
import android.webkit.SslErrorHandler
import android.webkit.URLUtil
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.lifecycle.Lifecycle
import androidx.webkit.ServiceWorkerClientCompat
import androidx.webkit.ServiceWorkerControllerCompat
import androidx.webkit.WebViewFeature
import org.json.JSONObject
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import java.time.Instant
import java.util.UUID

class MainActivity : ComponentActivity() {
    private var webView: WebView? = null
    private var cairnfieldWebChromeClient: CairnfieldWebChromeClient? = null
    private var loadingOverlay: FrameLayout? = null
    private var loadingAnimationView: CairnfieldLoadingView? = null
    private var loadingRevealGate: LoadingRevealGate? = null
    private var updatePromptPolicy = UpdatePromptPolicy()
    private var clipModeActive = false
    private var clipUrl = ""
    private var clipFolderPath = ""
    private var clipTitle = ""
    private var clipBar: View? = null
    private var clipCallbackInterface: Any? = null
    private val cairnfieldShareStore by lazy { CairnfieldShareStore(applicationContext) }
    private val appAssetLoader by lazy { CairnfieldAssetServer.buildLoader(applicationContext) }
    private val notificationPermissionLauncher = registerForActivityResult(ActivityResultContracts.RequestPermission()) { }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        updatePromptPolicy = UpdatePromptPolicy(savedInstanceState?.getString(STATE_PROMPTED_UPDATE_NAME).orEmpty())
        installBackNavigation()
        WindowCompat.setDecorFitsSystemWindows(window, false)
        WindowInsetsControllerCompat(window, window.decorView).apply {
            isAppearanceLightStatusBars = true
            isAppearanceLightNavigationBars = true
        }
        NotificationChannels.ensure(this)
        UpdateCheckWorker.schedule(this)
        requestNotificationPermission()
        if (CairnfieldPrefs.serverUrl(this).isBlank()) {
            showSetup()
        } else {
            showWeb(
                intent,
                savedInstanceState?.getBundle(STATE_WEB_VIEW),
                savedInstanceState?.getLong(STATE_LOADING_ANIMATION_ELAPSED_MS) ?: 0L
            )
            if (savedInstanceState?.getBoolean(STATE_CLIP_MODE) == true) {
                enterClipMode(
                    savedInstanceState.getString(STATE_CLIP_URL).orEmpty(),
                    savedInstanceState.getString(STATE_CLIP_FOLDER).orEmpty(),
                    savedInstanceState.getString(STATE_CLIP_TITLE).orEmpty()
                )
            }
        }
    }

    override fun onStart() {
        super.onStart()
        checkForAppUpdate()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        if (webView == null) {
            if (CairnfieldPrefs.serverUrl(this).isBlank()) showSetup() else showWeb(intent)
        } else {
            shareUrlForIntent(intent)?.let { target ->
                webView?.loadUrl(target)
                consumeShareIntent()
            }
        }
    }

    override fun onPause() {
        rememberCurrentLocation()
        CookieManager.getInstance().flush()
        super.onPause()
    }

    override fun onSaveInstanceState(outState: Bundle) {
        rememberCurrentLocation()
        outState.putString(STATE_PROMPTED_UPDATE_NAME, updatePromptPolicy.lastPromptedVersionName)
        outState.putLong(
            STATE_LOADING_ANIMATION_ELAPSED_MS,
            loadingAnimationView?.elapsedMs ?: CairnfieldLoadingView.DURATION_MS
        )
        outState.putBoolean(STATE_CLIP_MODE, clipModeActive)
        if (clipModeActive) {
            outState.putString(STATE_CLIP_URL, clipUrl)
            outState.putString(STATE_CLIP_FOLDER, clipFolderPath)
            outState.putString(STATE_CLIP_TITLE, clipTitle)
        }
        webView?.let { view ->
            val state = Bundle()
            view.saveState(state)
            outState.putBundle(STATE_WEB_VIEW, state)
        }
        super.onSaveInstanceState(outState)
    }

    override fun onDestroy() {
        cancelLoadingExperience()
        cairnfieldWebChromeClient?.cancelPendingRequest()
        cairnfieldWebChromeClient = null
        webView?.destroy()
        webView = null
        super.onDestroy()
    }

    @Deprecated("The file chooser uses the platform activity result callback.")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (cairnfieldWebChromeClient?.handleActivityResult(requestCode, resultCode, data) == true) return
        super.onActivityResult(requestCode, resultCode, data)
    }

    private fun showSetup(initialUrl: String = CairnfieldPrefs.serverUrl(this), message: String = "") {
        cancelLoadingExperience()
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            gravity = Gravity.CENTER
            setBackgroundColor(SHELL_BACKGROUND)
            setPadding(dp(24), dp(24), dp(24), dp(24))
            layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT)
        }
        val title = TextView(this).apply {
            text = "cairnfield"
            textSize = 28f
        }
        val feedback = TextView(this).apply {
            text = message
            setTextColor(Color.rgb(151, 43, 43))
            visibility = if (message.isBlank()) View.GONE else View.VISIBLE
        }
        val input = EditText(this).apply {
            hint = "https://notes.example.com"
            inputType = android.text.InputType.TYPE_CLASS_TEXT or android.text.InputType.TYPE_TEXT_VARIATION_URI
            setSingleLine(true)
            setText(initialUrl)
            setSelection(text.length)
        }
        val connect = Button(this).apply { text = "Connect" }
        connect.setOnClickListener {
            val normalized = CairnfieldPrefs.normalizeBaseUrl(input.text.toString())
            if (normalized.isEmpty()) {
                input.error = "Enter a valid HTTPS cairnfield server URL."
                input.requestFocus()
                return@setOnClickListener
            }

            input.isEnabled = false
            connect.isEnabled = false
            connect.text = "Checking..."
            feedback.setTextColor(Color.rgb(74, 81, 78))
            feedback.text = "Checking the cairnfield server..."
            feedback.visibility = View.VISIBLE

            Thread({
                val result = CairnfieldServerValidator.validate(normalized)
                runOnUiThread {
                    if (isFinishing || isDestroyed) return@runOnUiThread
                    input.isEnabled = true
                    connect.isEnabled = true
                    connect.text = "Connect"
                    if (!result.valid) {
                        feedback.setTextColor(Color.rgb(151, 43, 43))
                        feedback.text = result.message
                        return@runOnUiThread
                    }
                    CairnfieldPrefs.setServerUrl(this@MainActivity, normalized)
                    showWeb(intent)
                }
            }, "cairnfield-server-check").start()
        }
        root.addView(title, spacedLayoutParams(top = 0, bottom = 12))
        root.addView(feedback, spacedLayoutParams(bottom = 12))
        root.addView(input, spacedLayoutParams(bottom = 8))
        root.addView(connect, spacedLayoutParams(bottom = 4))
        applySystemBarInsets(root, dp(24))
        setContentView(root)
    }

    private fun showWeb(
        sourceIntent: Intent?,
        restoredState: Bundle? = null,
        restoredLoadingElapsedMs: Long = 0L
    ) {
        cancelLoadingExperience()
        val view = WebView(this)
        view.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS
        webView = view
        val serverOrigin = CairnfieldPrefs.serverUrl(this)
        val loadingAnimation = CairnfieldLoadingView(this).apply {
            importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO
        }
        val loadingOverlay = FrameLayout(this).apply {
            setBackgroundColor(SHELL_BACKGROUND)
            isClickable = true
            importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_YES
            contentDescription = getString(R.string.app_name)
            setOnLongClickListener {
                showServerChangePrompt()
                true
            }
            addView(
                loadingAnimation,
                FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    ViewGroup.LayoutParams.MATCH_PARENT
                )
            )
        }
        fun removeLoadingOverlay() {
            (view.parent as? View)?.setBackgroundColor(Color.WHITE)
            (loadingOverlay.parent as? ViewGroup)?.removeView(loadingOverlay)
            if (this.loadingOverlay === loadingOverlay) this.loadingOverlay = null
            if (loadingAnimationView === loadingAnimation) loadingAnimationView = null
        }
        lateinit var revealGate: LoadingRevealGate
        revealGate = LoadingRevealGate {
            if (webView !== view || loadingRevealGate !== revealGate || loadingOverlay.parent == null) {
                return@LoadingRevealGate
            }
            loadingRevealGate = null
            loadingOverlay.isClickable = false
            loadingOverlay.setOnLongClickListener(null)
            loadingOverlay.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS
            view.importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_AUTO
            if (!loadingAnimation.animationsEnabled) {
                removeLoadingOverlay()
            } else {
                loadingOverlay.animate()
                    .alpha(0f)
                    .setDuration(LOADING_CROSSFADE_MS)
                    .withEndAction(::removeLoadingOverlay)
                    .start()
            }
        }
        loadingAnimation.onAnimationComplete = revealGate::markAnimationReady
        installShareServiceWorkerInterceptor(serverOrigin)
        CookieManager.getInstance().setAcceptCookie(true)
        CookieManager.getInstance().setAcceptThirdPartyCookies(view, true)
        view.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            databaseEnabled = true
            cacheMode = WebSettings.LOAD_DEFAULT
            mediaPlaybackRequiresUserGesture = false
            mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
            setSupportMultipleWindows(true)
        }
        view.addJavascriptInterface(
            AndroidWebBridge(cairnfieldShareStore, serverOrigin) { url, folderPath, title ->
                runOnUiThread { enterClipMode(url, folderPath, title) }
            },
            "cairnfieldAndroid"
        )
        view.webViewClient = object : WebViewClient() {
            private var mainFrameCommitted = false
            private var failureHandled = false
            private var mainNavigationGeneration = 0L

            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                val url = request.url.toString()
                // In clip mode, same-site navigations and redirects stay inside so the
                // user can log in or pass a JS challenge before clipping.
                if (request.isForMainFrame &&
                    CairnfieldClipMode.allowsInWebView(clipModeActive, CairnfieldClipMode.isSameSite(clipUrl, url))
                ) {
                    return false
                }
                val context = WebNavigationContext(
                    isMainFrame = request.isForMainFrame,
                    hasUserGesture = request.hasGesture(),
                    isRedirect = request.isRedirect
                )
                val action = WebNavigationPolicy.action(serverOrigin, url, context)
                val handled = handleWebNavigation(url, context, action)
                if (action == WebNavigationAction.BLOCK && request.isForMainFrame &&
                    (request.isRedirect || !request.hasGesture())
                ) {
                    failInitialLoad(view, "The server redirected outside the configured cairnfield address.")
                }
                return handled
            }

            override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? =
                cairnfieldShareStore.intercept(request, serverOrigin)
                    ?: appAssetLoader.shouldInterceptRequest(request.url)
                    ?: super.shouldInterceptRequest(view, request)

            override fun onPageStarted(view: WebView, url: String, favicon: Bitmap?) {
                mainNavigationGeneration += 1
                if (!clipModeActive) injectAndroidBridge(view)
                if (webView === view && loadingRevealGate === revealGate) {
                    revealGate.markContentPending()
                }
            }

            override fun onPageCommitVisible(view: WebView, url: String) {
                mainFrameCommitted = true
                CairnfieldPrefs.rememberVisitedUrl(this@MainActivity, url)
            }

            override fun onPageFinished(view: WebView, url: String) {
                CairnfieldPrefs.rememberVisitedUrl(this@MainActivity, url)
                if (CairnfieldPrefs.internalLocation(serverOrigin, url) != null) {
                    val expectedGeneration = mainNavigationGeneration
                    view.postVisualStateCallback(System.nanoTime(), object : WebView.VisualStateCallback() {
                        override fun onComplete(requestId: Long) {
                            if (webView !== view || loadingRevealGate !== revealGate ||
                                mainNavigationGeneration != expectedGeneration ||
                                CairnfieldPrefs.internalLocation(serverOrigin, view.url.orEmpty()) == null
                            ) return
                            revealGate.markContentReady()
                        }
                    })
                }
            }

            override fun doUpdateVisitedHistory(view: WebView, url: String, isReload: Boolean) {
                CairnfieldPrefs.rememberVisitedUrl(this@MainActivity, url)
            }

            override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
                if (request.isForMainFrame) {
                    failInitialLoad(view, "Could not connect to this cairnfield server. Check the address and try again.")
                }
            }

            override fun onReceivedHttpError(view: WebView, request: WebResourceRequest, errorResponse: WebResourceResponse) {
                if (request.isForMainFrame && errorResponse.statusCode >= 400) {
                    failInitialLoad(view, "The server did not recognize cairnfield at this address. Check the URL and try again.")
                }
            }

            override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
                handler.cancel()
                failInitialLoad(view, "cairnfield could not verify this server's HTTPS certificate. Check the URL and certificate, then try again.")
            }

            private fun failInitialLoad(view: WebView, message: String) {
                if (mainFrameCommitted || failureHandled) return
                failureHandled = true
                view.post { showConnectionFailure(view, message) }
            }
        }
        cairnfieldWebChromeClient = CairnfieldWebChromeClient(this) { navigation ->
            // Target-blank links stay in the WebView while clip mode is active.
            if (CairnfieldClipMode.allowsInWebView(clipModeActive, CairnfieldClipMode.isSameSite(clipUrl, navigation.url))) {
                webView?.loadUrl(navigation.url)
            } else {
                handleWebNavigation(
                    navigation.url,
                    WebNavigationContext(
                        isMainFrame = true,
                        hasUserGesture = navigation.hasUserGesture,
                        isRedirect = navigation.isRedirect,
                        isNewWindow = true
                    )
                )
            }
        }.also { view.webChromeClient = it }
        view.setDownloadListener { url, userAgent, contentDisposition, mimeType, _ ->
            startDownload(url, userAgent, contentDisposition, mimeType)
        }
        val root = FrameLayout(this).apply {
            setBackgroundColor(SHELL_BACKGROUND)
            addView(view, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
            addView(loadingOverlay, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        }
        this.loadingOverlay = loadingOverlay
        loadingAnimationView = loadingAnimation
        loadingRevealGate = revealGate
        applySystemBarInsets(root)
        setContentView(root)
        if (lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) checkForAppUpdate()

        val shareTarget = shareUrlForIntent(sourceIntent)
        val restored = shareTarget == null && restoredState != null && view.restoreState(restoredState) != null
        if (restored) view.reload() else view.loadUrl(shareTarget ?: urlForIntent())
        loadingAnimation.start(restoredLoadingElapsedMs)
        if (shareTarget != null) consumeShareIntent()
    }

    private fun urlForIntent(): String {
        return CairnfieldPrefs.lastVisitedUrl(this).takeIf { it.isNotBlank() }
            ?: CairnfieldPrefs.buildUrl(this, "/")
    }

    private fun shareUrlForIntent(sourceIntent: Intent?): String? {
        if (sourceIntent?.action != Intent.ACTION_SEND && sourceIntent?.action != Intent.ACTION_SEND_MULTIPLE) {
            return null
        }
        if (CairnfieldPrefs.serverUrl(this).isBlank()) return null
        val text = sourceIntent.getStringExtra(Intent.EXTRA_TEXT).orEmpty()
        if (!CairnfieldSharePolicy.preferTextShare(text)) {
            cairnfieldShareStore.capture(sourceIntent)?.let { shareID ->
                return shareTargetUrl("android_share" to shareID)
            }
        }
        if (text.isBlank()) return null
        val subject = sourceIntent.getStringExtra(Intent.EXTRA_SUBJECT).orEmpty()
        return shareTargetUrl("share_text" to text, "share_subject" to subject)
    }

    private fun shareTargetUrl(vararg params: Pair<String, String>): String {
        val builder = Uri.parse(CairnfieldPrefs.buildUrl(this, "/")).buildUpon()
        params.forEach { (key, value) ->
            if (value.isNotBlank()) builder.appendQueryParameter(key, value)
        }
        return builder.build().toString()
    }

    private fun consumeShareIntent() {
        setIntent(Intent(this, MainActivity::class.java).setAction(Intent.ACTION_MAIN))
    }

    private fun showConnectionFailure(failedView: WebView, message: String) {
        if (webView !== failedView || isFinishing || isDestroyed) return
        cancelLoadingExperience()
        cairnfieldWebChromeClient?.cancelPendingRequest()
        cairnfieldWebChromeClient = null
        webView = null
        failedView.stopLoading()
        showSetup(CairnfieldPrefs.serverUrl(this), message)
        failedView.destroy()
    }

    private fun showServerChangePrompt() {
        AlertDialog.Builder(this)
            .setTitle("Change server")
            .setMessage("Connect to a different cairnfield server?")
            .setNegativeButton("Cancel", null)
            .setPositiveButton("Change") { _, _ ->
                cancelLoadingExperience()
                cairnfieldWebChromeClient?.cancelPendingRequest()
                cairnfieldWebChromeClient = null
                val view = webView
                webView = null
                view?.stopLoading()
                showSetup(CairnfieldPrefs.serverUrl(this))
                view?.destroy()
            }
            .show()
    }

    private fun cancelLoadingExperience() {
        loadingRevealGate?.cancel()
        loadingRevealGate = null
        loadingAnimationView?.cancel()
        loadingAnimationView = null
        loadingOverlay?.animate()?.cancel()
        (loadingOverlay?.parent as? ViewGroup)?.removeView(loadingOverlay)
        loadingOverlay = null
    }

    private fun rememberCurrentLocation() {
        CairnfieldPrefs.rememberVisitedUrl(this, webView?.url)
    }

    // --- In-app clip mode ----------------------------------------------------

    private fun enterClipMode(url: String, folderPath: String, title: String) {
        val view = webView ?: return
        if (!CairnfieldClipMode.isHttpUrl(url)) return
        clipModeActive = true
        clipUrl = url
        clipFolderPath = folderPath
        clipTitle = title
        showClipBar()
        view.loadUrl(url)
    }

    private fun exitClipMode(loadAppHome: Boolean) {
        clipModeActive = false
        clipUrl = ""
        clipFolderPath = ""
        clipTitle = ""
        cancelClipWatchdog()
        dismissClipBar()
        if (loadAppHome) webView?.loadUrl(CairnfieldPrefs.buildUrl(this, "/"))
    }

    private fun showClipBar() {
        val view = webView ?: return
        val parent = view.parent as? ViewGroup ?: return
        dismissClipBar()
        val bar = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setBackgroundColor(Color.WHITE)
            setPadding(dp(16), dp(8), dp(8), dp(8))
            elevation = dp(4).toFloat()
        }
        val label = TextView(this).apply {
            text = "Clip this page?"
            layoutParams = LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f)
        }
        val clipButton = Button(this).apply { text = "Clip" }
        val cancelButton = Button(this).apply { text = "Cancel" }
        clipButton.setOnClickListener { clipCurrentPage(clipButton) }
        cancelButton.setOnClickListener { exitClipMode(loadAppHome = true) }
        bar.addView(label)
        bar.addView(clipButton)
        bar.addView(cancelButton)
        parent.addView(
            bar,
            FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.WRAP_CONTENT,
                Gravity.BOTTOM
            )
        )
        clipBar = bar
    }

    private fun dismissClipBar() {
        (clipBar?.parent as? ViewGroup)?.removeView(clipBar)
        clipBar = null
    }

    private var clipWatchdog: Runnable? = null

    private fun clipCurrentPage(clipButton: Button) {
        val view = webView ?: return
        clipButton.isEnabled = false
        // Prefer the bundled SingleFile library for a self-contained capture
        // (CSS and images inlined); fall back to the naive DOM snapshot if the
        // module cannot load or the pipeline errors.
        val callback = object {
            @JavascriptInterface
            fun done(resultJson: String) {
                cancelClipWatchdog()
                view.post { handleClipSnapshot(CairnfieldClipMode.parsePageSnapshot(resultJson), clipButton) }
            }
        }
        clipCallbackInterface = callback
        view.addJavascriptInterface(callback, "cairnfieldClipCallback")
        view.evaluateJavascript(CairnfieldClipMode.SINGLE_FILE_RUN_JS, null)
        val watchdog = Runnable {
            if (clipCallbackInterface === callback) {
                cancelClipWatchdog()
                runNaiveClipSnapshot(clipButton)
            }
        }
        clipWatchdog = watchdog
        view.postDelayed(watchdog, CLIP_SINGLEFILE_TIMEOUT_MS)
    }

    private fun cancelClipWatchdog() {
        clipWatchdog?.let { webView?.removeCallbacks(it) }
        clipWatchdog = null
        if (clipCallbackInterface != null) {
            webView?.removeJavascriptInterface("cairnfieldClipCallback")
            clipCallbackInterface = null
        }
    }

    private fun handleClipSnapshot(snapshot: ClipPageSnapshot?, clipButton: Button) {
        if (isFinishing || isDestroyed) return
        if (snapshot == null) {
            runNaiveClipSnapshot(clipButton)
            return
        }
        val htmlBytes = snapshot.html.toByteArray(Charsets.UTF_8)
        if (htmlBytes.size > CairnfieldClipMode.MAX_CLIP_HTML_BYTES) {
            clipButton.isEnabled = true
            Toast.makeText(this, "Page too large to clip.", Toast.LENGTH_LONG).show()
            return
        }
        uploadClip(snapshot, htmlBytes, clipButton)
    }

    private fun runNaiveClipSnapshot(clipButton: Button) {
        val view = webView ?: return
        view.evaluateJavascript(CairnfieldClipMode.SERIALIZER_JS) { result ->
            if (isFinishing || isDestroyed) return@evaluateJavascript
            val snapshot = result?.let(CairnfieldClipMode::parsePageSnapshot)
            if (snapshot == null) {
                clipButton.isEnabled = true
                Toast.makeText(this, "Clip failed: could not read this page.", Toast.LENGTH_LONG).show()
                return@evaluateJavascript
            }
            val htmlBytes = snapshot.html.toByteArray(Charsets.UTF_8)
            if (htmlBytes.size > CairnfieldClipMode.MAX_CLIP_HTML_BYTES) {
                clipButton.isEnabled = true
                Toast.makeText(this, "Page too large to clip.", Toast.LENGTH_LONG).show()
                return@evaluateJavascript
            }
            uploadClip(snapshot, htmlBytes, clipButton)
        }
    }

    private fun uploadClip(snapshot: ClipPageSnapshot, htmlBytes: ByteArray, clipButton: Button) {
        val serverUrl = CairnfieldPrefs.serverUrl(this)
        if (serverUrl.isBlank()) {
            clipButton.isEnabled = true
            return
        }
        val endpoint = CairnfieldPrefs.buildUrl(this, "/api/clip/html")
        // Look up cookies for the full endpoint URL: the session cookie's path
        // is the app base path (e.g. /notes/), which does not prefix-match the
        // bare server URL, so getCookie(serverUrl) would return nothing.
        val cookie = CookieManager.getInstance().getCookie(endpoint).orEmpty()
        val metadata = CairnfieldClipMode.metadataJson(
            title = clipTitle.ifBlank { snapshot.title },
            pageUrl = snapshot.url.ifBlank { clipUrl },
            folderPath = clipFolderPath,
            capturedAt = Instant.now().toString()
        )
        Thread({
            val outcome = runCatching { postClip(endpoint, cookie, metadata, htmlBytes) }
            runOnUiThread {
                if (isFinishing || isDestroyed || !clipModeActive) return@runOnUiThread
                outcome.fold(
                    onSuccess = { slug ->
                        if (slug.isNullOrBlank()) {
                            clipButton.isEnabled = true
                            Toast.makeText(this, "Clip failed: the server response had no note to open.", Toast.LENGTH_LONG).show()
                        } else {
                            exitClipMode(loadAppHome = false)
                            webView?.loadUrl(CairnfieldPrefs.buildUrl(this, "/notes/$slug/x"))
                        }
                    },
                    onFailure = { error ->
                        clipButton.isEnabled = true
                        val detail = error.message?.takeIf { it.isNotBlank() } ?: "could not reach the server."
                        Toast.makeText(
                            this,
                            "Clip failed: $detail",
                            Toast.LENGTH_LONG
                        ).show()
                    }
                )
            }
        }, "cairnfield-clip-upload").start()
    }

    private fun postClip(endpoint: String, cookie: String, metadata: String, htmlBytes: ByteArray): String? {
        val boundary = "cairnfieldClip" + UUID.randomUUID().toString().replace("-", "")
        val metadataBytes = metadata.toByteArray(Charsets.UTF_8)
        val htmlHeader = (
            "--$boundary\r\n" +
                "Content-Disposition: form-data; name=\"html\"; filename=\"clip.html\"\r\n" +
                "Content-Type: text/html\r\n\r\n"
            ).toByteArray(Charsets.UTF_8)
        val metadataHeader = (
            "\r\n--$boundary\r\n" +
                "Content-Disposition: form-data; name=\"metadata\"\r\n\r\n"
            ).toByteArray(Charsets.UTF_8)
        val trailer = "\r\n--$boundary--\r\n".toByteArray(Charsets.UTF_8)
        val connection = (URL(endpoint).openConnection() as HttpURLConnection).apply {
            requestMethod = "POST"
            connectTimeout = 15_000
            readTimeout = 60_000
            doOutput = true
            useCaches = false
            instanceFollowRedirects = false
            setRequestProperty("User-Agent", "cairnfield-android")
            setRequestProperty("Accept", "application/json")
            if (cookie.isNotBlank()) setRequestProperty("Cookie", cookie)
            setRequestProperty("Content-Type", "multipart/form-data; boundary=$boundary")
            setFixedLengthStreamingMode(
                htmlHeader.size + htmlBytes.size + metadataHeader.size + metadataBytes.size + trailer.size
            )
        }
        try {
            connection.outputStream.use { output ->
                output.write(htmlHeader)
                output.write(htmlBytes)
                output.write(metadataHeader)
                output.write(metadataBytes)
                output.write(trailer)
            }
            val status = connection.responseCode
            val body = (if (status in 200..299) connection.inputStream else connection.errorStream)
                ?.bufferedReader()?.use { it.readText() }.orEmpty()
            if (status !in 200..299) throw IOException("HTTP $status: ${body.take(300).ifBlank { "no response body" }}")
            return CairnfieldClipMode.parseClipSlug(body)
        } finally {
            connection.disconnect()
        }
    }

    private fun handleWebNavigation(
        candidate: String,
        context: WebNavigationContext,
        action: WebNavigationAction = WebNavigationPolicy.action(CairnfieldPrefs.serverUrl(this), candidate, context)
    ): Boolean {
        return when (action) {
            WebNavigationAction.ALLOW_IN_WEBVIEW -> false
            WebNavigationAction.OPEN_INTERNAL -> {
                webView?.loadUrl(candidate)
                true
            }
            WebNavigationAction.OPEN_EXTERNAL -> {
                openExternalUrl(candidate)
                true
            }
            WebNavigationAction.BLOCK -> {
                if (context.isMainFrame && context.hasUserGesture && !context.isRedirect) {
                    Toast.makeText(this, "cairnfield blocked an unsupported link.", Toast.LENGTH_SHORT).show()
                }
                true
            }
        }
    }

    private fun openExternalUrl(candidate: String) {        val uri = Uri.parse(candidate)
        if (uri.scheme?.lowercase() !in setOf("http", "https", "mailto")) return
        try {
            startActivity(Intent(Intent.ACTION_VIEW, uri).addCategory(Intent.CATEGORY_BROWSABLE))
        } catch (_: ActivityNotFoundException) {
            Toast.makeText(this, "No app can open this link.", Toast.LENGTH_SHORT).show()
        }
    }

    private fun startDownload(url: String?, userAgent: String?, contentDisposition: String?, mimeType: String?) {
        val uri = Uri.parse(url.orEmpty())
        if (uri.scheme?.lowercase() !in setOf("http", "https")) {
            Toast.makeText(this, "This download is not supported.", Toast.LENGTH_SHORT).show()
            return
        }
        val fileName = URLUtil.guessFileName(url, contentDisposition, mimeType)
        val request = DownloadManager.Request(uri).apply {
            setMimeType(mimeType)
            addRequestHeader("User-Agent", userAgent.orEmpty())
            CookieManager.getInstance().getCookie(url)?.let { addRequestHeader("Cookie", it) }
            setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, fileName)
        }
        try {
            getSystemService(DownloadManager::class.java).enqueue(request)
            Toast.makeText(this, "Downloading $fileName", Toast.LENGTH_SHORT).show()
        } catch (_: Exception) {
            Toast.makeText(this, "Could not start the download.", Toast.LENGTH_SHORT).show()
        }
    }

    private fun injectAndroidBridge(view: WebView) {
        val name = JSONObject.quote(BuildConfig.VERSION_NAME)
        val code = BuildConfig.VERSION_CODE
        view.evaluateJavascript(
            "(function() {" +
                "var bridge = window.cairnfieldAndroid || {};" +
                "window.cairnfieldAndroid = {" +
                "versionName: $name, " +
                "versionCode: $code, " +
                "getVersionName: function() { return $name; }, " +
                "getVersionCode: function() { return $code; }, " +
                "getSharedFilesManifest: function(shareId) {" +
                "return bridge.getSharedFilesManifest ? bridge.getSharedFilesManifest(shareId) : '';" +
                "}, " +
                "releaseShare: function(shareId) {" +
                "if (bridge.releaseShare) bridge.releaseShare(shareId);" +
                "}, " +
                "clipInApp: function(url, folderPath, title) {" +
                "if (bridge.clipInApp) bridge.clipInApp(url, folderPath, title);" +
                "}" +
                "};" +
                "})();",
            null
        )
    }

    private fun checkForAppUpdate() {
        if (CairnfieldPrefs.serverUrl(this).isBlank()) return
        UpdateChecker.checkInForeground(this, force = true, shouldPrompt = updatePromptPolicy::shouldPrompt)
    }

    private fun installShareServiceWorkerInterceptor(serverOrigin: String) {
        if (!WebViewFeature.isFeatureSupported(WebViewFeature.SERVICE_WORKER_BASIC_USAGE) ||
            !WebViewFeature.isFeatureSupported(WebViewFeature.SERVICE_WORKER_SHOULD_INTERCEPT_REQUEST)
        ) return
        // Fetches owned by an active service worker bypass the page WebViewClient.
        ServiceWorkerControllerCompat.getInstance().setServiceWorkerClient(object : ServiceWorkerClientCompat() {
            override fun shouldInterceptRequest(request: WebResourceRequest): WebResourceResponse? =
                cairnfieldShareStore.intercept(request, serverOrigin)
        })
    }

    private fun installBackNavigation() {
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (clipModeActive) {
                    exitClipMode(loadAppHome = true)
                    return
                }
                val view = webView
                if (view?.canGoBack() == true) {
                    view.goBack()
                    return
                }
                AndroidBackNavigation.noteFallbackUrl(CairnfieldPrefs.serverUrl(this@MainActivity), view?.url)
                    ?.let {
                        view?.loadUrl(it)
                        return
                    }

                isEnabled = false
                onBackPressedDispatcher.onBackPressed()
            }
        })
    }

    private fun applySystemBarInsets(view: View, contentPadding: Int = 0) {
        ViewCompat.setOnApplyWindowInsetsListener(view) { target, insets ->
            val safe = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout())
            val keyboard = insets.getInsets(WindowInsetsCompat.Type.ime())
            target.setPadding(
                contentPadding + safe.left,
                contentPadding + safe.top,
                contentPadding + safe.right,
                contentPadding + maxOf(safe.bottom, keyboard.bottom)
            )
            insets
        }
        ViewCompat.requestApplyInsets(view)
    }

    private fun spacedLayoutParams(top: Int = 0, bottom: Int = 0) =
        LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
            topMargin = dp(top)
            bottomMargin = dp(bottom)
        }

    private fun dp(value: Int): Int = (value * resources.displayMetrics.density).toInt()

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    companion object {
        private const val STATE_WEB_VIEW = "cairnfield.web_view_state"
        private const val STATE_PROMPTED_UPDATE_NAME = "cairnfield.prompted_update_name"
        private const val STATE_LOADING_ANIMATION_ELAPSED_MS = "cairnfield.loading_animation_elapsed_ms"
        private const val STATE_CLIP_MODE = "cairnfield.clip_mode"
        private const val STATE_CLIP_URL = "cairnfield.clip_url"
        private const val STATE_CLIP_FOLDER = "cairnfield.clip_folder"
        private const val STATE_CLIP_TITLE = "cairnfield.clip_title"
        private const val CLIP_SINGLEFILE_TIMEOUT_MS = 45_000L
        private const val LOADING_CROSSFADE_MS = 160L
        private val SHELL_BACKGROUND = Color.rgb(242, 240, 235)
    }
}
