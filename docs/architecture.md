# Architecture

cairnfield is a multi-user, single-binary web app. One Go process serves the
REST API and the compiled React SPA, persists everything in one SQLite
database plus on-disk blob files, and keeps one Bleve search index per user.
There is no external service dependency at runtime (PDF/legacy-Office text
extraction optionally shells out to `pdftotext`/`antiword`/`catdoc` when
present).

```
browser (React SPA) ──┐
                      ├─→ Go http.Server ──→ SQLite (notes, users, versions, ...)
clipper extension ────┘         ├──────────→ blob files on disk (attachments, backups)
                                └──────────→ Bleve index per user (search)
```

## Startup and wiring

`cmd/cairnfield/main.go` is deliberately thin:

1. `config.Load()` — reads `CAIRNFIELD_*` env vars (`backend/config/config.go`).
2. `store.Open(dbPath)` — opens SQLite, runs inline migrations
   (`backend/store/store.go`).
3. `search.OpenPerUser(indexPath)` — per-user Bleve index manager
   (`backend/search/search.go`).
4. **Startup reindex**: every user's current notes are re-pushed into Bleve
   (`rebuildSearchIndexes`, `main.go:70`). The index is treated as a
   disposable projection of SQLite — see [Search](#search).
5. `web.New(...).Handler()` — builds the HTTP server (`backend/web/server.go`);
   the SPA is served from `frontend/dist` relative to the working directory.
6. `http.Server` with a 10s `ReadHeaderTimeout` and graceful shutdown on
   SIGINT/SIGTERM.

## Configuration

All configuration is environment variables. Each also accepts a legacy
`NOTES_*`-prefixed alias (`getenvAny`, `backend/config/config.go:23`).

| Variable | Default | Purpose |
| --- | --- | --- |
| `CAIRNFIELD_ADDR` | `:8080` | Listen address |
| `CAIRNFIELD_DATA_DIR` | `/data` | Data root |
| `CAIRNFIELD_DB_PATH` | `$DATA_DIR/cairnfield.db` | SQLite path |
| `CAIRNFIELD_INDEX_PATH` | `$DATA_DIR/bleve` | Bleve index root |
| `CAIRNFIELD_SESSION_TTL` | `720h` | Session lifetime |
| `CAIRNFIELD_COOKIE_SECURE` | `false` | `Secure` flag on cookies |
| `CAIRNFIELD_BASE_PATH` | `""` | Serve under a sub-path behind a reverse proxy |
| `CAIRNFIELD_OIDC_ISSUER` / `_CLIENT_ID` / `_CLIENT_SECRET` | — | Enable OIDC login |
| `CAIRNFIELD_OIDC_REDIRECT_URL` | derived from request | Explicit callback URL for proxies |
| `CAIRNFIELD_OIDC_SCOPES` | `openid email profile` | Requested scopes |
| `CAIRNFIELD_OIDC_NAME` | `OIDC` | Login-button label |
| `CAIRNFIELD_OIDC_ALLOWED_EMAILS` / `_ALLOWED_DOMAINS` | — | Optional sign-in allowlists |

## Data directory layout

```
<DATA_DIR>/
  cairnfield.db                              SQLite database (single file)
  bleve/<userID>/bleve-v2/                   one Bleve index per user
  users/<userID>/blobs/
    assets/<YYYY>/<MM>/<hash16>-<name>       attachment blobs (content-addressed)
  backups/<userID>/<id>-<filename>.zip       generated backup archives (7-day TTL)
```

The `data/` directory in the repo root is a local dev instance of this layout.
`data.wiped-*` directories are point-in-time snapshots taken before a local
reset; they are not used by the app.

## Backend packages

### `auth` — passwords and tokens

`password.go`: Argon2id (64 MiB, 3 iterations, 16-byte salt, 32-byte key) with
a PHC-style `argon2id$v=19$...` encoding and constant-time verification.
`token.go`: 32-byte random opaque tokens, base64-rawurl encoded. Both session
cookies and API tokens are stored server-side only as SHA-256 hex hashes, so
the database never contains a usable credential.

### `store` — SQLite schema and data access

One 2,362-line `store.go` contains all migrations and queries. The DB is
opened with `?_foreign_keys=on&_busy_timeout=5000` and `MaxOpenConns(1)`.
Migrations are idempotent `CREATE TABLE IF NOT EXISTS` plus
duplicate-column-tolerant `ALTER TABLE ADD COLUMN` statements — no version
table, no migration files. Timestamps are Unix seconds; booleans are 0/1.

Tables:

- **users** — email (unique), name, `password_hash`, `is_admin`, `theme`,
  `date_format`.
- **sessions** — `token_hash` (SHA-256 of the cookie token, unique),
  `expires_at`, `last_seen_at`.
- **api_tokens** — named bearer tokens for the clipper extension, soft-revoked
  via `revoked_at`, stored hashed.
- **folders** — materialized paths (`/journal/2026`), not a tree; hierarchy is
  implicit in the path string. Per-folder `display_mode`
  (`list`/`gallery`/`moodboard`) and `sort_mode`.
- **notes** — `owner_user_id`, `folder_path`, title, globally-unique 8-letter
  `slug` (used in URLs), `current_version_id`, `is_encrypted`, `trashed_at`.
- **note_versions** — full content history. Every save either updates the
  newest version in place (autosave coalescing: same author, <1 h old, not
  conflicted, not forced) or appends a new row. `client_id` is a
  client-supplied idempotency key used by offline sync. `conflicted` marks
  versions saved against a stale `base_version_id`; the note's
  `current_version_id` is not advanced for conflicts.
- **note_shares** — per-user shares with `read`/`write` permission. No public
  links. Encrypted notes cannot be shared.
- **note_user_state** — per-user overlay for shared notes: a recipient's own
  folder placement, trash, and star, independent of the owner. Star state for
  *all* notes lives here.
- **note_templates** — `{date} {datetime} {year} {month} {day} {sequence}`
  placeholders; one `is_default`; `create_once` gives daily-note find-or-create
  semantics. New users are seeded with a "Daily Note" template.
- **moodboard_items** — custom per-note `position` for moodboard folders.
- **assets** — attachment metadata: unique slug, filename, content type,
  `blob_path`, `sha256`, `encrypted`, extracted `search_text`.
- **encryption_keys** — armored public keys plus optionally a
  passphrase-protected private key (`storage_mode`); one `is_default` per
  user. The server never sees an unprotected private key.
- **backup_exports** — async export jobs (`running`/`ready`/`failed`) with
  7-day expiry.

Access control funnels through `canAccessNote()` (`store.go:1199`): owner, or
a `note_shares` row. It also resolves the caller's effective folder/trash/star
overlay and their permission (`owner`/`read`/`write`). Admins can manage users
but gain no access to other users' notes.

Key behaviors:

- **Save** (`SaveNote`, `store.go:1482`): requires owner/write; detects
  conflicts from `base_version_id`; coalesces autosaves.
- **Trash** is dual: owners flip `notes.trashed_at`, share recipients flip
  only their own `note_user_state.trashed_at` (`store.go:1598`).
- **Wipe** (`store.go:1654`) hard-deletes for owners (cascades to versions,
  shares, state) and merely drops the share for recipients. Requires the note
  to be trashed first.
- **Shares**: owner-only grants by email (`UpsertShare`); recipients' edits
  update content/title but never the owner's folder placement.
- **Moodboards**: only leaf folders (no child folders) may use
  `display_mode = 'moodboard'` (`store.go:746`); gallery mode has no such
  restriction.

### `blob` — file storage

Content-addressed per-user filesystem store (`backend/blob/blob.go`).
`SaveAsset` writes `users/<id>/blobs/assets/YYYY/MM/<sha256[:16]>-<safe-name>`
(mode 0600, dirs 0700) and rejects path traversal. `OpenUserBlob` only opens
paths inside the caller's own `users/<id>/blobs/` prefix. There is no
server-side crypto: the `encrypted` flag on assets is client-declared (the
client encrypts before upload); encrypted blobs are stored and served
verbatim.

### `search` — Bleve indexes

One lazily-opened Bleve index per user (`<indexPath>/<userID>/bleve-v2`),
guarded by a global map mutex plus per-user write mutexes. The mapping is
non-dynamic and non-stored: keyword fields (`user_id`, `folder_path`,
`is_encrypted`, `is_shared`, `has_image`), text fields (`title`,
`title_compound`, `content`, `headers`, `tags`, `path_text`, `asset_text`,
`compound`), and a date field (`updated_at`). The `*_compound` fields are
lowercase alphanumeric-only concatenations that enable substring-ish prefix
matching.

Rules:

- **Encrypted notes are never indexed** — indexing one deletes its document ID
  instead; rebuilds skip them.
- A note's document folds in title, folder path, body, `header_json`-derived
  tags, and extracted attachment text (`asset_text`).
- Indexes are updated inline after each mutating API call (fire-and-forget)
  and **fully rebuilt from SQLite on every startup**, so `bleve/` can be
  deleted safely at rest.
- Trashing or wiping a note removes it from the owner's index
  (`search.Service.Delete`), untrashing re-indexes it, and the startup
  rebuild only includes non-trashed notes, so trashed notes never appear in
  search results.

Query language (`buildQuery`, `search.go:251`): free text (ANDed terms matched
across all text fields with field boosts) plus operators — `title:`, `path:`
(folder prefix), `tag:`, `after:YYYY-MM-DD`, `before:`, `year:YYYY`,
`is:encrypted`, `is:shared`, `has:image`, and quoted phrases. Every query is
ANDed with the caller's `user_id`, so **search only covers notes the caller
owns**, not notes shared with them.

### `document` — searchable-text extraction

`SearchableText(filename, contentType, data)` (`backend/document/extract.go`)
extracts up to 1 MB of indexable text:

- **HTML**: regex tag stripping + entity unescape.
- **PDF**: shells out to `pdftotext -enc UTF-8 -layout` (optional runtime dep;
  32 MB input cap, 10s timeout).
- **DOCX/ODF**: pure-Go zip + streaming XML parse of the document XML.
- **Legacy .doc**: tries `antiword`, then `catdoc`.
- **Text-like** (`text/*`, JSON, CSV, source files, ...): indexed directly.

Extraction runs on asset upload, clip, and import; results land in
`assets.search_text` and are folded into the note's search document. Encrypted
assets are never extracted.

### `web` — HTTP server

Plain `net/http` + `http.ServeMux`, no framework. `Server.Handler()`
(`server.go:79`) mounts `/api/`, `/assets/`, and an SPA fallback; with
`BasePath` set it strips the prefix and injects `<base href>` into
`index.html` so the whole app works under a sub-path.

Middleware chain: security headers → `withCurrentUser` (resolves the session
cookie into request context; never trusts a client-supplied user id) →
per-request 15s timeout. See [api.md](api.md) for the endpoint reference.

Auth mechanisms:

- **Session cookie** `cairnfield_session` — HttpOnly, SameSite=Lax, 30-day
  TTL, scoped to the base path. Stored as SHA-256 hash.
- **CSRF** — double-submit: a readable `cairnfield_csrf` cookie is issued on
  every request; non-GET API calls must echo it in `X-CSRF-Token`
  (constant-time compare). The four `/api/clip/*` mutation endpoints are
  exempt because they use bearer auth.
- **Bearer tokens** — `Authorization: Bearer cairnfield_...` against
  `api_tokens`, for the browser extension only.

### OIDC

A hand-rolled OIDC relying party (`backend/oidc`, ~380 lines, no library):
discovery, authorization-code flow with state+nonce in an HttpOnly cookie,
RS256-only ID-token validation with per-request JWKS fetch and manual PKCS#1
v1.5 verification, issuer/audience/expiry/nonce checks, userinfo fallback for
the email claim, rejection of unverified emails, and optional email/domain
allowlists.

**Account linking is email-based and pre-provisioned only**: the OIDC email
must match an existing cairnfield user; no accounts are auto-created. Password
login stays enabled alongside OIDC. The code was extracted from rolltop's OIDC
plugin and made a first-class package — see
[rolltop-lineage.md](rolltop-lineage.md).

## Features that cut across layers

### Client-side PGP encryption

Encryption is entirely client-side (OpenPGP.js). The server stores, verbatim:
ciphertext note bodies flagged `notes.is_encrypted`, pre-encrypted attachment
blobs flagged `assets.encrypted`, and key material in `encryption_keys`
(armored public keys + optional passphrase-protected private keys). Server-side
consequences of the flags: excluded from Bleve indexing and text extraction,
list previews replaced with an "Encrypted note" placeholder, sharing rejected,
assets served as `application/octet-stream` for client-side decryption.
Backup and sync carry ciphertext through untouched.

### Sharing

Owner grants `read` or `write` to another registered user by email. Recipients
see the note in their lists with `shared_permission`; their folder placement,
star, and trash are private overlays (`note_user_state`). Recipients cannot
re-share, cannot see other shares, and do not get the note in search results,
but they can fetch the owner's attachment blobs for notes shared with them
(the asset handler serves assets whose note the caller can access; unattached
assets stay owner-only). `DELETE /api/notes/{key}/share/{userID}` unshares:
the owner may remove any recipient, a recipient may remove themselves; the
recipient's `note_user_state` overlay is deleted with the share.

### Web clipping

The browser extension (see [extension.md](extension.md)) posts to three
multipart bearer-authed endpoints: `clip/html` (≤50 MB + optional preview
image + JSON metadata), `clip/pdf` (≤80 MB, sniffed), `clip/image` (≤25 MB).
Each creates a note containing source-attribution markdown plus the original
capture as an asset, records `header_json` (`kind: "webpage"|"clip"`, clip
metadata, preview asset), and indexes merged search text. Clipped HTML is
served back under a strict sandbox CSP.

### Import and export

- **Export** (`backend/web/api_backup.go`): an async job (30-minute timeout,
  one running export per user) writes a zip of `notes/<folder>/<Title>-<slug>.md`
  (current versions only), `trash/...`, `assets/<slug>-<filename>`, and a
  `manifest.json` with full metadata. Files are kept for 7 days
  (`CleanupExpiredBackupExports`, hourly).
- **Import** (`POST /api/import`): a single file or a zip ≤100 MB. Markdown
  files become notes (zip paths → folders, zip mtimes → timestamps); other
  files become "document notes" (note + asset + extracted text). Obsidian
  `![[embeds]]` are rewritten to asset URLs when the zip contains matching
  files. If a zip contains any markdown, non-markdown entries are treated only
  as embed sources. A zip with a `manifest.json` in the backup shape above is
  instead **restored** (`restoreBackupZip`): notes keep their original slug
  (when still free), folder, timestamps and trash state with a single version
  each, assets are re-linked via the manifest's `note_id` (renamed asset
  slugs are rewritten in restored bodies), and the response is
  `{restored: true, notes, assets, folders}`. Restore is not idempotent —
  restoring twice duplicates notes.

### Offline sync

Two endpoints support the SPA's offline mode: `GET /api/sync/bootstrap`
(dumps all current notes + folders + server time) and `POST /api/sync/push`
(batched `create`/`update` ops, idempotent by `client_id`, with per-op
conflict reporting). The client mirrors everything into IndexedDB and queues
edits while offline — details in [frontend.md](frontend.md#offline-support).

## Security posture summary

- Argon2id password hashing; sessions and API tokens stored only as SHA-256
  hashes; constant-time compares for passwords, CSRF tokens, and OIDC state.
- HttpOnly + SameSite=Lax session cookie; CSRF double-submit on all
  cookie-authed mutations.
- Per-user scoping enforced at every layer: `user_id` on every row, blob paths
  restricted to the caller's prefix, search queries ANDed with `user_id`.
- Upload size caps everywhere (assets 25 MB, clip 50/80/25 MB, import 100 MB),
  per-request 15s timeout, path-traversal guards on blob and backup access.
- Clipped/served HTML assets get a sandbox CSP; encrypted assets are served as
  opaque octet-streams.
- No rate limiting on login; OIDC JWKS is fetched per callback without
  caching.
