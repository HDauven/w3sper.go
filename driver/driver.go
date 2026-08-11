// Package driver defines the protocol/crypto boundary of the w3sper Go
// library. In the reference JavaScript SDK this boundary is implemented by the
// `wallet-core` WASM module. Reimplementing the full BLS12-381 / Poseidon / ZK
// Phoenix stack in Go is a large, separate effort; this interface documents the
// exact operations a concrete driver must provide so the rest of the library
// can be exercised end-to-end.
package driver

import "math/big"

// Note is a raw phoneix note entry (nullifier key : note value) as returned by
// the treasury, keyed by nullifier.
type Note struct {
	// Nullifier is the 32-byte nullifier key of the note.
	Nullifier []byte
	// Value is the opaque note value bytes.
	Value []byte
}

// MoonlightInput describes an account (moonlight) transaction.
type MoonlightInput struct {
	Seed          []byte
	SenderIndex   uint64
	Receiver      []byte // nil for contract deployment
	TransferValue *big.Int
	Deposit       *big.Int
	GasLimit      *big.Int
	GasPrice      *big.Int
	Nonce         *big.Int
	ChainID       int
	Data          []byte // serialized rkyv payload, or nil
}

// PhoenixInput describes a phoenix (shielded) transaction that must be proven.
type PhoenixInput struct {
	Seed        []byte
	SenderIndex uint64
	Receiver    []byte
	Inputs      []Note
	Openings    [][]byte
	Root        []byte
	Value       *big.Int // transfer or allocate value
	GasLimit    *big.Int
	GasPrice    *big.Int
	ChainID     int
	Data        []byte // serialized rkyv payload, or nil
}

// Driver performs the cryptographic protocol operations.
type Driver interface {
	// GenerateProfile derives a 160-byte profile buffer (address || account)
	// from seed and index n.
	GenerateProfile(seed []byte, index uint64) ([]byte, error)

	// Balance computes {value, spendable} for an address from its notes.
	Balance(seed []byte, index uint64, notes []Note) (*big.Int, *big.Int, error)

	// PickNotes selects notes spending up to value for an owner.
	PickNotes(seed []byte, ownerIndex uint64, notes []Note, value *big.Int) ([]Note, error)

	// Moonlight builds and signs an account transaction, returning the serialized
	// transaction bytes and its hash.
	Moonlight(in MoonlightInput) (tx []byte, hash string, err error)

	// Phoenix builds an unproven phoenix transaction and its circuits.
	Phoenix(in PhoenixInput) (tx []byte, circuits []byte, err error)

	// IntoProven attaches a proof to an unproven transaction.
	IntoProven(tx, proof []byte) ([]byte, string, error)

	// MinimumStake returns the minimum stake amount allowed on the network.
	MinimumStake() (*big.Int, error)
}

// ErrDriverRequired is returned when an operation requires a [Driver] that has
// not been provided.
var ErrDriverRequired = &DriverRequiredError{}

// DriverRequiredError indicates a missing protocol driver.
type DriverRequiredError struct{}

func (e *DriverRequiredError) Error() string {
	return "w3sper: protocol driver required; no wallet-core Driver is configured"
}
