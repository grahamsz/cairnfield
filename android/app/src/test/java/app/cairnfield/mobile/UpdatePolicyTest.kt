package app.cairnfield.mobile

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdatePolicyTest {
    @Test
    fun onlyNewerVersionCodesAreOffered() {
        assertTrue(UpdatePolicy.shouldOffer(2, 1))
        assertFalse(UpdatePolicy.shouldOffer(1, 1))
        assertFalse(UpdatePolicy.shouldOffer(1, 2))
    }

    @Test
    fun cachedOfferWithSameVersionCodeDoesNotNeedDownload() {
        assertFalse(UpdatePolicy.needsAPKDownload(2, 2))
        assertTrue(UpdatePolicy.needsAPKDownload(2, null))
        assertTrue(UpdatePolicy.needsAPKDownload(3, 2))
    }

    @Test
    fun checksumIsEitherAbsentOrAFullSHA256() {
        assertTrue(UpdatePolicy.validSHA256(""))
        assertTrue(UpdatePolicy.validSHA256("a".repeat(64)))
        assertFalse(UpdatePolicy.validSHA256("abc123"))
    }

    @Test
    fun serverMetadataProducesInstallableOfferWithRelativeApkUrl() {
        val offer = UpdatePolicy.parseServerOffer(
            installedVersionCode = 1,
            serverBaseURL = "https://notes.example.com",
            metadata = serverJson(
                versionCode = 2,
                versionName = "0.2.0",
                apkUrl = "android/cairnfield.apk",
                sha256 = "a".repeat(64)
            )
        )

        assertEquals(2, offer?.versionCode)
        assertEquals("0.2.0", offer?.versionName)
        assertEquals("https://notes.example.com/android/cairnfield.apk", offer?.apkURL)
        assertEquals("a".repeat(64), offer?.sha256)
    }

    @Test
    fun absoluteApkUrlIsUsedAsIs() {
        val offer = UpdatePolicy.parseServerOffer(
            installedVersionCode = 1,
            serverBaseURL = "https://notes.example.com",
            metadata = serverJson(
                versionCode = 2,
                versionName = "0.2.0",
                apkUrl = "https://cdn.example.com/android/cairnfield.apk"
            )
        )

        assertEquals("https://cdn.example.com/android/cairnfield.apk", offer?.apkURL)
    }

    @Test
    fun leadingSlashInRelativeApkUrlIsResolved() {
        val offer = UpdatePolicy.parseServerOffer(
            installedVersionCode = 1,
            serverBaseURL = "https://notes.example.com",
            metadata = serverJson(
                versionCode = 2,
                versionName = "0.2.0",
                apkUrl = "/android/cairnfield.apk"
            )
        )

        assertEquals("https://notes.example.com/android/cairnfield.apk", offer?.apkURL)
    }

    @Test
    fun nonHttpsApkUrlsAreRejected() {
        assertNull(
            UpdatePolicy.parseServerOffer(
                installedVersionCode = 1,
                serverBaseURL = "https://notes.example.com",
                metadata = serverJson(
                    versionCode = 2,
                    versionName = "0.2.0",
                    apkUrl = "http://notes.example.com/android/cairnfield.apk"
                )
            )
        )
    }

    @Test
    fun currentOrOlderServerVersionProducesNoOffer() {
        assertNull(
            UpdatePolicy.parseServerOffer(
                installedVersionCode = 2,
                serverBaseURL = "https://notes.example.com",
                metadata = serverJson(
                    versionCode = 2,
                    versionName = "0.2.0",
                    apkUrl = "android/cairnfield.apk"
                )
            )
        )
        assertNull(
            UpdatePolicy.parseServerOffer(
                installedVersionCode = 3,
                serverBaseURL = "https://notes.example.com",
                metadata = serverJson(
                    versionCode = 2,
                    versionName = "0.2.0",
                    apkUrl = "android/cairnfield.apk"
                )
            )
        )
    }

    @Test
    fun missingRequiredFieldsProduceNoOffer() {
        assertNull(
            UpdatePolicy.parseServerOffer(
                installedVersionCode = 1,
                serverBaseURL = "https://notes.example.com",
                metadata = serverJson(
                    versionCode = 0,
                    versionName = "0.2.0",
                    apkUrl = "android/cairnfield.apk"
                )
            )
        )
        assertNull(
            UpdatePolicy.parseServerOffer(
                installedVersionCode = 1,
                serverBaseURL = "https://notes.example.com",
                metadata = serverJson(
                    versionCode = 2,
                    versionName = "",
                    apkUrl = "android/cairnfield.apk"
                )
            )
        )
        assertNull(
            UpdatePolicy.parseServerOffer(
                installedVersionCode = 1,
                serverBaseURL = "https://notes.example.com",
                metadata = serverJson(
                    versionCode = 2,
                    versionName = "0.2.0",
                    apkUrl = ""
                )
            )
        )
    }

    @Test
    fun invalidSha256IsRejected() {
        assertNull(
            UpdatePolicy.parseServerOffer(
                installedVersionCode = 1,
                serverBaseURL = "https://notes.example.com",
                metadata = serverJson(
                    versionCode = 2,
                    versionName = "0.2.0",
                    apkUrl = "android/cairnfield.apk",
                    sha256 = "not-a-digest"
                )
            )
        )
    }

    private fun serverJson(
        versionCode: Int,
        versionName: String,
        apkUrl: String,
        sha256: String = ""
    ): JSONObject = JSONObject(
        "{\"versionCode\":" + versionCode +
            ",\"versionName\":\"" + versionName + "\"" +
            ",\"apkUrl\":\"" + apkUrl + "\"" +
            ",\"sha256\":\"" + sha256 + "\"}"
    )
}
