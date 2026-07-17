package app.cairnfield.mobile

import org.junit.Assert.assertEquals
import org.junit.Test

class WebNavigationPolicyTest {
    private val origin = "https://notes.example.test"
    private val clicked = WebNavigationContext(isMainFrame = true, hasUserGesture = true)

    @Test
    fun sameOriginNavigationStaysInCairnfield() {
        assertEquals(
            WebNavigationAction.ALLOW_IN_WEBVIEW,
            WebNavigationPolicy.action(origin, "$origin/notes/42/my-note", clicked)
        )
        assertEquals(
            WebNavigationAction.ALLOW_IN_WEBVIEW,
            WebNavigationPolicy.action(origin, "https://notes.example.test:443/folders", clicked)
        )
        assertEquals(
            WebNavigationAction.OPEN_INTERNAL,
            WebNavigationPolicy.action(origin, "$origin/search/test?q=test", clicked.copy(isNewWindow = true))
        )
    }

    @Test
    fun clickedExternalWebLinksOpenOutsideCairnfield() {
        listOf(
            "https://www.example.test/article#section",
            "http://legacy.example.test/info",
            "https://notes.example.test:8443/folders"
        ).forEach { candidate ->
            assertEquals(
                "Expected $candidate to open externally",
                WebNavigationAction.OPEN_EXTERNAL,
                WebNavigationPolicy.action(origin, candidate, clicked)
            )
        }
    }

    @Test
    fun externalRedirectsAndBackgroundNavigationsCannotLaunchApps() {
        val external = "https://outside.example.test/landing"
        listOf(
            clicked.copy(isRedirect = true),
            clicked.copy(hasUserGesture = false),
            clicked.copy(isMainFrame = false)
        ).forEach { context ->
            assertEquals(
                WebNavigationAction.BLOCK,
                WebNavigationPolicy.action(origin, external, context)
            )
        }
    }

    @Test
    fun clickedMailtoLinksOpenExternally() {
        assertEquals(
            WebNavigationAction.OPEN_EXTERNAL,
            WebNavigationPolicy.action(origin, "MAILTO:person@example.test?subject=Hello", clicked)
        )
        assertEquals(
            WebNavigationAction.BLOCK,
            WebNavigationPolicy.action(
                origin,
                "mailto:person@example.test",
                clicked.copy(hasUserGesture = false)
            )
        )
    }

    @Test
    fun unsafeOrMalformedSchemesAreBlocked() {
        listOf(
            "javascript:alert(1)",
            "intent://scan/#Intent;scheme=zxing;end",
            "file:///sdcard/private.txt",
            "content://app.cairnfield.mobile/private",
            "data:text/html,hello",
            "https://user:password@example.test/",
            "https:///missing-host",
            "not a url"
        ).forEach { candidate ->
            assertEquals(
                "Expected $candidate to be blocked",
                WebNavigationAction.BLOCK,
                WebNavigationPolicy.action(origin, candidate, clicked)
            )
        }
    }
}
