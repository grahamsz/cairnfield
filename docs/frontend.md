# Frontend

React 19 + TypeScript (strict) + Vite 6, styled with a single SCSS file. Vite's
root is `frontend/`; `base: "./"` (relative asset URLs) plus a server-injected
`<base href>` make the SPA work under any sub-path. The dev server proxies
`/api` and `/assets` to `127.0.0.1:8080` (`vite.config.ts`).

## Structure

The app is intentionally flat. `frontend/src/main.tsx` mounts `<App/>`,
imports the Fraunces webfont and `styles.scss`, and registers the service
worker. Everything else lives in a handful of modules:

| File | Role |
| --- | --- |
| `App.tsx` (~3,500 lines) | The entire SPA: root state, shell, and every component |
| `api.ts` | Thin typed REST client (all endpoints, CSRF header injection) |
| `base.ts` | Base-path helpers (`appURL`, `appPathname`) |
| `types.ts` | All API types |
| `crypto.ts` | OpenPGP.js helpers (key gen, encrypt/decrypt text & bytes) |
| `offline.ts` | IndexedDB layer (offline mirror, edit queue, key store) |
| `presence.ts` | WebSocket presence client (watch/unwatch, reconnect, callbacks) |
| `styles.scss` | All styles (theme maps → CSS custom properties) |

Main components inside `App.tsx` (line numbers approximate):

- `App` (111) — root, holds ~30 `useState` hooks; there is no router, context,
  or store library. State and callbacks flow down as props.
- `Setup` / `Login` / `AuthForm` (1100–1108) — first-run admin creation,
  password login, OIDC provider buttons from `bootstrap.auth_providers`.
- `EditorView` (1130) — title, folder chip, clip-source chip, PGP status,
  save-state indicator, autosave, editor-mode toggle.
- `DocumentNoteView` (1608) — viewer for document/webpage notes (image, PDF
  iframe, sandboxed HTML iframe, file download).
- `FolderBrowser` (2041) — sidebar folder tree with inline creation,
  per-note rows with type icons, HTML5 drag & drop (note→folder, folder→
  folder, OS files→import), Starred/Trash pseudo-folders.
- `MoodboardView` (1664) / `MoodboardPreview` / `PDFMoodboardPreview` —
  masonry tiles (preview asset → first asset → pdfjs canvas → sandboxed HTML →
  file icon → markdown "note paper"), drag-reorder, file-drop import.
- `RichMarkdownEditor` (1918) / `RawMarkdownEditor` (2030) — the two editor
  modes.
- `NoteListView` (2241) — shared list for folder contents, search results,
  and trash: multi-select, bulk star, match highlighting, pager.
- `History` (2328) — version list, restore, pick-two client-side line diff.
- `ShareDialog` (2392), `PGPUnlockDialog` (2188).
- `SettingsView` (2459) — profile, API tokens, extension setup handshake,
  templates, PGP keys, backups. `AdminView` (2887) — user management.
- `ToastStack` (2894) — notifications.

## Routing

No react-router. The current view is a `useState<View>` (`"editor" |
"folder" | "search" | "settings" | "admin"`) with a conditional render chain.
Three routes are deep-linkable via manual `history.pushState` and a `popstate`
listener:

- `/notes/{slug}/{title-slug}`
- `/folders/{path}?page=N`
- `/search/{slug}?q=…&page=N`

Settings and admin have no URL. `base.ts` reads the server-injected `<base
href>` once and prefixes every app/API/asset URL, so all routes work under
`CAIRNFIELD_BASE_PATH`.

## Markdown editing

Two modes, toggled in the editor:

- **Rich** — `@mdxeditor/editor` (Lexical-based) with headings, lists, quote,
  thematic break, link (+dialog), table, image, code-block/CodeMirror
  (fixed language list), markdown shortcuts, and a toolbar. Drag-and-drop files
  onto the editor upload them and insert image/link markdown. Stored markdown
  is kept base-path-independent (`prefixAssetURLs`/`unprefixAssetURLs`), and
  decrypted attachments appear as `blob:` URLs that are swapped back before
  saving.
- **Raw** — a plain `<textarea>` with file-drop upload.

`@uiw/react-markdown-preview` is used only for the "note paper" moodboard
tiles. `pdfjs-dist` renders PDF first pages for moodboard tiles and import
previews (worker bundled via a `?url` import).

Autosave debounces 1.8s after content changes and also flushes on note switch,
tab hide, `pagehide`, and `beforeunload`. Saves send `base_version_id` +
`client_id` + `autosave`; the server coalesces autosaves into the current
version and flags conflicts, which surface as a toast.

## Client-side PGP

`crypto.ts` wraps OpenPGP.js v6: ECC curve25519 key generation with
passphrase, armored text encrypt/decrypt, binary encrypt/decrypt for
attachments, passphrase policy (≥14 chars, rejects name/email fragments and
obvious prefixes).

- Note **title and body** are encrypted as separate PGP message blocks before
  saving; attachments of encrypted notes are encrypted client-side and
  uploaded as `application/octet-stream` with `encrypted=1`.
- Private keys live either **browser-only** (IndexedDB `pgp_keys`) or as a
  **passphrase-protected server copy** (`encryption_keys` table) — chosen in
  Settings before generate/import.
- Unlocking (`PGPUnlockDialog`) verifies the passphrase and keeps keys in
  React memory for 15 min–4 h. While unlocked, list titles are batch-decrypted
  (cached in a ref Map), and encrypted assets are fetched, decrypted, and
  exposed as `blob:` URLs (revoked on note change).
- Locking wipes caches, re-blinds titles (list rows show deterministic
  pseudo-random ovals instead of titles), and broadcasts to other tabs via
  `BroadcastChannel` plus a service-worker relay.

Encrypted notes cannot be shared, are never indexed server-side, and show an
"Encrypted note" placeholder in lists.

## Offline support

Three pieces:

1. **Service worker** (`frontend/public/sw.js`) — precaches the app shell;
   network-first for navigations, cache-first with background refresh for
   static GETs; `/api/` and `/assets/` always bypass the cache. The PWA
   manifest makes the app installable (`display: standalone`).
2. **IndexedDB mirror** (`offline.ts`, DB `cairnfield-offline-v1`) — stores
   `notes` (full note+version), `folders`, `edits` (pending ops keyed by
   `client_id`), `pgp_keys`, and `meta` (cached bootstrap + server time).
3. **Sync flow** — online loads call `GET /api/sync/bootstrap` and mirror
   everything into IndexedDB. If bootstrap fails, the app restores from cache
   and shows an "Offline mode" pill. Saves made offline are queued in `edits`
   (creates get temporary negative IDs); on reconnect the queue is pushed
   one op at a time to `POST /api/sync/push`, temp IDs are rewritten to real
   IDs everywhere, and the mirror is refreshed. Offline search scans the
   cached notes client-side (decrypting when unlocked).

Server-only mutations (folder ops, move, star, trash, import, uploads,
sharing) are refused while offline with explanatory toasts.

## Styling

One `styles.scss`, plain SCSS, no framework. Two theme maps — `classic` (warm
paper, terracotta accent `#c46b44`) and `dark` — are expanded into CSS custom
properties on `:root` / `:root[data-theme="dark"]`. The theme is persisted in
`localStorage` and synced to the user profile. Fraunces is the display font
(brand, titles, editor headings); system-ui for UI, ui-monospace for code and
the raw editor. Layout: sticky 64px topbar, CSS-grid app shell with a
resizable sidebar (220–480px), one mobile breakpoint (≤900px) with an
off-canvas sidebar. Vendor styles (MDXEditor, @uiw preview) are remapped to
the app palette via CSS-variable overrides.

## Loading splash

The app shows a "dropping stones" animation while booting: the three stones of
the cairn logo drop in from above one at a time (bottom → middle → top), each
landing with a small rock/wobble, then the assembled cairn idles with a gentle
pulse. It is pure CSS (staggered keyframes on per-stone `<g>` wrappers,
`animation-fill-mode: both`) and exists in two forms:

- `frontend/index.html` holds a self-contained inline copy (hardcoded colors)
  inside `#root`, visible before the JS bundle loads; React's first render
  replaces it.
- The `LoadingStones` component in `App.tsx` (next to `AuthShell`) is used for
  the bootstrap loading state, with the keyframes in `styles.scss`.

The Android app renders the same animation natively — see
[android.md](android.md#loading-animation).

## Native Android bridge

When the app runs inside the companion Android WebView app, the page sees
`window.cairnfieldAndroid` (a synchronous `JavascriptInterface` plus an early
injected stub). The `isNativeAndroid()` helper in `App.tsx` gates Android-only
web UI on it — currently it just hides the sidebar card offering the native
APK download, which is pointless inside the native app. The bridge also
exposes `getSharedFilesManifest(shareId)` / `releaseShare(shareId)` for
incoming shares.

### Incoming shares

`nativeShare.ts` + the `IncomingShareDialog` implement the web half of
Android's share sheet (see [android.md](android.md#share-targets-action_send)):

- `?share_text=…&share_subject=…` — a shared text/link opens the dialog with
  a title input, folder picker (moodboard folders marked "(board)"), and text
  preview; when the text contains a URL, a "Clip the full page" option
  (default on) clips it. **In the native Android shell the in-app clip is the
  default**: the dialog hands the URL straight to
  `cairnfieldAndroid.clipInApp(url, folder, title)` and the page is rendered
  and captured on the device (JavaScript, site logins, and LAN/private
  addresses all work — none of which the server-side fetch can do). On
  desktop the save goes via `POST /api/clip/url` (SSRF-guarded); if the
  server flags the clip with `clip_warning` (JS wall / login wall / thin
  content), a follow-up dialog offers "Open in app to clip" (native shell
  only; hands the URL to the Android in-app clip mode and trashes the partial
  note) or "Keep partial clip".
- `?android_share=<sessionId>` — shared files: the dialog loads the manifest
  through the bridge, downloads the bytes from same-origin
  `/cairnfield-native-share/<session>/<token>` URLs, previews them, and saves
  each file through `POST /api/import` into the chosen folder.

Both params are captured once on app load and stripped from the URL
immediately; file sessions are released after the bytes are fetched.

## Live presence

`presence.ts` maintains a singleton WebSocket connection to `{base}/ws`
(lazy connect, exponential-backoff reconnect, watch state resent on
reconnect). When a note is open, `App` watches it with the editor's current
dirty state (lifted straight from EditorView's save-status state — content
change → `editing: true`, save → `false`). The editor shows a presence chip
next to the save-state chip: "…is editing now" (warning) when another session
or user is actively editing the same note, "Open in another tab" / "…is
viewing" otherwise. When a `note_saved` broadcast arrives for the open note,
the app either quietly reloads it (clean editor + toast "{name} updated this
note") or warns that the next save may conflict (dirty editor). Echoes of the
tab's own saves are skipped deterministically: every save carries a per-tab
`editor_id` that the broadcast reflects, and content changes are detected by
comparing `content_sha256` against the loaded version's `body_sha256` (so
coalesced autosaves, which keep the version id, still register).

## Known loose ends

- Settings/admin views are not deep-linkable.
