// Package tx implements the transaction builders of the w3sper SDK. The
// builders orchestrate note selection, gas, and the [driver] to produce signed
// (or provable) transactions.
//
// The cryptographic operations (key derivation, note picking, transaction
// signing, ZK proving) are delegated to a [driver.Driver]. In the reference
// JavaScript SDK this is the wallet-core WASM module; in Go it is a pluggable
// interface. The pure orchestration logic (chain id, gas, nonce resolution,
// stake validation) lives here.
package tx

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/dusk-network/w3sper-go/base58"
	"github.com/dusk-network/w3sper-go/chainid"
	"github.com/dusk-network/w3sper-go/driver"
	"github.com/dusk-network/w3sper-go/gas"
	"github.com/dusk-network/w3sper-go/network"
	"github.com/dusk-network/w3sper-go/profile"
)

// Canonical transfer contract id used on Dusk networks.
const TRANSFER = "0100000000000000000000000000000000000000000000000000000000000000"

// Canonical stake contract id used on Dusk networks.
const STAKE = "0200000000000000000000000000000000000000000000000000000000000000"

// ChainSource resolves the chain id for a transaction, either from an explicit
// value or from the network's node info.
type ChainSource interface {
	ChainID() (int, error)
}

// NetworkChainSource resolves the chain id from a Rusk node's info.
func NetworkChainSource(c *network.Client) ChainSource {
	return &networkChainSource{c: c}
}

type networkChainSource struct {
	c *network.Client
}

func (s *networkChainSource) ChainID() (int, error) {
	info, err := network.NewNode(s.c).Info(nil)
	if err != nil {
		return 0, err
	}
	return info.ChainID, nil
}

// Treasury abstracts the account/address balance and stake lookups used by the
// builders. A concrete treasury is backed by a synced node.
type Treasury interface {
	// AccountBalance returns the moonlight balance of an account key.
	AccountBalance(account []byte) (AccountBalance, error)
	// AddressNotes returns the owned notes of a phoenix address key.
	AddressNotes(address []byte) ([]driver.Note, error)
	// StakeInfo returns staking information for an account key.
	StakeInfo(account []byte) (StakeInfo, error)
}

// AccountBalance holds a moonlight account's nonce and value.
type AccountBalance struct {
	Nonce *big.Int
	Value *big.Int
}

// StakeInfo holds staking details.
type StakeInfo struct {
	Amount     *StakeAmount // nil if no active stake
	Reward     *big.Int
	Faults     int
	HardFaults int
}

// StakeAmount holds the staked amount details.
type StakeAmount struct {
	Value       *big.Int
	Locked      *big.Int
	Eligibility *big.Int
	Total       *big.Int
}

// BookEntry binds a profile to a treasury and driver. It is the counterpart of
// the JavaScript SDK's `BookEntry` (a profile-bound builder facade).
type BookEntry struct {
	profile  *profile.Profile
	treasury Treasury
	driver   driver.Driver
}

// NewBookEntry returns a book entry for the given profile.
func NewBookEntry(p *profile.Profile, treasury Treasury, d driver.Driver) *BookEntry {
	return &BookEntry{profile: p, treasury: treasury, driver: d}
}

// Profile returns the profile bound to this entry.
func (e *BookEntry) Profile() *profile.Profile { return e.profile }

// Treasury returns the treasury bound to this entry.
func (e *BookEntry) Treasury() Treasury { return e.treasury }

// Driver returns the driver bound to this entry.
func (e *BookEntry) Driver() driver.Driver { return e.driver }

// Builder carries the shared attributes of a transaction builder.
type Builder struct {
	entry *BookEntry
	chain ChainSource
	attrs attributes
}

type attributes struct {
	amount   *big.Int
	deposit  *big.Int
	gas      *gas.Config
	payload  interface{}
	chain    *int
	nonce    *big.Int
	receiver []byte
}

func newBuilder(e *BookEntry) *Builder {
	return &Builder{entry: e}
}

func (b *Builder) Entry() *BookEntry { return b.entry }

func (b *Builder) requireDriver() (driver.Driver, error) {
	if b.entry == nil || b.entry.driver == nil {
		return nil, driver.ErrDriverRequired
	}
	return b.entry.driver, nil
}

func (b *Builder) requireChain() (int, error) {
	if b.attrs.chain != nil {
		return chainid.Validate(*b.attrs.chain)
	}
	if b.chain != nil {
		return b.chain.ChainID()
	}
	return 0, fmt.Errorf("tx: chain id is required")
}

func (b *Builder) gas() *gas.Config {
	if b.attrs.gas != nil {
		return b.attrs.gas
	}
	return gas.New(nil, nil)
}

func (b *Builder) requireTreasury() (Treasury, error) {
	if b.entry == nil || b.entry.treasury == nil {
		return nil, fmt.Errorf("tx: a treasury is required")
	}
	return b.entry.treasury, nil
}

func (b *Builder) decodeReceiver(s string) ([]byte, error) {
	bytes, err := base58.Decode(s)
	if err != nil {
		return nil, fmt.Errorf("tx: invalid receiver: %w", err)
	}
	if len(bytes) == 0 {
		return nil, fmt.Errorf("tx: invalid receiver identifier")
	}
	return bytes, nil
}

func (b *Builder) profileIndex() uint64 {
	if i := b.entry.profile.Index(); i >= 0 {
		return uint64(i)
	}
	return 0
}

// profileSeed returns the 64-byte seed used for key derivation. It requires a
// real seed derived by a Generator; a profile parsed from public keys alone
// cannot sign and must not silently use its public account key as seed material.
func (b *Builder) profileSeed() ([]byte, error) {
	if b.entry == nil || b.entry.profile == nil {
		return nil, fmt.Errorf("tx: profile is required to sign")
	}
	if s := b.entry.profile.Seed(); len(s) > 0 {
		return s, nil
	}
	return nil, fmt.Errorf("tx: profile has no seed and cannot sign; derive it from a Generator")
}

// IsDriverRequired reports whether err is a missing-driver error.
func IsDriverRequired(err error) bool {
	return errors.Is(err, driver.ErrDriverRequired)
}
