package bookkeeper

import (
	"context"
	"encoding/hex"
	"io"
	"sync"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/network"
	"github.com/HDauven/w3sper.go/profile"
	"github.com/HDauven/w3sper.go/tx"
)

// NetworkTreasury fetches current public state and retains a caller-owned note
// snapshot. Sync replaces notes only after a complete scan and spent check.
// It is not a durable database and does not reserve nonces or pending notes.
type NetworkTreasury struct {
	accounts  *network.AccountSyncer
	addresses *network.AddressSyncer
	mu        sync.RWMutex
	notes     map[string][]driver.Note
}

func NewNetworkTreasury(n *network.Client, d driver.Protocol) *NetworkTreasury {
	return &NetworkTreasury{accounts: network.NewAccountSyncer(n, d), addresses: network.NewAddressSyncer(n, d), notes: make(map[string][]driver.Note)}
}
func (t *NetworkTreasury) AccountBalanceContext(ctx context.Context, key []byte) (tx.AccountBalance, error) {
	return t.accounts.Balance(ctx, key)
}
func (t *NetworkTreasury) StakeInfoContext(ctx context.Context, key []byte) (tx.StakeInfo, error) {
	return t.accounts.Stake(ctx, key)
}
func (t *NetworkTreasury) StakeKeysContext(ctx context.Context, key []byte) (*network.StakeKeys, error) {
	out, e := t.accounts.StakeKeys(ctx, [][]byte{key})
	if e != nil {
		return nil, e
	}
	return out[0], nil
}
func (t *NetworkTreasury) AddressNotesContext(ctx context.Context, key []byte) ([]driver.Note, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return driver.CloneNotes(t.notes[string(key)]), nil
}
func (t *NetworkTreasury) SetNotes(address []byte, notes []driver.Note) {
	copy := driver.CloneNotes(notes)
	t.mu.Lock()
	t.notes[string(address)] = copy
	t.mu.Unlock()
}
func (t *NetworkTreasury) Sync(ctx context.Context, signers ...*profile.Signer) (driver.SyncInfo, error) {
	stream, e := t.addresses.Notes(ctx, signers, network.SyncOptions{})
	if e != nil {
		return driver.SyncInfo{}, e
	}
	defer stream.Close()
	owned := make([][]driver.Note, len(signers))
	var info driver.SyncInfo
	for {
		batch, e := stream.Next(ctx)
		if e == io.EOF {
			break
		}
		if e != nil {
			return info, e
		}
		info = batch.Info
		for i, notes := range batch.Owned {
			owned[i] = append(owned[i], notes...)
		}
	}
	var nullifiers [][]byte
	for _, notes := range owned {
		for _, n := range notes {
			nullifiers = append(nullifiers, n.Nullifier)
		}
	}
	spent, e := t.addresses.Spent(ctx, nullifiers)
	if e != nil {
		return info, e
	}
	set := make(map[string]bool)
	for _, n := range spent {
		set[hex.EncodeToString(n)] = true
	}
	for i, notes := range owned {
		filtered := notes[:0]
		for _, note := range notes {
			if !set[hex.EncodeToString(note.Nullifier)] {
				filtered = append(filtered, note)
			}
		}
		owned[i] = filtered
	}
	if e = ctx.Err(); e != nil {
		return info, e
	}
	for _, s := range signers {
		if e = s.Check(); e != nil {
			return info, e
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, s := range signers {
		t.notes[string(s.Address().Bytes())] = driver.CloneNotes(owned[i])
	}
	return info, nil
}
