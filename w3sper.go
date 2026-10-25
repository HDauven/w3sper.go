// Package w3sper exposes a Go SDK for Dusk, pinned to W3sper.js 97f1cf26.
// It supports Moonlight/Phoenix transactions, staking, deployment, committed
// calls, RUES events, synchronization and contract data drivers. Cryptography
// runs in the bundled wallet-core WASM using wazero; Phoenix proofs come from
// the node prover. Public Profiles cannot sign: explicitly issue and dispose a
// profile.Signer. See the subpackages for the context-aware APIs and README.md
// for ownership, cancellation and acceptance-test limits.
package w3sper

import (
	"github.com/HDauven/w3sper.go/base16"
	"github.com/HDauven/w3sper.go/base58"
	"github.com/HDauven/w3sper.go/bookkeeper"
	"github.com/HDauven/w3sper.go/bookmark"
	"github.com/HDauven/w3sper.go/chainid"
	"github.com/HDauven/w3sper.go/contract"
	"github.com/HDauven/w3sper.go/datadriver"
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
	Driver             = driver.Driver
	Protocol           = driver.Protocol
	Signer             = profile.Signer
	ProfileGenerator   = profile.Generator
	Contract           = contract.Contract
	DataDriverRegistry = datadriver.Registry
	Bookkeeper         = bookkeeper.Bookkeeper
	TransactionResult  = tx.Result
	// DriverRequiredError indicates a missing protocol driver.
	DriverRequiredError = driver.DriverRequiredError
	// Gas is a gas configuration.
	Gas = gas.Config
	// Bookmark is an 8-byte sync cursor.
	Bookmark = bookmark.Bookmark
	// Profile is an address/account keypair.
	Profile = profile.Profile
	// NetworkClient is a Rusk HTTP/GraphQL/RUES client.
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

// NewNetwork returns an HTTP/GraphQL/RUES client for a Rusk node URL.
func NewNetwork(url string, opts ...network.Option) (*network.Client, error) {
	return network.New(url, opts...)
}

// NewNetworkNode returns a node component helper for the given network client.
func NewNetworkNode(c *network.Client) *network.Node {
	return network.NewNode(c)
}
