// Package base16 provides lowercase hexadecimal encoding/decoding of byte
// slices, used for Dusk identifiers and hashes.
package base16

import (
	"encoding/hex"
	"fmt"
)

// Encode returns the lowercase hexadecimal representation of b.
func Encode(b []byte) string {
	return hex.EncodeToString(b)
}

// Decode parses a hexadecimal string (either case) into bytes.
func Decode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("base16: odd length string %q", s)
	}
	return hex.DecodeString(s)
}
