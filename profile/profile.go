// Package profile models Dusk profile keys: a pair of a Phoenix address key
// (64 bytes) and a Moonlight account key (96 bytes) derived from a single
// seed. The actual cryptographic derivation is performed by a [Driver].
package profile

import (
	"fmt"

	"github.com/HDauven/w3sper.go/base58"
)

const (
	// AddressKeyLen is the byte length of a Phoenix address key.
	AddressKeyLen = 64
	// AccountKeyLen is the byte length of a Moonlight account key.
	AccountKeyLen = 96
	// ProfileLen is the combined length of an address and account key.
	ProfileLen = AddressKeyLen + AccountKeyLen
	// MaxProfileIndex is the largest profile index wallet-core supports. Its
	// FFI takes the index as a u8.
	MaxProfileIndex = int64(255)
)

// ProfileIndex is a profile derivation index as wallet-core's FFI accepts it
// (u8).
type ProfileIndex uint8

// Driver performs the cryptographic derivation of a profile from a seed and
// index. It mirrors the wallet-core WASM `generate_profile` export.
type Driver interface {
	GenerateProfile(seed []byte, index ProfileIndex) ([]byte, error)
}

// Generator is a deterministic profile factory backed by a seed provider. It
// caches profiles by their derivation index, so calling Next() before Default()
// does not misplace the index-0 profile.
// sourceToken is a non-zero-size identity token marking a generator (and the
// profiles it derives) as belonging to one seed source. Zero-size struct
// pointers may compare equal for distinct allocations in Go, so a non-zero-size
// token guarantees per-generator pointer identity.
type sourceToken struct{ _ int }

type Generator struct {
	seeder    func() ([]byte, error)
	generator Driver
	profiles  map[int64]*Profile
	nextIndex int64 // monotonic; starts at 1, index 0 is reserved for Default
	source    *sourceToken
}

// NewGenerator returns a profile generator that derives profiles from seeds
// produced by seeder and index n, using driver for cryptographic derivation.
func NewGenerator(seeder func() ([]byte, error), driver Driver) *Generator {
	return &Generator{
		seeder:    seeder,
		generator: driver,
		profiles:  make(map[int64]*Profile),
		nextIndex: 1,
		source:    &sourceToken{},
	}
}

// sameSourceOf reports whether p was derived by this generator (same seed
// source). Profile source identity is tested with a comparable pointer.
func (g *Generator) sameSourceOf(p *Profile) bool {
	return p != nil && p.source == g.source
}

// Key is a single Dusk key (address or account) with a Base58 string form.
type Key struct {
	buf []byte
}

func (k *Key) String() string {
	return base58.Encode(k.buf)
}

// Bytes returns a copy of the key bytes.
func (k *Key) Bytes() []byte {
	out := make([]byte, len(k.buf))
	copy(out, k.buf)
	return out
}

// Profile is a pair of Dusk account and Phoenix address keys derived from the
// same seed.
type Profile struct {
	buf     []byte
	address *Key
	account *Key
	index   int64 // -1 until generated through a Generator
	seed    []byte
	source  *sourceToken
}

// newProfile builds a Profile from a 160-byte buffer.
func newProfile(buf []byte) (*Profile, error) {
	if len(buf) != ProfileLen {
		return nil, fmt.Errorf("profile: expected %d bytes, got %d", ProfileLen, len(buf))
	}
	p := &Profile{
		buf:     make([]byte, ProfileLen),
		index:   -1,
		address: &Key{},
		account: &Key{},
	}
	copy(p.buf, buf)
	p.address.buf = p.buf[:AddressKeyLen]
	p.account.buf = p.buf[AddressKeyLen:]
	return p, nil
}

// Address returns the Phoenix address key.
func (p *Profile) Address() *Key { return p.address }

// Account returns the Moonlight account key.
func (p *Profile) Account() *Key { return p.account }

// Bytes returns a copy of the full profile buffer (address || account).
func (p *Profile) Bytes() []byte {
	out := make([]byte, len(p.buf))
	copy(out, p.buf)
	return out
}

// Index returns the profile's index within its generator, or -1 if it was not
// produced by a generator.
func (p *Profile) Index() int64 { return p.index }

// Seed returns a copy of the seed this profile was derived from, or nil if it
// was not derived by a [Generator].
func (p *Profile) Seed() []byte {
	if len(p.seed) == 0 {
		return nil
	}
	out := make([]byte, len(p.seed))
	copy(out, p.seed)
	return out
}

// Next derives and returns the next profile, advancing a monotonic index that
// skips the default (index 0) profile until it has been generated.
func (g *Generator) Next() (*Profile, error) {
	if g.nextIndex > MaxProfileIndex {
		return nil, fmt.Errorf("profile: cannot derive beyond index %d (wallet-core u8 index space)", MaxProfileIndex)
	}
	p, err := g.nth(g.nextIndex)
	if err != nil {
		return nil, err
	}
	g.nextIndex++
	return p, nil
}

// Default returns (and caches) the default profile at index 0.
func (g *Generator) Default() (*Profile, error) {
	return g.nth(0)
}

// At returns the profile previously generated at index, or nil.
func (g *Generator) At(index int64) *Profile {
	return g.profiles[index]
}

// IndexOf returns the index of the profile, or -1 if it is unknown or was
// derived by a different source.
func (g *Generator) IndexOf(p *Profile) int64 {
	if !g.sameSourceOf(p) {
		return -1
	}
	return p.index
}

// Len returns the number of profiles generated so far.
func (g *Generator) Len() int { return len(g.profiles) }

// nth derives and caches the profile at index n (idempotent).
func (g *Generator) nth(n int64) (*Profile, error) {
	if !validIndex(n) {
		return nil, fmt.Errorf("profile: index %d out of range [0, %d]", n, MaxProfileIndex)
	}
	if cached := g.profiles[n]; cached != nil {
		return cached, nil
	}

	if g.generator == nil {
		return nil, fmt.Errorf("profile: a driver is required to generate profiles")
	}

	seed, err := g.seeder()
	if err != nil {
		return nil, err
	}
	buf, err := g.generator.GenerateProfile(seed, ProfileIndex(n))
	if err != nil {
		return nil, err
	}
	p, err := newProfile(buf)
	if err != nil {
		return nil, err
	}
	p.index = n
	p.seed = append([]byte(nil), seed...)
	p.source = g.source
	g.profiles[n] = p
	return p, nil
}

// validIndex reports whether n is within wallet-core's u8 index space.
func validIndex(n int64) bool {
	return n >= 0 && n <= MaxProfileIndex
}

// Parse builds a Profile from a 160-byte buffer (address || account).
func Parse(buf []byte) (*Profile, error) {
	return newProfile(buf)
}

// EncodeAccount returns the Base58 account key form of a 96-byte key.
func EncodeAccount(b []byte) string {
	return base58.Encode(b)
}

// DecodeKey decodes a Base58 key into its raw bytes.
func DecodeKey(s string) ([]byte, error) {
	return base58.Decode(s)
}

// TypeOf returns the key type described by a Base58 value: "address" for
// 64-byte keys, "account" for 96-byte keys, or "undefined" otherwise.
func TypeOf(value string) string {
	bytes, err := base58.Decode(value)
	if err != nil {
		return "undefined"
	}
	switch len(bytes) {
	case AddressKeyLen:
		return "address"
	case AccountKeyLen:
		return "account"
	default:
		return "undefined"
	}
}
