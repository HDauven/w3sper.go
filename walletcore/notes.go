// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package walletcore

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/internal/capability"
	"github.com/HDauven/w3sper.go/profile"
)

func (w *WalletCore) NoteItemSize() uint32 { return w.glob.ItemSize }

func (w *WalletCore) BalanceFor(ctx context.Context, s *profile.Signer, notes []driver.Note) (value, spendable uint64, err error) {
	notes = driver.CloneNotes(notes)
	err = s.UseKeys(ctx, capability.Grant(), capability.View, func(ctx context.Context, key []byte) error {
		return w.operation(ctx, func(a *arena) error {
			b, n, e := w.noteValuesContent(ctx, notes)
			if e != nil {
				return e
			}
			k, p, out := a.box(key), a.box(newDataBuffer(b, n)), a.space(16)
			if e = a.call("balance_with_key", k, p, out); e != nil {
				return e
			}
			b, e = a.read(out, 16)
			if e != nil {
				return e
			}
			value = leUint64(b)
			spendable = leUint64(b[8:])
			return nil
		})
	})
	if err != nil {
		value, spendable = 0, 0
	}
	return
}
func (w *WalletCore) PickFor(ctx context.Context, s *profile.Signer, notes []driver.Note, value uint64) (out []driver.Note, err error) {
	notes = driver.CloneNotes(notes)
	err = s.UseKeys(ctx, capability.Grant(), capability.View, func(ctx context.Context, key []byte) error {
		return w.operation(ctx, func(a *arena) error {
			b, n, e := w.noteEntriesContent(ctx, notes)
			if e != nil {
				return e
			}
			k, p := a.box(key), a.box(newDataBuffer(b, n))
			if e = a.call("pick_notes_with_key", k, a.u64(value), p); e != nil {
				return e
			}
			length, e := w.readU32(ctx, uint32(p))
			if e != nil {
				return e
			}
			if length > uint32(len(b)+8) {
				return fmt.Errorf("walletcore: oversized picked note output")
			}
			b, e = a.read(p+4, length)
			if e != nil {
				return e
			}
			out, e = w.parseNoteList(b, len(b)-8)
			return e
		})
	})
	if err != nil {
		out = nil
	}
	return
}

// vector resolves an archived RKYV Vec footer using its signed relative pointer.
func vector(b []byte, at, itemSize int) (start, count int, err error) {
	if at < 0 || at > len(b)-8 || itemSize <= 0 {
		return 0, 0, fmt.Errorf("walletcore: invalid vector footer")
	}
	offset := int64(int32(binary.LittleEndian.Uint32(b[at:])))
	n := int64(binary.LittleEndian.Uint32(b[at+4:]))
	p := int64(at) + offset
	if p < 0 || p > int64(len(b)) || n > (int64(len(b))-p)/int64(itemSize) {
		return 0, 0, fmt.Errorf("walletcore: vector outside archive")
	}
	return int(p), int(n), nil
}
func (w *WalletCore) parseNoteList(b []byte, at int) ([]driver.Note, error) {
	size := int(w.glob.KeySize + w.glob.ItemSize)
	start, count, e := vector(b, at, size)
	if e != nil {
		return nil, e
	}
	out := make([]driver.Note, count)
	for i := range out {
		p := start + i*size
		out[i] = driver.Note{Nullifier: cloneBytes(b[p : p+32]), Value: cloneBytes(b[p+32 : p+size])}
	}
	return out, nil
}
func (w *WalletCore) MapOwned(ctx context.Context, signers []*profile.Signer, leaves []byte) (out [][]driver.Note, info driver.SyncInfo, err error) {
	signers = append([]*profile.Signer(nil), signers...)
	leaves = cloneBytes(leaves)
	if len(signers) == 0 || len(signers) > 256 {
		return nil, info, fmt.Errorf("walletcore: expected 1..256 signers")
	}
	if len(leaves)%int(w.glob.ItemSize) != 0 {
		return nil, info, fmt.Errorf("walletcore: truncated note leaf")
	}
	for _, s := range signers {
		if !signers[0].SameSourceOf(s) {
			return nil, info, fmt.Errorf("walletcore: owners must share a source")
		}
	}
	// Nested snapshots also combine each signer's disposal context.
	var capture func(context.Context, int, []byte) error
	capture = func(ctx context.Context, i int, keys []byte) error {
		if i < len(signers) {
			return signers[i].UseKeys(ctx, capability.Grant(), capability.Phoenix, func(ctx context.Context, key []byte) error {
				next := append(cloneBytes(keys), key...)
				defer clear(next)
				return capture(ctx, i+1, next)
			})
		}
		return w.operation(ctx, func(a *arena) error {
			k, p, slot, last := a.box(keys), a.box(newDataBuffer(leaves, uint32(len(leaves)/int(w.glob.ItemSize)))), a.space(4), a.space(16)
			if e := a.call("map_owned_with_keys", k, uint64(len(keys)), p, slot, last); e != nil {
				return e
			}
			b, e := a.take(slot, false)
			if e != nil {
				return e
			}
			start, n, e := vector(b, len(b)-8, 8)
			if e != nil {
				return e
			}
			if n != len(signers) {
				return fmt.Errorf("walletcore: owner count mismatch")
			}
			out = make([][]driver.Note, n)
			for i := range out {
				out[i], e = w.parseNoteList(b, start+i*8)
				if e != nil {
					return e
				}
			}
			b, e = a.read(last, 16)
			if e != nil {
				return e
			}
			info = driver.SyncInfo{BlockHeight: leUint64(b), Bookmark: leUint64(b[8:])}
			return nil
		})
	}
	err = capture(ctx, 0, nil)
	if err != nil {
		out = nil
		info = driver.SyncInfo{}
	}
	return
}
func (w *WalletCore) Bookmarks(ctx context.Context, notes []driver.Note) (out []uint64, err error) {
	notes = driver.CloneNotes(notes)
	if len(notes) == 0 {
		return []uint64{}, ctx.Err()
	}
	err = w.operation(ctx, func(a *arena) error {
		b, n, e := w.noteValuesContent(ctx, notes)
		if e != nil {
			return e
		}
		p, slot := a.box(newDataBuffer(b, n)), a.space(4)
		if e = a.call("bookmarks", p, slot); e != nil {
			return e
		}
		target, e := w.readU32(ctx, uint32(slot))
		if e != nil {
			return e
		}
		defer w.free(context.WithoutCancel(ctx), target, n*8)
		b, e = a.read(uint64(target), n*8)
		if e != nil {
			return e
		}
		out = make([]uint64, n)
		for i := range out {
			out[i] = leUint64(b[i*8:])
		}
		return nil
	})
	return
}
func (w *WalletCore) AccountsIntoRaw(ctx context.Context, accounts [][]byte) (out [][]byte, err error) {
	b := make([]byte, 0, len(accounts)*96)
	for _, account := range accounts {
		if e := validateAccountKey(account); e != nil {
			return nil, e
		}
		b = append(b, account...)
	}
	if len(accounts) == 0 {
		return [][]byte{}, ctx.Err()
	}
	err = w.operation(ctx, func(a *arena) error {
		p, slot := a.box(prefix(b)), a.space(4)
		if e := a.call("accounts_into_raw", p, slot); e != nil {
			return e
		}
		raw, e := a.take(slot, false)
		if e != nil {
			return e
		}
		if len(raw)%len(accounts) != 0 {
			return fmt.Errorf("walletcore: invalid account output")
		}
		size := len(raw) / len(accounts)
		out = make([][]byte, len(accounts))
		for i := range out {
			out[i] = cloneBytes(raw[i*size : (i+1)*size])
		}
		return nil
	})
	return
}
