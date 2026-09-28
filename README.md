# w3sper.go

A Go SDK for Dusk, with profile derivation, Moonlight transaction signing,
unit conversions, and a GraphQL client.

Key derivation and signing use the bundled `wallet-core` WebAssembly module
through [wazero](https://wazero.io), a pure-Go WebAssembly runtime.

## Install

Requires Go 1.25 or newer.

```sh
GOPRIVATE=github.com/HDauven/w3sper.go go get github.com/HDauven/w3sper.go@latest
```

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

The [signing example](examples/main.go) derives profiles and signs a Moonlight
transfer using an in-memory account balance:

```sh
go run ./examples
```

It runs offline and does not submit the transaction.

## Packages

| Package | Purpose |
|---------|---------|
| `lux` | Exact Lux ↔ Dusk decimal conversion |
| `gas` | Gas configuration; defaults to limit 2,900,000,000 and price 1 |
| `chainid` | Chain ID validation, 0–255 |
| `base16` / `base58` | Hex and Bitcoin-alphabet Base58 encoding |
| `bookmark` | Eight-byte sync cursors |
| `profile` | Profile generation and public address/account identifiers |
| `network` | GraphQL queries, node information, and block height |
| `driver` | Cryptographic driver interface |
| `walletcore` | Bundled wallet-core WASM runtime |
| `tx` | Transaction builders |
| `bookkeeper` | Profile-bound balances and transaction construction |

## Transactions

The transaction builders support Moonlight account value transfers. Phoenix
transfers require note synchronization and proving, and `AddressTransfer.Build`
returns an error until those are available. Staking, deployment, contract calls,
and RUES subscriptions are not implemented.

## Wallet-core lifecycle

Load the runtime with `walletcore.Load(ctx)` and release it with `Close(ctx)`.
Calls on each instance are serialized. Returned bytes are copied out of WASM
memory and remain valid after later calls or closing the runtime.

Seeds, key lengths, chain IDs, and unsigned 64-bit amounts are checked before
WASM calls. Wallet-core failures return typed errors. Parsed public profiles
contain no signing authority; signing requires a seed-backed generated profile.

## Build and test

```sh
go build ./...
go test -shuffle=on ./...
go test -race ./...
go vet ./...
gofmt -l .
```

CI also runs staticcheck and govulncheck.

## License

[Mozilla Public License 2.0](LICENSE).
