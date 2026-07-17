# HTTP API reference

All endpoints are relative to the base path (`/` by default, or
`$CAIRNFIELD_BASE_PATH/`). Dispatch lives in `Server.handleAPI`
(`backend/web/server.go:152`).

## Conventions

- **Auth**: session cookie (`cairnfield_session`, HttpOnly, SameSite=Lax) for
  the SPA; `Authorization: Bearer cairnfield_...` API tokens for the clipper.
  Handlers marked **auth** require a logged-in user; **admin** requires
  `is_admin`.
- **CSRF**: every non-GET `/api/` request authenticated by cookie must send
  `X-CSRF-Token` matching the readable `cairnfield_csrf` cookie. Exempt:
  `clip/*` (bearer-auth only).
- **Note keys**: `{key}` accepts either the numeric note ID or the 8-letter
  slug.
- **Errors**: JSON `{"error": "..."}` with an appropriate status. Every
  request runs under a 15s timeout.
- **Pagination**: list endpoints take `page` (1-based) and return 25 items per
  page plus `has_more`.

## Auth and account

| Method & path | Auth | Description |
| --- | --- | --- |
| `GET /api/bootstrap` | — | `{users_exist, user, csrf, templates, auth_providers}`. Drives first-run setup and login UI. |
| `POST /api/setup` | — | One-time creation of the first (admin) user. 403 once any user exists. |
| `POST /api/login` | — | `{email, password}` → sets session cookie. |
| `POST /api/logout` | auth | Deletes the session and clears the cookie. |
| `PUT /api/profile` | auth | Update `date_format`, `theme`. |
| `GET /api/tokens` | auth | List API tokens (hashes never returned). |
| `POST /api/tokens` | auth | Create a token; `raw_token` (`cairnfield_...`) returned once. |
| `DELETE /api/tokens/{id}` | auth | Revoke (soft). |
| `GET /api/extension/zip` | auth | Stream the bundled browser extension as a zip. |

## OIDC

| Method & path | Auth | Description |
| --- | --- | --- |
| `GET /api/oidc/login` | — | Starts the flow; sets a 10-min `cairnfield_oidc_state` cookie, 302s to the provider. |
| `GET /api/oidc/callback` | — | Verifies state/code/ID token, signs in the local user whose email matches, redirects to the app. Errors redirect back with `?oidc_error=`. |

Enabled only when `CAIRNFIELD_OIDC_*` is configured; advertised via
`bootstrap.auth_providers`. See [architecture.md](architecture.md#oidc).

## Admin and users

| Method & path | Auth | Description |
| --- | --- | --- |
| `GET /api/admin/users` | admin | List all users. |
| `POST /api/admin/users` | admin | Create a user `{email, name, password, is_admin}`. |
| `GET /api/users` | auth | List *other* users (for the share picker). |

## Notes

| Method & path | Auth | Description |
| --- | --- | --- |
| `GET /api/notes` | auth | Query: `folder`, `page`, `trash=1`, `starred=1`, `descendants=1`. Paginated summaries. |
| `POST /api/notes` | auth | Create `{template_id?, selected_folder?}`. Returns `reused: true` when a `create_once` template matched an existing note. |
| `GET /api/notes/{key}` | auth | Note + current version + shares + assets. |
| `PUT /api/notes/{key}` | auth (write) | Save `{title, folder_path, content, header_json, base_version_id, client_id, is_encrypted, autosave}`. Returns `conflict: true` if `base_version_id` was stale (saved as a conflicted version). |
| `POST /api/notes/{key}/folder` | auth | Move to `{folder_path}` (per-user placement for shared notes). |
| `GET /api/notes/{key}/versions` | auth | Full version history with author labels. |
| `POST /api/notes/{key}/restore` | auth (write) | Point `current_version_id` at `{version_id}`. |
| `POST /api/notes/{key}/star` | auth | `{starred: bool}` — per-user. |
| `POST /api/notes/{key}/trash` | auth | Trash (owner: globally; recipient: own view only). |
| `POST /api/notes/{key}/untrash` | auth | Restore from trash. |
| `DELETE /api/notes/{key}/wipe` | auth | Hard-delete. Must be trashed first; recipients only drop their share. |
| `POST /api/notes/{key}/share` | owner | `{email, permission: "read"\|"write"}`. Encrypted notes rejected. |
| `DELETE /api/notes/{key}/share/{userID}` | auth | Remove a share. The owner may remove any recipient; anyone else may only remove their own `userID` (leave a note shared with them). Returns `{"ok": true}`; 404 when the note or share does not exist, 403 when a non-owner targets another user. Also deletes the recipient's per-user state overlay. |

## Folders and moodboards

| Method & path | Auth | Description |
| --- | --- | --- |
| `GET /api/folders` | auth | All folders (auto-creates `/`, merges shared-note placements). |
| `POST /api/folders` | auth | Create `{path}`. |
| `POST /api/folders/move` | auth | Move `{source, target_parent}`; re-paths notes and descendants. |
| `POST /api/folders/mode` | auth | Set `{path, mode: list\|gallery\|moodboard, sort_mode}`. Moodboard requires a leaf folder. |
| `GET /api/moodboard` | auth | `folder` (+`descendants=1` for gallery): notes with first asset, header-designated `preview_asset`, and custom position. |
| `POST /api/moodboard/order` | auth | Persist tile order `{folder, note_ids[]}`. |

## Templates

| Method & path | Auth | Description |
| --- | --- | --- |
| `GET /api/templates` | auth | List the caller's templates. |
| `POST /api/templates` | auth | Create `{name, title_template, folder_template, body_template, is_default, create_once}`. |
| `PUT /api/templates/{id}` | auth | Update. |
| `DELETE /api/templates/{id}` | auth | Delete. |

Placeholders: `{date} {datetime} {year} {month} {day} {sequence}`.

## Assets

| Method & path | Auth | Description |
| --- | --- | --- |
| `POST /api/assets` | auth | Multipart upload (≤25 MB): `file`, optional `note_id`, `content_type`, `encrypted`. Extracts search text unless encrypted. Returns `/assets/{slug}/{filename}`. |
| `GET /assets/{id-or-slug}/{filename...}` | session | Serve a blob. Served to the asset owner, or to anyone who can access the note the asset is attached to (e.g. share recipients); unattached assets stay owner-only. Encrypted assets are served as `application/octet-stream`; `text/html` assets get a strict sandbox CSP. |

Note the deliberate overlap: built frontend files also live under
`/assets/...` (from `frontend/dist`); the blob handler falls through to the
static file server when the first path segment is not a numeric ID or slug.

## Search

| Method & path | Auth | Description |
| --- | --- | --- |
| `GET /api/search` | auth | `q`, `page`. Bleve query (see operators below), results hydrated to note summaries with previews. |

Query operators: free text (ANDed, boosted per field), `title:`, `path:`
(prefix match on folder), `tag:`, `after:YYYY-MM-DD`, `before:YYYY-MM-DD`,
`year:YYYY`, `is:encrypted`, `is:shared`, `has:image`, quoted phrases. Search
only covers notes the caller **owns** (encrypted notes are never indexed).

## Sync (offline client)

| Method & path | Auth | Description |
| --- | --- | --- |
| `GET /api/sync/bootstrap` | auth | All current notes (note + version) + folders + `server_time`. |
| `POST /api/sync/push` | auth | Batch `{ops: [{op: "create"\|"update", ...}]}`; creates dedupe by `client_id`; per-op results include errors/conflicts. |

Used by the SPA's IndexedDB offline mode — see
[frontend.md](frontend.md#offline-support).

## Live presence (WebSocket)

`GET /ws` — session cookie auth (no CSRF, GET upgrade), mounted outside
`/api/` so no 15s timeout; the `Origin` header must match the host. Uses
`github.com/coder/websocket`.

Client → server:

```json
{"type":"watch","note_id":123,"editing":false}
{"type":"unwatch","note_id":123}
```

Server → client:

```json
{"type":"presence","note_id":123,"participants":[{"user_id":2,"name":"Bob","email":"b@x","same_user":false,"editing":true,"sessions":1}]}
{"type":"note_saved","note_id":123,"version_id":456,"title":"T","by_user_id":2,"by_name":"Bob","by_email":"b@x","saved_at":1721320000}
{"type":"error","message":"note not accessible"}
```

- `presence` is sent to every watcher of the note whenever the watch/editing
  set changes. It excludes the receiving connection itself; the recipient's
  other sessions appear as one entry with `same_user: true` and a `sessions`
  count. `editing` is OR-ed across a user's connections.
- `note_saved` is broadcast to all watchers of the note (including the
  saver's connections) after a successful non-conflict save or version
  restore.
- Watching requires note access (owner or share recipient); inaccessible
  notes are rejected with an `error` message.
- The server pings every 30s; message read limit is 4 KB.

## Web clipping (extension)

Bearer-token only (API token), CSRF-exempt, multipart. See
[extension.md](extension.md).

| Method & path | Description |
| --- | --- |
| `GET /api/clip/bootstrap` | `{user, folders, board_folders, app_version}` for destination pickers. |
| `POST /api/clip/html` | Fields: `html` (≤50 MB), `preview` (≤8 MB PNG, optional), `metadata` (JSON). |
| `POST /api/clip/pdf` | Fields: `pdf` (≤80 MB, content sniffed), optional `preview`, `metadata`. |
| `POST /api/clip/image` | Fields: `image` (≤25 MB, must be `image/*`), `metadata`. |

`metadata` JSON: `{title, source_url, page_url, selection_text, search_text,
folder_path, destination_kind, captured_at}`. Each clip creates a note with
source-attribution markdown, stores the capture as an asset, and sets
`header_json` (`kind`, `clip`, `asset`, optional `preview_asset`).

## Import and export

| Method & path | Auth | Description |
| --- | --- | --- |
| `POST /api/import` | auth | Multipart: `file` (single file or `.zip` ≤100 MB, per-entry ≤5 MB), optional `preview` PNG, form value `folder_path`. Markdown → notes; other files → document notes; Obsidian `![[embeds]]` rewritten to asset URLs. A zip containing a backup `manifest.json` (see below) is restored instead, returning `{"restored": true, "notes": N, "assets": M, "folders": K}`. |
| `POST /api/backups` | auth | Start an export job (409 if one is already running). Async, 30-min timeout. |
| `GET /api/backups` | auth | List exports with `download_url` for ready ones. |
| `GET /api/backups/{id}/download` | auth | Stream the zip (410 after the 7-day expiry). |

Backup zip layout: `notes/<folder>/<Title>-<slug>.md`, `trash/...`,
`assets/<slug>-<filename>`, `manifest.json`. Current versions only; encrypted
content exported as ciphertext. Importing such a zip restores it: notes keep
their original slug (when free), folder, timestamps and trash state, assets
are re-linked to the restored notes. Restore is **not** idempotent —
restoring the same zip twice duplicates the notes.

## Encryption keys

| Method & path | Auth | Description |
| --- | --- | --- |
| `GET /api/keys` | auth | List the caller's keys. |
| `POST /api/keys` | auth | Register `{label, fingerprint, public_key_armored, encrypted_private_key?, storage_mode}`. First key becomes default. |
| `POST /api/keys/{id}/default` | auth | Mark default (transactionally clears others). |

The server only ever stores armored public keys and passphrase-protected
private keys; all crypto happens in the browser.
