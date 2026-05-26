# ADR-0001: Ethereum nonce model alignment

- **Status:** Accepted
- **Date:** 2026-05-26
- **Decision driver:** clarify how dinonce's `lineage` concept maps to the
  Ethereum execution-layer nonce model so operators can deploy it correctly
  for both EOAs and ERC-4337 account-abstraction wallets without changing the
  API surface.

## Context

dinonce was designed before account-abstraction landed. Its data model is:

```
lineage(id, ext_id, next_nonce, leased_nonce_count, released_nonce_count,
        max_leased_nonce_count, max_nonce_value, version)
ticket(lineage_id, ext_id, nonce, lease_status, leased_at)
```

Each lineage carries a strictly monotonically-increasing 64-bit `next_nonce`
counter and a bounded pool of leased-but-not-yet-completed tickets. This
matches the Ethereum execution-layer transaction model exactly: a single
sender has a single sequential nonce space and the network rejects any
transaction whose nonce is not "next-expected" for that sender. Replay
protection and gap-avoidance are the two properties the design optimises
for, and these properties carry through EIP-1559 (type-2 fee market),
EIP-4844 (blob transactions), and EIP-7702 (set-EOA-code transactions)
unchanged — those EIPs alter the fee market and the transaction-type byte,
not the nonce.

ERC-4337 (account abstraction) is the one shift worth aligning with. A 4337
wallet ("SmartAccount") submits `UserOperation`s through a bundler instead
of native transactions. The EntryPoint contract enforces nonces using a
**2D nonce**: `nonce = (nonceKey << 64) | nonceSequence`, where `nonceKey`
is `uint192`. Each `nonceKey` is an independent sequence, allowing parallel
in-flight ops per account. This is implemented in `INonceManager` on the
EntryPoint and is exposed by every major 4337 bundler.

## Decision

dinonce will **not** add `nonceKey` as a first-class field. Instead, the
mapping is left to the operator via the existing `ext_id` (lineage external
identifier) field, which the service already treats as a free-form
namespacing key. The recommended encodings are:

- **EOA**: one lineage per `(chain_id, sender_address)`. Recommended
  `ext_id` shape: `eoa:<chain_id>:<sender_address>`, e.g.
  `eoa:1:0xabc…`.

- **ERC-4337 SmartAccount**: one lineage per
  `(chain_id, sender_address, nonceKey)`. Recommended `ext_id` shape:
  `aa:<chain_id>:<sender_address>:<nonceKey>`, where `nonceKey` is the
  `uint192` value used in the EntryPoint nonce. A SmartAccount that uses
  only the default key (`0`) needs exactly one lineage.

Within a lineage, `dinonce` issues monotonically-increasing nonces starting
at `startLeasingFrom`. For 4337 lineages this is the `nonceSequence`
component (lower 64 bits of the EntryPoint nonce); the operator is
responsible for composing the full 256-bit EntryPoint nonce
(`(nonceKey << 64) | nonceSequence`) at the point of UserOperation
construction. dinonce does not need to know the `nonceKey` for any
correctness property — sequence-monotonicity within the lineage is
sufficient.

## Why not first-class `nonceKey`?

Three reasons.

1. The lineage model already supports the necessary semantics: an
   arbitrary number of independent monotonic counters, each identified by
   a free-form key. Adding `nonceKey` as a column would duplicate the
   `ext_id` namespacing role with no new behaviour.
2. The operator chooses the `nonceKey` allocation policy (per-relayer,
   per-tx-class, per-time-window…). That policy is application-level and
   doesn't belong in dinonce.
3. Future non-Ethereum chains (Cosmos, Solana, …) have similarly
   axis-shaped nonce models that the same `ext_id`-as-namespace pattern
   covers without further schema churn.

## What this ADR does **not** cover

- Tx-hash tracking, replacement-chain reasoning, or stuck-ticket
  reconciliation. These are real prod concerns for executors that submit
  underpriced or evicted transactions, but they belong to a separate
  decision because they imply storing on-chain metadata in dinonce and
  changing the lifecycle semantics from "client tells us the outcome" to
  "we observe the outcome". A future ADR can revisit this if needed.
- A nonce-cancellation helper (zero-value self-transfer at a stuck nonce
  with a 12.5%+ fee bump). This is the executor's responsibility; dinonce
  doesn't need to know about it.

## Consequences

- The API surface (`api/api.yaml`) is unchanged. No migration is required.
- The README documents the recommended `ext_id` encodings; clients are
  free to choose any namespacing scheme as long as it is consistent within
  their fleet.
- A future ADR can add `tx_hash`, `gas_price`, and stuck-ticket detection
  if and when an operator needs dinonce to own that part of the lifecycle.
