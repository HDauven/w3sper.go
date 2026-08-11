// Package walletcore drives the Dusk `wallet-core` WASM module from Go using
// the wazero pure-Go WebAssembly runtime. It implements the cryptographic
// protocol ops — key derivation, note picking, transaction signing, ZK proving
// inputs — exactly as the w3sper.js SDK does via the `@dusk/exu` runtime, but
// without a JavaScript host.
//
// The `.wasm` is the same artifact the JavaScript SDK bundles
// (`wallet-core-1.7.1.wasm`); this package embeds it and exposes it through
// the [driver.Driver] interface.
//
// # Concurrency
//
// A [WalletCore] is a single WASM instance with one linear memory. Calls are
// serialized with an internal mutex, so methods are safe for concurrent use
// but do not run in parallel. Create multiple instances for parallel, isolated
// workers.
package walletcore

import (
	"context"
	_ "embed"
	"fmt"
	"sync"

	"github.com/dusk-network/w3sper-go/driver"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Version is the bundled wallet-core version.
const Version = "1.7.1"

//go:embed wallet-core-1.7.1.wasm
var wasmBytes []byte

// opsCtx is the background context used for wazero calls when no explicit
// context is threaded through the driver.Driver interface.
func opsCtx() context.Context {
	return context.Background()
}

// Globals holds the parsed wallet-core module globals.
type Globals struct {
	KeySize  uint32
	ItemSize uint32
	MinStake uint64
	HeapBase uint32
}

// WalletCore is a loaded instance of the wallet-core WASM module. Each
// instance has its own linear memory and serialized calls.
type WalletCore struct {
	mod    api.Module
	rt     wazero.Runtime
	glob   Globals
	mu     sync.Mutex
	alive  bool
	allocs map[uint32]uint32
}

// Compile-time assertion that WalletCore implements the full driver contract.
var _ driver.Driver = (*WalletCore)(nil)

// Load instantiates the embedded wallet-core WASM and reads its globals.
func Load(ctx context.Context) (*WalletCore, error) {
	rt := wazero.NewRuntime(ctx)
	mod, err := rt.InstantiateWithConfig(ctx, wasmBytes, wazero.NewModuleConfig().WithName("wallet-core"))
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("walletcore: instantiate: %w", err)
	}

	glob, err := readGlobals(mod)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}

	return &WalletCore{mod: mod, rt: rt, glob: glob, alive: true}, nil
}

// Close releases the WASM runtime. It blocks until any in-flight call finishes
// and renders the instance unusable.
func (w *WalletCore) Close(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.alive {
		return nil
	}
	w.alive = false
	return w.rt.Close(ctx)
}

// checkAlive returns an error if the instance has been closed.
func (w *WalletCore) checkAlive() error {
	if !w.alive {
		return ErrClosed
	}
	return nil
}

// Globals returns the parsed module globals.
func (w *WalletCore) Globals() Globals { return w.glob }

// runLocked executes fn with the instance lock held and the lifecycle checked.
// Public methods must hold the lock across the entire WASM operation (the call
// plus reading any outputs and freeing allocations) so a concurrent method
// cannot reuse memory before this one reads it.
func (w *WalletCore) runLocked(ctx context.Context, fn func(w *WalletCore) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.checkAlive(); err != nil {
		return err
	}

	return fn(w)
}

// invoke performs a single wallet-core export call and validates the returned
// status code. The caller must hold the instance lock.
func (w *WalletCore) invoke(ctx context.Context, op string, fn api.Function, args ...uint64) error {
	res, err := fn.Call(ctx, args...)
	if err != nil {
		return fmt.Errorf("walletcore: %s: %w", op, err)
	}
	return checkCode(op, res)
}

// checkCode validates the numeric status returned by a wallet-core export.
func checkCode(op string, results []uint64) error {
	if len(results) == 0 {
		return fmt.Errorf("walletcore: %s: missing wallet-core status", op)
	}
	if results[0] != 0 {
		return walletcoreError(op, uint32(results[0]))
	}
	return nil
}

// walletcoreError maps a wallet-core error code to a stable error.
func walletcoreError(op string, code uint32) error {
	return &CodeError{Op: op, Code: code}
}

// CodeError is a wallet-core error with its numeric code.
type CodeError struct {
	Op   string
	Code uint32
}

func (e *CodeError) Error() string {
	return fmt.Sprintf("walletcore: %s: error code %d", e.Op, e.Code)
}

// ErrClosed is returned when an operation is attempted on a closed instance.
var ErrClosed = fmt.Errorf("walletcore: instance is closed")

// readGlobals dereferences the wallet-core globals, which are addresses of
// static memory slots, not values.
func readGlobals(mod api.Module) (Globals, error) {
	read := func(name string) (uint64, error) {
		g := mod.ExportedGlobal(name)
		if g == nil {
			return 0, fmt.Errorf("walletcore: missing global %q", name)
		}
		return g.Get(), nil
	}

	heapBase, err := read("__heap_base")
	if err != nil {
		return Globals{}, err
	}

	mem := mod.Memory()
	readU32 := func(addr uint32) (uint32, error) {
		b, ok := mem.Read(addr, 4)
		if !ok || len(b) < 4 {
			return 0, fmt.Errorf("walletcore: cannot read u32 at %d", addr)
		}
		return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, nil
	}

	key, err := read("KEY_SIZE")
	if err != nil {
		return Globals{}, err
	}
	item, err := read("ITEM_SIZE")
	if err != nil {
		return Globals{}, err
	}
	min, err := read("MINIMUM_STAKE")
	if err != nil {
		return Globals{}, err
	}

	realKey, err := readU32(uint32(key))
	if err != nil {
		return Globals{}, err
	}
	realItem, err := readU32(uint32(item))
	if err != nil {
		return Globals{}, err
	}

	minB, ok := mem.Read(uint32(min), 8)
	if !ok || len(minB) < 8 {
		return Globals{}, fmt.Errorf("walletcore: cannot read MINIMUM_STAKE at %d", min)
	}
	realMin := uint64(minB[0]) | uint64(minB[1])<<8 | uint64(minB[2])<<16 | uint64(minB[3])<<24 |
		uint64(minB[4])<<32 | uint64(minB[5])<<40 | uint64(minB[6])<<48 | uint64(minB[7])<<56

	return Globals{
		KeySize:  realKey,
		ItemSize: realItem,
		MinStake: realMin,
		HeapBase: uint32(heapBase),
	}, nil
}
