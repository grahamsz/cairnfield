package app.cairnfield.mobile

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class CairnfieldSharePolicyTest {
    @Test
    fun onlyAndroidContentProviderStreamsAreAccepted() {
        assertTrue(CairnfieldSharePolicy.acceptsScheme("content"))
        assertTrue(CairnfieldSharePolicy.acceptsScheme("CONTENT"))
        assertFalse(CairnfieldSharePolicy.acceptsScheme("file"))
        assertFalse(CairnfieldSharePolicy.acceptsScheme("https"))
        assertFalse(CairnfieldSharePolicy.acceptsScheme(null))
    }

    @Test
    fun sharedStreamsUseAReservedSameOriginPath() {
        assertEquals(
            "https://notes.example.com/cairnfield-native-share/session/token",
            CairnfieldSharePolicy.shareUrl("https://notes.example.com/", "session", "token")
        )
        assertEquals(
            listOf("session", "token"),
            CairnfieldSharePolicy.requestParts(
                "https://notes.example.com",
                "https://notes.example.com/cairnfield-native-share/session/token"
            )
        )
    }

    @Test
    fun shareUrlsRideUnderTheConfiguredBasePath() {
        val origin = "https://notes.example.com/notes"
        assertEquals(
            "https://notes.example.com/notes/cairnfield-native-share/session/token",
            CairnfieldSharePolicy.shareUrl(origin, "session", "token")
        )
        assertEquals(
            listOf("session", "token"),
            CairnfieldSharePolicy.requestParts(
                origin,
                "https://notes.example.com/notes/cairnfield-native-share/session/token"
            )
        )
        // Requests outside the configured base path are not share requests.
        assertNull(
            CairnfieldSharePolicy.requestParts(
                origin,
                "https://notes.example.com/cairnfield-native-share/session/token"
            )
        )
    }

    @Test
    fun sharedStreamRequestsRejectOtherOriginsAndMalformedPaths() {
        val origin = "https://notes.example.com"
        assertNull(
            CairnfieldSharePolicy.requestParts(
                origin,
                "https://other.example.com/cairnfield-native-share/session/token"
            )
        )
        assertNull(CairnfieldSharePolicy.requestParts(origin, "$origin/cairnfield-native-share/session"))
        assertNull(CairnfieldSharePolicy.requestParts(origin, "$origin/cairnfield-native-share/session/token/extra"))
        assertNull(CairnfieldSharePolicy.requestParts(origin, "$origin/folders"))
        assertNull(CairnfieldSharePolicy.requestParts(origin, "$origin/cairnfield-native-share/session/token", "POST"))
    }

    @Test
    fun queryAndFragmentDoNotAffectPathMatching() {
        assertEquals(
            listOf("session", "token"),
            CairnfieldSharePolicy.requestParts(
                "https://notes.example.com",
                "https://notes.example.com/cairnfield-native-share/session/token?dl=1#preview"
            )
        )
    }
}
