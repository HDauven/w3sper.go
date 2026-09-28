package walletcore

import (
	"context"
	"encoding/binary"
	"fmt"
)

// Memory helpers mirror the JS allocator plus the wallet-core malloc/free ABI.
// wallet-core's `free(ptr, size)` requires the allocation length, so every
// allocation is size-tracked in w.allocs. All callers operate under the
// instance lock (runLocked), so the map needs no separate synchronization.

// alloc allocates n bytes in WASM memory, records their size, and returns the
// pointer.
func (w *WalletCore) alloc(ctx context.Context, n uint32) (uint32, error) {
	fn := w.mod.ExportedFunction("malloc")
	if fn == nil {
		return 0, fmt.Errorf("walletcore: malloc not exported")
	}
	res, err := fn.Call(ctx, uint64(n))
	if err != nil {
		return 0, fmt.Errorf("walletcore: malloc(%d): %w", n, err)
	}
	ptr := uint32(res[0])
	w.track(ptr, n)
	return ptr, nil
}

// track records an allocation's size.
func (w *WalletCore) track(ptr, size uint32) {
	if w.allocs == nil {
		w.allocs = make(map[uint32]uint32)
	}
	if size > 0 {
		w.allocs[ptr] = size
	}
}

// free releases a previously tracked allocation. If an out-of-line size is
// provided (for untracked wallet-core output allocations), it is used directly.
func (w *WalletCore) free(ctx context.Context, ptr uint32, outOfLineSize ...uint32) error {
	var size uint32
	if len(outOfLineSize) > 0 {
		size = outOfLineSize[0]
	} else if s, ok := w.allocs[ptr]; ok {
		size = s
		delete(w.allocs, ptr)
	}

	fn := w.mod.ExportedFunction("free")
	if fn == nil {
		return nil
	}
	if _, err := fn.Call(ctx, uint64(ptr), uint64(size)); err != nil {
		return fmt.Errorf("walletcore: free(%d): %w", ptr, err)
	}
	return nil
}

// writeBytes writes b into memory at ptr.
func (w *WalletCore) writeBytes(ctx context.Context, ptr uint32, b []byte) error {
	if len(b) > 0 && !w.mod.Memory().Write(ptr, b) {
		return fmt.Errorf("walletcore: mem write out of bounds at %d len %d", ptr, len(b))
	}
	return nil
}

// readBytes reads n bytes from memory at ptr and returns an owned copy.
func (w *WalletCore) readBytes(ctx context.Context, ptr uint32, n uint32) ([]byte, error) {
	view, ok := w.mod.Memory().Read(ptr, n)
	if !ok {
		return nil, fmt.Errorf("walletcore: mem read out of bounds at %d len %d", ptr, n)
	}
	return cloneBytes(view), nil
}

// cloneBytes copies a wazero memory view into a Go-owned slice. Wazero memory
// reads are views into linear memory that may be reused later; copying protects
// callers from silent aliasing.
func cloneBytes(in []byte) []byte {
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

// box allocates n bytes and copies src into them, returning the pointer.
func (w *WalletCore) box(ctx context.Context, src []byte) (uint32, error) {
	ptr, err := w.alloc(ctx, uint32(len(src)))
	if err != nil {
		return 0, err
	}
	if err := w.writeBytes(ctx, ptr, src); err != nil {
		return 0, err
	}
	return ptr, nil
}

// readU32 reads a little-endian u32 at ptr.
func (w *WalletCore) readU32(ctx context.Context, ptr uint32) (uint32, error) {
	b, err := w.readBytes(ctx, ptr, 4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

// takeLenPrefixedOutput reads the pointer stored at slot (a pointer handle),
// where the pointed-to allocation is produced by wallet-core's
// write_len_prefixed: [u32 len][bytes]. It returns an owned copy of the payload
// and frees the WASM allocation. includeLength controls whether the leading
// 4-byte length is included in the returned payload.
func (w *WalletCore) takeLenPrefixedOutput(ctx context.Context, slot uint32, includeLength bool) ([]byte, error) {
	// The slot stores the allocation pointer.
	target, err := w.readU32(ctx, slot)
	if err != nil {
		return nil, err
	}
	return w.takeAt(ctx, target, includeLength)
}

// takeAt reads and frees a length-prefixed allocation at target.
func (w *WalletCore) takeAt(ctx context.Context, target uint32, includeLength bool) ([]byte, error) {
	length, err := w.readU32(ctx, target)
	if err != nil {
		return nil, err
	}

	// Read the payload first (owned copy).
	var payload []byte
	if includeLength {
		payload, err = w.readBytes(ctx, target, 4+length)
	} else {
		payload, err = w.readBytes(ctx, target+4, length)
	}
	if err != nil {
		_ = w.free(ctx, target)
		return nil, err
	}

	// Release the wallet-core-owned allocation out-of-line.
	release := target
	fn := w.mod.ExportedFunction("free")
	if fn != nil {
		// free(ptr, size) where size = 4 + length.
		_, _ = fn.Call(ctx, uint64(release), uint64(4+length))
	}

	return payload, nil
}
