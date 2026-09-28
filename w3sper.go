// Package w3sper is a Go port of the Dusk Network w3sper SDK. It provides the
// pure, deterministic layers of the SDK — Lux/Dusk conversion, gas, chain id
// validation, Base16/Base58 encoding, bookmarks, profiles, a GraphQL network
// client, and transaction builders — plus a pluggable [driver.Driver] that
// defines the cryptographic protocol boundary (key derivation, note picking,
// transaction signing, ZK proving).
//
// In the reference JavaScript SDK the cryptographic operations are performed
// by the wallet-core WASM module. This Go port keeps that boundary behind the
// driver interface so the rest of the library can be exercised end-to-end with
// a concrete driver.
package w3sper

import (
	"github.com/HDauven/w3sper.go/base16"
	"github.com/HDauven/w3sper.go/base58"
	"github.com/HDauven/w3sper.go/bookmark"
	"github.com/HDauven/w3sper.go/chainid"
	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/gas"
	"github.com/HDauven/w3sper.go/lux"
	"github.com/HDauven/w3sper.go/network"
	"github.com/HDauven/w3sper.go/profile"
	"github.com/HDauven/w3sper.go/tx"
	"math/big"
)

// Package aliases re-exported for convenience.
const (
	// TRANSFER is the canonical transfer contract id on Dusk networks.
	TRANSFER = tx.TRANSFER
	// STAKE is the canonical stake contract id on Dusk networks.
	STAKE = tx.STAKE
)

// Network constants.
const (
	LOCALNET = network.LOCALNET
	MAINNET  = network.MAINNET
	TESTNET  = network.TESTNET
	DEVNET   = network.DEVNET
)

// Lux providers.
var (
	// FormatToDusk converts a Lux amount into its Dusk decimal string.
	FormatToDusk = lux.FormatToDusk
	// ParseFromDusk parses a Dusk decimal string into Lux.
	ParseFromDusk = lux.ParseFromDusk
)

// Encoders.
var (
	// Base16Encode encodes bytes as lowercase hex.
	Base16Encode = base16.Encode
	// Base58Encode encodes bytes as Base58.
	Base58Encode = base58.Encode
)

// Re-exported types.
type (
	// Driver is the cryptographic protocol boundary.
	Driver = driver.Driver
	// DriverRequiredError indicates a missing protocol driver.
	DriverRequiredError = driver.DriverRequiredError
	// Gas is a gas configuration.
	Gas = gas.Config
	// Bookmark is an 8-byte sync cursor.
	Bookmark = bookmark.Bookmark
	// Profile is an address/account keypair.
	Profile = profile.Profile
	// NetworkClient is a GraphQL-over-HTTP Rusk client.
	NetworkClient = network.Client
	// BookEntry is a profile-bound transaction facade.
	BookEntry = tx.BookEntry
	// AccountBalance is a moonlight balance.
	AccountBalance = tx.AccountBalance
	// StakeInfo is staking information.
	StakeInfo = tx.StakeInfo
	// NonceResult is the result of an account transaction.
	NonceResult = tx.NonceResult
	// NullifierResult is the result of a phoenix transaction.
	NullifierResult = tx.NullifierResult
)

// Drivers.
var (
	// ErrDriverRequired is returned when a driver is required but missing.
	ErrDriverRequired = driver.ErrDriverRequired
	// IsDriverRequired reports whether err is a missing-driver error.
	IsDriverRequired = tx.IsDriverRequired
)

// Chain id validation.
var (
	// ValidateChainID validates a Dusk chain identifier.
	ValidateChainID = chainid.Validate
)

// NewGas returns a gas configuration.
func NewGas(limit, price *big.Int) *gas.Config { return gas.New(limit, price) }

// NewNetwork returns a GraphQL network client for a Rusk node URL.
func NewNetwork(url string, opts ...network.Option) (*network.Client, error) {
	return network.New(url, opts...)
}

// NewNetworkNode returns a node component helper for the given network client.
func NewNetworkNode(c *network.Client) *network.Node {
	return network.NewNode(c)
}
