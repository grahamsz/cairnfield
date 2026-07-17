# Android app

`android/` is a native Android companion app: a Kotlin WebView shell around a
self-hosted cairnfield server, modeled on (and partly ported from) rolltop's
Android app. It gives the web app an installable home-screen presence with a
native loading animation, self-updates from the same server, and the same
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
  {...}` injection in `onPageStarted` lets the web app detect the native shell
  (used to hide the sidebar APK-download card inside the native app). Beside
  the version accessors it exposes `getSharedFilesManifest(shareId)` /
  `releaseShare(shareId)` for incoming shares (see below).

### Share targets (ACTION_SEND)

The app registers for `ACTION_SEND` / `ACTION_SEND_MULTIPLE` (`text/plain`,
`text/html`, `image/*`, `application/pdf`). When the shared text contains a
URL it wins over any attached streams (browsers like Brave attach a page
screenshot as a stream; `CairnfieldSharePolicy.preferTextShare` keeps the
URL), and text/link shares load `{server}/?share_text=…&share_subject=…`.
File shares are captured into
`CairnfieldShareStore` (in-memory sessions, 15-minute TTL, ported from
rolltop's `NativeShareStore`) and the app loads
`{server}/?android_share={sessionId}`. The web app fetches the manifest via
the bridge and the bytes from same-origin
`/cairnfield-native-share/{sessionId}/{token}` URLs, which
`CairnfieldShareStore.intercept` serves through both the page's
`WebViewClient.shouldInterceptRequest` and a `ServiceWorkerClient`
(fetches owned by an active service worker bypass the page client).

### Loading animation

`CairnfieldLoadingView` is a custom `View` that renders the cairnfield logo as
three stones which **drop in from above one at a time (bottom → middle → top)
and rock side to side before settling** — the same animation the web app shows
in its boot splash. The SVG path data from `logo.svg` is parsed with
`PathParser.createPathFromPathData()`, the logo's `translate` offset baked in
via a `Matrix`. A single 1.8s `ValueAnimator` drives the timeline: three
staggered drops that decelerate into place from above (no overshoot — a stone
never dips below its resting position), then two damped rock cycles
of the whole cairn around its base; afterwards an infinite subtle pulse holds
the settled logo until the page is ready. `LoadingRevealGate` (ported from
rolltop) coordinates the crossfade from loading view to WebView. The launcher
icon, monochrome icon, and Android 12+ splash all use the same cairn on the
paper background `#f2f0eb`.

## Self-updates from the same server

The app updates itself from the cairnfield server it is connected to — not
from a separate store or GitHub release feed. Whenever the server's Docker
image is rebuilt with a newer APK, the app offers to install it.

The server exposes two endpoints:

- `GET /android/latest.json` — update metadata: `{versionCode, versionName,
  apkUrl, sha256}`. `apkUrl` is rewritten by the server to the absolute public
  URL of the APK, honoring reverse-proxy headers and `CAIRNFIELD_BASE_PATH`.
- `GET /android/cairnfield.apk` — the signed APK, served with
  `Content-Disposition: attachment; filename="cairnfield.apk"`.

Update flow:

1. A daily `UpdateCheckWorker` and a foreground check on `MainActivity.onStart`
   fetch `{server}/android/latest.json`.
2. `UpdatePolicy` compares `versionCode` from the server against
   `BuildConfig.VERSION_CODE`.
3. A newer version prompts once per `versionCode` (`UpdatePromptPolicy`);
   on accept, the APK downloads from the resolved `apkUrl` to
   `cacheDir/updates` (250 MB cap).
4. Optional SHA-256 check, then `UpdateAPKValidator` verifies package name,
   matching `versionCode`, and signing-lineage compatibility before installing
   via `UpdateInstallActivity` with a FileProvider
   (`REQUEST_INSTALL_PACKAGES`; unknown-sources permission requested first if
   needed).

The APK is bundled into the Docker image under `frontend/dist/android/`:
CI builds the signed release APK, copies it to `frontend/public/android/`,
generates `latest.json`, and Vite copies the directory into `frontend/dist/`
during `npm run build`. The web app's Settings page also links directly to
`/android/cairnfield.apk` for first-time installs.

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

`.github/workflows/ci.yml` builds and embeds the Android app into the Docker
image on every push/PR:

- On all pushes: Android unit tests + debug APK artifact.
- On non-PR pushes (where signing secrets are available): signed release APK
  → `frontend/public/android/cairnfield.apk` + `latest.json` → Vite copies it
  into `frontend/dist/android/` → Docker image contains it.
- A verification step extracts the APK from the built image and confirms the
  metadata `sha256` and `versionCode` match the actual APK.

Signing secrets (repository secrets, required for the release APK to be
embedded):

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
