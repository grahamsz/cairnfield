package app.cairnfield.mobile

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.UUID

class CairnfieldShareSessionsTest {
    private var now = 1_000_000L
    private val sessions = CairnfieldShareSessions { now }

    private fun entry(
        name: String = "photo.jpg",
        type: String = "image/jpeg",
        size: Long = 1234L
    ) = CairnfieldShareSessions.Entry(
        UUID.randomUUID().toString(),
        "content://media/external/images/1",
        name,
        type,
        size
    )

    @Test
    fun manifestDescribesTheCapturedFiles() {
        val sessionID = sessions.create(listOf(entry()))
        val manifest = sessions.manifest(sessionID, ORIGIN)!!
        assertEquals(sessionID, manifest.getString("shareId"))
        val files = manifest.getJSONArray("files")
        assertEquals(1, files.length())
        val file = files.getJSONObject(0)
        assertEquals("photo.jpg", file.getString("name"))
        assertEquals("image/jpeg", file.getString("type"))
        assertEquals(1234L, file.getLong("size"))
        val url = file.getString("url")
        val prefix = "$ORIGIN/cairnfield-native-share/$sessionID/"
        assertTrue(url.startsWith(prefix))
        // The per-item token in the URL routes back to the same entry.
        val token = url.removePrefix(prefix)
        assertTrue(token.isNotBlank())
        assertEquals("photo.jpg", sessions.lookup(sessionID, token)?.name)
    }

    @Test
    fun manifestFileUrlsIncludeTheConfiguredBasePath() {
        val sessionID = sessions.create(listOf(entry()))
        val url = sessions.manifest(sessionID, "https://notes.example.com/notes")!!
            .getJSONArray("files")
            .getJSONObject(0)
            .getString("url")
        assertTrue(url.startsWith("https://notes.example.com/notes/cairnfield-native-share/$sessionID/"))
    }

    @Test
    fun manifestListsEveryCapturedFile() {
        val sessionID = sessions.create(listOf(entry(name = "a.pdf", type = "application/pdf"), entry(name = "b.png", type = "image/png")))
        val files = sessions.manifest(sessionID, ORIGIN)!!.getJSONArray("files")
        assertEquals(2, files.length())
        assertEquals("a.pdf", files.getJSONObject(0).getString("name"))
        assertEquals("b.png", files.getJSONObject(1).getString("name"))
    }

    @Test
    fun sessionsExpireAfterTheTtl() {
        val sessionID = sessions.create(listOf(entry()))
        assertNotNull(sessions.manifest(sessionID, ORIGIN))
        now += CairnfieldShareSessions.SESSION_TTL_MS
        assertNotNull(sessions.manifest(sessionID, ORIGIN))
        now += 1
        assertNull(sessions.manifest(sessionID, ORIGIN))
        assertNull(sessions.lookup(sessionID, "token"))
    }

    @Test
    fun creatingASessionPrunesExpiredOnes() {
        val expired = sessions.create(listOf(entry()))
        now += CairnfieldShareSessions.SESSION_TTL_MS + 1
        val fresh = sessions.create(listOf(entry()))
        assertNull(sessions.manifest(expired, ORIGIN))
        assertNotNull(sessions.manifest(fresh, ORIGIN))
    }

    @Test
    fun releaseDropsTheSession() {
        val sessionID = sessions.create(listOf(entry()))
        sessions.release(sessionID)
        assertNull(sessions.manifest(sessionID, ORIGIN))
        // Releasing unknown sessions is a no-op.
        sessions.release(sessionID)
        assertNull(sessions.manifest("missing", ORIGIN))
    }

    companion object {
        private const val ORIGIN = "https://notes.example.com"
    }
}
