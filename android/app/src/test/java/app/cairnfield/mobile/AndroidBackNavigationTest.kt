package app.cairnfield.mobile

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class AndroidBackNavigationTest {
    private val origin = "https://notes.example.test"

    @Test
    fun notePageFallsBackToTheNotesRoot() {
        assertEquals(
            "$origin/",
            AndroidBackNavigation.noteFallbackUrl(origin, "$origin/notes/42/my-note")
        )
        assertEquals(
            "$origin/",
            AndroidBackNavigation.noteFallbackUrl(origin, "$origin/notes/42/my-note?edit=1")
        )
    }

    @Test
    fun nonNoteAndForeignPagesDoNotOverrideNormalActivityBack() {
        assertNull(AndroidBackNavigation.noteFallbackUrl(origin, "$origin/folders/projects"))
        assertNull(AndroidBackNavigation.noteFallbackUrl(origin, "$origin/"))
        assertNull(
            AndroidBackNavigation.noteFallbackUrl(origin, "https://other.example.test/notes/42/my-note")
        )
        assertNull(AndroidBackNavigation.noteFallbackUrl(origin, null))
    }
}
