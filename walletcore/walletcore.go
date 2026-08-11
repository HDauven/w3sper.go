// Package walletcore drives the Dusk `wallet-core` WASM module from Go using
// the wazero pure-Go WebAssembly runtime. It implements the cryptographic
// protocol ops — key derivation, note picking, transaction signing, ZK proving
// inputs — exactly as the w3sper.js SDK does via the `@dusk/exu` runtime, but
// without a JavaScript host.
//
// The `.wasm` is the same artifact the JavaScript SDK bundles
// (`wallet-core-1.7.1.wasm`); this package embeds it and exposes it through
// the [driver.Driver] interface.
package walletcore

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/dusk-network/w3sper-go/driver"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Compile-time assertion that WalletCore implements the full driver contract.
var _ driver.Driver = (*WalletCore)(nil)

// Version is the bundled wallet-core version.
const Version = "1.7.1"

//go:embed wallet-core-1.7.1.wasm
var wasmBytes []byte

// Globals holds the parsed wallet-core module globals.
type Globals struct {
	KeySize  uint32
	ItemSize uint32
	MinStake uint64
	HeapBase uint32
}

// WalletCore is a loaded instance of the wallet-core WASM module.
type WalletCore struct {
	mod  api.Module
	rt   wazero.Runtime
	glob Globals
}

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

	return &WalletCore{mod: mod, rt: rt, glob: glob}, nil
}

// Close releases the WASM runtime.
func (w *WalletCore) Close(ctx context.Context) error {
	return w.rt.Close(ctx)
}

// Globals returns the parsed module globals.
func (w *WalletCore) Globals() Globals { return w.glob }

// Module returns the underlying WASM module for low-level access.
func (w *WalletCore) Module() api.Module { return w.mod }

func readGlobals(mod api.Module) (Globals, error) {
	read := func(name string) (uint64, error) {
		g := mod.ExportedGlobal(name)
		if g == nil {
			return 0, fmt.Errorf("walletcore: missing global %q", name)
		}
		return g.Get(), nil
	}

	key, err := read("KEY_SIZE")
	if err != nil {
		return Globals{}, err
	}
	item, err := read("ITEM_SIZE")
	if err != nil {
		return Globals{}, err
	}
	minStake, err := read("MINIMUM_STAKE")
	if err != nil {
		return Globals{}, err
	}
	heapBase, err := read("__heap_base")
	if err != nil {
		return Globals{}, err
	}

	// The KEY_SIZE/ITEM_SIZE/MINIMUM_STAKE globals are ADDRESSES of static
	// slots. Dereference them: u32 for sizes, u64 for the minimum stake.
	mem := mod.Memory()
	readU32 := func(addr uint32) (uint32, bool) {
		b, ok := mem.Read(addr, 4)
		if !ok || len(b) < 4 {
			return 0, false
		}
		return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, true
	}

	itemAddr := uint32(item)
	keyAddr := uint32(key)

	var realKey, realItem uint32
	var realMin uint64

	realItem, ok := readU32(itemAddr)
	if !ok {
		return Globals{}, fmt.Errorf("walletcore: cannot read ITEM_SIZE at %d", itemAddr)
	}
	realKey, ok = readU32(keyAddr)
	if !ok {
		return Globals{}, fmt.Errorf("walletcore: cannot read KEY_SIZE at %d", keyAddr)
	}
	minB, ok := mem.Read(uint32(minStake), 8)
	if !ok || len(minB) < 8 {
		return Globals{}, fmt.Errorf("walletcore: cannot read MINIMUM_STAKE at %d", minStake)
	}
	realMin = uint64(minB[0]) | uint64(minB[1])<<8 | uint64(minB[2])<<16 | uint64(minB[3])<<24 |
		uint64(minB[4])<<32 | uint64(minB[5])<<40 | uint64(minB[6])<<48 | uint64(minB[7])<<56

	return Globals{
		KeySize:  realKey,
		ItemSize: realItem,
		MinStake: realMin,
		HeapBase: uint32(heapBase),
	}, nil
}
