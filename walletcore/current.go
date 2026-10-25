// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
// ABI: dusk-network wallet-core, pinned to w3sper.js 97f1cf26.
package walletcore

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/internal/capability"
	"github.com/HDauven/w3sper.go/profile"
)

var _ driver.Protocol = (*WalletCore)(nil)

// arena owns exactly sized host allocations for one serialized ABI call.
type arena struct {
	w    *WalletCore
	ctx  context.Context
	ptrs []uint32
	err  error
}

func (a *arena) box(b []byte) uint64 {
	if a.err != nil {
		return 0
	}
	p, e := a.w.box(a.ctx, b)
	a.err = e
	if e == nil {
		a.ptrs = append(a.ptrs, p)
	}
	return uint64(p)
}
func (a *arena) space(n int) uint64  { return a.box(make([]byte, n)) }
func (a *arena) u64(n uint64) uint64 { return a.box(leU64Bytes(n)) }
func (a *arena) u32(n uint32) uint64 {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], n)
	return a.box(b[:])
}
func (a *arena) pair(b []byte) (uint64, uint64) { return a.u32(uint32(len(b))), a.box(b) }
func (a *arena) call(name string, args ...uint64) error {
	if a.err != nil {
		return a.err
	}
	if e := a.ctx.Err(); e != nil {
		return e
	}
	fn := a.w.mod.ExportedFunction(name)
	if fn == nil {
		return fmt.Errorf("walletcore: missing export %s", name)
	}
	return a.w.invoke(a.ctx, name, fn, args...)
}
func (a *arena) close() {
	for i := len(a.ptrs) - 1; i >= 0; i-- {
		_ = a.w.free(context.WithoutCancel(a.ctx), a.ptrs[i])
	}
}
func (a *arena) take(slot uint64, header bool) ([]byte, error) {
	return a.w.takeLenPrefixedOutput(a.ctx, uint32(slot), header)
}
func (a *arena) read(ptr uint64, n uint32) ([]byte, error) {
	return a.w.readBytes(a.ctx, uint32(ptr), n)
}
func (w *WalletCore) operation(ctx context.Context, fn func(*arena) error) error {
	return w.runLocked(ctx, func(w *WalletCore) error { a := &arena{w: w, ctx: ctx}; defer a.close(); return fn(a) })
}
func prefix(b []byte) []byte {
	out := make([]byte, 4+len(b))
	binary.LittleEndian.PutUint32(out, uint32(len(b)))
	copy(out[4:], b)
	return out
}

func (w *WalletCore) GenerateProfileKeys(ctx context.Context, seed []byte, index profile.ProfileIndex) (out []byte, err error) {
	defer func() {
		if err != nil {
			clear(out)
			out = nil
		}
	}()
	if err = validateSeed(seed); err != nil {
		return
	}
	seed = cloneBytes(seed)
	defer clear(seed)
	err = w.operation(ctx, func(a *arena) error {
		input, output := a.box(seed), a.space(320)
		if e := a.call("generate_profile_keys", input, uint64(index), output); e != nil {
			return e
		}
		out, err = a.read(output, 320)
		return err
	})
	return
}
func (w *WalletCore) SignBLSMessage(ctx context.Context, s *profile.Signer, message []byte) (signature []byte, err error) {
	if len(message) > math.MaxInt32-176 {
		return nil, fmt.Errorf("walletcore: message too large")
	}
	message = cloneBytes(message)
	defer clear(message)
	err = s.UseKeys(ctx, capability.Grant(), capability.BLS, func(ctx context.Context, key []byte) error {
		return w.operation(ctx, func(a *arena) error {
			k, m, out := a.box(key), a.box(message), a.space(144)
			if e := a.call("sign_bls_message_with_key", k, m, uint64(len(message)), out); e != nil {
				return e
			}
			b, e := a.read(out, 144)
			if e != nil {
				return e
			}
			if !bytes.Equal(b[:96], s.Account().Bytes()) {
				return fmt.Errorf("walletcore: signature account mismatch")
			}
			signature = cloneBytes(b[96:])
			return nil
		})
	})
	if err != nil {
		clear(signature)
		signature = nil
	}
	return
}

// CreateData serializes transaction data with wallet-core (not host-side RKYV).
func (w *WalletCore) CreateData(ctx context.Context, p driver.Payload) (out []byte, err error) {
	defer func() {
		if err != nil {
			out = nil
		}
	}()
	count := 0
	if p.Memo != nil {
		count++
	}
	if p.Call != nil {
		count++
	}
	if p.Deploy != nil {
		count++
	}
	if count != 1 {
		return nil, fmt.Errorf("walletcore: payload must have exactly one variant")
	}
	// The pinned JS driver treats an empty memo as absent transaction data.
	if p.Memo != nil && len(p.Memo) == 0 {
		err = w.operation(ctx, func(*arena) error { return nil })
		return
	}
	p.Memo = bytes.Clone(p.Memo)
	if p.Call != nil {
		c := *p.Call
		c.ContractID = cloneBytes(c.ContractID)
		c.Args = cloneBytes(c.Args)
		p.Call = &c
		if len(c.ContractID) != 32 || c.Function == "" || !utf8.ValidString(c.Function) {
			return nil, fmt.Errorf("walletcore: invalid contract call")
		}
	}
	if p.Deploy != nil {
		d := cloneDeploy(*p.Deploy)
		p.Deploy = &d
		if len(d.Bytecode) == 0 || len(d.Owner) == 0 {
			return nil, fmt.Errorf("walletcore: deployment requires bytecode and owner")
		}
	}
	err = w.operation(ctx, func(a *arena) error {
		slot := a.space(4)
		var e error
		switch {
		case p.Deploy != nil:
			d := p.Deploy
			bl, b := a.pair(d.Bytecode)
			ol, o := a.pair(d.Owner)
			il, i := a.pair(d.InitArgs)
			e = a.call("create_deploy_tx_data", bl, b, ol, o, il, i, a.u64(d.Nonce), slot)
		case p.Call != nil:
			c := p.Call
			fl, f := a.pair([]byte(c.Function))
			al, args := a.pair(c.Args)
			id := a.box(c.ContractID)
			if c.Committed {
				e = a.call("create_committed_call_tx_data", fl, f, al, args, id, slot)
			} else {
				e = a.call("create_tx_data", fl, f, al, args, id, 0, 0, slot)
			}
		default:
			ml, m := a.pair(p.Memo)
			e = a.call("create_tx_data", 0, 0, 0, 0, a.space(32), ml, m, slot)
		}
		if e != nil {
			return e
		}
		out, e = a.take(slot, true)
		return e
	})
	return
}
func cloneDeploy(d driver.Deployment) driver.Deployment {
	d.Bytecode = cloneBytes(d.Bytecode)
	d.Owner = cloneBytes(d.Owner)
	d.InitArgs = cloneBytes(d.InitArgs)
	return d
}
func (w *WalletCore) ContractID(ctx context.Context, d driver.Deployment) (out []byte, err error) {
	defer func() {
		if err != nil {
			out = nil
		}
	}()
	d = cloneDeploy(d)
	if len(d.Bytecode) == 0 || len(d.Owner) == 0 {
		return nil, fmt.Errorf("walletcore: deployment requires bytecode and owner")
	}
	err = w.operation(ctx, func(a *arena) error {
		bl, b := a.pair(d.Bytecode)
		ol, o := a.pair(d.Owner)
		dst := a.space(32)
		if e := a.call("contract_id", bl, b, ol, o, a.u64(d.Nonce), dst); e != nil {
			return e
		}
		out, err = a.read(dst, 32)
		return err
	})
	return
}
func (w *WalletCore) ProveTransaction(ctx context.Context, tx, proof []byte) (out driver.RawTransaction, err error) {
	defer func() {
		if err != nil {
			out = driver.RawTransaction{}
		}
	}()
	tx = cloneBytes(tx)
	proof = cloneBytes(proof)
	if len(tx) < 4 || uint64(binary.LittleEndian.Uint32(tx)) != uint64(len(tx)-4) || len(proof) == 0 {
		return out, fmt.Errorf("walletcore: invalid unproven archive or empty proof")
	}
	err = w.operation(ctx, func(a *arena) error {
		t, p, slot, h := a.box(tx), a.box(prefix(proof)), a.space(4), a.space(64)
		if e := a.call("into_proven", t, p, slot, h); e != nil {
			return e
		}
		out, err = a.result(slot, h, false)
		return err
	})
	return
}
func (a *arena) result(slot, second uint64, shielded bool) (out driver.RawTransaction, err error) {
	// Read/free both outputs even if one read fails.
	out.Buffer, err = a.take(slot, shielded)
	if shielded {
		var e error
		out.Circuits, e = a.take(second, false)
		if err == nil {
			err = e
		}
	} else {
		var b []byte
		b, e := a.read(second, 64)
		if err == nil {
			err = e
		}
		out.Hash = string(b)
	}
	return
}

func (w *WalletCore) Build(ctx context.Context, in driver.TransactionInput) (out driver.RawTransaction, err error) {
	if err = in.Signer.Check(); err != nil {
		return
	}
	if in.Operation > driver.WithdrawReward {
		return out, fmt.Errorf("walletcore: invalid operation")
	}
	if _, err = validateChainID(in.ChainID); err != nil {
		return
	}
	if in.GasLimit == 0 || in.GasPrice == 0 || in.GasLimit > math.MaxUint64/in.GasPrice {
		return out, fmt.Errorf("walletcore: gas components and total must be positive u64")
	}
	// Snapshot caller-owned buffers before waiting on signers/runtime.
	in.Receiver = bytes.Clone(in.Receiver)
	in.Root = cloneBytes(in.Root)
	in.Data = bytes.Clone(in.Data)
	in.Notes = driver.CloneNotes(in.Notes)
	openings := make([][]byte, len(in.Openings))
	for i, o := range in.Openings {
		openings[i] = cloneBytes(o)
	}
	in.Openings = openings
	shielded := in.Shielded || in.Operation == driver.Unshield
	if in.Operation == driver.Shield && shielded {
		return out, fmt.Errorf("walletcore: shield requires account payment")
	}
	if shielded {
		if len(in.Root) != 32 || len(in.Notes) == 0 || len(in.Notes) > 4 || len(in.Openings) != len(in.Notes) {
			return out, fmt.Errorf("walletcore: Phoenix requires root and 1..4 matching notes/openings")
		}
		for _, o := range in.Openings {
			if len(o) == 0 {
				return out, fmt.Errorf("walletcore: missing opening")
			}
		}
	}
	if in.Operation == driver.Transfer {
		if shielded {
			err = validateAddressKey(in.Receiver)
		} else if in.Receiver != nil {
			err = validateAccountKey(in.Receiver)
		}
		if err != nil {
			return
		}
	}
	if in.Data != nil && (len(in.Data) < 4 || uint64(binary.LittleEndian.Uint32(in.Data)) != uint64(len(in.Data)-4)) {
		return out, fmt.Errorf("walletcore: data must be a length-prefixed archive")
	}
	if in.Amount > math.MaxUint64-in.Deposit || in.Amount+in.Deposit > math.MaxUint64-in.GasLimit*in.GasPrice {
		return out, fmt.Errorf("walletcore: total transaction value overflows u64")
	}
	kind := capability.BLS
	if shielded {
		kind = capability.Phoenix
	}
	if in.Operation == driver.Shield || in.Operation == driver.Unshield || (shielded && in.Operation >= driver.Stake) {
		kind = capability.Both
	}
	err = in.Signer.UseKeys(ctx, capability.Grant(), kind, func(ctx context.Context, key []byte) error {
		run := func(ctx context.Context, key []byte) error { return w.buildKeys(ctx, in, key, shielded, &out) }
		if in.Operation >= driver.Stake {
			owner := in.Owner
			if owner == nil {
				owner = in.Signer
			}
			if !in.Signer.SameSourceOf(owner) {
				return fmt.Errorf("walletcore: stake owner must belong to the same wallet")
			}
			return owner.UseKeys(ctx, capability.Grant(), capability.BLS, func(ctx context.Context, ownerKey []byte) error {
				both := append(cloneBytes(key), ownerKey...)
				defer clear(both)
				return run(ctx, both)
			})
		}
		return run(ctx, key)
	})
	if err != nil {
		out = driver.RawTransaction{}
	}
	return
}
func (w *WalletCore) buildKeys(ctx context.Context, in driver.TransactionInput, key []byte, shielded bool, out *driver.RawTransaction) error {
	return w.operation(ctx, func(a *arena) error {
		k := a.box(key)
		amount, gl, gp := a.u64(in.Amount), a.u64(in.GasLimit), a.u64(in.GasPrice)
		slot, second := a.space(4), a.space(64)
		if shielded {
			second = a.space(4)
		}
		var rng, notes, openings, root uint64
		if shielded || in.Operation == driver.Shield || in.Operation == driver.Unstake || in.Operation == driver.WithdrawReward {
			r := make([]byte, 32)
			if _, e := randRead(r); e != nil {
				return e
			}
			rng = a.box(r)
			clear(r)
		}
		if shielded {
			var b []byte
			var count uint32
			var e error
			if in.Operation == driver.Unstake || in.Operation == driver.WithdrawReward {
				b, count, e = w.noteEntriesContent(ctx, in.Notes)
			} else {
				b, count, e = w.noteValuesContent(ctx, in.Notes)
			}
			if e != nil {
				return e
			}
			notes = a.box(newDataBuffer(b, count))
			b = nil
			for _, o := range in.Openings {
				b = append(b, o...)
			}
			openings = a.box(newDataBuffer(b, uint32(len(in.Openings))))
			root = a.box(in.Root)
		}
		chain := uint64(in.ChainID)
		var e error
		switch in.Operation {
		case driver.Transfer:
			var data, receiver uint64
			if in.Data != nil {
				data = a.box(in.Data)
			}
			if in.Receiver != nil {
				receiver = a.box(in.Receiver)
			}
			if shielded {
				e = a.call("phoenix_with_key", rng, k, receiver, notes, openings, root, amount, a.u64(in.Deposit), gl, gp, chain, data, slot, second)
			} else {
				e = a.call("moonlight_with_key", k, receiver, amount, a.u64(in.Deposit), gl, gp, a.u64(in.Nonce), chain, data, slot, second)
			}
		case driver.Shield:
			e = a.call("moonlight_to_phoenix_with_keys", rng, k, amount, gl, gp, a.u64(in.Nonce), chain, slot, second)
		case driver.Unshield:
			b := make([]byte, 0, len(in.Notes)*32)
			for _, n := range in.Notes {
				if len(n.Nullifier) != 32 {
					return fmt.Errorf("walletcore: nullifier must be 32 bytes")
				}
				b = append(b, n.Nullifier...)
			}
			e = a.call("phoenix_to_moonlight_with_keys", rng, k, notes, openings, a.box(newDataBuffer(b, uint32(len(in.Notes)))), root, amount, gl, gp, chain, slot, second)
		default:
			name := map[driver.Operation]string{driver.Stake: "stake", driver.Unstake: "unstake", driver.WithdrawReward: "stake_reward"}[in.Operation]
			if shielded {
				e = a.call("phoenix_"+name+"_with_keys", rng, k, notes, openings, root, amount, gl, gp, chain, slot, second)
			} else {
				args := []uint64{k, amount, gl, gp, a.u64(in.Nonce), chain, slot, second}
				if in.Operation != driver.Stake {
					args = append([]uint64{rng}, args...)
				}
				e = a.call("moonlight_"+name+"_with_keys", args...)
			}
		}
		if e != nil {
			return e
		}
		*out, e = a.result(slot, second, shielded)
		return e
	})
}
