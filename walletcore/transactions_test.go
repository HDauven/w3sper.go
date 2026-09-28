package walletcore

import (
	"context"
	"math/big"
	"testing"

	"github.com/HDauven/w3sper.go/driver"
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

func TestGenerateProfileReturnsOwnedCopy(t *testing.T) {
	w := testWallet(t)

	first, err := w.GenerateProfile(testSeed(), 0)
	if err != nil {
		t.Fatal(err)
	}
	expected := cloneBytes(first)

	// Deriving a second profile must not mutate the first one's bytes, even
	// though the WASM allocation behind `first` is released and reused.
	if _, err := w.GenerateProfile(testSeed(), 1); err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(first, expected) {
		t.Fatal("returned profile aliases reusable WASM memory")
	}

	// And the second profile must differ from the first (ownership is a copy).
	second, err := w.GenerateProfile(testSeed(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if bytesEqual(first, second) {
		t.Fatal("profiles at different indexes are identical (data not copied)")
	}
}

func TestMoonlightSignsTransaction(t *testing.T) {
	w := testWallet(t)

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
		TransferValue: big.NewInt(1000000000),
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

// TestBalanceTolerantOfUnexpectedNote runs wallet-core's `balance` against a
// synthetic note to confirm the FFI call path (Argument marshalling, output
// parsing) behaves without error. A zero-filled note is not a valid encrypted
// note, so the returned values are not meaningful; the point is that the ABI
// call itself succeeds (balance uses calculate_unchecked).
func TestBalanceTolerantOfUnexpectedNote(t *testing.T) {
	w := testWallet(t)

	seed := testSeed()
	note := driver.Note{
		Nullifier: make([]byte, w.glob.KeySize),
		Value:     make([]byte, w.glob.ItemSize),
	}

	value, spendable, err := w.Balance(seed, 0, []driver.Note{note})
	if err != nil {
		t.Fatal(err)
	}
	if value.Sign() < 0 || spendable.Sign() < 0 {
		t.Fatalf("negative balance returned: %s %s", value, spendable)
	}
	t.Logf("balance value=%s spendable=%s", value, spendable)
}

// TestPickNotesValidation covers the input validation surface of `pick_notes`
// that does not require valid encrypted note fixtures. Actual note selection
// needs real note data (see the review / integration roadmap).
func TestPickNotesValidation(t *testing.T) {
	w := testWallet(t)
	seed := testSeed()

	// Empty note list returns empty without invoking the WASM.
	picked, err := w.PickNotes(seed, 0, nil, big.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	if len(picked) != 0 {
		t.Fatalf("expected empty pick, got %d", len(picked))
	}

	// A negative value is rejected before touching the WASM.
	note := driver.Note{
		Nullifier: make([]byte, w.glob.KeySize),
		Value:     make([]byte, w.glob.ItemSize),
	}
	if _, err := w.PickNotes(seed, 0, []driver.Note{note}, big.NewInt(-1)); err == nil {
		t.Fatal("expected error for negative value")
	}

	// A nil value is rejected.
	if _, err := w.PickNotes(seed, 0, []driver.Note{note}, nil); err == nil {
		t.Fatal("expected error for nil value")
	}

	// A short seed is rejected before reaching the WASM.
	if _, err := w.PickNotes(make([]byte, 4), 0, []driver.Note{note}, big.NewInt(1)); err == nil {
		t.Fatal("expected error for short seed")
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
