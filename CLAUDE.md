# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Common commands

Dev tools (`oapi-codegen`, `golangci-lint`, `govulncheck`, `gosec`) are declared as **Go `tool` directives in `go.mod`**. Invoke them via `go tool <name>` — there is no install step.

```sh
make build              # CGO_ENABLED=0 build with ldflags version into ./dist/dinonce
make oapi               # regen internal/api/generated/api.gen.go from api/api.yaml
make test               # unit tests (-race, -short) — only ./internal/api/...
make test-integration   # brings up Postgres on :5433 via docker-compose.test.yaml, runs ./internal/ticket/...
make test-integration-up / down  # if you want to keep the DB running between iterations
make lint vuln sec      # golangci-lint, govulncheck, gosec — all via `go tool`
make cover              # full coverage including integration

# Run a single test (integration tests need :5433 Postgres up):
go test -race -count=1 -run TestServicer_LeaseTicket$ ./internal/ticket/psql/
go test -race -count=1 -run TestLeaseTicket_StatusMapping ./internal/api/
```

`docker` is aliased to `podman` on the maintainer's machine. `make test-integration` calls `docker compose`; if podman-compose isn't installed, fall back to `podman run` directly with the env vars from `docker-compose.test.yaml`.

## Architecture

dinonce hands out monotonically-increasing nonces ("tickets") per "lineage" (per-account nonce sequence). Most of the race-critical state lives in **PostgreSQL stored procedures** (`scripts/psql/migrations/`), not in Go — the Go code orchestrates and translates errors.

### Request flow

```
HTTP (echo, :5010)
  ↓  internal/api/api.go         OpenAPI validator middleware + status-code mapping
ticket.Servicer interface         internal/ticket/servicer.go (the contract)
  ↓
psql.Servicer                     internal/ticket/psql/psql_servicer.go
  ↓  calls create_ticket / release_ticket / close_ticket stored procs
PostgreSQL (pgx/v5 stdlib)
```

The OpenAPI spec at `api/api.yaml` is the source of truth. `internal/api/generated/api.gen.go` is regenerated from it by `make oapi`; **never hand-edit the generated file** — `make oapi` will blow it away.

### Two HTTP surfaces

- `:5010` — the OpenAPI surface, plus `/metrics` (Prometheus) and `/version` (build info). The OpenAPI request-validator middleware has a Skipper that whitelists `/metrics` and `/version`; new ad-hoc endpoints must be added there too or they'll 400.
- `:5001` — `/livez` (process), `/readyz` (DB ping), and `/` (back-compat catch-all that runs the same checkers as `/readyz`).

### Optimistic-lock retry contract

`LeaseTicket` / `ReleaseTicket` / `CloseTicket` retry up to **`optimisticLockMaxRetryAttempts = 20`** times with jittered exponential backoff (`optimisticLockSleepBase` 10ms → `optimisticLockSleepMax` 1s). On exhaustion they propagate `ticket.ErrTooManyConcurrentRequests`, which the HTTP layer maps to **409 Conflict**. Clients are expected to retry on 409.

The earlier code silently returned success on exhaustion; the integration tests' 64-way concurrency block (e.g. `TestServicer_LeaseTicket_Concurrency`) is what protects this invariant. If you ever lower the retry budget, those tests will start flaking.

### pgx array codec (load-bearing)

`pgx/v5/stdlib` does **not** transparently encode/decode Postgres array types through `database/sql`. `internal/ticket/psql/arrays.go` provides `stringArray` (driver.Valuer for `character varying(64)[]` parameters) and `int64Array` (sql.Scanner for `bigint[]` results). These speak the Postgres array text format directly and are unit-tested in `arrays_test.go`.

If you add a new query that takes or returns a Postgres array, wrap parameters with `stringArray(slice)` (or add a new typed wrapper) and scan results into `&int64Array{}` — not into `[]int64` directly.

### Lineage model and Ethereum semantics

A `lineage` is one independent monotonic nonce sequence identified by an `extId`. The recommended encoding is `eoa:<chain>:<sender>` for EOAs and `aa:<chain>:<sender>:<nonceKey>` for ERC-4337 accounts. See `docs/adr/0001-ethereum-nonce-model-alignment.md` for the full rationale and why `nonceKey` is intentionally not a first-class column.

## Conventions

- **Conventional Commits** are load-bearing: release-please reads `feat:` / `fix:` / `BREAKING CHANGE:` to compute the next semver tag and the CHANGELOG entry. `chore:` / `docs:` / `test:` / `build:` / `ci:` are kept out of the user-facing changelog by `release-please-config.json`.
- **Versioning**: `go.mod` is `github.com/matelang/dinonce/v3`. The v2 path (`github.com/welthee/dinonce/v2`) is frozen. Any change that breaks the import path or the public API needs the `!` marker in the commit type and a `BREAKING CHANGE:` footer.
- **golangci-lint config** uses the v2 schema (`linters.settings.<name>` and the `formatters:` block, not the v1 `linters-settings:`). `revive` rules `var-naming`, `exported`, `package-comments`, `redefines-builtin-id` are off because they fight inherited names; `gosec` excludes G404 because the retry jitter intentionally uses `math/rand/v2`.
- **Tests**: integration tests anchor the migrate source URL via `runtime.Caller(0)` to compute an absolute path. Don't change it back to a `file://../../` relative URL — `net/url` parses the leading `..` as the host and the migrate file driver then fails with `open .: no such file or directory`.

## CI quirk to know about

GitHub Actions does **not** fire a newly-added workflow file on the same merge commit that adds it. Both `ci.yml` and `release.yml` now declare `workflow_dispatch:` so they can be manually triggered via `gh workflow run` for sanity checks, and subsequent PRs / pushes will pick them up automatically.
