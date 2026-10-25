package tx_test

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/profile"
	"github.com/HDauven/w3sper.go/tx"
	"github.com/HDauven/w3sper.go/walletcore"
)

type snapshotTreasury struct{}

func (snapshotTreasury) AccountBalance([]byte) (tx.AccountBalance, error) {
	return tx.AccountBalance{Nonce: big.NewInt(7), Value: big.NewInt(100000000000000)}, nil
}
func (snapshotTreasury) AddressNotes([]byte) ([]driver.Note, error) { return nil, nil }
func (snapshotTreasury) StakeInfo([]byte) (tx.StakeInfo, error) {
	return tx.StakeInfo{Reward: big.NewInt(0)}, nil
}
func setup(t *testing.T) (*walletcore.WalletCore, *profile.Generator, *profile.Signer) {
	t.Helper()
	ctx := context.Background()
	w, e := walletcore.Load(ctx)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { w.Close(ctx) })
	seed := make([]byte, 64)
	for i := range seed {
		seed[i] = byte(i)
	}
	g := profile.NewGenerator(func() ([]byte, error) { return seed, nil }, w)
	s, e := g.DefaultSigner(ctx)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Dispose)
	return w, g, s
}
func TestAccountTransferBuild(t *testing.T) {
	w, g, s := setup(t)
	receiver, e := g.Next()
	if e != nil {
		t.Fatal(e)
	}
	entry := tx.NewBookEntry(s, snapshotTreasury{}, w)
	route, e := entry.Transfer(big.NewInt(5)).Chain(42).To(receiver.Account().String())
	if e != nil {
		t.Fatal(e)
	}
	r, e := route.Account.BuildContext(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	if r.Nonce.Uint64() != 8 || r.ChainID != 42 || len(r.Hash) != 64 || len(r.Buffer) == 0 {
		t.Fatalf("bad result: %+v", r)
	}
}
func TestRoutedTransferIndependence(t *testing.T) {
	w, g, s := setup(t)
	a, _ := g.Derive(1)
	b, _ := g.Derive(2)
	amount := big.NewInt(5)
	entry := tx.NewBookEntry(s, snapshotTreasury{}, w)
	base := entry.Transfer(amount).Chain(42)
	first, _ := base.To(a.Account().String())
	second, _ := base.To(b.Account().String())
	amount.SetInt64(99)
	base.Amount(big.NewInt(200))
	one, e := first.Account.Build()
	if e != nil {
		t.Fatal(e)
	}
	two, e := second.Account.Build()
	if e != nil {
		t.Fatal(e)
	}
	if one.Hash == two.Hash || !bytes.Contains(one.Buffer, a.Account().Bytes()) || !bytes.Contains(two.Buffer, b.Account().Bytes()) {
		t.Fatal("routes alias")
	}
}
func TestPublicProfileCannotSign(t *testing.T) {
	w, g, _ := setup(t)
	p, _ := g.Default()
	route, _ := tx.NewTransfer(tx.NewBookEntry(p, snapshotTreasury{}, w)).Chain(42).Amount(big.NewInt(1)).To(p.Account().String())
	if _, e := route.Account.Build(); e == nil {
		t.Fatal("public profile signed")
	}
}
func TestAccountTransferRequiresDriver(t *testing.T) {
	_, _, s := setup(t)
	route, _ := tx.NewTransfer(tx.NewBookEntry(s, snapshotTreasury{}, nil)).Chain(42).To(s.Account().String())
	if _, e := route.Account.Build(); !tx.IsDriverRequired(e) {
		t.Fatalf("wrong error %v", e)
	}
}
func TestTransferValidation(t *testing.T) {
	w, _, s := setup(t)
	entry := tx.NewBookEntry(s, snapshotTreasury{}, w)
	if _, e := entry.Transfer(big.NewInt(1)).To("not-a-key"); e == nil {
		t.Fatal("invalid receiver accepted")
	}
	for _, b := range []*tx.Builder{entry.Transfer(big.NewInt(-1)).Chain(42), entry.Transfer(nil).Chain(42), entry.Transfer(big.NewInt(1)).Chain(256), entry.Transfer(big.NewInt(1)).Chain(42).Gas(tx.Gas(big.NewInt(0), nil)), entry.Transfer(big.NewInt(1)).Chain(42).Nonce(new(big.Int).SetUint64(^uint64(0)))} {
		route, _ := b.To(s.Account().String())
		if _, e := route.Account.Build(); e == nil {
			t.Fatal("invalid transfer accepted")
		}
	}
}
func TestCommittedRestrictions(t *testing.T) {
	w, g, s := setup(t)
	other, _ := g.Derive(1)
	entry := tx.NewBookEntry(s, snapshotTreasury{}, w)
	id := make([]byte, 32)
	id[0] = 1
	for _, b := range []*tx.Builder{entry.Transfer(big.NewInt(1)).ContractCall(driver.Call{ContractID: id, Function: "noop"}).Committed(), entry.Transfer(big.NewInt(0)).Memo([]byte("memo")).Committed(), entry.Transfer(big.NewInt(0)).Deposit(big.NewInt(1)).ContractCall(driver.Call{ContractID: id, Function: "noop"}).Committed()} {
		route, _ := b.Chain(42).To(s.Account().String())
		if _, e := route.Account.Build(); e == nil {
			t.Fatal("invalid committed call accepted")
		}
	}
	route, _ := entry.Transfer(big.NewInt(0)).Chain(42).ContractCall(driver.Call{ContractID: id, Function: "noop"}).Committed().To(other.Account().String())
	if _, e := route.Account.Build(); e == nil {
		t.Fatal("committed foreign envelope accepted")
	}
}
func TestDisposedSignerAndCanceledBuild(t *testing.T) {
	w, _, s := setup(t)
	route, _ := tx.NewBookEntry(s, snapshotTreasury{}, w).Transfer(big.NewInt(1)).Chain(42).To(s.Account().String())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := route.Account.BuildContext(ctx, nil); e == nil {
		t.Fatal("canceled build succeeded")
	}
	s.Dispose()
	if _, e := route.Account.Build(); e == nil {
		t.Fatal("disposed signer signed")
	}
}
