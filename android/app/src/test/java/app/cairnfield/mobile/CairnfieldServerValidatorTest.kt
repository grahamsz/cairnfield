package app.cairnfield.mobile

import org.json.JSONObject
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL

class CairnfieldServerValidatorTest {
    @Test
    fun acceptsCairnfieldBootstrapResponse() {
        val result = validate(
            contentType = "application/json; charset=utf-8",
            body = """{"users_exist":true,"user":null,"csrf":"token","templates":[],"auth_providers":[]}"""
        )

        assertTrue(result.message, result.valid)
    }

    @Test
    fun rejectsGenericJsonAndHtmlSites() {
        assertFalse(validate(body = """{"status":"ok"}""").valid)
        assertFalse(validate(contentType = "text/html", body = "<html>Not cairnfield</html>").valid)
    }

    @Test
    fun rejectsNonSuccessfulResponse() {
        val result = validate(status = 404, body = "{}")

        assertFalse(result.valid)
        assertTrue(result.message.contains("HTTP 404"))
    }

    @Test
    fun bootstrapMarkersRequireTypedContractFields() {
        assertTrue(
            CairnfieldServerValidator.hasBootstrapMarkers(
                JSONObject("""{"users_exist":false,"user":null,"csrf":"token"}""")
            )
        )
        assertFalse(
            CairnfieldServerValidator.hasBootstrapMarkers(
                JSONObject("""{"users_exist":"false","user":null,"csrf":"token"}""")
            )
        )
        assertFalse(
            CairnfieldServerValidator.hasBootstrapMarkers(
                JSONObject("""{"user":null,"csrf":"token"}""")
            )
        )
    }

    private fun validate(
        status: Int = 200,
        contentType: String = "application/json",
        body: String
    ): CairnfieldServerValidator.Result = CairnfieldServerValidator.validateWith("https://notes.example.com") { url ->
        FakeConnection(url, status, contentType, body)
    }

    private class FakeConnection(
        url: URL,
        private val status: Int,
        private val responseContentType: String,
        body: String
    ) : HttpURLConnection(url) {
        private val response = body.toByteArray(Charsets.UTF_8)

        override fun connect() = Unit

        override fun disconnect() = Unit

        override fun usingProxy(): Boolean = false

        override fun getResponseCode(): Int = status

        override fun getContentType(): String = responseContentType

        override fun getInputStream(): InputStream = ByteArrayInputStream(response)
    }
}
