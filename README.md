![dinonce_gopher](gopher.png)

# dinonce — a distributed nonce ticketing service

[![ci](https://github.com/matelang/dinonce/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/matelang/dinonce/actions/workflows/ci.yml)
[![release](https://github.com/matelang/dinonce/actions/workflows/release.yml/badge.svg?branch=main)](https://github.com/matelang/dinonce/actions/workflows/release.yml)
[![codecov](https://codecov.io/gh/matelang/dinonce/branch/main/graph/badge.svg)](https://codecov.io/gh/matelang/dinonce)
[![Go Reference](https://pkg.go.dev/badge/github.com/matelang/dinonce/v3.svg)](https://pkg.go.dev/github.com/matelang/dinonce/v3)
[![Go Report Card](https://goreportcard.com/badge/github.com/matelang/dinonce/v3)](https://goreportcard.com/report/github.com/matelang/dinonce/v3)
[![CodeQL](https://github.com/matelang/dinonce/actions/workflows/ci.yml/badge.svg?branch=main&event=push&job=codeql)](https://github.com/matelang/dinonce/security/code-scanning)
[![Security policy](https://img.shields.io/badge/security-policy-blue)](SECURITY.md)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Renovate enabled](https://img.shields.io/badge/renovate-enabled-brightgreen.svg)](renovate.json)
[![Latest release](https://img.shields.io/github/v/release/matelang/dinonce?sort=semver)](https://github.com/matelang/dinonce/releases)

For most blockchain clients it is essential to keep track of transaction
nonces that protect against duplicate transactions and replay attacks.

You can read more about nonces
[here](https://medium.com/swlh/ethereum-series-understanding-nonce-3858194b39bf).
A non-partitioned client application — a mobile wallet, MetaMask — will easily
do this for you. Tracking nonces gets trickier once your application needs to
run in a distributed, split-brain-tolerant fashion.

dinonce is a nonce ticketing service that lets multiple tx executors share a
single account's nonce sequence while avoiding:

- double spending,
- and gaps in the nonce sequence that would fill a network's tx pool.

## How it works

dinonce supports ticketing for multiple nonce sequences in parallel. An
identity on a given blockchain has its own sequence of nonces; dinonce calls
that sequence a **lineage**.

For each lineage a client gets a leased nonce **ticket** for a transaction,
identified by an `externalId` that uniquely names the transaction in the
calling system. If the operation succeeds, dinonce holds a lease on the
newly-associated nonce.

Because most tx executors operate with **at-least-once semantics**, the
transaction can either:

- complete (be mined on-chain), in which case the client **closes** the ticket
  so the nonce can never be re-leased, or
- fail for non-transient reasons, in which case the client **releases** the
  ticket so the nonce is re-assigned to the next lease request, avoiding a gap
  in the sequence.

### Lineage and Ethereum account models

For EOAs the mapping is one lineage per `(chain, sender)`. For ERC-4337
account-abstraction wallets the mapping is one lineage per
`(chain, sender, nonceKey)` because UserOperations support 2D nonces. See
[ADR-0001](docs/adr/0001-ethereum-nonce-model-alignment.md) for the full
discussion.

## API

dinonce is built contract-first with OpenAPI 3.0. The spec lives at
[`api/api.yaml`](./api/api.yaml) and is the source of truth for the generated
server stubs in `internal/api/generated/`.

Generate a client library in any language with the
[openapi-generator](https://github.com/OpenAPITools/openapi-generator).

### Surfaces

| Port | Path        | Purpose                                                   |
|-----:|-------------|-----------------------------------------------------------|
| 5010 | `/lineages…`| OpenAPI surface (see `api/api.yaml`)                      |
| 5010 | `/metrics`  | Prometheus metrics                                        |
| 5010 | `/version`  | Build metadata (version, commit, build date)              |
| 5001 | `/livez`    | Liveness probe — process is running (no dependency check) |
| 5001 | `/readyz`   | Readiness probe — registered dependencies are healthy     |

## Configuration

dinonce reads `config.yaml` from `/opt/dinonce/config/`, `$HOME/.dinonce/config/`,
or `./.config/`. Every key can also be supplied as an environment variable
prefixed with `DINONCE_` and dots replaced with underscores, e.g.:

```sh
DINONCE_BACKENDCONFIG_HOST=db.internal
DINONCE_BACKENDCONFIG_PORT=5432
DINONCE_BACKENDCONFIG_USER=dinonce
DINONCE_BACKENDCONFIG_PASSWORD=...   # mount via Kubernetes Secret
DINONCE_LOGGER_LEVEL=info
```

Connection-pool knobs:

| Key                                  | Default | Purpose                              |
|--------------------------------------|---------|--------------------------------------|
| `backendConfig.maxOpenConns`         | 25      | Max concurrent DB connections        |
| `backendConfig.maxIdleConns`         | 5       | Max idle connections kept around     |
| `backendConfig.connMaxLifetime`      | 30m     | Recycle connections after this age   |

## Deployment

Images are published to **GHCR** at
[`ghcr.io/matelang/dinonce`](https://github.com/matelang/dinonce/pkgs/container/dinonce).
They are multi-arch (`linux/amd64` and `linux/arm64`), distroless-based, and
signed with cosign (keyless OIDC). A CycloneDX SBOM is attached to every
release archive.

To verify an image:

```sh
cosign verify ghcr.io/matelang/dinonce:vX.Y.Z \
  --certificate-identity-regexp='^https://github.com/matelang/dinonce/' \
  --certificate-oidc-issuer='https://token.actions.githubusercontent.com'
```

A Helm chart lives under [`deployments/helm`](./deployments/helm). The
operator is responsible for creating the `dinonce-config` ConfigMap (or
Secret) containing a `config.yaml`, structured like
[`.config/config.yaml`](./.config/config.yaml).

A Terraform module under
[`deployments/terraform/modules/helm-aws-rds-psql`](./deployments/terraform/modules/helm-aws-rds-psql)
provisions an AWS RDS Aurora PostgreSQL database, creates a namespace in
Kubernetes, and deploys the Helm chart; see the
[example](./deployments/terraform/examples/helm-aws-rds-psql).

## Backends

dinonce is designed to support multiple storage backends provided they
respect the lineage/ticket semantics. The current backend is PostgreSQL.
Contributions implementing other backends are welcome — see
[CONTRIBUTING.md](./CONTRIBUTING.md).

## Development

```sh
make oapi              # regenerate OpenAPI stubs after editing api/api.yaml
make build             # build the binary with embedded version
make test              # unit tests (-race, fast)
make test-integration  # spin up Postgres on :5433, run integration tests
make lint vuln sec     # golangci-lint, govulncheck, gosec (all via `go tool`)
make cover             # full coverage including integration
```

See [CONTRIBUTING.md](./CONTRIBUTING.md) for the contribution workflow and
[SECURITY.md](./SECURITY.md) for the vulnerability-reporting process.

## Heritage

dinonce was originally developed at [welthee](https://welthee.com); this
repository is an actively-maintained fork. The v2 module path
(`github.com/welthee/dinonce/v2`) is frozen; new work happens against the v3
module path (`github.com/matelang/dinonce/v3`).
