// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package profile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/HDauven/w3sper.go/internal/capability"
)

var ErrDisposed = errors.New("profile: signer has been disposed")
var ErrSignerRequired = errors.New("profile: a key-backed Signer is required")

// KeyDriver implements current wallet-core issuance and exact-byte BLS signing.
type KeyDriver interface {
	GenerateProfileKeys(context.Context, []byte, ProfileIndex) ([]byte, error)
	SignBLSMessage(context.Context, *Signer, []byte) ([]byte, error)
}

// Signer owns selected child keys, never a seed. Public identity remains readable
// after Dispose. Dispose rejects new operations and discards stale results; it
// cannot revoke signatures or requests already sent. Always dispose on wallet lock.
type Signer struct {
	profile *Profile
	driver  KeyDriver
	mu      sync.Mutex
	keys    []byte
	ctx     context.Context
	cancel  context.CancelFunc
}

func (g *Generator) Signer(ctx context.Context, p *Profile) (*Signer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e := g.lock(ctx); e != nil {
		return nil, e
	}
	defer g.unlock()
	if !g.sameSourceOf(p) {
		return nil, fmt.Errorf("profile: identity belongs to another generator")
	}
	d, ok := g.generator.(KeyDriver)
	if !ok {
		return nil, fmt.Errorf("profile: driver does not support key issuance")
	}
	if g.seeder == nil {
		return nil, fmt.Errorf("profile: seed provider required")
	}
	seed, err := g.seeder(ctx)
	if err != nil {
		return nil, err
	}
	seed = append([]byte(nil), seed...)
	defer clear(seed)
	if len(seed) != 64 {
		return nil, fmt.Errorf("profile: seed must contain exactly 64 bytes")
	}
	material, err := d.GenerateProfileKeys(ctx, seed, ProfileIndex(p.index))
	defer clear(material)
	if err != nil {
		return nil, err
	}
	if len(material) != 320 || !bytes.Equal(material[160:], p.buf) {
		return nil, fmt.Errorf("profile: provider seed no longer matches captured identity")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(context.Background())
	return &Signer{profile: p, driver: d, keys: append([]byte(nil), material[:160]...), ctx: life, cancel: cancel}, nil
}
func (g *Generator) DefaultSigner(ctx context.Context) (*Signer, error) {
	p, err := g.DeriveContext(ctx, 0)
	if err != nil {
		return nil, err
	}
	return g.Signer(ctx, p)
}
func (g *Generator) NextSigner(ctx context.Context) (*Signer, error) {
	p, err := g.NextContext(ctx)
	if err != nil {
		return nil, err
	}
	return g.Signer(ctx, p)
}
func (s *Signer) Profile() *Profile {
	if s == nil {
		return nil
	}
	return s.profile
}
func (s *Signer) Account() *Key { return s.profile.Account() }
func (s *Signer) Address() *Key { return s.profile.Address() }
func (s *Signer) SameSourceOf(other *Signer) bool {
	return s != nil && other != nil && s.profile.SameSourceOf(other.profile)
}
func (s *Signer) Check() error {
	if s == nil || s.ctx == nil {
		return ErrSignerRequired
	}
	if s.ctx.Err() != nil {
		return ErrDisposed
	}
	return nil
}

// OperationContext combines caller cancellation with signer disposal. Call cancel
// when finished; disposal never cancels unrelated callers or the driver runtime.
func (s *Signer) OperationContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if err := s.Check(); err != nil {
		return nil, nil, err
	}
	op, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	return op, func() { stop(); cancel() }, nil
}
func (s *Signer) Dispose() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	clear(s.keys)
	s.keys = nil
}
func (s *Signer) SignBLSMessage(ctx context.Context, message []byte) ([]byte, error) {
	if err := s.Check(); err != nil {
		return nil, err
	}
	message = append([]byte(nil), message...)
	defer clear(message)
	return s.driver.SignBLSMessage(ctx, s, message)
}

// UseKeys is sealed SDK-internal access. Each operation receives an owned,
// short-lived snapshot, wiped on return. The token cannot be issued by clients.
func (s *Signer) UseKeys(ctx context.Context, token capability.Token, kind capability.Kind, fn func(context.Context, []byte) error) error {
	if !token.Valid() {
		return ErrSignerRequired
	}
	if err := s.Check(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if err := s.Check(); err != nil {
		s.mu.Unlock()
		return err
	}
	start, end := kind.Range()
	if end == 0 {
		s.mu.Unlock()
		return errors.New("profile: invalid key selection")
	}
	snapshot := append([]byte(nil), s.keys[start:end]...)
	s.mu.Unlock()
	defer clear(snapshot)
	op, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	defer func() { stop(); cancel() }()
	err := fn(op, snapshot)
	if e := s.Check(); e != nil {
		return e
	}
	if e := op.Err(); e != nil {
		return e
	}
	return err
}
