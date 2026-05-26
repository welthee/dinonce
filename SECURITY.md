# Security Policy

## Supported versions

| Version | Supported          |
|---------|--------------------|
| v3.x    | :white_check_mark: |
| v2.x    | :x: (frozen)       |
| < v2    | :x:                |

## Reporting a vulnerability

Please **do not** open a public GitHub issue for security-sensitive reports.

Instead, use GitHub's private vulnerability reporting on this repository
(*Security → Report a vulnerability*). If that is unavailable, email
`mate.lang@binhatch.com` with the subject prefix `[dinonce-security]`.

You can expect:

- Acknowledgment within 3 business days.
- A triage decision (accepted, needs-more-info, declined) within 10 business
  days.
- Coordinated disclosure on a timeline that balances user safety with
  reasonable time-to-fix, typically 90 days.

## Hardening posture

- The HTTP API does not implement authentication. Operators must deploy
  dinonce behind an authenticating ingress, mesh policy, or sidecar.
- The service exposes Prometheus metrics on the main port and a separate
  healthcheck port — restrict these to your scrape and probe networks.
- Database credentials are read from `config.yaml` or environment variables
  prefixed with `DINONCE_`. Mount the file via a Kubernetes `Secret`; never
  commit it.
- Container images are built `nonroot` on a distroless base, signed with
  cosign (keyless OIDC), and shipped with a CycloneDX SBOM.
- CI runs `govulncheck`, `gosec`, CodeQL, and Trivy on every PR and main
  build.
