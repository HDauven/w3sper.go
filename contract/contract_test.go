package contract

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/HDauven/w3sper.go/datadriver"
	"github.com/HDauven/w3sper.go/network"
	"github.com/HDauven/w3sper.go/profile"
	"github.com/HDauven/w3sper.go/tx"
	"github.com/HDauven/w3sper.go/walletcore"
)

func TestRealDriverFacadeReadsAndTransactions(t *testing.T) {
	ctx := context.Background()
	code, e := os.ReadFile("../testdata/transfer-driver.wasm")
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/on/contracts:"+network.TransferContract+"/chain_id" {
			t.Errorf("wrong path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte{42})
	}))
	defer server.Close()
	n, _ := network.New(server.URL)
	defer n.DataDrivers.Close(ctx)
	_ = n.DataDrivers.Register(network.TransferContract, datadriver.Bytes(code))
	core, e := walletcore.Load(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer core.Close(ctx)
	g := profile.NewGenerator(func() ([]byte, error) { return make([]byte, 64), nil }, core)
	signer, e := g.DefaultSigner(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer signer.Dispose()
	c, e := New(network.TransferContract, n, tx.NewBookEntry(signer, nil, core))
	if e != nil {
		t.Fatal(e)
	}
	value, e := c.Call(ctx, "chain_id", nil)
	if e != nil || string(value) != "42" {
		t.Fatal(string(value), e)
	}
	if _, e = c.Schema(ctx); e != nil {
		t.Fatal(e)
	}
	if v, e := c.Version(ctx); e != nil || v == "" {
		t.Fatal(v, e)
	}
	b, e := c.Transaction(ctx, "deposit", big.NewInt(2))
	if e != nil {
		t.Fatal(e)
	}
	route, e := b.Deposit(big.NewInt(2)).Nonce(big.NewInt(0)).Chain(42).To(signer.Account().String())
	if e != nil {
		t.Fatal(e)
	}
	r, e := route.Account.BuildContext(ctx, nil)
	if e != nil || len(r.Buffer) == 0 {
		t.Fatal(e)
	}
	public, e := New(network.TransferContract, n, tx.NewBookEntry(signer.Profile(), nil, core))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = public.Transaction(ctx, "deposit", big.NewInt(2)); !errors.Is(e, profile.ErrSignerRequired) {
		t.Fatal(e)
	}
}
func TestContractDecodeErrorsAndOwnership(t *testing.T) {
	ctx := context.Background()
	code, e := os.ReadFile("../testdata/transfer-driver.wasm")
	if e != nil {
		t.Fatal(e)
	}
	d, e := datadriver.Load(ctx, code)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close(ctx)
	c, e := New(network.TransferContract, nil, nil, d)
	if e != nil {
		t.Fatal(e)
	}
	payload := []byte(`{"value":"9007199254740993"}`)
	event := network.Event{Payload: payload, Headers: http.Header{"Content-Type": []string{"application/json"}}}
	out, e := c.decode(ctx, "deposit", event)
	if e != nil || !bytes.Equal(out, payload) {
		t.Fatal(string(out), e)
	}
	clear(out)
	if bytes.Equal(payload, out) {
		t.Fatal("event aliases input")
	}
	if _, e = c.decode(ctx, "deposit", network.Event{Payload: []byte{255, 0}}); e == nil {
		t.Fatal("malformed binary event accepted")
	}
	if _, e = c.Call(ctx, "chain_id", nil); e == nil {
		t.Fatal("missing network accepted")
	}
	if _, e = New("short", nil, nil, d); e == nil {
		t.Fatal("short contract id accepted")
	}
}
