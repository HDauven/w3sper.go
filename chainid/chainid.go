// Package chainid validates Dusk chain identifiers.
package chainid

import "fmt"

// MaxChainID is the largest valid chain identifier.
const MaxChainID = 0xff

// Validate returns the validated chain id as an int. It rejects values outside
// the range [0, MaxChainID] and non-integer values.
func Validate(chainID any) (int, error) {
	switch v := chainID.(type) {
	case int:
		return inRange(int64(v))
	case int8:
		return inRange(int64(v))
	case int16:
		return inRange(int64(v))
	case int32:
		return inRange(int64(v))
	case int64:
		return inRange(v)
	case uint:
		return inRange(int64(v))
	case uint8:
		return inRange(int64(v))
	case uint16:
		return inRange(int64(v))
	case uint32:
		return inRange(int64(v))
	case uint64:
		if v > MaxChainID {
			return 0, fmt.Errorf("chain id must be between 0 and %d", MaxChainID)
		}
		return int(v), nil
	default:
		return 0, fmt.Errorf("chain id must be an integer, got %T", chainID)
	}
}

func inRange(v int64) (int, error) {
	if v < 0 || v > MaxChainID {
		return 0, fmt.Errorf("chain id must be between 0 and %d", MaxChainID)
	}
	return int(v), nil
}
