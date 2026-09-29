# w3sper.go

Go SDK for Dusk, targeting W3sper.js at **`97f1cf26eeeb62d56e656871087ae2f02d960d72`**.
Cryptography runs in the actual bundled wallet-core WASM through
[wazero](https://wazero.io); no JS process, simulated signing, or mock proofs.
Phoenix proofs are requested from the node's prover and attached with wallet-core.

Requires Go 1.25+; development/CI checks use Go 1.27.1.

```sh
GOPRIVATE=github.com/HDauven/w3sper.go GOTOOLCHAIN=go1.27.1 \
  go get github.com/HDauven/w3sper.go
```

## Identity is not authority

A `profile.Profile` contains only public account/address keys. Generate an explicit
`profile.Signer`, dispose it when the wallet locks, and close the runtime separately.
The deprecated `Profile.Seed()` always returns nil. A provider's later refusal to
supply a seed **does not revoke previously issued signers**.

```go
ctx := context.Background()
core, err := walletcore.Load(ctx)
if err != nil { return err }
defer core.Close(context.Background())

// seedProvider is wallet-owned and returns exactly 64 bytes.
generator := profile.NewGenerator(seedProvider, core)
sender, err := generator.DeriveContext(ctx, 0)
if err != nil { return err }
signer, err := generator.Signer(ctx, sender)
if err != nil { return err }
defer signer.Dispose()

entry := tx.NewBookEntry(signer, nil, core)
routed, err := entry.Transfer(big.NewInt(123)).
    Chain(42).Nonce(big.NewInt(7)).Memo([]byte("hello")).
    To(receiver.Account().String())
if err != nil { return err }
built, err := routed.Account.BuildContext(ctx, nil) // signs nonce 8
```

Imports for this fragment: `context`, `math/big`, and SDK packages `profile`, `tx`,
`walletcore`. [`examples/main.go`](examples/main.go) is a complete, runnable,
**offline-only** example with a public synthetic seed:

```sh
GOPRIVATE=github.com/HDauven/w3sper.go GOTOOLCHAIN=go1.27.1 go run ./examples
```

`signer.SignBLSMessage(ctx, bytes)` signs the exact bytes using Dusk BLS V2 and
returns a 48-byte signature. It adds **no prefix, domain, prehash, origin binding,
approval policy, or replay protection**. Define these in your application protocol.

## Network and wallet workflows

Create `network.New(url, network.WithTimeout(120*time.Second))`. HTTP reads do not
require a WebSocket connection; call `Connect(ctx)` for events. `Disconnect(ctx)`
closes the session, not independent HTTP reads, the wallet-core runtime, or loaded
data drivers. Close `client.DataDrivers` when the client is no longer used.

- `network.NewNode(client)`: info (the actual `/on/node/info` RPC), height, CRS,
  provisioners. `client.GasPrice`, `Query`, `QueryRaw`, `Ping`, `PingWebSocket`.
- `client.Scope(name).WithID(id)`: raw `Call`, streaming `Stream`, `Subscribe`,
  `Once`. `Blocks`, `Transactions`, and `Contracts` are convenient scopes.
- `AccountSyncer`: balances/nonces, stakes, owner keys and enriched `History`.
- `AddressSyncer`: note streams, ownership, nullifiers, openings and root.
  `SyncOptions` supports height/position cursors, chunk sizes and progress.
  Extra ownership workers are independently loaded runtimes owned by the caller.
- `bookkeeper.NewNetworkTreasury(client, core)`: current public reads plus a note
  snapshot. `Sync(ctx, signers...)` replaces it only after a complete scan and
  spent-nullifier check. `SetNotes` supports application-managed snapshots.
- `bookkeeper.New(treasury, core, bookkeeper.WithNetwork(client))`: public/private
  balances, note picking, stake/owner queries, and signer-bound `As` entries.
  Custom I/O treasuries should implement `tx.ContextTreasury` and `StakeKeySource`.

For Phoenix, first synchronize owned notes into the treasury. Route a transfer to
`receiver.Address().String()` and call `routed.Address.BuildContext(ctx, client)`.
The builder picks notes, gets openings/root, builds circuits, requests the real
proof and attaches it. Public destinations use account payment; address
destinations use Phoenix payment. `Shield` and `Unshield` convert within the
sender's own public/private identities.

### Transaction builders

| Entry method | Configuration |
| --- | --- |
| `Transfer(amount)` | `To`, `Memo`, `ContractCall`, `Payload`, `Deposit`, `Committed` |
| `Shield(amount)`, `Unshield(amount)` | Public→private and private→public |
| `Deploy(bytecode)` | `InitArgs`, `DeployNonce`, `DeploymentOwner`, `Public` / `Shielded` |
| `Stake(amount)`, `Topup(amount)` | `Owner`, `OwnerSigners`, `Public` / `Shielded` |
| `Unstake(amount)` / `Unstake(nil)` | Partial / full unstake, same owner/payment options |
| `Withdraw(amount)` | Stake reward withdrawal, same owner/payment options |

All support `Gas`, `Chain`, and `BuildContext`. Public transactions resolve the
current nonce from treasury/network unless `Nonce`/`AccountNonce` is supplied;
**the supplied value is the current nonce, not the next nonce**. Deployment's
`DeployNonce` is independent. Builders snapshot before I/O; simultaneous mutation
*during* that initial snapshot is unsupported. Reuse across sequential builds is
supported, but neither builders nor treasuries reserve nonces or pending notes.

`tx.Result` contains owned bytes, hash, chain ID, next nonce or spent nullifiers,
and the deployment contract ID where applicable. `Execute` preverifies and
propagates. `ExecuteAndWait` registers before propagation and returns an execution
event. **Admission or an execution event is not proof of success**: inspect mined
`err` and resulting state, and apply your own finality policy.

Committed calls require a zero-value public self envelope and a contract call.
The live target must support **host-backed B ABI**; a legacy A ABI Bob/Alice target
can be deployed successfully yet reject committed dispatch. The SDK cannot upgrade
a contract's ABI. Stake owners must be matching same-wallet signers; contract-owned
stake is deliberately not treated as wallet-key authority.

### Contracts and data drivers

Register a driver with `client.DataDrivers.Register(contractID, source)` using
`datadriver.Bytes`, `File`, `URL`, or a context-aware `Source`. For stake-owner
reads, register the stake contract's matching data driver. Registries deduplicate
loads; replacement/deletion preserves existing references until registry `Close`.

`contract.New(id, client, entry)` resolves the registry lazily; an explicitly
loaded driver can also be supplied. Methods: `Schema`, `Version`, `Encode`, `Call`,
`Transaction`, `Subscribe`, `Once`. A public-only entry cannot build transactions.
A returned transaction builder still needs payment routing (`To(signer.Account().String())`
or `To(signer.Address().String())`) and any required deposit. Use profile keys'
`.String()` for destinations and hex IDs for scoped contracts.

`datadriver.Driver` provides JSON↔RKYV input/output/event conversion. `Encode` and
contract methods encode `big.Int` as decimal strings and byte slices as byte
arrays; `EncodeJSON` accepts explicit JSON. Decoding returns `json.RawMessage` so
large integers are never forced through float64. ABI signatures, bounds, statuses,
JSON/UTF-8 and lifecycle are checked. Traps, allocator corruption and malformed
native output retire the instance; ordinary reported operation errors do not.
Output capacity is 64 KiB, as in the pinned JS
loader. Required real transfer/stake artifacts and their provenance are in
[`testdata/README.md`](testdata/README.md); arbitrary production drivers remain
application-supplied.

## Cancellation, ownership and limits

- Network operations default to **120 seconds**, including HTTP response-body
  consumption. `WithRequestTimeout(ctx, duration)` overrides this; zero disables
  that deadline, not caller cancellation. Bookkeeper has its own default budget.
- Subscription budgets cover registration only. The context, `Cancel`, or `Close`
  controls lifetime; `Once` and `ExecuteAndWait` include the event wait. Streams
  are bounded; a slow subscriber fails with `ErrSlowConsumer` and releases its
  remote registration without dropping a sibling. `Cancel` schedules bounded
  cleanup; `Close` waits up to five seconds for cleanup acknowledgement.
- Close every HTTP response body, note/history stream, and subscription. Pull-based
  streams have no idle producer goroutine. Canceling a pending stream read is
  terminal. Paused HTTP streams retain their original deadline.
- WebSocket handshake cancellation covers TCP/TLS, pending HTTP upgrade and RUES
  hello. The handshake context is detached after connection. Probe timeouts do
  not cancel sibling probes or poison the session. Reconnect/replay is
  application-managed. `Connected` is local socket state; `WaitDisconnected`
  observes retirement, not chain health.
- One wallet-core instance serializes operations and close. Waiting for admission
  is cancelable. **Already-running synchronous wallet-core WASM is not preempted**;
  cancellation/disposal rejects its result after it returns. Stopping arbitrary
  data-driver WASM *does* close that driver instance; reload after such a failure.
  Untrusted deallocation gets a separate 250 ms cleanup budget.
- Custom seed, treasury, source, HTTP transport, JSON marshaler and progress
  callbacks must cooperate with contexts. Unlike racing an asynchronous JS
  promise, Go cannot promptly return from a synchronous callback that ignores its
  context without leaving its work running. The SDK does not spawn unbounded
  orphan goroutines to hide that limitation. Local file reads are also synchronous.
- Seeds and selected-key snapshots are copied and best-effort wiped. Go/WASM/GC
  memory erasure is **not guaranteed**. Disposal cannot revoke signatures already
  returned, reverse requests already sent, or roll back node execution.
- There is no durable wallet database, automatic nonce/note reservation, recovery,
  event replay, confirmation policy or recipient-checksum validation. These are
  application responsibilities, also not supplied by the pinned JS wallet facade.

Gas defaults apply only to omitted values. `gas.NewChecked` / `Validate` reject
zero, negative, overflowing components and overflowing total fees. Amounts, seeds,
key/contract lengths, chain IDs, nonces, note framing, owners and payload variants
are validated. `profile.TypeOf` classifies Base58 length, not curve validity or
recipient correctness; wallet-core validates cryptographic keys. Some invalid
inputs that JS silently discards are rejected rather than emulating unsafe behavior.

## Validation and live acceptance

[`docs/parity.md`](docs/parity.md) maps implemented groups to evidence and remaining
limits. Offline tests use **independent pinned-JS vectors**, real wallet-core and
real contract data-driver WASMs. Negative-only miniature WASMs exercise malformed
ABI and cancellation/cleanup attacks; they are not cryptographic success fixtures.

```sh
GOPRIVATE=github.com/HDauven/w3sper.go GOTOOLCHAIN=go1.27.1 go test -shuffle=on ./...
GOPRIVATE=github.com/HDauven/w3sper.go GOTOOLCHAIN=go1.27.1 go test -race ./...
GOPRIVATE=github.com/HDauven/w3sper.go GOTOOLCHAIN=go1.27.1 go vet ./...
gofmt -l .
```

The runnable acceptance command emits a JSON report on stdout and progress on
stderr. It never initializes/resets a node. **Submissions require `-submit`** and
funded synthetic profiles; do not use production funds. `-index` selects sender;
receiver/owner/reward-staker are the next three profiles. The reward fixture must
already have stake owned by the owner profile. Newly opened stakes are fully
unstaked in a successful run; an interrupted run may require application recovery.

```sh
# W3SPER_SEED: 64-byte synthetic seed as 128 hex characters.
# W3SPER_NODE_URL: Phoenix-enabled Rusk 1.7.1 with archive and prover.
GOPRIVATE=github.com/HDauven/w3sper.go GOTOOLCHAIN=go1.27.1 \
  go run ./cmd/live-parity -index 10 -group all -submit > live-report.json
```

Groups: `read`, `transfers`, `contracts`, `staking`, `all`. The full local acceptance
passed **22 real transactions**, exact state/fee/nullifier assertions, history,
block/transaction/decoded contract events, corrupt-transaction and wrong-owner
rejections. It is evidence for this pinned local configuration, not a claim that
all future networks, arbitrary contracts, failure timings or providers are covered.

## License

[Mozilla Public License 2.0](LICENSE). See fixture provenance for bundled artifacts.
