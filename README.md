# w3sper Go SDK

`w3sper.go` is an independently maintained Go port of the Dusk Network
`w3sper` SDK, hosted at [`HDauven/w3sper.go`](https://github.com/HDauven/w3sper.go).
It lets Go applications manage profiles, convert value units, validate chain
identifiers, query a Dusk Rusk node over GraphQL, and build and sign transactions.

This experimental project was extracted from
[Dusk Rusk PR #359](https://github.com/dusk-network/rusk-private/pull/359)
at commit `e096a859f55a8e2bb3583bdb44ceed52ba4d7110`. The three Go SDK commits
retain their original authorship and dates; only their SDK subtrees are carried
into this repository. The original Dusk MPL-2.0 license is retained.
The embedded wallet-core WASM is unchanged from that revision. Compatibility
with current live networks has not been established by this extraction.

It does **not** reimplement the Dusk cryptography. Instead it reuses the same
`wallet-core` WASM artifact the JavaScript SDK bundles, executed from Go with
the [wazero](https://wazero.io) pure-Go WebAssembly runtime. The WASM is
driven through the same `malloc`/memory conventions w3sper.js uses, so key
derivation, note picking and transaction signing behave identically.

## Status

- The pure, deterministic layers are fully implemented and tested.
- The **cryptographic protocol layer** is implemented in
  [`walletcore`](./walletcore), which loads the embedded `wallet-core` WASM and
  exposes it through the [`driver.Driver`](./driver/driver.go) interface.
  Profile derivation and account (moonlight) transaction **signing are verified
  against the real WASM** (see the walletcore tests / `TestEndToEndMoonlightTransfer`).
- The **shielded (Phoenix) flow** is scaffolded but not yet exercised end-to-end:
  it requires a network syncer for merkle openings/root and the node's prover
  endpoint, plus `into_proven` proving. This is the main remaining work.

### Implemented

| Package | Mirror of | Notes |
|---------|-----------|-------|
| `lux` | `lux.js` | Lux ↔ Dusk decimal conversion, exact big-int arithmetic |
| `gas` | `gas.js` | Gas config with defaults (2.9 G limit, price 1) |
| `chainid` | `chain-id.js` | Chain id validation `[0,255]` |
| `base16` / `base58` | `encoders/b16.js`, `encoders/b58.js` | Lowercase hex, Bitcoin-alphabet Base58 (no checksum) |
| `bookmark` | `network/bookmark.js` | 8-byte sync cursor |
| `profile` | `profile.js` | Address (64B) + account (96B) keypair model + generator |
| `network` | `network/graphql.js` + node component | GraphQL-over-HTTP client, node info, block height |
| `driver` | `protocol-driver/mod.js` (boundary) | The protocol/crypto interface |
| `walletcore` | `protocol-driver/mod.js` (implementation) | wazero driver over the embedded `wallet-core` WASM |
| `tx` | `transaction.js` | Transaction builders; account (moonlight) flow wired + signed |
| `bookkeeper` | `bookkeeper.js` | Profile-bound wallet facade |

### Remaining work

- **Phoenix (shielded) flow** — note picking, merkle openings/root sync, ZK
  proving (`network.prove`), `IntoProven`. The `AddressTransfer` path needs the
  network sync/prover wiring.
- **Stake builders** (stake/unstake/withdraw/top-up) over moonlight/phoenix.
- **RUES WebSocket** components (event subscription). The GraphQL query surface
  is implemented; the event-driven RUES protocol is out of scope here.
- **Data-driver contracts** (`Contract`, contract-call encoding via rkyv / the
  per-contract data-driver WASM).

## Scope (current == Moonlight value transfer only)

The transaction-builder layer currently supports **Moonlight (account) value
transfers only**. Shielded (`AddressTransfer`), stake, deployment and contract
call builders are not yet wired; `AddressTransfer.Build` returns a clear error
until the Phoenix sync/proving path lands. See _Remaining work_ below.

### Hardened host boundary

This pass applied the review's concrete correctness fixes before further
layering:

- **DataBuffer ABI** now matches `buffer.js` exactly (`setLayout` derives
  alignment from content length; empty collections zero the footer;
  `packNoteEntries` writes the footer via the same path).
- **Owned outputs**: every returned slice is copied out of WASM memory, and
  WASM heap allocations are freed (`free(ptr, len)`); results survive later
  allocations (covered by a regression test).
- **Error codes**: every wallet-core export is checked and mapped to a typed
  error; no more ignored status codes.
- **Boundary validation**: seeds (64B), account keys (96B), address keys (64B),
  chain id and non-negative `uint64` amounts are validated before touching
  WASM; malformed inputs return errors instead of panicking.
- **Concurrency**: calls on one instance are serialized with a mutex and the
  lifecycle (alive/closed) is checked.
- **Generator** is no longer order-dependent (`Default()` safely precedes
  `Next()`), and parsed public profiles are never silently usable as signers.

### Remaining work

- **Phoenix (shielded) flow** — note picking, merkle openings/root sync, ZK
  proving (`network.prove`), `IntoProven`. This needs a network syncer and
  prover wiring before `AddressTransfer` can be trustworthy, plus real
  encrypted-note fixtures for the note-sync operations.
- **Stake builders** (stake/unstake/withdraw/top-up) over moonlight/phoenix.
- **Contract deployment and contract calls** (rkyv payload serialization).
- **RUES WebSocket** components (event subscription, node-info via RUES). The
  GraphQL query surface is implemented; the event-driven RUES protocol is out of
  scope here.
- **Data-driver contracts** (`Contract` + per-contract data-driver WASM).
- **Real localnet integration** — derive a profile, resolve nonce, sign, then
  submit through preverify/propagate and confirm on-chain.

## Layout

```
w3sper.go/
  go.mod            module github.com/HDauven/w3sper.go
  w3sper.go         root package, re-exports
  lux/ gas/ chainid/ base16/ base58/ bookmark/
  profile/ network/ driver/ tx/ bookkeeper/ walletcore/
  .github/workflows/ci.yml   (Go CI)
```

## Install

Requires Go 1.25 or newer. This is a private repository: authenticate Git with
GitHub first, and include `github.com/HDauven/w3sper.go` in `GOPRIVATE` to avoid
sending private module requests to the public Go proxy or checksum database.
For example, from your application's Go module:

```sh
GOPRIVATE=github.com/HDauven/w3sper.go go get github.com/HDauven/w3sper.go@latest
```

If you already use `GOPRIVATE`, append this module to your existing patterns
rather than replacing them.

## Usage

```go
package main

import (
	"context"
	"fmt"
	"math/big"

	"github.com/HDauven/w3sper.go"
)

func main() {
	// Lux → Dusk
	s, _ := w3sper.FormatToDusk(big.NewInt(11000000001)) // "11.000000001"

	// GraphQL client for a Rusk node
	c, _ := w3sper.NewNetwork("http://127.0.0.1:8080")
	h, _ := w3sper.NewNetworkNode(c).BlockHeight(context.Background())

	fmt.Println(s, h)
}
```

## Build & test

From the repository root:

```sh
go build ./...
go test -shuffle=on ./...
go test -race ./...
go vet ./...
gofmt -l .
```

CI runs from the repository root on GitHub-hosted Ubuntu with Go 1.27,
including pinned staticcheck and govulncheck versions. Run the offline signing
example with `go run ./examples`; it does not submit a transaction.

## License

This project is licensed under the [Mozilla Public License Version 2.0](LICENSE).
