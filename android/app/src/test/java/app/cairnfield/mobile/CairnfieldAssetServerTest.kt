package app.cairnfield.mobile

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class CairnfieldAssetServerTest {
    @Test
    fun mimeTypesCoverTheSingleFileTree() {
        assertEquals("text/javascript", CairnfieldAssetServer.mimeTypeFor("single-file/single-file.js"))
        assertEquals("text/javascript", CairnfieldAssetServer.mimeTypeFor("single-file/core/index.js"))
        assertEquals("text/css", CairnfieldAssetServer.mimeTypeFor("single-file/vendor/x.css"))
        assertEquals("image/svg+xml", CairnfieldAssetServer.mimeTypeFor("icon.svg"))
        assertEquals("font/woff2", CairnfieldAssetServer.mimeTypeFor("fonts/a.woff2"))
        assertEquals("application/octet-stream", CairnfieldAssetServer.mimeTypeFor("blob.bin"))
        assertEquals("application/octet-stream", CairnfieldAssetServer.mimeTypeFor("noext"))
    }

    @Test
    fun singleFileRunnerUsesTheBundledScriptAndBridgeCallback() {
        val js = CairnfieldClipMode.SINGLE_FILE_RUN_JS
        assertTrue(js.contains(CairnfieldClipMode.SINGLE_FILE_BUNDLE_URL))
        assertTrue(js.contains("window.CFSingleFile.getPageData"))
        assertTrue(js.contains("removeScripts: true"))
        assertTrue(js.contains("insertMetaCSP: false"))
        assertTrue(js.contains("window.cairnfieldClipCallback.done"))
        assertTrue(js.contains("script.onerror"))
        assertFalse(js.contains("import("))
    }
}
