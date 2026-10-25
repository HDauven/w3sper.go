// Package bookkeeper provides profile-bound balances, note selection and
// transaction construction over a wallet-owned treasury.
package bookkeeper

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/network"
	"github.com/HDauven/w3sper.go/profile"
	"github.com/HDauven/w3sper.go/tx"
)

type Treasury = tx.Treasury
type ContextTreasury = tx.ContextTreasury

// Deprecated: address strings cannot authorize private reads; use a Signer.
var ErrPhoenixUnavailable = profile.ErrSignerRequired

type Bookkeeper struct {
	treasury any
	driver   driver.Driver
	network  *network.Client
	timeout  time.Duration
}
type Option func(*Bookkeeper)

func WithNetwork(n *network.Client) Option { return func(b *Bookkeeper) { b.network = n } }
func WithTimeout(d time.Duration) Option   { return func(b *Bookkeeper) { b.timeout = d } }
func New(treasury any, d driver.Driver, opts ...Option) *Bookkeeper {
	b := &Bookkeeper{treasury: treasury, driver: d, timeout: 120 * time.Second}
	for _, o := range opts {
		o(b)
	}
	return b
}
func (b *Bookkeeper) Treasury() any { return b.treasury }
func (b *Bookkeeper) context(ctx context.Context) (context.Context, context.CancelFunc) {
	if b.timeout < 0 {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		return ctx, cancel
	}
	if b.timeout > 0 {
		return context.WithTimeout(ctx, b.timeout)
	}
	return context.WithCancel(ctx)
}

type AddressBalance struct{ Value, Spendable *big.Int }
type BalanceResult struct {
	Account *tx.AccountBalance
	Address *AddressBalance
}

func (b BalanceResult) AccountBalance() *tx.AccountBalance { return b.Account }
func (b BalanceResult) GetAddress() *AddressBalance        { return b.Address }
func (b *Bookkeeper) Balance(identity any) (BalanceResult, error) {
	return b.BalanceContext(context.Background(), identity)
}
func (b *Bookkeeper) BalanceContext(ctx context.Context, identity any) (BalanceResult, error) {
	ctx, cancel := b.context(ctx)
	defer cancel()
	if s, ok := identity.(*profile.Signer); ok {
		op, close, e := s.OperationContext(ctx)
		if e != nil {
			return BalanceResult{}, e
		}
		defer close()
		d, ok := b.driver.(driver.Protocol)
		if !ok {
			return BalanceResult{}, driver.ErrDriverRequired
		}
		entry := tx.NewBookEntry(s, b.treasury, b.driver)
		notes, e := entry.Notes(op)
		if e != nil {
			return BalanceResult{}, e
		}
		v, sp, e := d.BalanceFor(op, s, notes)
		if e != nil {
			return BalanceResult{}, e
		}
		return BalanceResult{Address: &AddressBalance{new(big.Int).SetUint64(v), new(big.Int).SetUint64(sp)}}, nil
	}
	key, e := accountKey(identity)
	if e != nil {
		return BalanceResult{}, e
	}
	out, e := b.account(ctx, key)
	if e == nil {
		e = ctx.Err()
	}
	if e != nil {
		return BalanceResult{}, e
	}
	if out.Nonce == nil || out.Value == nil || !out.Nonce.IsUint64() || !out.Value.IsUint64() {
		return BalanceResult{}, fmt.Errorf("bookkeeper: invalid account state")
	}
	out.Nonce = new(big.Int).Set(out.Nonce)
	out.Value = new(big.Int).Set(out.Value)
	return BalanceResult{Account: &out}, nil
}
func accountKey(identity any) ([]byte, error) {
	var key []byte
	switch v := identity.(type) {
	case *profile.Signer:
		if v != nil && v.Profile() != nil {
			key = v.Account().Bytes()
		}
	case *profile.Profile:
		if v != nil {
			key = v.Account().Bytes()
		}
	case *profile.Key:
		if v != nil {
			key = v.Bytes()
		}
	case string:
		var e error
		key, e = profile.DecodeKey(v)
		if e != nil {
			return nil, e
		}
	case []byte:
		key = append([]byte(nil), v...)
	default:
		return nil, fmt.Errorf("bookkeeper: account identifier required")
	}
	if len(key) == 64 {
		return nil, profile.ErrSignerRequired
	}
	if len(key) != 96 {
		return nil, fmt.Errorf("bookkeeper: invalid account")
	}
	return key, nil
}
func (b *Bookkeeper) account(ctx context.Context, key []byte) (tx.AccountBalance, error) {
	switch t := b.treasury.(type) {
	case ContextTreasury:
		return t.AccountBalanceContext(ctx, key)
	case Treasury:
		if e := ctx.Err(); e != nil {
			return tx.AccountBalance{}, e
		}
		out, e := t.AccountBalance(key)
		if e == nil {
			e = ctx.Err()
		}
		return out, e
	}
	if b.network != nil {
		if d, ok := b.driver.(network.AccountCodec); ok {
			return network.NewAccountSyncer(b.network, d).Balance(ctx, key)
		}
	}
	return tx.AccountBalance{}, fmt.Errorf("bookkeeper: account treasury required")
}
func (b *Bookkeeper) MinimumStake() (*big.Int, error) { return b.GetMinimumStake(context.Background()) }
func (b *Bookkeeper) GetMinimumStake(ctx context.Context) (*big.Int, error) {
	ctx, cancel := b.context(ctx)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if b.driver == nil {
		return nil, driver.ErrDriverRequired
	}
	if d, ok := b.driver.(driver.Protocol); ok {
		return d.MinimumStakeContext(ctx)
	}
	out, e := b.driver.MinimumStake()
	if e == nil {
		e = ctx.Err()
	}
	return out, e
}
func (b *Bookkeeper) As(identity any) (*tx.BookEntry, error) {
	entry := tx.NewBookEntry(identity, b.treasury, b.driver)
	if entry.Profile() == nil {
		return nil, fmt.Errorf("bookkeeper: profile or signer required")
	}
	return entry, nil
}
func (b *Bookkeeper) Pick(ctx context.Context, s *profile.Signer, amount *big.Int) ([]driver.Note, error) {
	if amount == nil || !amount.IsUint64() {
		return nil, fmt.Errorf("bookkeeper: pick amount must be u64")
	}
	value := amount.Uint64()
	ctx, cancel := b.context(ctx)
	defer cancel()
	ctx, close, e := s.OperationContext(ctx)
	if e != nil {
		return nil, e
	}
	defer close()
	d, ok := b.driver.(driver.Protocol)
	if !ok {
		return nil, driver.ErrDriverRequired
	}
	notes, e := tx.NewBookEntry(s, b.treasury, b.driver).Notes(ctx)
	if e != nil {
		return nil, e
	}
	_, sp, e := d.BalanceFor(ctx, s, notes)
	if e != nil {
		return nil, e
	}
	if sp < value {
		return nil, fmt.Errorf("bookkeeper: insufficient spendable funds")
	}
	return d.PickFor(ctx, s, notes, value)
}
func (b *Bookkeeper) StakeInfo(ctx context.Context, identity any) (out tx.StakeInfo, err error) {
	ctx, cancel := b.context(ctx)
	defer cancel()
	defer func() {
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			out = tx.StakeInfo{}
		} else {
			out = tx.CloneStakeInfo(out)
		}
	}()
	key, e := accountKey(identity)
	if e != nil {
		return tx.StakeInfo{}, e
	}
	switch t := b.treasury.(type) {
	case ContextTreasury:
		return t.StakeInfoContext(ctx, key)
	case Treasury:
		if e = ctx.Err(); e != nil {
			return tx.StakeInfo{}, e
		}
		v, e := t.StakeInfo(key)
		if e == nil {
			e = ctx.Err()
		}
		return v, e
	}
	if b.network != nil {
		if d, ok := b.driver.(network.AccountCodec); ok {
			return network.NewAccountSyncer(b.network, d).Stake(ctx, key)
		}
	}
	return tx.StakeInfo{}, fmt.Errorf("bookkeeper: stake treasury required")
}
func (b *Bookkeeper) StakeKeys(ctx context.Context, identity any) (out *network.StakeKeys, err error) {
	ctx, cancel := b.context(ctx)
	defer cancel()
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
	key, e := accountKey(identity)
	if e != nil {
		return nil, e
	}
	if t, ok := b.treasury.(tx.StakeKeySource); ok {
		return t.StakeKeysContext(ctx, key)
	}
	if b.network != nil {
		if d, ok := b.driver.(network.AccountCodec); ok {
			out, e := network.NewAccountSyncer(b.network, d).StakeKeys(ctx, [][]byte{key})
			if e != nil {
				return nil, e
			}
			return out[0], nil
		}
	}
	return nil, fmt.Errorf("bookkeeper: stake owner lookup requires a provider")
}
func (b *Bookkeeper) StakeOwner(ctx context.Context, identity any) (*network.StakeOwner, error) {
	keys, e := b.StakeKeys(ctx, identity)
	if e != nil || keys == nil {
		return nil, e
	}
	return &keys.Owner, nil
}
