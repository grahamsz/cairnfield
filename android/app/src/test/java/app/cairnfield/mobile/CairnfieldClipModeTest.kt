package app.cairnfield.mobile

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class CairnfieldClipModeTest {
    @Test
    fun sameSiteMatchesHostAndSubdomains() {
        assertTrue(CairnfieldClipMode.isSameSite("https://news.example.com/article", "https://news.example.com/login"))
        assertTrue(CairnfieldClipMode.isSameSite("https://news.example.com/a", "https://cdn.news.example.com/x.png"))
        assertTrue(CairnfieldClipMode.isSameSite("https://www.example.com/a", "https://example.com/b"))
        assertFalse(CairnfieldClipMode.isSameSite("https://news.example.com/a", "https://other.example.org/b"))
        assertFalse(CairnfieldClipMode.isSameSite("https://example.com/a", "https://example.com.evil.org/b"))
        assertFalse(CairnfieldClipMode.isSameSite("https://example.com/a", "ftp://example.com/b"))
    }

    @Test
    fun clipModeNavigationRule() {
        assertTrue(CairnfieldClipMode.allowsInWebView(clipMode = true, isSameSite = true))
        assertFalse(CairnfieldClipMode.allowsInWebView(clipMode = true, isSameSite = false))
        assertFalse(CairnfieldClipMode.allowsInWebView(clipMode = false, isSameSite = true))
    }

    @Test
    fun isHttpUrlOnlyAcceptsHttpSchemes() {
        assertTrue(CairnfieldClipMode.isHttpUrl("https://example.com/page"))
        assertTrue(CairnfieldClipMode.isHttpUrl("http://example.com"))
        assertFalse(CairnfieldClipMode.isHttpUrl("file:///sdcard/x.html"))
        assertFalse(CairnfieldClipMode.isHttpUrl("javascript:alert(1)"))
        assertFalse(CairnfieldClipMode.isHttpUrl("not a url"))
    }

    @Test
    fun metadataJsonCarriesTheClipFields() {
        val metadata = JSONObject(CairnfieldClipMode.metadataJson("Title", "https://example.com/a", "/clips", "2026-07-17T00:00:00Z"))
        assertEquals("Title", metadata.getString("title"))
        assertEquals("https://example.com/a", metadata.getString("source_url"))
        assertEquals("https://example.com/a", metadata.getString("page_url"))
        assertEquals("/clips", metadata.getString("folder_path"))
        assertEquals("folder", metadata.getString("destination_kind"))
        assertEquals("2026-07-17T00:00:00Z", metadata.getString("captured_at"))
        assertEquals("/", JSONObject(CairnfieldClipMode.metadataJson("t", "u", "", "c")).getString("folder_path"))
    }

    @Test
    fun parsePageSnapshotUnwrapsEvaluateJavascript() {
        val inner = JSONObject()
            .put("title", "Page")
            .put("url", "https://example.com/a")
            .put("html", "<!doctype html>\n<html></html>")
        val wrapped = JSONObject.quote(inner.toString())
        val snapshot = CairnfieldClipMode.parsePageSnapshot(wrapped)
        assertEquals("Page", snapshot?.title)
        assertEquals("https://example.com/a", snapshot?.url)
        assertTrue(snapshot?.html?.startsWith("<!doctype html>") == true)
        assertNull(CairnfieldClipMode.parsePageSnapshot("null"))
        assertNull(CairnfieldClipMode.parsePageSnapshot(JSONObject.quote("{\"title\":\"x\"}")))
    }

    @Test
    fun parseClipSlugReadsTheNoteSlug() {
        assertEquals(
            "abcd1234",
            CairnfieldClipMode.parseClipSlug("""{"note":{"id":7,"slug":"abcd1234"},"version":{}}""")
        )
        assertNull(CairnfieldClipMode.parseClipSlug("""{"error":"nope"}"""))
        assertNull(CairnfieldClipMode.parseClipSlug("not json"))
    }

    @Test
    fun serializerJsStripsActiveContentAndAbsolutizes() {
        val js = CairnfieldClipMode.SERIALIZER_JS
        assertTrue(js.contains("script,noscript,iframe,object,embed,template"))
        assertTrue(js.contains("removeAttribute"))
        assertTrue(js.contains("new URL(value, location.href)"))
        assertTrue(js.contains("insertBefore(base, head.firstChild)"))
        assertTrue(js.contains("'<!doctype html>\\n'"))
    }
}
