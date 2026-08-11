package walletcore

import (
	"context"
	"math/big"
	"testing"

	"github.com/dusk-network/w3sper-go/driver"
)

// testWallet loads a fresh wallet-core instance for a test.
func testWallet(t *testing.T) *WalletCore {
	t.Helper()
	w, err := Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close(context.Background()) })
	return w
}

func testSeed() []byte {
	seed := make([]byte, 64)
	for i := range seed {
		seed[i] = byte(i*7 + 1)
	}
	return seed
}

func TestMoonlightSignsTransaction(t *testing.T) {
	w := testWallet(t)

	// Derive a profile so moonlight has a valid sender.
	profile, err := w.GenerateProfile(testSeed(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(profile) != 160 {
		t.Fatalf("profile len %d", len(profile))
	}

	extra, err := w.GenerateProfile(testSeed(), 1)
	if err != nil {
		t.Fatal(err)
	}
	receiver := extra[64:160] // account (BLS) key, 96 bytes

	in := driver.MoonlightInput{
		Seed:          testSeed(),
		SenderIndex:   0,
		Receiver:      receiver,
		TransferValue: big.NewInt(1000000000), // 1 Dusk
		Deposit:       big.NewInt(0),
		GasLimit:      big.NewInt(2900000000),
		GasPrice:      big.NewInt(1),
		Nonce:         big.NewInt(1),
		ChainID:       0x2a,
	}

	tx, hash, err := w.Moonlight(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(tx) == 0 {
		t.Fatal("empty transaction")
	}
	if hash == "" {
		t.Fatal("empty hash")
	}
	t.Logf("tx bytes=%d hash=%s", len(tx), hash)
}

func TestBalanceAndPickNotes(t *testing.T) {
	w := testWallet(t)

	seed := testSeed()
	// Generate a note by deriving a profile and constructing a minimal note.
	profile, err := w.GenerateProfile(seed, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = profile

	// Build a single note of the correct item size.
	itemSize := w.glob.ItemSize
	note := driver.Note{
		Nullifier: make([]byte, w.glob.KeySize),
		Value:     make([]byte, itemSize),
	}
	notes := []driver.Note{note}

	value, spendable, err := w.Balance(seed, 0, notes)
	if err != nil {
		t.Fatal(err)
	}
	_ = value
	_ = spendable
	t.Logf("balance value=%s spendable=%s", value, spendable)

	picked, err := w.PickNotes(seed, 0, notes, big.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("picked=%d", len(picked))
}
