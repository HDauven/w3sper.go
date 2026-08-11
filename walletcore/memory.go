package walletcore

import (
	"context"
	"encoding/binary"
	"fmt"
)

// alloc allocates n bytes in WASM memory and returns the pointer.
func (w *WalletCore) alloc(ctx context.Context, n uint32) (uint32, error) {
	fn := w.mod.ExportedFunction("malloc")
	if fn == nil {
		return 0, fmt.Errorf("walletcore: malloc not exported")
	}
	res, err := fn.Call(ctx, uint64(n))
	if err != nil {
		return 0, fmt.Errorf("walletcore: malloc(%d): %w", n, err)
	}
	return uint32(res[0]), nil
}

// free releases an allocation.
func (w *WalletCore) free(ctx context.Context, ptr uint32) error {
	fn := w.mod.ExportedFunction("free")
	if fn == nil {
		return nil
	}
	_, err := fn.Call(ctx, uint64(ptr))
	if err != nil {
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

// readBytes reads n bytes from memory at ptr.
func (w *WalletCore) readBytes(ctx context.Context, ptr uint32, n uint32) ([]byte, error) {
	out, ok := w.mod.Memory().Read(ptr, n)
	if !ok {
		return nil, fmt.Errorf("walletcore: mem read out of bounds at %d len %d", ptr, n)
	}
	return out, nil
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

// writeU32 writes a little-endian u32 at ptr.
func (w *WalletCore) writeU32(ctx context.Context, ptr uint32, v uint32) error {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return w.writeBytes(ctx, ptr, b[:])
}

// writeU64 writes a little-endian u64 at ptr.
func (w *WalletCore) writeU64(ctx context.Context, ptr uint32, v uint64) error {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return w.writeBytes(ctx, ptr, b[:])
}

// readU32 reads a little-endian u32 at ptr.
func (w *WalletCore) readU32(ctx context.Context, ptr uint32) (uint32, error) {
	b, err := w.readBytes(ctx, ptr, 4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

// readU64 reads a little-endian u64 at ptr.
func (w *WalletCore) readU64(ctx context.Context, ptr uint32) (uint64, error) {
	b, err := w.readBytes(ctx, ptr, 8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

// readLenPrefixed reads a u32 length at ptr followed by that many bytes at
// ptr+4. This is the DataBuffer layout produced by buffer.js `from`.
func (w *WalletCore) readLenPrefixed(ctx context.Context, ptr uint32) ([]byte, error) {
	length, err := w.readU32(ctx, ptr)
	if err != nil {
		return nil, err
	}
	return w.readBytes(ctx, ptr+4, length)
}

// readLengthPointer reads the target pointer stored at ptr (a pointer handle),
// then reads a u32 length at that target and the following bytes (excluding the
// length prefix). This mirrors reading a returned boxed databuffer.
func (w *WalletCore) readBoxedDatabuffer(ctx context.Context, ptr uint32, includeLength bool) ([]byte, error) {
	target, err := w.readU32(ctx, ptr)
	if err != nil {
		return nil, err
	}
	length, err := w.readU32(ctx, target)
	if err != nil {
		return nil, err
	}
	offset := uint32(0)
	if !includeLength {
		offset = 4
	}
	return w.readBytes(ctx, target+offset, length)
}
