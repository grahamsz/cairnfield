# Android app

`android/` is a native Android companion app: a Kotlin WebView shell around a
self-hosted cairnfield server, modeled on (and partly ported from) rolltop's
Android app. It gives the web app an installable home-screen presence with a
native loading animation, self-updates from GitHub releases, and the same
offline support as the PWA.

- Package / applicationId: `app.cairnfield.mobile`
- minSdk 26, target/compileSdk 35, Kotlin, Gradle 8.10.2 wrapper (AGP 8.7.3)
- No push notifications, no accounts beyond the server URL — the app is a
  thin shell; all logic lives in the web app it loads.

## How it works

### Server setup

On first launch the app asks for the server URL and validates it with
`GET {url}/api/bootstrap` — a JSON body containing a `users_exist` boolean
identifies a cairnfield server (`CairnfieldServerValidator`). The URL is
persisted in SharedPreferences (`CairnfieldPrefs`). To change servers later:
long-press the loading overlay → confirm → setup screen with the current URL
prefilled.

### WebView shell (`MainActivity`)

- JavaScript, DOM storage, and database storage enabled; cache mode
  `LOAD_DEFAULT`.
- **Service workers are enabled** via `ServiceWorkerController` — this is what
  lets the SPA's offline mode (service worker cache + IndexedDB mirror +
  queued edits, see [frontend.md](frontend.md#offline-support)) work inside
  the app exactly as in the browser PWA.
- File uploads work through `onShowFileChooser` (attachment `<input
  type=file>`); downloads through `setDownloadListener` → DownloadManager
  (backup zips, attachments).
- `WebNavigationPolicy` keeps same-server URLs in the WebView and opens
  everything else in the external browser; `AndroidBackNavigation` maps the
  back button onto WebView history with a fallback to the app root.
- A `cairnfieldAndroid` JavascriptInterface plus a `window.cairnfieldAndroid =
  {versionName, versionCode}` injection in `onPageStarted` lets the web app
  detect the native shell (used to hide the PWA install prompt).

### Loading animation

`CairnfieldLoadingView` is a custom `View` that renders the cairnfield logo as
three stones which **drop in from above one at a time (bottom → middle → top)
and rock side to side before settling** — the same animation the web app shows
in its boot splash. The SVG path data from `logo.svg` is parsed with
`PathParser.createPathFromPathData()`, the logo's `translate` offset baked in
via a `Matrix`. A single 1.8s `ValueAnimator` drives the timeline: three
staggered drops with an overshoot landing bounce, then two damped rock cycles
of the whole cairn around its base; afterwards an infinite subtle pulse holds
the settled logo until the page is ready. `LoadingRevealGate` (ported from
rolltop) coordinates the crossfade from loading view to WebView. The launcher
icon, monochrome icon, and Android 12+ splash all use the same cairn on the
paper background `#f2f0eb`.

### Self-updates

The app updates itself from GitHub releases — no Play Store involved:

1. A daily `UpdateCheckWorker` and a foreground check on `MainActivity.onStart`
   call `https://api.github.com/repos/grahamsz/cairnfield/releases/latest`.
2. `UpdatePolicy` compares `tag_name` (leading `v` stripped) against
   `BuildConfig.VERSION_NAME` and picks the `cairnfield-android.apk` asset
   (fallback: first `.apk`, HTTPS only), honoring its SHA-256 digest when the
   release provides one.
3. A newer version prompts once per version (`UpdatePromptPolicy`); on accept
   the APK downloads to `cacheDir/updates` (250 MB cap), is verified
   (`UpdateAPKValidator`: package name, versionName, and signing-lineage
   compatibility — an APK signed with a different key is rejected), and
   installed via `UpdateInstallActivity` with a FileProvider
   (`REQUEST_INSTALL_PACKAGES`; the user is sent to the unknown-sources
   setting first if needed).

Because update checks compare version **names**, every tag must bump the
version: CI derives `versionName` from the tag, so tagging `v0.2.0` is enough
— see [CI and signing](#ci-and-signing).

## Building locally

Prerequisites: JDK 17+ and the Android SDK.

```sh
cd android
echo "sdk.dir=/path/to/Android/Sdk" > local.properties   # gitignored
./gradlew :app:assembleDebug :app:testDebugUnitTest
```

The debug build needs no keystore. Release builds read signing config from
environment variables (or gradle properties); when unset, `release` falls back
to the debug signature so local release builds never fail:

- `CAIRNFIELD_KEYSTORE_FILE`, `CAIRNFIELD_KEYSTORE_PASSWORD`,
  `CAIRNFIELD_KEY_ALIAS`, `CAIRNFIELD_KEY_PASSWORD`
- `CAIRNFIELD_ANDROID_VERSION_NAME` / `CAIRNFIELD_ANDROID_VERSION_CODE`
  (default `0.1.0` / `1`)

## CI and signing

`.github/workflows/android.yml` runs on every push/PR and on `v*` tags:
unit tests → debug APK (uploaded as a workflow artifact) → on tags only,
a signed release APK published to the GitHub release as
`cairnfield-android.apk` (the exact name the update checker looks for).
`versionName` comes from the tag (`v0.2.0` → `0.2.0`), `versionCode` from the
run number.

Signing secrets (repository secrets, all required for tagged builds):

| Secret | Content |
| --- | --- |
| `ANDROID_KEYSTORE_BASE64` | `base64 -w0` of the release `.jks` |
| `ANDROID_KEYSTORE_PASSWORD` | keystore store password |
| `ANDROID_KEY_ALIAS` | key alias (`cairnfield`) |
| `ANDROID_KEY_PASSWORD` | key password (same as store password — PKCS12 requires it) |

The keystore itself is **not** in the repo. Losing it means updates can no
longer be installed over the old app (signature mismatch), so keep a backup.

## Testing

`./gradlew :app:testDebugUnitTest` — JVM unit tests (Robolectric-free) ported
from rolltop's suite for the classes kept here: server validator, prefs,
loading reveal gate, web navigation, back navigation, update policy, update
prompt policy, APK validator.
