// Package base58 implements the Bitcoin alphabet Base58 encoding used by Dusk
// for public account and address keys. It mirrors the w3sper JavaScript SDK's
// `encoders/b58` module (no checksum).
package base58

import (
	"fmt"
	"math/big"
)

const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var alphabetIdx [256]int8

func init() {
	for i := range alphabetIdx {
		alphabetIdx[i] = -1
	}
	for i := 0; i < len(alphabet); i++ {
		alphabetIdx[alphabet[i]] = int8(i)
	}
}

// Encode returns the Base58 encoding of b.
func Encode(b []byte) string {
	if len(b) == 0 {
		return ""
	}

	// Count the number of leading zero bytes.
	zeros := 0
	for zeros < len(b) && b[zeros] == 0 {
		zeros++
	}

	// Convert the input to a big integer.
	x := new(big.Int).SetBytes(b)

	radix := big.NewInt(58)
	mod := new(big.Int)
	out := make([]byte, 0, len(b)*138/100+1)

	for x.Sign() > 0 {
		x.DivMod(x, radix, mod)
		out = append(out, alphabet[mod.Int64()])
	}

	// Leading zero bytes map to '1'.
	for i := 0; i < zeros; i++ {
		out = append(out, '1')
	}

	// Reverse.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}

	return string(out)
}

// Decode parses a Base58 string into bytes. It returns an error on invalid
// characters.
func Decode(s string) ([]byte, error) {
	if len(s) == 0 {
		return []byte{}, nil
	}

	// Count leading '1's.
	zeros := 0
	for zeros < len(s) && s[zeros] == '1' {
		zeros++
	}

	x := new(big.Int)
	radix := big.NewInt(58)

	for i := 0; i < len(s); i++ {
		b := alphabetIdx[s[i]]
		if b < 0 {
			return nil, fmt.Errorf("base58: invalid character %q", s[i])
		}
		x.Mul(x, radix)
		x.Add(x, big.NewInt(int64(b)))
	}

	out := x.Bytes()
	if zeros > 0 {
		out = append(make([]byte, zeros), out...)
	}

	return out, nil
}
