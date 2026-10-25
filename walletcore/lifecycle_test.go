package walletcore

import (
	"bytes"
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/profile"
)

func TestSignerIssuanceDisposalAndCanceledAdmission(t *testing.T) {
	w := testWallet(t)
	seed := make([]byte, 64)
	g := profile.NewGenerator(func() ([]byte, error) { return seed, nil }, w)
	p, e := g.Default()
	if e != nil {
		t.Fatal(e)
	}
	s, e := g.Signer(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Dispose()
	other, e := g.Signer(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Dispose()
	original := p.Bytes()
	mutated := p.Bytes()
	clear(mutated)
	if !bytes.Equal(original, p.Bytes()) || p.Seed() != nil {
		t.Fatal("public identity ownership")
	}
	seed[0] = 1
	if _, e = g.Signer(context.Background(), p); e == nil {
		t.Fatal("changed provider identity accepted")
	}
	seed[0] = 0
	unrelated := profile.NewGenerator(func() ([]byte, error) { return seed, nil }, w)
	if _, e = unrelated.Signer(context.Background(), p); e == nil {
		t.Fatal("foreign identity issued")
	}
	<-w.gate
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.SignBLSMessage(ctx, []byte{0, 255}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := s.SignBLSMessage(context.Background(), []byte("queued")); done <- e }()
	s.Dispose()
	select {
	case e = <-done:
		if !errors.Is(e, profile.ErrDisposed) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("disposal didn't cancel queued signing")
	}
	w.gate <- struct{}{}
	if _, e = other.SignBLSMessage(context.Background(), []byte("independent")); e != nil {
		t.Fatal("sibling authority revoked", e)
	}
	if !bytes.Equal(p.Bytes(), s.Profile().Bytes()) {
		t.Fatal("disposal erased public identity")
	}
	if len(w.allocs) != 0 {
		t.Fatalf("host allocation leak: %v", w.allocs)
	}
}
func TestCoreNegativeBoundariesAndReuse(t *testing.T) {
	w := testWallet(t)
	_, ss := oracleSigners(t, w, loadOracle(t))
	ctx := context.Background()
	s := ss[0]
	valid := driver.TransactionInput{Signer: s, Receiver: ss[1].Account().Bytes(), Amount: 1, GasLimit: 100000000, GasPrice: 1, Nonce: 1, ChainID: 42}
	for _, change := range []func(*driver.TransactionInput){func(v *driver.TransactionInput) { v.Signer = nil }, func(v *driver.TransactionInput) { v.ChainID = 256 }, func(v *driver.TransactionInput) { v.GasPrice = 0 }, func(v *driver.TransactionInput) { v.GasLimit = math.MaxUint64; v.GasPrice = 2 }, func(v *driver.TransactionInput) { v.Receiver = make([]byte, 12) }, func(v *driver.TransactionInput) { v.Operation = 255 }, func(v *driver.TransactionInput) { v.Data = []byte{1} }, func(v *driver.TransactionInput) {
		v.Shielded = true
		v.Receiver = ss[1].Address().Bytes()
		v.Root = make([]byte, 32)
	}} {
		v := valid
		change(&v)
		if _, e := w.Build(ctx, v); e == nil {
			t.Fatal("invalid transaction accepted")
		}
	}
	for _, p := range []driver.Payload{{}, {Memo: []byte{}, Call: &driver.Call{}}, {Call: &driver.Call{ContractID: make([]byte, 32), Function: ""}}, {Deploy: &driver.Deployment{Owner: s.Account().Bytes()}}} {
		if _, e := w.CreateData(ctx, p); e == nil {
			t.Fatal("invalid payload accepted")
		}
	}
	if _, e := w.ProveTransaction(ctx, []byte{1, 0, 0, 0}, []byte{1}); e == nil {
		t.Fatal("malformed proof transaction accepted")
	}
	if _, _, e := w.MapOwned(ctx, ss, []byte{1}); e == nil {
		t.Fatal("truncated note accepted")
	}
	if _, e := w.AccountsIntoRaw(ctx, [][]byte{{1}}); e == nil {
		t.Fatal("short account accepted")
	}
	good, e := w.Build(ctx, valid)
	if e != nil || len(good.Buffer) == 0 {
		t.Fatal("negative validation poisoned WASM", e)
	}
	if len(w.allocs) != 0 {
		t.Fatal("allocation leak", w.allocs)
	}
	if e = w.Close(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SignBLSMessage(ctx, nil); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	if _, e = w.MinimumStake(); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	if e = w.Close(ctx); e != nil {
		t.Fatal(e)
	}
}
func TestConcurrentRealWASMCallsAndReturnedOwnership(t *testing.T) {
	w := testWallet(t)
	_, ss := oracleSigners(t, w, loadOracle(t))
	ctx := context.Background()
	want, e := ss[0].SignBLSMessage(ctx, []byte("concurrent"))
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			got, e := ss[0].SignBLSMessage(ctx, []byte("concurrent"))
			if e != nil || !bytes.Equal(got, want) {
				t.Errorf("signature %v", e)
			}
			clear(got)
		})
	}
	wg.Wait()
	if len(w.allocs) != 0 {
		t.Fatal("allocation leak")
	}
	again, e := ss[0].SignBLSMessage(ctx, []byte("concurrent"))
	if e != nil || !bytes.Equal(again, want) {
		t.Fatal("returned slice aliased WASM", e)
	}
}
func TestGeneratorContextProviderAndWaitingCancellation(t *testing.T) {
	w := testWallet(t)
	started := make(chan struct{})
	g := profile.NewGeneratorContext(func(ctx context.Context) ([]byte, error) { close(started); <-ctx.Done(); return nil, ctx.Err() }, w)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := g.DeriveContext(ctx, 0); done <- e }()
	<-started
	waiting, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if _, e := g.DeriveContext(waiting, 1); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if g.Len() != 0 {
		t.Fatal("cached canceled derivation")
	}
}
