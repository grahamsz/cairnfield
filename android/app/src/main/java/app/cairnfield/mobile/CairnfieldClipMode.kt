package app.cairnfield.mobile

import org.json.JSONObject
import org.json.JSONTokener
import java.net.URI

/**
 * Pure decision and payload logic for in-app clip mode: which navigations stay
 * inside the WebView while a page is being clipped, what metadata accompanies
 * the serialized page upload, and how the server's clip response is read.
 */
internal object CairnfieldClipMode {
    const val MAX_CLIP_HTML_BYTES = 40 * 1024 * 1024

    /** Origin the app serves its bundled SingleFile assets from inside the WebView. */
    const val SINGLE_FILE_MODULE_URL =
        "https://appassets.androidplatform.net/assets/single-file/single-file.js"

    /**
     * Runs the bundled SingleFile library against the rendered page and hands
     * the result to the native cairnfieldClipCallback JavascriptInterface as
     * {"title","url","html"} (or {"error"}). Options mirror the extension's
     * full-page capture in extension/content-capture.js.
     */
    const val SINGLE_FILE_RUN_JS = """
(function() {
  import("$SINGLE_FILE_MODULE_URL").then(function(singleFile) {
    return singleFile.getPageData({
      blockScripts: true,
      removeHiddenElements: true,
      removeUnusedStyles: false,
      removeUnusedFonts: false,
      removeScripts: true,
      removeFrames: true,
      loadDeferredImages: true,
      networkTimeout: 8000,
      maxResourceSizeEnabled: true,
      maxResourceSize: 16,
      compressHTML: false,
      insertMetaCSP: false,
      insertMetaNoIndex: true,
      insertCanonicalLink: true,
      url: location.href
    }, {}, document, window);
  }).then(function(pageData) {
    var html = typeof pageData.content === "string" ? pageData.content : new TextDecoder().decode(new Uint8Array(pageData.content));
    window.cairnfieldClipCallback.done(JSON.stringify({ title: document.title, url: location.href, html: html }));
  }).catch(function(err) {
    window.cairnfieldClipCallback.done(JSON.stringify({ error: String(err) }));
  });
})()
"""

    /**
     * JavaScript fallback for pages where the SingleFile pipeline fails: a
     * naive snapshot returning {"title", "url", "html"} where html is a
     * doctype-prefixed cleaned DOM clone (scripts stripped, URLs absolutized,
     * <base> inserted).
     */
    const val SERIALIZER_JS = """
(function() {
  var clone = document.documentElement.cloneNode(true);
  var stripped = clone.querySelectorAll('script,noscript,iframe,object,embed,template');
  for (var i = 0; i < stripped.length; i++) {
    var node = stripped[i];
    if (node.parentNode) node.parentNode.removeChild(node);
  }
  var all = clone.querySelectorAll('*');
  for (var j = 0; j < all.length; j++) {
    var el = all[j];
    var attrs = el.attributes;
    var remove = [];
    for (var k = 0; k < attrs.length; k++) {
      var name = attrs[k].name.toLowerCase();
      if (name.indexOf('on') === 0 || name === 'srcdoc' || name === 'nonce' ||
          name === 'integrity' || name === 'srcset') {
        remove.push(attrs[k].name);
      }
    }
    for (var m = 0; m < remove.length; m++) el.removeAttribute(remove[m]);
    var urlAttrs = ['href', 'src', 'poster'];
    for (var n = 0; n < urlAttrs.length; n++) {
      var attr = urlAttrs[n];
      if (el.hasAttribute(attr)) {
        var value = el.getAttribute(attr);
        if (value) {
          try { el.setAttribute(attr, new URL(value, location.href).href); } catch (e) {}
        }
      }
    }
  }
  var head = clone.querySelector('head');
  if (head) {
    var base = document.createElement('base');
    base.setAttribute('href', location.href);
    head.insertBefore(base, head.firstChild);
  }
  return JSON.stringify({
    title: document.title,
    url: location.href,
    html: '<!doctype html>\n' + clone.outerHTML
  });
})()
"""

    /** While clip mode is active, only same-site navigations stay in the WebView. */
    fun allowsInWebView(clipMode: Boolean, isSameSite: Boolean): Boolean = clipMode && isSameSite

    /** True when candidate is an http(s) URL on the clip page's host or a sibling/parent subdomain. */
    fun isSameSite(clipUrl: String, candidate: String): Boolean {
        val clipHost = httpHost(clipUrl) ?: return false
        val targetHost = httpHost(candidate) ?: return false
        return targetHost == clipHost ||
            targetHost.endsWith(".$clipHost") ||
            clipHost.endsWith(".$targetHost")
    }

    /** Clip mode may only load http(s) pages in the WebView. */
    fun isHttpUrl(value: String): Boolean = httpHost(value) != null

    fun metadataJson(title: String, pageUrl: String, folderPath: String, capturedAt: String): String =
        JSONObject()
            .put("title", title)
            .put("source_url", pageUrl)
            .put("page_url", pageUrl)
            .put("selection_text", "")
            .put("search_text", "")
            .put("folder_path", folderPath.ifBlank { "/" })
            .put("destination_kind", "folder")
            .put("captured_at", capturedAt)
            .toString()

    /**
     * Unwraps the JSON-encoded string delivered by evaluateJavascript and reads
     * the serialized page. Returns null when the script produced no usable html.
     */
    fun parsePageSnapshot(evaluateJavascriptResult: String): ClipPageSnapshot? = try {
        val raw = JSONTokener(evaluateJavascriptResult).nextValue() as? String ?: return null
        val json = JSONObject(raw)
        val html = json.optString("html").takeIf { it.isNotBlank() } ?: return null
        ClipPageSnapshot(
            title = json.optString("title"),
            url = json.optString("url"),
            html = html
        )
    } catch (_: Exception) {
        null
    }

    /** Reads the created note's slug from the clip endpoint's 2xx response body. */
    fun parseClipSlug(responseBody: String): String? = try {
        JSONObject(responseBody)
            .optJSONObject("note")
            ?.optString("slug")
            .orEmpty()
            .takeIf { it.isNotBlank() }
    } catch (_: Exception) {
        null
    }

    private fun httpHost(value: String): String? = try {
        URI(value.trim()).takeIf {
            it.isAbsolute && (it.scheme.equals("http", ignoreCase = true) ||
                it.scheme.equals("https", ignoreCase = true))
        }?.host?.lowercase()?.takeIf { it.isNotBlank() }
    } catch (_: Exception) {
        null
    }
}

internal data class ClipPageSnapshot(
    val title: String,
    val url: String,
    val html: String
)
