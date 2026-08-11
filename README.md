# w3sper Go SDK

`w3sper.go` is a Go port of the Dusk Network `w3sper` SDK (the JavaScript SDK
lives in `../w3sper.js`). It lets Go applications interact with a Dusk Rusk
node: manage profiles, convert value units, validate chain identifiers, query
the node over GraphQL, and build and sign transactions.

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

## Layout

```
w3sper.go/
  go.mod            module github.com/dusk-network/w3sper-go
  w3sper.go         root package, re-exports
  lux/ gas/ chainid/ base16/ base58/ bookmark/
  profile/ network/ driver/ tx/ bookkeeper/
```

## Usage

```go
package main

import (
	"context"
	"fmt"

	"github.com/dusk-network/w3sper-go"
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

```sh
go build ./...
go test ./...
go vet ./...
gofmt -l .
```

## License

This project is licensed under the [Mozilla Public License Version 2.0](../../LICENSE).
