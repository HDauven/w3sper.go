// Package tx builds public and shielded Dusk transactions using wallet-core.
// Builders are mutable and not safe for concurrent mutation. BuildContext takes
// an owned snapshot before any I/O. It does not reserve account nonces.
package tx

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/network"
	"github.com/HDauven/w3sper.go/profile"
)

const TRANSFER = network.TransferContract
const STAKE = network.StakeContract

type ChainSource interface{ ChainID() (int, error) }
type ContextChainSource interface {
	ChainIDContext(context.Context) (int, error)
}

func NetworkChainSource(c *network.Client) ChainSource { return &networkChainSource{c} }

type networkChainSource struct{ c *network.Client }

func (s *networkChainSource) ChainID() (int, error) { return s.ChainIDContext(context.Background()) }
func (s *networkChainSource) ChainIDContext(ctx context.Context) (int, error) {
	info, e := network.NewNode(s.c).Info(ctx)
	if e != nil {
		return 0, e
	}
	return info.ChainID, nil
}

// Treasury is the legacy snapshot-provider interface. New I/O providers should
// implement ContextTreasury so cancellation reaches their actual operations.
type Treasury interface {
	AccountBalance([]byte) (AccountBalance, error)
	AddressNotes([]byte) ([]driver.Note, error)
	StakeInfo([]byte) (StakeInfo, error)
}
type ContextTreasury interface {
	AccountBalanceContext(context.Context, []byte) (AccountBalance, error)
	AddressNotesContext(context.Context, []byte) ([]driver.Note, error)
	StakeInfoContext(context.Context, []byte) (StakeInfo, error)
}
type StakeKeySource interface {
	StakeKeysContext(context.Context, []byte) (*network.StakeKeys, error)
}
type AccountBalance = network.AccountBalance
type StakeInfo = network.StakeInfo
type StakeAmount = network.StakeAmount

// CloneStakeInfo takes an owned snapshot of treasury-provided state.
func CloneStakeInfo(in StakeInfo) StakeInfo {
	out := in
	out.Reward = copyInt(in.Reward)
	if in.Amount != nil {
		a := *in.Amount
		a.Value = copyInt(a.Value)
		a.Locked = copyInt(a.Locked)
		a.Eligibility = copyInt(a.Eligibility)
		a.Total = copyInt(a.Total)
		out.Amount = &a
	}
	return out
}

type BookEntry struct {
	profile  *profile.Profile
	signer   *profile.Signer
	treasury any
	driver   driver.Driver
}

// NewBookEntry accepts a public Profile (read-only) or a Signer. A generated
// public profile is deliberately not signing authority.
func NewBookEntry(identity any, treasury any, d driver.Driver) *BookEntry {
	e := &BookEntry{treasury: treasury, driver: d}
	switch v := identity.(type) {
	case *profile.Profile:
		e.profile = v
	case *profile.Signer:
		e.signer = v
		e.profile = v.Profile()
	}
	return e
}
func (e *BookEntry) Profile() *profile.Profile          { return e.profile }
func (e *BookEntry) Signer() *profile.Signer            { return e.signer }
func (e *BookEntry) Treasury() any                      { return e.treasury }
func (e *BookEntry) Driver() driver.Driver              { return e.driver }
func (e *BookEntry) Transfer(amount *big.Int) *Transfer { return NewTransfer(e).Amount(amount) }
func (e *BookEntry) Deploy(bytecode []byte) *Builder    { return NewDeployment(e, bytecode) }
func (e *BookEntry) Shield(amount *big.Int) *Builder {
	return NewOperation(e, driver.Shield).Amount(amount)
}
func (e *BookEntry) Unshield(amount *big.Int) *Builder {
	return NewOperation(e, driver.Unshield).Amount(amount)
}
func (e *BookEntry) Stake(amount *big.Int) *Builder {
	return NewOperation(e, driver.Stake).Amount(amount)
}
func (e *BookEntry) Topup(amount *big.Int) *Builder { b := e.Stake(amount); b.topup = true; return b }
func (e *BookEntry) Unstake(amount *big.Int) *Builder {
	b := NewOperation(e, driver.Unstake)
	if amount != nil {
		b.Amount(amount)
	}
	return b
}
func (e *BookEntry) Withdraw(amount *big.Int) *Builder {
	return NewOperation(e, driver.WithdrawReward).Amount(amount)
}

func (e *BookEntry) AccountBalance(ctx context.Context, n *network.Client) (out AccountBalance, err error) {
	if e == nil || e.profile == nil {
		return out, fmt.Errorf("tx: public identity required")
	}
	if err = ctx.Err(); err != nil {
		return
	}
	key := e.profile.Account().Bytes()
	switch t := e.treasury.(type) {
	case ContextTreasury:
		out, err = t.AccountBalanceContext(ctx, key)
	case Treasury:
		out, err = t.AccountBalance(key)
	default:
		if n == nil {
			return out, fmt.Errorf("tx: account treasury or network required")
		}
		d, ok := e.driver.(network.AccountCodec)
		if !ok {
			return out, driver.ErrDriverRequired
		}
		out, err = network.NewAccountSyncer(n, d).Balance(ctx, key)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		if out.Nonce == nil || out.Value == nil || !out.Nonce.IsUint64() || !out.Value.IsUint64() {
			return AccountBalance{}, fmt.Errorf("tx: invalid account state")
		}
		out.Nonce = new(big.Int).Set(out.Nonce)
		out.Value = new(big.Int).Set(out.Value)
	}
	return
}
func (e *BookEntry) Notes(ctx context.Context) (out []driver.Note, err error) {
	if e == nil || e.profile == nil {
		return nil, fmt.Errorf("tx: identity required")
	}
	if err = ctx.Err(); err != nil {
		return
	}
	switch t := e.treasury.(type) {
	case ContextTreasury:
		out, err = t.AddressNotesContext(ctx, e.profile.Address().Bytes())
	case Treasury:
		out, err = t.AddressNotes(e.profile.Address().Bytes())
	default:
		return nil, fmt.Errorf("tx: note treasury required")
	}
	if err == nil {
		err = ctx.Err()
	}
	return driver.CloneNotes(out), err
}
func (e *BookEntry) StakeInfo(ctx context.Context, n *network.Client) (out StakeInfo, err error) {
	defer func() {
		if err == nil {
			out = CloneStakeInfo(out)
		}
	}()
	if e == nil || e.profile == nil {
		return out, fmt.Errorf("tx: identity required")
	}
	if err = ctx.Err(); err != nil {
		return
	}
	switch t := e.treasury.(type) {
	case ContextTreasury:
		out, err = t.StakeInfoContext(ctx, e.profile.Account().Bytes())
	case Treasury:
		out, err = t.StakeInfo(e.profile.Account().Bytes())
	default:
		if n == nil {
			return out, fmt.Errorf("tx: stake treasury or network required")
		}
		d, ok := e.driver.(network.AccountCodec)
		if !ok {
			return out, driver.ErrDriverRequired
		}
		out, err = network.NewAccountSyncer(n, d).Stake(ctx, e.profile.Account().Bytes())
	}
	if err == nil {
		err = ctx.Err()
	}
	return
}
func (e *BookEntry) StakeKeys(ctx context.Context, n *network.Client) (out *network.StakeKeys, err error) {
	if e == nil || e.profile == nil {
		return nil, fmt.Errorf("tx: identity required")
	}
	defer func() {
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			out = nil
		} else if out != nil {
			copy := *out
			out = &copy
		}
	}()
	if t, ok := e.treasury.(StakeKeySource); ok {
		return t.StakeKeysContext(ctx, e.profile.Account().Bytes())
	}
	if n == nil {
		return nil, fmt.Errorf("tx: stake owner lookup requires a provider")
	}
	d, ok := e.driver.(network.AccountCodec)
	if !ok {
		return nil, driver.ErrDriverRequired
	}
	keys, err := network.NewAccountSyncer(n, d).StakeKeys(ctx, [][]byte{e.profile.Account().Bytes()})
	if err != nil {
		return nil, err
	}
	return keys[0], nil
}
func IsDriverRequired(err error) bool { return errors.Is(err, driver.ErrDriverRequired) }
