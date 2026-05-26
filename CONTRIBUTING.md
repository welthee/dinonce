# Contributing to dinonce

Thanks for your interest in contributing. dinonce is a small project and the
contribution process is intentionally lightweight.

## Ground rules

- All changes go through a pull request against `main`.
- Commits follow [Conventional Commits](https://www.conventionalcommits.org/).
  release-please relies on this to compute the next version and produce a
  changelog entry, so `feat:`, `fix:`, `chore:`, `docs:`, `refactor:` and
  `BREAKING CHANGE:` footers are load-bearing.
- New behavior needs a test. Bug fixes need a test that fails before the fix
  and passes after.
- Run `make lint test` locally before opening a PR; the CI gate enforces
  the same checks.

## Development environment

You need a Go toolchain matching the `go` directive in `go.mod` (currently
1.24) and a running container runtime for integration tests
(Docker or Podman).

```sh
# install pinned dev tools (oapi-codegen, golangci-lint, govulncheck, gosec)
make tools

# regenerate the OpenAPI server stubs after editing api/api.yaml
make oapi

# unit tests
make test

# integration tests against a throwaway Postgres in a container
make test-integration

# vulnerability scan
make vuln
```

## Project layout

```
api/                 OpenAPI spec (contract-first source of truth)
cmd/dinonce/         main package
internal/api/        HTTP handlers; generated/ holds oapi-codegen output
internal/ticket/     domain interface + Postgres implementation
scripts/psql/        SQL migrations driven by golang-migrate
deployments/         Helm chart + Terraform modules
docs/adr/            Architecture Decision Records
```

## Releases

Releases are cut automatically by release-please when a PR with release-worthy
commits is merged to `main`. The release PR updates `CHANGELOG.md`,
bumps the version, and tags the commit. Docker images and signed artifacts are
published as part of the same workflow.
