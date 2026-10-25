package tx_test

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/network"
	"github.com/HDauven/w3sper.go/profile"
	"github.com/HDauven/w3sper.go/tx"
)

type blockingTreasury struct {
	entered, release chan struct{}
	stake            bool
}

func (b blockingTreasury) wait(ctx context.Context) error {
	close(b.entered)
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b blockingTreasury) AccountBalanceContext(ctx context.Context, key []byte) (tx.AccountBalance, error) {
	if e := b.wait(ctx); e != nil {
		return tx.AccountBalance{}, e
	}
	return snapshotTreasury{}.AccountBalance(key)
}
func (b blockingTreasury) AddressNotesContext(ctx context.Context, key []byte) ([]driver.Note, error) {
	return nil, ctx.Err()
}
func (b blockingTreasury) StakeInfoContext(ctx context.Context, key []byte) (tx.StakeInfo, error) {
	if b.stake {
		if e := b.wait(ctx); e != nil {
			return tx.StakeInfo{}, e
		}
	}
	return snapshotTreasury{}.StakeInfo(key)
}
func TestBuildSnapshotBeforeTreasuryAwait(t *testing.T) {
	w, _, s := setup(t)
	treasury := blockingTreasury{make(chan struct{}), make(chan struct{}), false}
	route, _ := tx.NewBookEntry(s, treasury, w).Transfer(big.NewInt(5)).Memo([]byte("before")).Chain(42).To(s.Account().String())
	builder := route.Account.Builder
	done := make(chan *tx.Result, 1)
	errs := make(chan error, 1)
	go func() { result, e := builder.BuildContext(context.Background(), nil); done <- result; errs <- e }()
	<-treasury.entered
	builder.Amount(big.NewInt(99)).Memo([]byte("after")).Chain(43)
	close(treasury.release)
	result := <-done
	if e := <-errs; e != nil {
		t.Fatal(e)
	}
	baseline, _ := tx.NewBookEntry(s, snapshotTreasury{}, w).Transfer(big.NewInt(5)).Memo([]byte("before")).Chain(42).To(s.Account().String())
	expected, e := baseline.Account.Build()
	if e != nil || result.Hash != expected.Hash {
		t.Fatal("in-flight builder wasn't snapshotted", e)
	}
}
func TestDisposeDuringTreasuryAndOwnerLookup(t *testing.T) {
	for _, owner := range []bool{false, true} {
		t.Run(map[bool]string{false: "sender", true: "owner"}[owner], func(t *testing.T) {
			w, g, s := setup(t)
			treasury := blockingTreasury{make(chan struct{}), make(chan struct{}), owner}
			entry := tx.NewBookEntry(s, treasury, w)
			route, _ := entry.Transfer(big.NewInt(1)).Chain(42).To(s.Account().String())
			b := route.Account.Builder
			authority := s
			if owner {
				p, _ := g.Derive(1)
				var e error
				authority, e = g.Signer(context.Background(), p)
				if e != nil {
					t.Fatal(e)
				}
				defer authority.Dispose()
				b = entry.Stake(big.NewInt(1000000000000)).Owner(authority).Chain(42)
			}
			done := make(chan error, 1)
			go func() { _, e := b.BuildContext(context.Background(), nil); done <- e }()
			<-treasury.entered
			authority.Dispose()
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("stale build succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("disposal did not cancel treasury I/O")
			}
		})
	}
}

type stakeTreasury struct {
	snapshotTreasury
	state tx.StakeInfo
	owner network.StakeOwner
}

func (s stakeTreasury) StakeInfo([]byte) (tx.StakeInfo, error) { return s.state, nil }
func (s stakeTreasury) StakeKeysContext(context.Context, []byte) (*network.StakeKeys, error) {
	return &network.StakeKeys{Owner: s.owner}, nil
}
func TestStakeOwnershipAndAmountValidation(t *testing.T) {
	w, g, s := setup(t)
	p, _ := g.Derive(1)
	owner, e := g.Signer(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	defer owner.Dispose()
	state := tx.StakeInfo{Amount: &tx.StakeAmount{Total: big.NewInt(2000000000000)}, Reward: big.NewInt(5)}
	entry := tx.NewBookEntry(s, stakeTreasury{state: state, owner: network.StakeOwner{Account: owner.Account().String()}}, w)
	for _, b := range []*tx.Builder{entry.Stake(big.NewInt(1000000000000)), entry.Topup(big.NewInt(0)), entry.Topup(big.NewInt(1)).Owner(s), entry.Unstake(big.NewInt(1000000000001)).Owner(owner), entry.Unstake(big.NewInt(3000000000000)).Owner(owner), entry.Withdraw(big.NewInt(6)).Owner(owner)} {
		if _, e := b.Chain(42).Build(); e == nil {
			t.Fatal("invalid stake operation accepted")
		}
	}
	contractEntry := tx.NewBookEntry(s, stakeTreasury{state: state, owner: network.StakeOwner{Contract: tx.TRANSFER}}, w)
	if _, e = contractEntry.Unstake(nil).Owner(owner).Chain(42).Build(); e == nil {
		t.Fatal("contract ownership bypass")
	}
	sameSeedDifferentSource := profile.NewGenerator(func() ([]byte, error) {
		seed := make([]byte, 64)
		for i := range seed {
			seed[i] = byte(i)
		}
		return seed, nil
	}, w)
	foreign, e := sameSeedDifferentSource.DefaultSigner(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer foreign.Dispose()
	if _, e = entry.Topup(big.NewInt(1)).Owner(foreign).Chain(42).Build(); e == nil {
		t.Fatal("foreign wallet authority accepted")
	}
	owner.Dispose()
	if _, e = entry.Topup(big.NewInt(1)).Owner(owner).Chain(42).Build(); !errors.Is(e, profile.ErrDisposed) {
		t.Fatal(e)
	}
}
func TestDeploymentPayloadConflicts(t *testing.T) {
	w, _, s := setup(t)
	entry := tx.NewBookEntry(s, snapshotTreasury{}, w)
	for _, b := range []*tx.Builder{entry.Deploy([]byte{1}).Memo([]byte("ignored?")), entry.Deploy([]byte{1}).Amount(big.NewInt(1)), entry.Deploy([]byte{1}).Deposit(big.NewInt(1))} {
		if _, e := b.Chain(42).Nonce(big.NewInt(0)).Build(); e == nil {
			t.Fatal("conflicting deployment fields silently discarded")
		}
	}
}
