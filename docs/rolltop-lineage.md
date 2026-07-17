# Relationship to rolltop

cairnfield began as a ground-up rewrite of the application chassis of
[rolltop](https://github.com/grahamsz/rolltop) (developed at `../rolltop`), a
self-hosted IMAP email client by the same author. The lineage is code-carrying
rather than a git fork — cairnfield has its own fresh history — so shared code
was copied and then adapted. This document records what was carried over, what
changed, and why the two codebases look alike.

Both projects share the same product shape: a multi-user, single-binary Go app
serving a compiled React SPA, with SQLite for data, on-disk per-user blobs for
files, one Bleve search index per user, configuration purely through
environment variables, an AGPL license, and the same Dockerfile skeleton
(node build → Go build → alpine runtime, uid 10001, `/data` volume).

## Package-by-package

| Package | Verdict | Notes |
| --- | --- | --- |
| `backend/auth` | **Identical, byte-for-byte** | Same Argon2id parameters and opaque-token generation. The `HashPassword` doc comment in cairnfield still says "for local Rolltop users". Rolltop's `password_test.go` was not copied. |
| `backend/blob` | Adapted | Same `Store{Root}`, sha256-named files, `safeSegment`, and the per-user `OpenUserBlob` path guard. Rolltop stores `.eml`/attachments/contact icons/remote images; cairnfield keeps one verb (`SaveAsset` under `assets/YYYY/MM/`) and drops delete/ownership helpers. |
| `backend/config` | Adapted | Same env-var struct and defaults (`/data`, `:8080`, 720h sessions). Rolltop's `Load()` is strict and parses a master key + sync intervals; cairnfield's is lenient, accepts legacy `NOTES_*` aliases, and adds `BasePath` and OIDC settings. |
| `backend/search` | Adapted — same skeleton | Identical service shape: lazily opened per-user Bleve indexes with double-checked locking and per-user write mutexes, `StoreDynamic=false` mappings. Rolltop's is ~2,500 lines (recency ranking, sender boosts, repair tooling); cairnfield's is ~420 lines with note fields and `title:`/`path:`/`tag:` operators. Index location moved from `data/users/<id>/bleve` to `data/bleve/<id>/bleve-v2`. |
| `backend/store` | Rewritten | Rolltop: ~90 files, split system DB + lazily opened per-tenant DBs, numbered migration files. Cairnfield: one 2,362-line `store.go`, single DB, idempotent inline `CREATE TABLE IF NOT EXISTS` + duplicate-tolerant `ALTER`s. Shared verbatim: the `users`/`sessions` schema with `token_hash`, `TokenHash` = sha256-hex, the session SQL, and helpers (`nowUnix`, `unixTime`, `boolInt`, `cleanEmail`). Same SQLite DSN idiom (`?_foreign_keys=on&_busy_timeout=5000`). |
| `backend/web` | Adapted — same architecture | Shared: `Options`-struct constructor, one `/api/` dispatcher on `http.ServeMux`, `withCurrentUser` session middleware, `requireAuth`/`requireAdmin`, `writeJSON`/`writeAPIError`/`decodeJSON`, the `/api/bootstrap|setup|login|logout|profile|admin/users` shape, SPA fallback, HttpOnly SameSite=Lax cookies. Rolltop adds HMAC-derived CSRF keyed by a master key, plugin route registries, SSE, web push. Cairnfield uses a plain random CSRF cookie and adds base-path serving, clip endpoints, OIDC routes, backups/import, moodboards, and sync. |
| `backend/oidc` | Extracted from rolltop's plugin | Rolltop ships OIDC as a runtime plugin; cairnfield promoted it to a first-class package with function-for-function correspondence (`discoverOIDC`→`Discover`, `exchangeCode`→`ExchangeCode`, `validateIDToken`→`ValidateIDToken`, `fetchRS256Key` verbatim). Behavioral change: rolltop auto-creates users on first OIDC login; cairnfield only signs in pre-provisioned users. |

## Frontend

Same stack and tooling: React 19 + Vite 6 + TypeScript + sass, near-identical
`vite.config.ts` (root `frontend`, proxy to `127.0.0.1:8080`), same npm script
shapes. Shared dependencies include `@fontsource/fraunces`,
`@phosphor-icons/react`, `dompurify`, and `openpgp`. The typed API client is
the same pattern (identical `ApiError` and `parse<T>` helper).

Rolltop's client-side-PGP plugin seeded cairnfield's built-in
`frontend/src/crypto.ts` — a provenance fingerprint: cairnfield's
`passphraseIssues` still rejects passphrases starting with "rolltop"
(`frontend/src/crypto.ts:34`, copied from rolltop's
`plugins/client_side_pgp/frontend/crypto/pgp.ts`).

Divergences: rolltop has feature/component/lib directories, split SCSS
partials, plugin frontend builds, and web push; cairnfield is deliberately
flat (one `App.tsx`, one `styles.scss`) and adds markdown editors,
`pdfjs-dist`, base-path support (`base.ts`), and offline sync (`offline.ts`).

## Dropped from rolltop

- All mail machinery: `imapclient`, `smtpclient`, `syncer`, `mailparse`,
  `autocrypt`, `remoteimages`.
- The plugin system (backend manager + ~15 plugins); the useful ones (OIDC,
  client-side PGP) became first-class features.
- `crypto` (master-key envelope encryption) — only `TokenHash` survived, moved
  into `store`.
- Per-user database split, `buildinfo`, web push, the Android app, webhook
  support, blob retention.

## Added in cairnfield

- `backend/document` — searchable-text extraction for attachments.
- Notes domain: versioning, sharing with per-user overlays, starring, trash,
  templates, moodboards.
- Client-side PGP note/asset encryption as a core feature.
- Zip import/export backups, base-path serving, API tokens, offline
  bootstrap/push sync.
- The browser extension — rolltop has nothing like it (its clip endpoints,
  `GET /api/extension/zip`, and the setup `postMessage` handshake are all new).

## Shared conventions worth knowing

If you know rolltop, these idioms transfer directly:

- Tenant scoping everywhere: `user_id` on every row, blob paths under
  `users/<id>/blobs/`, search documents ANDed with the caller's `user_id`,
  one Bleve index per user.
- Auth model: Argon2id app passwords, opaque 256-bit tokens stored only as
  SHA-256 hex, HttpOnly SameSite=Lax session cookies, CSRF on mutations,
  `/api/setup` creates the first admin, admins manage users but cannot read
  their data.
- Helper vocabulary: `nowUnix`, `unixTime`, `boolInt`, `cleanEmail`,
  `safeSegment`, `writeJSON`/`writeAPIError`/`decodeJSON`, `TokenHash`,
  `NewOpaqueToken`.
- Dev checks: `npm run build`, `go test ./...`, `docker build`.
- Rolltop's "File overview:" header-comment convention survives only in the
  byte-copied `auth` files.

## One caveat: data layouts differ

Despite the similar shapes, backups and data directories do **not** transplant
between the projects:

- rolltop: `rolltop.db` + `users/<id>/{rolltop.db,bleve,blobs}` (per-tenant
  DBs, indexes under the user dir).
- cairnfield: `cairnfield.db` (single DB) + `bleve/<id>/bleve-v2/` +
  `users/<id>/blobs/`.

Also note cairnfield rebuilds every Bleve index from SQLite on each startup,
treating search as disposable, whereas rolltop treats its indexes as precious
(it ships an offline `reset-search` repair command). Rolltop is far more
test-dense; cairnfield keeps a handful of `httptest`-style tests in the same
style.
