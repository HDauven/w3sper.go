package walletcore

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/HDauven/w3sper.go/driver"
)

// These DataBuffer helpers mirror w3sper.js `src/protocol-driver/buffer.js` and
// the wallet-core `mem::read_buffer`/`write_len_prefixed` ABI.
//
// An input DataBuffer is laid out as:
//
//	[0..4)   u32 little-endian content length (content + 8-byte footer)
//	[4..)    concatenated items (flattened)
//	[-8..)   layout footer: u32 alignment, u32 item count
//
// The caller passes the pointer to the leading u32 length. wallet-core reads it
// with `read_buffer` and parses the following bytes as an rkyv archive that
// ends with the same 8-byte layout footer.
//
// wallet-core's in-place outputs (e.g. `pick_notes`) instead use:
//
//	[0..4)  u32 byte length
//	[4..)   serialized payload
// Heap-allocated outputs (e.g. `write_len_prefixed`) use the same shape.

// newDataBuffer builds a DataBuffer from a caller-provided content buffer and
// item count, returning bytes with the length header and layout footer set.
// It is the Go equivalent of buffer.js `from`: content already contains the
// flattened items.
func newDataBuffer(content []byte, count uint32) []byte {
	buf := make([]byte, 4+len(content)+8)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(content))+8)
	copy(buf[4:], content)
	_ = setLayout(buf[4:], count)
	return buf
}

// setLayout writes the alignment and item-count footer used by buffer.js.
// dataAndFooter must be the region after the 4-byte length header (content
// followed by the 8-byte footer). It matches the JavaScript `setLayout`, which
// derives alignment from the content length (len - 8).
//
// An empty collection retains a zeroed footer, matching buffer.js.
func setLayout(dataAndFooter []byte, count uint32) error {
	if len(dataAndFooter) < 8 {
		return fmt.Errorf("databuffer: missing layout footer")
	}
	if count == 0 {
		return nil
	}
	contentLen := len(dataAndFooter) - 8
	align := uint32(0) - uint32((contentLen+3)&^3)
	binary.LittleEndian.PutUint32(dataAndFooter[len(dataAndFooter)-8:], align)
	binary.LittleEndian.PutUint32(dataAndFooter[len(dataAndFooter)-4:], count)
	return nil
}

// noteValuesContent concatenates the note value bytes (each itemSize long).
func (w *WalletCore) noteValuesContent(ctx context.Context, notes []driver.Note) ([]byte, uint32, error) {
	itemSize := w.glob.ItemSize
	content := make([]byte, 0, len(notes)*int(itemSize))
	for _, n := range notes {
		if len(n.Value) != int(itemSize) {
			return nil, 0, fmt.Errorf("walletcore: note value size %d, want %d", len(n.Value), itemSize)
		}
		content = append(content, n.Value...)
	}
	return content, uint32(len(notes)), nil
}

// noteEntriesContent concatenates each note as key||value.
func (w *WalletCore) noteEntriesContent(ctx context.Context, notes []driver.Note) ([]byte, uint32, error) {
	keySize := w.glob.KeySize
	itemSize := w.glob.ItemSize
	entrySize := keySize + itemSize
	content := make([]byte, 0, len(notes)*int(entrySize))
	for _, n := range notes {
		if len(n.Nullifier) != int(keySize) {
			return nil, 0, fmt.Errorf("walletcore: note key size %d, want %d", len(n.Nullifier), keySize)
		}
		if len(n.Value) != int(itemSize) {
			return nil, 0, fmt.Errorf("walletcore: note value size %d, want %d", len(n.Value), itemSize)
		}
		content = append(content, n.Nullifier...)
		content = append(content, n.Value...)
	}
	return content, uint32(len(notes)), nil
}

func leUint64(b []byte) uint64 {
	return binary.LittleEndian.Uint64(b)
}
