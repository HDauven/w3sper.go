// Package bookkeeper provides wallet-orchestration helpers over a synced
// treasury snapshot, mirroring the w3sper JavaScript SDK's `Bookkeeper`. It
// binds a [tx.BookEntry] to a profile and exposes balance, stake and
// transaction-builder helpers.
package bookkeeper

import (
	"fmt"
	"math/big"

	"github.com/dusk-network/w3sper-go/driver"
	"github.com/dusk-network/w3sper-go/profile"
	"github.com/dusk-network/w3sper-go/tx"
)

// Treasury is the same interface consumed by the tx builders.
type Treasury = tx.Treasury

// ErrPhoenixUnavailable is returned for shielded (Phoenix) operations that are
// not supported by this Moonlight-only build.
var ErrPhoenixUnavailable = fmt.Errorf("bookkeeper: phoenix balance is unavailable in this Moonlight-only build")

// Bookkeeper is a profile-bound wallet facade over a treasury.
type Bookkeeper struct {
	treasury Treasury
	driver   driver.Driver
}

// New returns a bookkeeper over the given treasury and driver.
func New(treasury Treasury, d driver.Driver) *Bookkeeper {
	return &Bookkeeper{treasury: treasury, driver: d}
}

// Treasury returns the underlying treasury.
func (b *Bookkeeper) Treasury() Treasury { return b.treasury }

// Balance returns the moonlight balance of an account, or the phoenix balance
// of an address, based on the identifier type.
func (b *Bookkeeper) Balance(identifier string) (balance, error) {
	key, err := decodeKey(identifier)
	if err != nil {
		return balance{}, err
	}
	switch profile.TypeOf(identifier) {
	case "account":
		ab, err := b.accountBalance(key)
		if err != nil {
			return balance{}, err
		}
		return balance{Account: &ab}, nil
	case "address":
		// A Phoenix address balance requires the owner's seed and derivation
		// index; an address string alone is insufficient. Shielded support is out
		// of scope for this Moonlight-only build.
		return balance{}, ErrPhoenixUnavailable
	default:
		return balance{}, fmt.Errorf("bookkeeper: invalid identifier")
	}
}

// balance is a discriminated account/address balance.
type balance struct {
	Account *tx.AccountBalance
	Address *AddressBalance
}

// AddressBalance splits a phoenix balance into total and spendable values.
type AddressBalance struct {
	Value     *big.Int
	Spendable *big.Int
}

// Account returns the account balance, or nil for an address balance.
func (b balance) AccountBalance() *tx.AccountBalance { return b.Account }

// GetAddress returns the address balance, or nil for an account balance.
func (b balance) GetAddress() *AddressBalance { return b.Address }

// MinimumStake returns the minimum stake amount allowed on the network.
func (b *Bookkeeper) MinimumStake() (*big.Int, error) {
	if b.driver == nil {
		return nil, driver.ErrDriverRequired
	}
	return b.driver.MinimumStake()
}

// As returns a tx.BookEntry bound to the given profile.
func (b *Bookkeeper) As(p *profile.Profile) (*tx.BookEntry, error) {
	if p == nil {
		return nil, fmt.Errorf("bookkeeper: profile is required")
	}
	return tx.NewBookEntry(p, b.treasury, b.driver), nil
}

func (b *Bookkeeper) accountBalance(key []byte) (tx.AccountBalance, error) {
	return b.treasury.AccountBalance(key)
}

// addressBalance is not supported without an owner seed/index; see
// ErrPhoenixUnavailable.
func (b *Bookkeeper) addressBalance(key []byte) (*AddressBalance, error) {
	return nil, ErrPhoenixUnavailable
}

func decodeKey(identifier string) ([]byte, error) {
	if profile.TypeOf(identifier) == "undefined" {
		return nil, fmt.Errorf("bookkeeper: invalid identifier")
	}
	return profile.DecodeKey(identifier)
}
