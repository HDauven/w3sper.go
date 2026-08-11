// Package bookmark provides an opaque 8-byte cursor used to page note leaves
// from the transfer contract.
package bookmark

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
)

// Size is the fixed byte length of a bookmark.
const Size = 8

// Bookmark is an opaque 8-byte cursor. A bookmark whose bytes are all 0xff is
// the "none" bookmark (end of sync).
type Bookmark struct {
	data [Size]byte
}

// New returns a bookmark wrapping data, which must be exactly 8 bytes.
func New(data []byte) (*Bookmark, error) {
	if len(data) != Size {
		return nil, fmt.Errorf("bookmark: must be %d bytes, got %d", Size, len(data))
	}
	var b Bookmark
	copy(b.data[:], data)
	return &b, nil
}

// From converts a source into a bookmark. Supported sources are a *Bookmark,
// a *big.Int, an int, or raw bytes.
func From(source any) (*Bookmark, error) {
	switch v := source.(type) {
	case *Bookmark:
		return v.clone(), nil
	case *big.Int:
		return fromUint(v)
	case int:
		return fromUint(big.NewInt(int64(v)))
	case int64:
		return fromUint(big.NewInt(v))
	case uint64:
		return fromUint(new(big.Int).SetUint64(v))
	case []byte:
		return New(v)
	case [Size]byte:
		return New(v[:])
	default:
		return nil, fmt.Errorf("bookmark: unsupported source type %T", source)
	}
}

func fromUint(v *big.Int) (*Bookmark, error) {
	var buf [Size]byte
	binary.LittleEndian.PutUint64(buf[:], v.Uint64())
	return New(buf[:])
}

func (b *Bookmark) clone() *Bookmark {
	c := *b
	return &c
}

// Data returns a copy of the underlying 8 bytes.
func (b *Bookmark) Data() []byte {
	out := make([]byte, Size)
	copy(out, b.data[:])
	return out
}

// AsUint returns the bookmark interpreted as a little-endian unsigned integer.
func (b *Bookmark) AsUint() *big.Int {
	return new(big.Int).SetUint64(binary.LittleEndian.Uint64(b.data[:]))
}

// String returns the lowercase hexadecimal representation.
func (b *Bookmark) String() string {
	return hex.EncodeToString(b.data[:])
}

// IsNone reports whether every byte is 0xff (the "none" cursor).
func (b *Bookmark) IsNone() bool {
	for _, c := range b.data {
		if c != 0xff {
			return false
		}
	}
	return true
}
