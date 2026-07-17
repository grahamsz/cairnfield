package app.cairnfield.mobile

internal class UpdatePromptPolicy(initialVersionName: String = "") {
    var lastPromptedVersionName: String = initialVersionName
        private set
    private var lastPromptedVersionCode: Int = -1

    fun shouldPrompt(update: UpdateChecker.ReadyUpdate): Boolean {
        val alreadyPrompted = if (lastPromptedVersionCode != -1) {
            update.versionCode == lastPromptedVersionCode
        } else {
            update.versionName == lastPromptedVersionName
        }
        if (alreadyPrompted) return false

        lastPromptedVersionCode = update.versionCode
        lastPromptedVersionName = update.versionName
        return true
    }
}
