package app.cairnfield.mobile

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class UpdatePolicyTest {
    @Test
    fun onlyNewerVersionNamesAreOffered() {
        assertTrue(UpdatePolicy.shouldOffer("v0.2.0", "0.1.9"))
        assertTrue(UpdatePolicy.shouldOffer("0.10.0", "v0.9.9"))
        assertFalse(UpdatePolicy.shouldOffer("0.2.0", "v0.2.0"))
        assertFalse(UpdatePolicy.shouldOffer("v0.1.9", "0.2.0"))
    }

    @Test
    fun versionComparisonHandlesDottedComponents() {
        assertTrue(UpdatePolicy.compareVersions("1.0", "0.9.9") > 0)
        assertTrue(UpdatePolicy.compareVersions("0.2.1", "0.2") > 0)
        assertTrue(UpdatePolicy.compareVersions("0.2.0", "0.2.0") == 0)
        assertTrue(UpdatePolicy.compareVersions("0.2.0-beta", "0.1.9") > 0)
        assertTrue(UpdatePolicy.compareVersions("0.10.0", "0.9.9") > 0)
    }

    @Test
    fun aValidatedCachedOfferIsNotDownloadedAgain() {
        assertFalse(UpdatePolicy.needsAPKDownload("0.2.0", "0.2.0"))
        assertTrue(UpdatePolicy.needsAPKDownload("0.2.0", null))
        assertTrue(UpdatePolicy.needsAPKDownload("0.2.1", "0.2.0"))
        assertTrue(UpdatePolicy.needsAPKDownload("v0.2.0", "0.1.0"))
    }

    @Test
    fun checksumIsEitherAbsentOrAFullSHA256() {
        assertTrue(UpdatePolicy.validSHA256(""))
        assertTrue(UpdatePolicy.validSHA256("a".repeat(64)))
        assertFalse(UpdatePolicy.validSHA256("abc123"))
    }

    @Test
    fun releaseWithPreferredAssetProducesAnInstallableOffer() {
        val offer = UpdatePolicy.parseReleaseOffer(
            installedVersionName = "0.1.0",
            release = releaseJson(
                tag = "v0.2.0",
                assets = """
                    {"name":"cairnfield-server-linux-amd64","browser_download_url":"https://github.com/grahamsz/cairnfield/releases/download/v0.2.0/cairnfield-server-linux-amd64"},
                    {"name":"cairnfield-android.apk","browser_download_url":"https://github.com/grahamsz/cairnfield/releases/download/v0.2.0/cairnfield-android.apk","digest":"sha256:${"a".repeat(64)}"}
                """
            )
        )

        assertEquals("0.2.0", offer?.versionName)
        assertEquals("https://github.com/grahamsz/cairnfield/releases/download/v0.2.0/cairnfield-android.apk", offer?.apkURL)
        assertEquals("a".repeat(64), offer?.sha256)
    }

    @Test
    fun anyApkAssetIsAcceptedWhenThePreferredNameIsMissing() {
        val offer = UpdatePolicy.parseReleaseOffer(
            installedVersionName = "0.1.0",
            release = releaseJson(
                tag = "0.2.0",
                assets = """{"name":"app-release.apk","browser_download_url":"https://github.com/grahamsz/cairnfield/releases/download/v0.2.0/app-release.apk"}"""
            )
        )

        assertEquals("0.2.0", offer?.versionName)
        assertTrue(offer?.sha256?.isEmpty() == true)
    }

    @Test
    fun currentOrOlderReleasesAndApkLessReleasesProduceNoOffer() {
        assertNull(
            UpdatePolicy.parseReleaseOffer(
                "0.2.0",
                releaseJson(
                    tag = "v0.2.0",
                    assets = """{"name":"cairnfield-android.apk","browser_download_url":"https://github.com/grahamsz/cairnfield/releases/download/v0.2.0/cairnfield-android.apk"}"""
                )
            )
        )
        assertNull(
            UpdatePolicy.parseReleaseOffer(
                "0.2.0",
                releaseJson(
                    tag = "v0.1.0",
                    assets = """{"name":"cairnfield-android.apk","browser_download_url":"https://github.com/grahamsz/cairnfield/releases/download/v0.1.0/cairnfield-android.apk"}"""
                )
            )
        )
        assertNull(
            UpdatePolicy.parseReleaseOffer(
                "0.1.0",
                releaseJson(tag = "v0.2.0", assets = """{"name":"cairnfield-server-linux-amd64","browser_download_url":"https://github.com/grahamsz/cairnfield/releases/download/v0.2.0/cairnfield-server-linux-amd64"}""")
            )
        )
        assertNull(UpdatePolicy.parseReleaseOffer("0.1.0", JSONObject("""{"tag_name":"v0.2.0"}""")))
    }

    @Test
    fun assetUrlsMustBeHttps() {
        assertNull(
            UpdatePolicy.parseReleaseOffer(
                "0.1.0",
                releaseJson(
                    tag = "v0.2.0",
                    assets = """{"name":"cairnfield-android.apk","browser_download_url":"http://github.com/grahamsz/cairnfield/releases/download/v0.2.0/cairnfield-android.apk"}"""
                )
            )
        )
    }

    private fun releaseJson(tag: String, assets: String): JSONObject = JSONObject(
        """{"tag_name":"$tag","assets":[$assets]}"""
    )
}
