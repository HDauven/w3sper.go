package walletcore

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/dusk-network/w3sper-go/driver"
)

// These data-buffer helpers mirror w3sper.js `src/protocol-driver/buffer.js`.
// A "DataBuffer" is a byte layout used to transfer note batches to/from the
// wallet-core WASM:
//
//	[0..4)  u32 little-endian total content length (excl. the 4-byte header)
//	[4..)   concatenated items (flattened)
//	[-8..)  layout footer: u32 alignment, u32 item size
//
// Independent single values are packed with just the 4-byte length header.

// packNotesValues packs the note value bytes (each itemSize long) into a
// DataBuffer for the `balance` / note-sync calls. It returns the buffer bytes.
func (w *WalletCore) packNotesValues(ctx context.Context, notes []driver.Note) ([]byte, uint32, error) {
	itemSize := w.glob.ItemSize

	// total content length = sum of item sizes
	total := uint32(len(notes)) * itemSize
	contentLen := total + 8 // reserve layout footer

	// buffer: 4-byte length header + content + 8-byte layout
	buf := make([]byte, 4+contentLen)
	binary.LittleEndian.PutUint32(buf[0:4], contentLen)

	off := uint32(4)
	for _, n := range notes {
		value := n.Value
		if len(value) != int(itemSize) {
			return nil, 0, fmt.Errorf("walletcore: note value size %d, want %d", len(value), itemSize)
		}
		copy(buf[off:], value)
		off += itemSize
	}

	setLayout(buf[4+total:], uint32(len(notes)))

	ptr, err := w.box(ctx, buf)
	if err != nil {
		return nil, 0, err
	}
	return buf, ptr, nil
}

// setLayout writes the alignment and item-size footer used by buffer.js.
func setLayout(footer []byte, size uint32) {
	totalLength := len(footer)
	align := uint32(0) - uint32((totalLength+3)&^3)
	binary.LittleEndian.PutUint32(footer[len(footer)-8:], align)
	binary.LittleEndian.PutUint32(footer[len(footer)-4:], size)
}

// packNoteEntries packs the notes as key||value entries into a DataBuffer. The
// pointer returned is the address of the 4-byte length header.
func (w *WalletCore) packNoteEntries(ctx context.Context, notes []driver.Note, includeTrailingSize bool) (uint32, error) {
	keySize := w.glob.KeySize
	itemSize := w.glob.ItemSize
	entrySize := keySize + itemSize

	total := uint32(len(notes)) * entrySize
	contentLen := total + 8

	buf := make([]byte, 4+contentLen)
	binary.LittleEndian.PutUint32(buf[0:4], contentLen)

	off := uint32(4)
	for _, n := range notes {
		key := n.Nullifier
		if len(key) != int(keySize) {
			return 0, fmt.Errorf("walletcore: note key size %d, want %d", len(key), keySize)
		}
		copy(buf[off:], key)
		off += keySize

		value := n.Value
		if len(value) != int(itemSize) {
			return 0, fmt.Errorf("walletcore: note value size %d, want %d", len(value), itemSize)
		}
		copy(buf[off:], value)
		off += itemSize
	}

	if includeTrailingSize {
		// mirror map_owned: entries + trailing item count
		binary.LittleEndian.PutUint32(buf[off:], uint32(len(notes)))
		off += 4
	}

	return w.box(ctx, buf)
}

// packNoteValuesDataBuffer packs the note value bytes into a DataBuffer and
// returns the pointer to the 4-byte length header.
func (w *WalletCore) packNoteValuesDataBuffer(ctx context.Context, notes []driver.Note) (uint32, error) {
	total, entries, err := w.noteValuesContent(ctx, notes)
	if err != nil {
		return 0, err
	}
	buf := make([]byte, 4+total+8)
	binary.LittleEndian.PutUint32(buf[0:4], total+8)
	copy(buf[4:], entries)
	setLayout(buf[4+total:], uint32(len(notes)))
	return w.box(ctx, buf)
}

// noteValuesContent concatenates the note values (each itemSize long).
func (w *WalletCore) noteValuesContent(ctx context.Context, notes []driver.Note) (uint32, []byte, error) {
	itemSize := w.glob.ItemSize
	content := make([]byte, 0, len(notes)*int(itemSize))
	for _, n := range notes {
		if len(n.Value) != int(itemSize) {
			return 0, nil, fmt.Errorf("walletcore: note value size %d, want %d", len(n.Value), itemSize)
		}
		content = append(content, n.Value...)
	}
	return uint32(len(content)), content, nil
}

// packOpeningsDataBuffer packs the merkle openings into a DataBuffer.
func (w *WalletCore) packOpeningsDataBuffer(ctx context.Context, openings [][]byte) (uint32, error) {
	total := 0
	for _, o := range openings {
		total += len(o)
	}
	content := make([]byte, 0, total)
	for _, o := range openings {
		content = append(content, o...)
	}
	buf := make([]byte, 4+len(content)+8)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(content))+8)
	copy(buf[4:], content)
	setLayout(buf[4+len(content):], uint32(len(openings)))
	return w.box(ctx, buf)
}

// readPickedNotes reads a set of picked notes from a DataBuffer written in
// place by pick_notes. The buffer starts with a u32 length followed by
// key||value entries.
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

	count := len(raw) / int(entrySize)
	if len(raw) < 4 {
		count = 0
	}
	// The last 4 bytes may be the trailing length; use it as the count when
	// present.
	trailing := binary.LittleEndian.Uint32(raw[len(raw)-4:])
	if int(trailing) == count {
		count = int(trailing)
	}

	result := make([]driver.Note, 0, count)
	for i := 0; i < len(raw)-4 && len(result) < count; i += int(entrySize) {
		if i+int(entrySize) > len(raw) {
			break
		}
		key := make([]byte, keySize)
		copy(key, raw[i:])
		value := make([]byte, itemSize)
		copy(value, raw[i+int(keySize):])
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
