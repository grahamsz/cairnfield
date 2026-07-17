package app.cairnfield.mobile

internal class UpdatePromptPolicy(initialVersionName: String = "") {
    var lastPromptedVersionName: String = initialVersionName
        private set

    fun shouldPrompt(update: UpdateChecker.ReadyUpdate): Boolean {
        if (update.versionName == lastPromptedVersionName) return false
        lastPromptedVersionName = update.versionName
        return true
    }
}
