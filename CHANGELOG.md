# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Releases after v3.0.0 are produced automatically by release-please from
Conventional Commits.

## [Unreleased]

### Changed
- Module path renamed from `github.com/welthee/dinonce/v2` to
  `github.com/matelang/dinonce/v3`. Major bump because the import path
  changes are breaking for any downstream importer.
- Bumped minimum Go toolchain to 1.24.
- Replaced PostgreSQL driver `github.com/lib/pq` with
  `github.com/jackc/pgx/v5/stdlib`. The `*sql.DB` surface is unchanged.
- Replaced `github.com/deepmap/oapi-codegen` (archived) with
  `github.com/oapi-codegen/oapi-codegen/v2`.
- Switched the container base image to distroless and produce
  multi-arch (amd64, arm64) images.
- Replaced the `mpdred/semantic-tagger` release job with release-please.

### Added
- `/livez` and `/readyz` endpoints (process liveness and DB readiness).
- `/version` endpoint and `-version` CLI flag returning embedded build metadata.
- Optional OpenTelemetry tracing (config-gated).
- Connection-pool tuning knobs in `backendConfig`.
- Conventional unit tests for the HTTP handler layer.
- `docker-compose.test.yaml` for one-command integration-test bring-up.
- Architecture Decision Records under `docs/adr/`, including ADR-0001
  documenting the EOA vs ERC-4337 (sender, nonceKey) lineage mapping.
- CI jobs for `golangci-lint`, `govulncheck`, `gosec`, CodeQL, Trivy image
  scan, race-enabled tests, and Codecov upload.
- Cosign-signed container images and CycloneDX SBOMs (via syft).

### Fixed
- The optimistic-lock retry loops in `LeaseTicket`, `ReleaseTicket`, and
  `CloseTicket` no longer swallow the last error on retry exhaustion.
- The service now handles `SIGTERM` in addition to `SIGINT` and shuts the
  healthcheck server down gracefully.
- An unknown `backendKind` is now a fatal startup error instead of a no-op.
- `getLineageVersion` no longer leaks rows on the empty-result branch.
