# DesaKita

Village-tourism guide and booking site. The spec, in Indonesian, is the source of truth: [docs/SPEC.md](docs/SPEC.md). Read it before changing behaviour.

## Commands

```sh
cp .env.example .env                 # once; .env is git-ignored
go run ./cmd/desakita migrate up     # apply migrations
go run ./cmd/desakita migrate down 1 # roll back one
go run ./cmd/desakita seed           # idempotent dev seed (placeholder regions only)
go run ./cmd/desakita serve          # http://localhost:8080, /healthz pings the DB
sqlc generate                        # after editing queries/ or migrations/ (sqlc v1.29.0)
go test ./...                        # needs TEST_DATABASE_URL (user with CREATEDB)
docker compose up --build            # app + PostgreSQL 16 (DB on host port 5433)
```

Local dev on this machine uses Laragon's PostgreSQL 14 on localhost:5432 (user `postgres`, no password). SQL must stay compatible with PostgreSQL ≥ 14.

Windows Application Control blocks `-race` test binaries for DB packages here. Run `go test -race ./...` in WSL or CI instead.

## Layout and rules

- Modules live in `internal/<domain>/` (`auth`, `village`, `tourpackage`, `booking`, `admin`), each with handler → service → store. Shared plumbing goes in `internal/platform/`. sqlc output goes in `internal/db/` (generated, never edit).
- Dependencies point one way: `platform ← auth ← village ← tourpackage`, `platform ← booking`, `village ← admin`.
  - `booking` never imports `tourpackage` or `village`. It declares small interfaces (`PackageLookup`, `VillageAccess`), and `cmd/desakita` wires the adapters.
- All quota logic and its concurrency tests stay in `internal/booking/`. Quota changes are conditional `UPDATE … RETURNING` inside one transaction. Status transitions are `UPDATE … WHERE status IN (…)`. See SPEC §4.
- Every booking action checks ownership of that booking (traveler_id, or village_id ∈ the manager's villages). Unauthorized access returns 404.
- Money is `bigint` rupiah. Times are `timestamptz` (DB sessions run in UTC). Business dates use `Asia/Jakarta`.
- Migrations are append-only once committed. Add a new numbered pair instead of editing `000001`.
- Integration tests use `dbtest.New(t)`, which gives a fresh database cloned from a migrated template. Never point tests at the dev database.
- Code, identifiers and comments are in English. UI text goes through i18n (Indonesian only for now). Docs are in Indonesian.
- Never log passwords, booking notes or phone numbers.
