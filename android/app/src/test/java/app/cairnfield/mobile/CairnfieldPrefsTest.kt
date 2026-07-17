package app.cairnfield.mobile

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class CairnfieldPrefsTest {
    @Test
    fun normalizeBaseUrlAddsHttpsAndCanonicalizesTheOrigin() {
        assertEquals("https://notes.example.com", CairnfieldPrefs.normalizeBaseUrl(" notes.EXAMPLE.com/ "))
        assertEquals("https://notes.example.com", CairnfieldPrefs.normalizeBaseUrl("https://notes.example.com:443"))
        assertEquals("https://notes.example.com:8443", CairnfieldPrefs.normalizeBaseUrl("HTTPS://NOTES.EXAMPLE.COM:8443"))
        assertEquals("https://[2001:db8::1]:8443", CairnfieldPrefs.normalizeBaseUrl("https://[2001:db8::1]:8443"))
    }

    @Test
    fun normalizeBaseUrlAcceptsAndCanonicalizesBasePaths() {
        assertEquals("https://notes.example.com/cairnfield", CairnfieldPrefs.normalizeBaseUrl("https://notes.example.com/cairnfield"))
        assertEquals("https://notes.example.com/cairnfield", CairnfieldPrefs.normalizeBaseUrl("https://notes.example.com/cairnfield/"))
        assertEquals("https://notes.example.com/a/b", CairnfieldPrefs.normalizeBaseUrl("https://notes.example.com/a//b/"))
        assertEquals("https://notes.example.com:8443/cairnfield", CairnfieldPrefs.normalizeBaseUrl("https://notes.example.com:8443/cairnfield"))
    }

    @Test
    fun normalizeBaseUrlRejectsUnsafeUrls() {
        listOf(
            "http://notes.example.com",
            "ftp://notes.example.com",
            "https://user:password@notes.example.com",
            "https://notes.example.com?redirect=elsewhere",
            "https://notes.example.com/#fragment",
            "https://notes.example.com/cairnfield/../other",
            "https://bad host.example.com",
            "not a host"
        ).forEach { value ->
            assertEquals("Expected '$value' to be rejected", "", CairnfieldPrefs.normalizeBaseUrl(value))
        }
    }

    @Test
    fun internalLocationKeepsOnlySameOriginRoutes() {
        val base = "https://notes.example.com"

        assertEquals(
            "/notes/42?back=%2Ffolders#note",
            CairnfieldPrefs.internalLocation(base, "https://notes.example.com/notes/42?back=%2Ffolders#note")
        )
        assertEquals("/folders", CairnfieldPrefs.internalLocation(base, "https://notes.example.com:443/folders"))
        assertNull(CairnfieldPrefs.internalLocation(base, "https://other.example.com/folders"))
        assertNull(CairnfieldPrefs.internalLocation(base, "https://notes.example.com:444/folders"))
        assertNull(CairnfieldPrefs.internalLocation(base, "javascript:alert(1)"))
    }

    @Test
    fun internalLocationRequiresPathsUnderBasePath() {
        val base = "https://notes.example.com/cairnfield"

        assertEquals(
            "/cairnfield/notes/42",
            CairnfieldPrefs.internalLocation(base, "https://notes.example.com/cairnfield/notes/42")
        )
        assertEquals("/cairnfield/folders", CairnfieldPrefs.internalLocation(base, "https://notes.example.com/cairnfield/folders"))
        assertEquals("/cairnfield/", CairnfieldPrefs.internalLocation(base, "https://notes.example.com/cairnfield/"))
        assertNull(CairnfieldPrefs.internalLocation(base, "https://notes.example.com/other"))
        assertNull(CairnfieldPrefs.internalLocation(base, "https://other.example.com/cairnfield/folders"))
    }

    @Test
    fun resolveInternalUrlRejectsProtocolRelativeAndMalformedPaths() {
        val base = "https://notes.example.com"

        assertEquals(
            "https://notes.example.com/search/q/invoices?p=2#result",
            CairnfieldPrefs.resolveInternalUrl(base, "/search/q/invoices?p=2#result")
        )
        assertNull(CairnfieldPrefs.resolveInternalUrl(base, "//other.example.com/folders"))
        assertNull(CairnfieldPrefs.resolveInternalUrl(base, "https://other.example.com/folders"))
        assertNull(CairnfieldPrefs.resolveInternalUrl(base, "/folders?bad=%"))
    }
}
