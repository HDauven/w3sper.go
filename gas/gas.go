// Package gas provides a gas configuration for transactions, mirroring the
// w3sper JavaScript SDK's `Gas` class.
package gas

import "math/big"

const (
	// DefaultLimit is used when a non-positive limit is provided.
	DefaultLimit = 2_900_000_000
	// DefaultPrice is used when a non-positive price is provided.
	DefaultPrice = 1
)

// Config holds the gas limit and price of a transaction. The total is derived
// as limit * price.
type Config struct {
	Limit *big.Int
	Price *big.Int
	Total *big.Int
}

// New returns a gas configuration. A nil or non-positive limit selects the
// [DefaultLimit] and likewise a nil or non-positive price selects the
// [DefaultPrice].
func New(limit, price *big.Int) *Config {
	l := maxPositive(limit)
	if l == nil {
		l = big.NewInt(DefaultLimit)
	}
	p := maxPositive(price)
	if p == nil {
		p = big.NewInt(DefaultPrice)
	}

	total := new(big.Int).Mul(l, p)

	return &Config{Limit: l, Price: p, Total: total}
}

func maxPositive(v *big.Int) *big.Int {
	if v == nil || v.Sign() <= 0 {
		return nil
	}
	return new(big.Int).Set(v)
}
