# DesaKita

Village-tourism guide and booking site. The spec, in Indonesian, is the source of truth: [docs/SPEC.md](docs/SPEC.md). Read it before changing behaviour.

## Commands

```sh
cp .env.example .env                 # once; .env is git-ignored
go run ./cmd/desakita migrate up     # apply migrations
go run ./cmd/desakita migrate down 1 # roll back one
go run ./cmd/desakita seed           # idempotent dev seed (placeholder regions only)
go run ./cmd/desakita serve          # http://localhost:8080, /healthz pings the DB
go run ./cmd/desakita promote-admin EMAIL  # grant admin to an existing account (no web UI for this)
sqlc generate                        # after editing queries/ or migrations/ (sqlc v1.29.0)
npm install && npm run build:css     # rebuild web/static/app.css after template/class changes (output is committed)
npm run vendor:htmx                  # copy pinned htmx into web/static (committed)
go test ./...                        # needs TEST_DATABASE_URL (user with CREATEDB)
bash scripts/check.sh                # required before each commit: go vet, go test -race, govulncheck
npm run vendor:assets                # copy font (OFL) + Lucide icons (ISC) into web/ (committed)
PW_CHANNEL=msedge npm run e2e        # Playwright end-to-end + axe-core; e2e/fixture recreates desakita_e2e
docker compose up --build            # app + PostgreSQL 16 (DB on host port 5433)
```

Local dev on this machine uses Laragon's PostgreSQL 14 on localhost:5432 (user `postgres`, no password). SQL must stay compatible with PostgreSQL ≥ 14.

Windows Application Control blocks some Go test binaries here, unpredictably (e.g. `csrf.test.exe`, and any `-race` build). Run the suite in WSL instead. WSL uses mirrored networking, so localhost:5432 reaches Laragon:

```sh
wsl.exe -d Ubuntu -- bash -lc 'cd "/mnt/c/Users/ASUS/project golang 2" && TEST_DATABASE_URL="postgres://postgres@localhost:5432/postgres?sslmode=disable" go test ./...'
```

`-race` needs gcc; `build-essential` is installed in WSL, so run `bash scripts/check.sh` there. Run the Playwright suite from Windows (it uses the installed Edge via `PW_CHANNEL=msedge`; CI installs Chromium).

The module targets Go 1.26 (required by the patched `golang.org/x/image`).

## Layout and rules

- Modules live in `internal/<domain>/` (`auth`, `village`, `tourpackage`, `booking`, `admin`), each with handler → service → store. Shared plumbing goes in `internal/platform/`. sqlc output goes in `internal/db/` (generated, never edit).
- Dependencies point one way: `platform ← auth ← village ← tourpackage`, `platform ← booking`, `village ← admin`.
  - `booking` never imports `tourpackage` or `village`. It declares small interfaces (`PackageLookup`, `VillageAccess`), and `internal/app` wires the adapters.
- All quota logic and its concurrency tests stay in `internal/booking/`. Package data reaches booking through `PackageLookup`, which runs inside booking's transaction with `FOR SHARE`. The adapter is in `internal/app/adapters.go`. Booking takes an injectable clock (`booking.Config.Now`). The expiry job runs from `serve` via `App.RunBackground`, never in tests. Quota changes are conditional `UPDATE … RETURNING` inside one transaction. Status transitions are `UPDATE … WHERE status IN (…)`. See SPEC §4.
- Every booking action checks ownership of that booking (traveler_id, or village_id ∈ the manager's villages). Unauthorized access returns 404.
- Money is `bigint` rupiah. Times are `timestamptz` (DB sessions run in UTC). Business dates use `Asia/Jakarta`.
- Migrations are append-only once committed. Add a new numbered pair instead of editing `000001`.
- `internal/app` is the composition root: the only package importing every module. Routes in its session group get a 1 MiB body limit before CSRF. Upload routes need their own group with a larger limit.
- Every UI string goes through `{{t "key"}}` / i18n keys in `web/locales/id.json`. `web/i18n_test.go` fails on missing keys, and pages render missing keys as `⟦key⟧`.
- Integration tests use `dbtest.New(t)`, which gives a fresh database cloned from a migrated template. Never point tests at the dev database.
- Code, identifiers and comments are in English. UI text goes through i18n (Indonesian only for now). Docs are in Indonesian.
- Never log passwords, booking notes or phone numbers. Request logging is `httpx.AccessLog`, which writes JSON lines with the path only (no query string, no bodies). Panics go through `httpx.Recover`.
- `/healthz` is liveness and must never touch the DB. `/readyz` pings the DB. Dynamic pages and error pages send `Cache-Control: no-store`.
- Client IP comes from `httpx.ClientIP`. Proxy headers are trusted only with `TRUST_PROXY=true`. Never reintroduce chi's `middleware.RealIP`, because it lets clients spoof their IP past rate limits.
- Rate limits are wired in `internal/app` through handler hooks (`LoginLimit`, `RegisterLimit`, `CreateLimit`, `ReportLimit`). They are in-memory, which is enough for a single instance.
- `/admin` answers 404 to non-admins (`httpx.RequireAdmin`). Village status changes go through `village.Service` (Verify/Reject/Suspend/Reinstate) with guarded UPDATEs.
- Every admin action writes `audit.Record(ctx, tx, …)` inside the same transaction as the change (`internal/platform/audit`). `audit_log` is append-only, and a trigger rejects UPDATE, DELETE and TRUNCATE. A new action needs a migration that extends the CHECK list, plus a constant, a label key in `actionKeys`, and an i18n entry.
- E2E tests live in `e2e/tests`. The fixture seeds known accounts (`sari@e2e.test`, `pengelola@e2e.test`, `admin@e2e.test`, password `rahasia123`). Every page must pass axe-core WCAG 2.1 A/AA at 375 px and 1280 px. Don't add `outline-none` and don't use `text-stone-500` for text on the sand background.
- Keep CSP strict (`script-src 'self'`, `style-src 'self'`): no inline scripts or styles. htmx indicator styles are disabled via the `htmx-config` meta tag.
