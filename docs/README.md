# cairnfield developer documentation

cairnfield is a self-hosted markdown notes app: a single Go binary that serves a
compiled React SPA, stores data in SQLite and on-disk blobs, and indexes notes
with Bleve. This directory documents how it is built and how its pieces fit
together. For installation and configuration see the top-level
[README](../README.md).

## Contents

- [Architecture](architecture.md) — system design, backend packages, data
  model, search, security, configuration, on-disk layout.
- [HTTP API](api.md) — endpoint reference with auth requirements.
- [Frontend](frontend.md) — SPA structure, editors, client-side PGP, offline
  sync, theming.
- [Browser extension](extension.md) — the web clipper: capture paths, server
  protocol, setup handshake.
- [Android app](android.md) — the WebView companion app: loading animation,
  self-updates, offline support, signing and CI.
- [Relationship to rolltop](rolltop-lineage.md) — cairnfield started as a
  rewrite of [rolltop](../../rolltop)'s self-hosted app chassis; this
  documents what was carried over, adapted, dropped, and added.

## Repository layout

```
backend/        Go backend (module "cairnfield")
  auth/         Argon2id password hashing, opaque token generation
  blob/         content-addressed per-user file storage
  config/       environment-variable configuration
  document/     searchable-text extraction (PDF/DOCX/ODF/HTML/...)
  oidc/         hand-rolled OIDC relying party
  search/       per-user Bleve full-text indexes
  store/        SQLite schema, migrations, and all data access
  web/          HTTP server: REST API, SPA hosting, auth middleware
cmd/cairnfield/ main(); wiring and startup reindex
frontend/       React 19 + TypeScript + Vite SPA (src/ only; dist/ is built)
extension/      Manifest V3 web-clipper browser extension (vendored SingleFile)
android/        Kotlin WebView companion app (self-updating, offline-capable)
data/           local dev data directory (SQLite DB, bleve/, users/)
docs/           this documentation
```

## Development quickstart

Prerequisites: Go (version in `go.mod`), Node 20+.

```sh
# Terminal 1: backend on :8080
go run ./cmd/cairnfield

# Terminal 2: frontend dev server with HMR, proxying /api and /assets to :8080
npm install
npm run dev
```

The Vite dev server proxies `/api` and `/assets` to `127.0.0.1:8080`
(`vite.config.ts`), so run both for frontend work. For backend-only work,
`npm run build` once and the Go server serves `frontend/dist` directly.

## Checks

```sh
npm run build      # typecheck (tsc -b) + vite build
go test ./...      # backend tests
go build ./...     # backend compile
docker build .     # full image build (same as CI)
cd android && ./gradlew :app:assembleDebug :app:testDebugUnitTest   # Android app
```

CI runs the same checks: `.github/workflows/ci.yml` (Go + frontend + Docker
image, publishes `ghcr.io/<owner>/cairnfield`) and
`.github/workflows/android.yml` (Android tests + APK; tagged releases publish
a signed `cairnfield-android.apk` to the GitHub release).

## Environment

By default the dev server listens on `:8080` and writes to `./data` when run
from the repo (in Docker: `/data`). All configuration is via `CAIRNFIELD_*`
environment variables — see
[Configuration](architecture.md#configuration) for the full table.
