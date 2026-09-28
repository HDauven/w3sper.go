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

// boxDataBuffer writes buf into WASM memory and returns the pointer to the
// leading length header.
func (w *WalletCore) boxDataBuffer(ctx context.Context, buf []byte) (uint32, error) {
	return w.box(ctx, buf)
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

// boxNotesValues produces and returns a DataBuffer of note values.
func (w *WalletCore) boxNotesValues(ctx context.Context, notes []driver.Note) (uint32, error) {
	content, count, err := w.noteValuesContent(ctx, notes)
	if err != nil {
		return 0, err
	}
	return w.boxDataBuffer(ctx, newDataBuffer(content, count))
}

// boxNoteEntries produces and returns a DataBuffer of key||value note entries.
func (w *WalletCore) boxNoteEntries(ctx context.Context, notes []driver.Note) (uint32, error) {
	content, count, err := w.noteEntriesContent(ctx, notes)
	if err != nil {
		return 0, err
	}
	return w.boxDataBuffer(ctx, newDataBuffer(content, count))
}

// boxOpenings produces and returns a DataBuffer of merkle openings.
func (w *WalletCore) boxOpenings(ctx context.Context, openings [][]byte) (uint32, error) {
	total := 0
	for _, o := range openings {
		total += len(o)
	}
	content := make([]byte, 0, total)
	for _, o := range openings {
		content = append(content, o...)
	}
	return w.boxDataBuffer(ctx, newDataBuffer(content, uint32(len(openings))))
}

// readPickedNotes reads the notes pick_notes writes in place at ptr. After the
// call, ptr points to [u32 length][serialized picked-note entries]. The picked
// note list is serialized with the same key||value entry layout.
func (w *WalletCore) readPickedNotes(ctx context.Context, ptr uint32) ([]driver.Note, error) {
	length, err := w.readU32(ctx, ptr)
	if err != nil {
		return nil, err
	}
	keySize := w.glob.KeySize
	itemSize := w.glob.ItemSize
	entrySize := keySize + itemSize

	raw, err := w.readBytes(ctx, ptr+4, length)
	if err != nil {
		return nil, err
	}

	// The payload ends with an 8-byte layout footer (align + count) from the
	// note list serialization. Parse entries from the content region.
	content := raw
	if len(raw) >= 8 {
		count := binary.LittleEndian.Uint32(raw[len(raw)-4:])
		content = raw[:len(raw)-8]
		if int(count)*int(entrySize) <= len(content) {
			// Re-slice strictly to count entries.
			content = content[:int(count)*int(entrySize)]
		}
	}

	result := make([]driver.Note, 0, len(content)/int(entrySize))
	for i := 0; i+int(entrySize) <= len(content); i += int(entrySize) {
		key := make([]byte, keySize)
		copy(key, content[i:])
		value := make([]byte, itemSize)
		copy(value, content[i+int(keySize):])
		result = append(result, driver.Note{Nullifier: key, Value: value})
	}
	return result, nil
}

func leUint64(b []byte) uint64 {
	return binary.LittleEndian.Uint64(b)
}

func putLeUint64(b []byte, v uint64) {
	binary.LittleEndian.PutUint64(b, v)
}

func putLeUint32(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b, v)
}
