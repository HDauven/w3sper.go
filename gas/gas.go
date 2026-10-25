// Package gas validates positive u64 transaction gas and total fees.
package gas

import (
	"fmt"
	"math/big"
)

const (
	DefaultLimit = 2_900_000_000
	DefaultPrice = 1
)

type Config struct{ Limit, Price, Total *big.Int }

// New defaults only omitted values. Invalid values are retained and rejected by
// Validate and all signing boundaries. Use NewChecked for immediate validation.
func New(limit, price *big.Int) *Config {
	if limit == nil {
		limit = big.NewInt(DefaultLimit)
	}
	if price == nil {
		price = big.NewInt(DefaultPrice)
	}
	l, p := new(big.Int).Set(limit), new(big.Int).Set(price)
	return &Config{Limit: l, Price: p, Total: new(big.Int).Mul(l, p)}
}
func NewChecked(limit, price *big.Int) (*Config, error) {
	c := New(limit, price)
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}
func (c *Config) Validate() error {
	if c == nil || c.Limit == nil || c.Price == nil || c.Limit.Sign() <= 0 || c.Price.Sign() <= 0 || !c.Limit.IsUint64() || !c.Price.IsUint64() {
		return fmt.Errorf("gas: limit and price must be positive u64 integers")
	}
	if !new(big.Int).Mul(c.Limit, c.Price).IsUint64() {
		return fmt.Errorf("gas: total fee exceeds u64")
	}
	return nil
}
func (c *Config) Clone() *Config {
	if c == nil {
		return New(nil, nil)
	}
	return New(c.Limit, c.Price)
}
