package walletcore

import (
	"math/big"
	"testing"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/profile"
	"github.com/HDauven/w3sper.go/tx"
)

// integrationTreasury returns a fixed on-chain state for the test profile.
type integrationTreasury struct {
	nonce *big.Int
	value *big.Int
}

func (t *integrationTreasury) AccountBalance(account []byte) (tx.AccountBalance, error) {
	return tx.AccountBalance{Nonce: t.nonce, Value: t.value}, nil
}

func (t *integrationTreasury) AddressNotes(address []byte) ([]driver.Note, error) {
	return nil, nil
}

func (t *integrationTreasury) StakeInfo(account []byte) (tx.StakeInfo, error) {
	return tx.StakeInfo{}, nil
}

// TestEndToEndMoonlightTransfer derives a profile, builds an account transfer
// through the high-level tx builder, and signs it with the real wallet-core
// WASM driver. This exercises the full pure-Go orchestration + WASM crypto.
func TestEndToEndMoonlightTransfer(t *testing.T) {
	w := testWallet(t)

	seed := testSeed()
	gen := profile.NewGenerator(func() ([]byte, error) { return seed, nil }, w)
	sender, err := gen.Default()
	if err != nil {
		t.Fatal(err)
	}

	// Receiver: a second profile's account key.
	receiverProf, err := gen.Next()
	if err != nil {
		t.Fatal(err)
	}
	receiver := receiverProf.Account().String() // base58 account key

	treasury := &integrationTreasury{nonce: big.NewInt(5), value: big.NewInt(1_000_000_000_000)}
	entry := tx.NewBookEntry(sender, treasury, w)

	tr := tx.NewTransfer(entry).
		Amount(big.NewInt(1000000000)).
		Chain(0x2a)

	routed, err := tr.To(receiver)
	if err != nil {
		t.Fatal(err)
	}
	if routed.Account == nil {
		t.Fatal("expected account transfer")
	}

	res, err := routed.Account.Build()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Buffer) == 0 {
		t.Fatal("empty transaction buffer")
	}
	if res.ChainID != 0x2a {
		t.Errorf("chain id = %d, want 42", res.ChainID)
	}
	if res.Nonce.String() != "6" {
		t.Errorf("nonce = %s, want 6", res.Nonce)
	}
	if res.Hash == "" {
		t.Fatal("empty hash")
	}
	t.Logf("built %d-byte account tx, nonce=%s hash=%s", len(res.Buffer), res.Nonce, res.Hash)
}
