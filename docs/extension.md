# Browser extension (web clipper)

`extension/` is a Manifest V3 extension, "Cairnfield Clipper", that captures
web content into a cairnfield server as notes. It is distributed as a zip from
the server itself (`GET /api/extension/zip`) for sideloading, and configured
either through its options page or a one-click handshake from the web app's
Settings.

The extension never converts anything to markdown — it ships HTML, PDF, or PNG
plus searchable text to the server, which builds the note (see
[api.md](api.md#web-clipping-extension)).

## Layout

| File | Role |
| --- | --- |
| `manifest.json` | MV3 manifest: permissions, background worker, content script |
| `background.js` | Service worker (ES module): context menu, image-clip pipeline, notifications |
| `content-capture.js` | Injected on demand into pages; does the actual capture |
| `single-file/` | Vendored SingleFile library (AGPL, ES-module build) |
| `popup.html` / `popup.js` | Toolbar popup: destination picker + capture buttons |
| `image-picker.html` / `.js` | Destination popup for context-menu image clips |
| `options.html` / `options.js` | Server URL + API token settings |
| `shared.js` | Shared storage keys and authenticated `fetch` wrapper |
| `setup-bridge.js` | Content script that receives one-click setup from the web app |

Permissions: `activeTab`, `contextMenus`, `debugger`, `notifications`,
`scripting`, `storage`, plus optional host permission for the configured
server origin (requested at save time). The `single-file/` tree is a web-
accessible resource so injected code can import it inside the page.

## Capture paths

The popup offers a destination dropdown (folders from `clip/bootstrap`;
moodboard folders shown as "path (board)") and capture buttons:

- **Clip page as PDF** — injects `content-capture.js` to read the title,
  selection, and searchable text; attaches the Chrome debugger and calls
  `Page.printToPDF` (print background, CSS page size, 0.25" margins); also
  grabs a visible-tab PNG preview. POSTs to `/api/clip/pdf`.
- **Clip selection as image** — `content-capture.js` serializes the selection
  ranges and bounding rect and temporarily clears the selection; the popup
  screenshots the visible tab and crops to the rect on a canvas, then restores
  the selection. Title = first 80 chars of selected text. POSTs to
  `/api/clip/image`.
- **Send image to Cairnfield** (context menu) — right-click any image:
  `background.js` stashes a pending clip record, opens the `image-picker`
  popup for a destination, then fetches the image (extension origin bypasses
  page CORS) and POSTs to `/api/clip/image`. Success/failure surfaces as a
  notification.
- **Clip page as HTML** — captures the full page through SingleFile
  (`capture("page")` → `/api/clip/html`). Large pages (>40k elements or
  >30 MB) and any capture failure fall back to plain DOM-clone sanitization;
  `google.*` hosts are always captured as a "visual archive" (screenshot
  embedded in a self-contained HTML page) because they do not serialize
  reliably.
- **Clip selection as HTML** — clones the selection with inlined computed
  styles and runs SingleFile on the result (`capture("selection")` →
  `/api/clip/html`).

## How page capture works

`content-capture.js` is an IIFE injected by `chrome.scripting.executeScript`
(not a manifest content script). It exposes four `window.cairnfield*`
functions:

- `cairnfieldCapturePage()` — runs SingleFile with scripts/frames/hidden
  elements removed, deferred images loaded, 8s network timeout, 16 MB max
  resource size, uncompressed output. Post-cleans the result: strips
  `script/iframe/object/embed/template/noscript`, CSP metas, preload links,
  hidden nodes, all `on*` handler attributes, `srcdoc`, `nonce`, `integrity`,
  `srcset`; absolutizes URLs; inserts `<base href>`.
- `cairnfieldCaptureSelection()` — clones the selection's ranges, inlines
  computed styles (~40 properties) by matching clones back to source elements,
  wraps them in a fresh document (copied meta/title/styles + a `<main
  data-cairnfield-selection>` wrapper), then runs SingleFile on that.
- `cairnfieldCaptureSelectionImage()` — selection text + serialized ranges +
  bounding rect only; the screenshot itself is taken by the popup.
- `cairnfieldSearchablePageText()` — deduped aggregate of title, body text,
  alt/aria/title/placeholder attributes, input values, button/link text,
  capped at 250k chars, sent as `search_text` for server-side indexing.

## Server protocol

All calls go through `cairnfieldFetch` (`shared.js`) with
`Authorization: Bearer <token>`. Endpoints (all CSRF-exempt, bearer-only):

- `GET /api/clip/bootstrap` → `{user, folders, board_folders, app_version}`
  (destination pickers, connection test).
- `POST /api/clip/html` — multipart `html` ≤50 MB, optional `preview` ≤8 MB,
  `metadata` JSON.
- `POST /api/clip/pdf` — multipart `pdf` ≤80 MB, optional `preview`,
  `metadata`.
- `POST /api/clip/image` — multipart `image` ≤25 MB, `metadata`.

Metadata: `{title, source_url, page_url, selection_text, search_text,
folder_path, destination_kind ("folder"|"board"), captured_at}`.

## Configuration

- **Options page**: Cairnfield URL (stored in `chrome.storage.sync`) and API
  token (deliberately in `chrome.storage.local`, never synced). Saving
  requests host permission for the server origin and tests the connection
  against `clip/bootstrap`. Tokens are created in the web app under Settings →
  API tokens.
- **One-click setup**: the web app's Settings posts a
  `CAIRNFIELD_CLIPPER_SETUP` `window.postMessage` with `{baseUrl, token}`;
  `setup-bridge.js` (a content script on every http/https page) validates the
  payload, stores it, and replies `CAIRNFIELD_CLIPPER_SETUP_RESULT`. The web
  app shows the result, so users never copy-paste tokens. Note any page could
  post this message — the check only ensures the token *shape* is right.
