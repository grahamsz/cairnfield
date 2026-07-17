package app.cairnfield.mobile

import org.json.JSONArray
import org.json.JSONObject
import java.util.UUID

/**
 * In-memory bookkeeping for native share sessions, free of Android framework
 * types so it can be unit-tested on the JVM. Sessions expire after
 * [SESSION_TTL_MS] and are pruned on every access.
 */
internal class CairnfieldShareSessions(private val nowMs: () -> Long = System::currentTimeMillis) {
    data class Entry(
        val token: String,
        val uri: String,
        val name: String,
        val type: String,
        val size: Long
    )

    fun create(items: List<Entry>): String {
        val sessionID = UUID.randomUUID().toString()
        synchronized(sessions) {
            pruneLocked()
            sessions[sessionID] = Session(nowMs(), items)
        }
        return sessionID
    }

    fun manifest(sessionID: String, serverOrigin: String): JSONObject? {
        val session = synchronized(sessions) {
            pruneLocked()
            sessions[sessionID]
        } ?: return null
        return JSONObject().apply {
            put("shareId", sessionID)
            put("files", JSONArray().apply {
                session.items.forEach { item ->
                    put(JSONObject().apply {
                        put("name", item.name)
                        put("type", item.type)
                        put("size", item.size)
                        put("url", CairnfieldSharePolicy.shareUrl(serverOrigin, sessionID, item.token))
                    })
                }
            })
        }
    }

    fun lookup(sessionID: String, token: String): Entry? = synchronized(sessions) {
        pruneLocked()
        sessions[sessionID]?.items?.find { it.token == token }
    }

    fun release(sessionID: String) {
        synchronized(sessions) { sessions.remove(sessionID) }
    }

    private fun pruneLocked() {
        val cutoff = nowMs() - SESSION_TTL_MS
        sessions.entries.removeAll { it.value.createdAt < cutoff }
    }

    private data class Session(val createdAt: Long, val items: List<Entry>)

    private val sessions = mutableMapOf<String, Session>()

    companion object {
        const val SESSION_TTL_MS = 15 * 60_000L
    }
}
