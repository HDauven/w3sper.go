// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package tx

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"

	"github.com/HDauven/w3sper.go/base58"
	"github.com/HDauven/w3sper.go/chainid"
	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/gas"
	"github.com/HDauven/w3sper.go/network"
	"github.com/HDauven/w3sper.go/profile"
)

type Builder struct {
	entry                      *BookEntry
	chain                      ChainSource
	operation                  driver.Operation
	shielded, topup, committed bool
	amount, deposit, nonce     *big.Int
	gas                        *gas.Config
	chainID                    *int
	receiver                   []byte
	payload                    *driver.Payload
	deployment                 *driver.Deployment
	owner                      *profile.Signer
	owners                     []*profile.Signer
	err                        error
}
type Transfer = Builder
type AccountTransfer struct{ *Builder }
type AddressTransfer struct{ *Builder }
type Routed struct {
	Account *AccountTransfer
	Address *AddressTransfer
}

func NewTransfer(e *BookEntry) *Transfer { return NewOperation(e, driver.Transfer) }
func NewOperation(e *BookEntry, op driver.Operation) *Builder {
	return &Builder{entry: e, operation: op, shielded: op == driver.Unshield, gas: gas.New(nil, nil)}
}
func NewDeployment(e *BookEntry, bytecode []byte) *Builder {
	b := NewTransfer(e)
	b.deployment = &driver.Deployment{Bytecode: bytes.Clone(bytecode)}
	return b
}
func (b *Builder) Entry() *BookEntry { return b.entry }
func copyInt(v *big.Int) *big.Int {
	if v == nil {
		return nil
	}
	return new(big.Int).Set(v)
}
func (b *Builder) Amount(v *big.Int) *Builder {
	b.amount = copyInt(v)
	if v == nil {
		b.err = fmt.Errorf("tx: amount cannot be nil")
	}
	return b
}
func (b *Builder) Deposit(v *big.Int) *Builder {
	b.deposit = copyInt(v)
	if v == nil {
		b.err = fmt.Errorf("tx: deposit cannot be nil")
	}
	return b
}
func (b *Builder) Nonce(v *big.Int) *Builder {
	b.nonce = copyInt(v)
	if v == nil {
		b.err = fmt.Errorf("tx: nonce cannot be nil")
	}
	return b
}

// Nonce and AccountNonce specify the current account nonce; builds use nonce+1.
func (b *Builder) AccountNonce(v *big.Int) *Builder        { return b.Nonce(v) }
func (b *Builder) Gas(g *gas.Config) *Builder              { b.gas = g.Clone(); return b }
func Gas(limit, price *big.Int) *gas.Config                { return gas.New(limit, price) }
func (b *Builder) Chain(id int) *Builder                   { b.chainID = &id; return b }
func (b *Builder) ChainSource(source ChainSource) *Builder { b.chain = source; return b }
func (b *Builder) Public() *Builder                        { b.shielded = false; return b }
func (b *Builder) Shielded() *Builder                      { b.shielded = true; return b }
func (b *Builder) Owner(s *profile.Signer) *Builder        { b.owner = s; return b }
func (b *Builder) OwnerSigners(signers ...*profile.Signer) *Builder {
	b.owners = append([]*profile.Signer(nil), signers...)
	return b
}
func (b *Builder) Memo(memo []byte) *Builder {
	p := driver.Payload{Memo: append([]byte{}, memo...)}
	return b.Payload(p)
}
func (b *Builder) Payload(p driver.Payload) *Builder { b.payload = clonePayload(&p); return b }
func (b *Builder) ContractCall(call driver.Call) *Builder {
	return b.Payload(driver.Payload{Call: &call})
}
func (b *Builder) Committed() *Builder { b.committed = true; return b }
func (b *Builder) InitArgs(args []byte) *Builder {
	if b.deployment == nil {
		b.err = fmt.Errorf("tx: not a deployment")
	} else {
		b.deployment.InitArgs = bytes.Clone(args)
	}
	return b
}
func (b *Builder) DeployNonce(n uint64) *Builder {
	if b.deployment == nil {
		b.err = fmt.Errorf("tx: not a deployment")
	} else {
		b.deployment.Nonce = n
	}
	return b
}
func (b *Builder) DeploymentOwner(owner []byte) *Builder {
	if b.deployment == nil {
		b.err = fmt.Errorf("tx: not a deployment")
	} else {
		b.deployment.Owner = bytes.Clone(owner)
		if len(owner) == 0 {
			b.err = fmt.Errorf("tx: empty deployment owner")
		}
	}
	return b
}
func clonePayload(p *driver.Payload) *driver.Payload {
	if p == nil {
		return nil
	}
	out := *p
	out.Memo = bytes.Clone(p.Memo)
	if p.Call != nil {
		c := *p.Call
		c.ContractID = bytes.Clone(c.ContractID)
		c.Args = bytes.Clone(c.Args)
		out.Call = &c
	}
	if p.Deploy != nil {
		d := *p.Deploy
		d.Bytecode = bytes.Clone(d.Bytecode)
		d.Owner = bytes.Clone(d.Owner)
		d.InitArgs = bytes.Clone(d.InitArgs)
		out.Deploy = &d
	}
	return &out
}
func (b *Builder) snapshot() *Builder {
	out := *b
	out.amount = copyInt(b.amount)
	out.deposit = copyInt(b.deposit)
	out.nonce = copyInt(b.nonce)
	out.gas = b.gas.Clone()
	out.receiver = bytes.Clone(b.receiver)
	out.owners = append([]*profile.Signer(nil), b.owners...)
	out.payload = clonePayload(b.payload)
	if b.deployment != nil {
		out.deployment = clonePayload(&driver.Payload{Deploy: b.deployment}).Deploy
	}
	if b.chainID != nil {
		id := *b.chainID
		out.chainID = &id
	}
	return &out
}
func (b *Builder) To(identifier string) (*Routed, error) {
	key, e := base58.Decode(identifier)
	if e != nil {
		return nil, e
	}
	out := b.snapshot()
	out.receiver = key
	switch len(key) {
	case 96:
		out.shielded = false
		return &Routed{Account: &AccountTransfer{out}}, nil
	case 64:
		out.shielded = true
		return &Routed{Address: &AddressTransfer{out}}, nil
	default:
		return nil, fmt.Errorf("tx: invalid receiver identifier")
	}
}
func (b *Builder) Build() (*Result, error) { return b.BuildContext(context.Background(), nil) }
func (b *Builder) BuildContext(ctx context.Context, n *network.Client) (*Result, error) {
	copy := b.snapshot()
	if n != nil {
		op, cancel := n.ReadContext(ctx)
		defer cancel()
		ctx = op
	}
	return copy.build(ctx, n)
}
func u64(name string, v *big.Int) (uint64, error) {
	if v == nil {
		return 0, nil
	}
	if !v.IsUint64() {
		return 0, fmt.Errorf("tx: %s must be u64", name)
	}
	return v.Uint64(), nil
}
func (b *Builder) chainFor(ctx context.Context, n *network.Client) (int, error) {
	if b.chainID != nil {
		return chainid.Validate(*b.chainID)
	}
	if c, ok := b.chain.(ContextChainSource); ok {
		id, e := c.ChainIDContext(ctx)
		if e != nil {
			return 0, e
		}
		return chainid.Validate(id)
	}
	if b.chain != nil {
		if e := ctx.Err(); e != nil {
			return 0, e
		}
		id, e := b.chain.ChainID()
		if e != nil {
			return 0, e
		}
		return chainid.Validate(id)
	}
	if n != nil {
		info, e := network.NewNode(n).Info(ctx)
		if e != nil {
			return 0, e
		}
		return info.ChainID, nil
	}
	return 0, fmt.Errorf("tx: chain ID required")
}
func (b *Builder) build(ctx context.Context, n *network.Client) (*Result, error) {
	if b.err != nil {
		return nil, b.err
	}
	if b.entry == nil || b.entry.driver == nil {
		return nil, driver.ErrDriverRequired
	}
	s := b.entry.signer
	if err := s.Check(); err != nil {
		return nil, err
	}
	d, ok := b.entry.driver.(driver.Protocol)
	if !ok {
		return nil, fmt.Errorf("tx: current wallet-core protocol required")
	}
	ctx, cancel, e := s.OperationContext(ctx)
	if e != nil {
		return nil, e
	}
	defer cancel()
	if b.operation >= driver.Stake && b.owner != nil {
		if !s.SameSourceOf(b.owner) {
			return nil, fmt.Errorf("tx: owner must belong to same wallet")
		}
		op, end, err := b.owner.OperationContext(ctx)
		if err != nil {
			return nil, err
		}
		defer end()
		ctx = op
	}
	if e = b.gas.Validate(); e != nil {
		return nil, e
	}
	amount, e := u64("amount", b.amount)
	if e != nil {
		return nil, e
	}
	deposit, e := u64("deposit", b.deposit)
	if e != nil {
		return nil, e
	}
	chain, e := b.chainFor(ctx, n)
	if e != nil {
		return nil, e
	}
	in := driver.TransactionInput{Signer: s, Operation: b.operation, Shielded: b.shielded, Amount: amount, Deposit: deposit, GasLimit: b.gas.Limit.Uint64(), GasPrice: b.gas.Price.Uint64(), ChainID: chain, Receiver: b.receiver}
	if b.operation == driver.Unshield {
		in.Shielded = true
	}
	result := &Result{ChainID: chain}
	if b.operation >= driver.Stake {
		owner, value, e := b.stakeContext(ctx, n, amount)
		if e != nil {
			return nil, e
		}
		in.Owner = owner
		in.Amount = value
		op, close, e := owner.OperationContext(ctx)
		if e != nil {
			return nil, e
		}
		ctx = op
		defer close()
	}
	if b.payload != nil && b.payload.Deploy != nil {
		if b.deployment != nil || b.payload.Call != nil || b.payload.Memo != nil {
			return nil, fmt.Errorf("tx: conflicting deployment payload")
		}
		b.deployment = b.payload.Deploy
		b.payload = nil
	}
	if b.deployment != nil {
		if b.payload != nil || b.operation != driver.Transfer || in.Amount != 0 || in.Deposit != 0 {
			return nil, fmt.Errorf("tx: deployment requires a zero-value envelope without another payload")
		}
		self := s.Account().Bytes()
		if in.Shielded {
			self = s.Address().Bytes()
		}
		if in.Receiver != nil && !bytes.Equal(in.Receiver, self) {
			return nil, fmt.Errorf("tx: deployment receiver must be self")
		}
		if len(b.deployment.Owner) == 0 {
			b.deployment.Owner = s.Account().Bytes()
		}
		b.payload = &driver.Payload{Deploy: b.deployment}
		in.Amount, in.Deposit = 0, 0
		in.Receiver = nil
		if in.Shielded {
			in.Receiver = s.Address().Bytes()
		}
	}
	if b.committed || b.payload != nil && b.payload.Call != nil && b.payload.Call.Committed {
		if in.Shielded || b.operation != driver.Transfer || b.payload == nil || b.payload.Call == nil || b.deployment != nil || in.Amount != 0 || in.Deposit != 0 || !bytes.Equal(in.Receiver, s.Account().Bytes()) {
			return nil, fmt.Errorf("tx: committed calls require zero-value public self envelope and a contract call")
		}
		b.payload.Call.Committed = true
		in.Receiver = nil
	}
	if b.payload != nil {
		if b.operation != driver.Transfer {
			return nil, fmt.Errorf("tx: payloads only apply to transfers/deployments")
		}
		in.Data, e = d.CreateData(ctx, *b.payload)
		if e != nil {
			return nil, e
		}
	}
	if b.operation == driver.Transfer && in.Receiver == nil && b.deployment == nil && !(b.payload != nil && b.payload.Call != nil && b.payload.Call.Committed) {
		return nil, fmt.Errorf("tx: receiver required")
	}
	if in.Shielded {
		if n == nil {
			return nil, fmt.Errorf("tx: Phoenix requires network openings and prover")
		}
		fee := in.GasLimit * in.GasPrice
		pick := fee
		if b.operation != driver.Unstake && b.operation != driver.WithdrawReward {
			if in.Amount > math.MaxUint64-in.Deposit || in.Amount+in.Deposit > math.MaxUint64-fee {
				return nil, fmt.Errorf("tx: total value exceeds u64")
			}
			pick += in.Amount + in.Deposit
		}
		notes, e := b.entry.Notes(ctx)
		if e != nil {
			return nil, e
		}
		_, spendable, e := d.BalanceFor(ctx, s, notes)
		if e != nil {
			return nil, e
		}
		if spendable < pick {
			return nil, fmt.Errorf("tx: insufficient spendable Phoenix funds")
		}
		in.Notes, e = d.PickFor(ctx, s, notes, pick)
		if e != nil {
			return nil, e
		}
		if len(in.Notes) == 0 {
			return nil, fmt.Errorf("tx: no spendable notes")
		}
		syncer := network.NewAddressSyncer(n, d)
		in.Openings, e = syncer.Openings(ctx, in.Notes)
		if e != nil {
			return nil, e
		}
		in.Root, e = syncer.Root(ctx)
		if e != nil {
			return nil, e
		}
		for _, note := range in.Notes {
			result.Nullifiers = append(result.Nullifiers, bytes.Clone(note.Nullifier))
		}
	} else {
		var nonce uint64
		if b.nonce != nil {
			nonce, e = u64("nonce", b.nonce)
		} else {
			var bal AccountBalance
			bal, e = b.entry.AccountBalance(ctx, n)
			if e == nil {
				nonce, e = u64("nonce", bal.Nonce)
			}
		}
		if e != nil {
			return nil, e
		}
		if nonce == math.MaxUint64 {
			return nil, fmt.Errorf("tx: next nonce overflows u64")
		}
		in.Nonce = nonce + 1
		result.Nonce = new(big.Int).SetUint64(in.Nonce)
	}
	raw, e := d.Build(ctx, in)
	if e != nil {
		return nil, e
	}
	if in.Shielded {
		proof, e := n.Prove(ctx, raw.Circuits)
		if e != nil {
			return nil, e
		}
		if e = s.Check(); e != nil {
			return nil, e
		}
		raw, e = d.ProveTransaction(ctx, raw.Buffer, proof)
		if e != nil {
			return nil, e
		}
	}
	if b.deployment != nil {
		id, e := d.ContractID(ctx, *b.deployment)
		if e != nil {
			return nil, e
		}
		result.ContractID = hex.EncodeToString(id)
	}
	if e = s.Check(); e != nil {
		return nil, e
	}
	if in.Owner != nil {
		if e = in.Owner.Check(); e != nil {
			return nil, e
		}
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	result.Buffer, result.Hash = raw.Buffer, raw.Hash
	return result, nil
}
func (b *Builder) stakeContext(ctx context.Context, n *network.Client, amount uint64) (*profile.Signer, uint64, error) {
	s := b.entry.signer
	state, e := b.entry.StakeInfo(ctx, n)
	if e != nil {
		return nil, 0, e
	}
	minimum, e := b.entry.driver.(driver.Protocol).MinimumStakeContext(ctx)
	if e != nil {
		return nil, 0, e
	}
	if minimum == nil || !minimum.IsUint64() {
		return nil, 0, fmt.Errorf("tx: invalid minimum stake")
	}
	minStake := minimum.Uint64()
	hasStake := state.Amount != nil
	switch b.operation {
	case driver.Stake:
		if hasStake && !b.topup {
			return nil, 0, fmt.Errorf("tx: stake exists; use Topup")
		}
		if !hasStake && b.topup {
			return nil, 0, fmt.Errorf("tx: no stake to top up")
		}
		if amount == 0 || !b.topup && amount < minStake {
			return nil, 0, fmt.Errorf("tx: stake below minimum %d", minStake)
		}
	case driver.Unstake:
		if !hasStake {
			return nil, 0, fmt.Errorf("tx: no stake to unstake")
		}
		total := state.Amount.Total
		if total == nil {
			if state.Amount.Value == nil || state.Amount.Locked == nil {
				return nil, 0, fmt.Errorf("tx: invalid stake state")
			}
			total = new(big.Int).Add(state.Amount.Value, state.Amount.Locked)
		}
		if !total.IsUint64() {
			return nil, 0, fmt.Errorf("tx: invalid total stake")
		}
		if b.amount == nil {
			amount = total.Uint64()
		}
		if amount == 0 || amount > total.Uint64() {
			return nil, 0, fmt.Errorf("tx: invalid unstake amount")
		}
		remaining := total.Uint64() - amount
		if remaining > 0 && remaining < minStake {
			return nil, 0, fmt.Errorf("tx: remaining stake below minimum")
		}
	case driver.WithdrawReward:
		if state.Reward == nil || !state.Reward.IsUint64() || amount == 0 || amount > state.Reward.Uint64() {
			return nil, 0, fmt.Errorf("tx: invalid reward withdrawal")
		}
	}
	for _, candidate := range append(append([]*profile.Signer{}, b.owners...), b.owner) {
		if candidate == nil {
			continue
		}
		if e = candidate.Check(); e != nil {
			return nil, 0, e
		}
		if !s.SameSourceOf(candidate) {
			return nil, 0, fmt.Errorf("tx: owner must belong to same wallet")
		}
	}
	if !hasStake && b.operation == driver.Stake {
		if b.owner == nil && len(b.owners) > 0 {
			return nil, 0, fmt.Errorf("tx: new stake requires explicit Owner, not candidates")
		}
		if b.owner != nil {
			return b.owner, amount, nil
		}
		return s, amount, nil
	}
	keys, e := b.entry.StakeKeys(ctx, n)
	if e != nil {
		return nil, 0, e
	}
	if keys == nil || keys.Owner.Account == "" {
		return nil, 0, fmt.Errorf("tx: account owner authority unavailable (contract-owned stake cannot use wallet keys)")
	}
	if b.owner != nil && b.owner.Account().String() != keys.Owner.Account {
		return nil, 0, fmt.Errorf("tx: explicit owner does not match on-chain stake")
	}
	candidates := append([]*profile.Signer{b.owner, s}, b.owners...)
	for _, candidate := range candidates {
		if candidate != nil && candidate.Account().String() == keys.Owner.Account {
			return candidate, amount, nil
		}
	}
	return nil, 0, fmt.Errorf("tx: signer for on-chain owner unavailable")
}
