package app.cairnfield.mobile

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdatePromptPolicyTest {
    @Test
    fun promptsOnceForEachNewReadyVersion() {
        val policy = UpdatePromptPolicy()

        assertTrue(policy.shouldPrompt(UpdateChecker.ReadyUpdate("0.2.0", 200)))
        assertFalse(policy.shouldPrompt(UpdateChecker.ReadyUpdate("0.2.0", 200)))
        assertTrue(policy.shouldPrompt(UpdateChecker.ReadyUpdate("0.3.0", 300)))
        assertEquals("0.3.0", policy.lastPromptedVersionName)
    }

    @Test
    fun restoredActivityDoesNotRepeatTheSameDialog() {
        val policy = UpdatePromptPolicy("0.2.0")

        assertFalse(policy.shouldPrompt(UpdateChecker.ReadyUpdate("0.2.0", 200)))
        assertTrue(policy.shouldPrompt(UpdateChecker.ReadyUpdate("0.3.0", 300)))
    }
}
