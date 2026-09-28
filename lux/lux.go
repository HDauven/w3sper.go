// Package lux provides conversion between the integer Lux representation of
// Dusk amounts and the human-readable Dusk decimal format.
//
// Lux values are stored as unsigned 64-bit integers on-chain and cannot be
// negative. The Dusk format represents a Lux value as a decimal number with a
// fixed precision of 9 decimal places. This package mirrors the behaviour of
// the w3sper JavaScript SDK's `lux` module.
package lux

import (
	"fmt"
	"math/big"
)

// DECIMALS is the number of fractional digits used by the Dusk format.
const DECIMALS = 9

var scaleFactor = pow10Big(DECIMALS)

func pow10Big(n int) *big.Int {
	v := big.NewInt(1)
	for i := 0; i < n; i++ {
		v.Mul(v, big.NewInt(10))
	}
	return v
}

// FormatToDusk converts a Lux amount into its human-readable Dusk decimal
// representation. Trailing zeros are omitted and a whole amount is returned
// without a fractional part.
func FormatToDusk(lux *big.Int) (string, error) {
	if lux == nil || !lux.IsUint64() {
		return "", fmt.Errorf("lux: cannot format negative value %s", lux)
	}

	unit := new(big.Int).Div(lux, scaleFactor)
	decimals := new(big.Int).Mod(lux, scaleFactor)

	if decimals.Sign() == 0 {
		return unit.String(), nil
	}

	frac := decimals.String()
	frac = padLeft(frac, DECIMALS, '0')
	frac = trimRight(frac, '0')

	return fmt.Sprintf("%s.%s", unit, frac), nil
}

// ParseFromDusk parses a Dusk decimal string into its Lux representation.
// The string must describe a non-negative number with at most [DECIMALS]
// fractional digits.
func ParseFromDusk(dusk string) (*big.Int, error) {
	units, frac, ok := splitDecimal(dusk)
	if !ok {
		return nil, fmt.Errorf("lux: cannot parse %q as a Lux value", dusk)
	}

	if len(frac) > DECIMALS {
		return nil, fmt.Errorf(
			"lux: cannot parse %q as a Lux value: amounts support at most %d fractional digits",
			dusk,
			DECIMALS,
		)
	}

	unitsBig, ok := new(big.Int).SetString(units, 10)
	if !ok {
		return nil, fmt.Errorf("lux: cannot parse %q as a Lux value", dusk)
	}

	frac = padRight(frac, DECIMALS, '0')
	fracBig, ok := new(big.Int).SetString(frac, 10)
	if !ok {
		return nil, fmt.Errorf("lux: cannot parse %q as a Lux value", dusk)
	}

	result := new(big.Int).Mul(unitsBig, scaleFactor)
	result.Add(result, fracBig)

	if !result.IsUint64() {
		return nil, fmt.Errorf("lux: value exceeds u64")
	}
	return result, nil
}

func splitDecimal(s string) (units, frac string, ok bool) {
	if s == "" {
		return "", "", false
	}

	dot := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			if dot != -1 {
				return "", "", false
			}
			dot = i
			continue
		}
		if c < '0' || c > '9' {
			return "", "", false
		}
	}

	if dot == -1 {
		return s, "0", true
	}

	if dot == 0 || dot == len(s)-1 {
		return "", "", false
	}
	return s[:dot], s[dot+1:], true
}

func padLeft(s string, n int, pad byte) string {
	if len(s) >= n {
		return s
	}
	buf := make([]byte, n-len(s))
	for i := range buf {
		buf[i] = pad
	}
	return string(buf) + s
}

func padRight(s string, n int, pad byte) string {
	if len(s) >= n {
		return s
	}
	buf := make([]byte, n-len(s))
	for i := range buf {
		buf[i] = pad
	}
	return s + string(buf)
}

func trimRight(s string, trim byte) string {
	i := len(s)
	for i > 0 && s[i-1] == trim {
		i--
	}
	return s[:i]
}
