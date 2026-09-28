package tx

import (
	"math/big"
	"testing"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/profile"
)

// mockDriver implements the driver.Driver interface for the account flow.
type mockDriver struct {
	gotNonce  *big.Int
	gotChain  int
	called    bool
	receivers [][]byte
}

func (m *mockDriver) GenerateProfile(seed []byte, index profile.ProfileIndex) ([]byte, error) {
	buf := make([]byte, profile.ProfileLen)
	// Place the seed at the start and fill the remainder deterministically.
	copy(buf, seed)
	for i := 64; i < len(buf); i++ {
		buf[i] = byte(i)
	}
	return buf, nil
}

func (m *mockDriver) Balance(seed []byte, index profile.ProfileIndex, notes []driver.Note) (*big.Int, *big.Int, error) {
	return big.NewInt(0), big.NewInt(0), nil
}

func (m *mockDriver) PickNotes(seed []byte, index profile.ProfileIndex, notes []driver.Note, value *big.Int) ([]driver.Note, error) {
	return nil, nil
}

func (m *mockDriver) Moonlight(in driver.MoonlightInput) ([]byte, string, error) {
	m.called = true
	m.gotNonce = in.Nonce
	m.gotChain = in.ChainID
	m.receivers = append(m.receivers, append([]byte(nil), in.Receiver...))
	return []byte{0xde, 0xad}, "0x" + in.Nonce.String(), nil
}

func (m *mockDriver) Phoenix(in driver.PhoenixInput) ([]byte, []byte, error) {
	return nil, nil, nil
}

func (m *mockDriver) IntoProven(tx, proof []byte) ([]byte, string, error) {
	return nil, "", nil
}

func (m *mockDriver) MinimumStake() (*big.Int, error) {
	return big.NewInt(1000000), nil
}

// mockTreasury returns a fixed balance.
type mockTreasury struct{}

func (mockTreasury) AccountBalance(account []byte) (AccountBalance, error) {
	return AccountBalance{Nonce: big.NewInt(7), Value: big.NewInt(100)}, nil
}

func (mockTreasury) AddressNotes(address []byte) ([]driver.Note, error) {
	return nil, nil
}

func (mockTreasury) StakeInfo(account []byte) (StakeInfo, error) {
	return StakeInfo{}, nil
}

func TestAccountTransferBuild(t *testing.T) {
	md := &mockDriver{}
	entry := NewBookEntry(mustProfile(t), mockTreasury{}, md)

	tr := NewTransfer(entry).
		Amount(big.NewInt(5)).
		Chain(0x2a)

	routed, err := tr.To(receiverAccountKey(t))
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
	if !md.called {
		t.Fatal("driver.Moonlight not called")
	}
	// nonce should be 7 + 1 = 8
	if md.gotNonce.Cmp(big.NewInt(8)) != 0 {
		t.Errorf("nonce = %s, want 8", md.gotNonce)
	}
	if md.gotChain != 0x2a {
		t.Errorf("chain = %x, want 2a", md.gotChain)
	}
	if res.ChainID != 0x2a {
		t.Errorf("result chain = %d, want 42", res.ChainID)
	}
}

// TestRoutedTransferIndependence checks that routing one base transfer to two
// different accounts keeps each build on its own receiver despite shared state.
func TestRoutedTransferIndependence(t *testing.T) {
	md := &mockDriver{}
	entry := NewBookEntry(mustProfile(t), mockTreasury{}, md)

	base := NewTransfer(entry).Amount(big.NewInt(5)).Chain(0x2a)

	first, err := base.To(receiverAccountKey(t))
	if err != nil {
		t.Fatal(err)
	}
	second, err := base.To(secondReceiverAccountKey(t))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := first.Account.Build(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Account.Build(); err != nil {
		t.Fatal(err)
	}

	if len(md.receivers) != 2 {
		t.Fatalf("expected 2 builds, got %d", len(md.receivers))
	}
	firstKey := decodeAccountKey(t, receiverAccountKey(t))
	secondKey := decodeAccountKey(t, secondReceiverAccountKey(t))
	if !bytesEqual(md.receivers[0], firstKey) {
		t.Fatal("first route signed to the wrong receiver")
	}
	if !bytesEqual(md.receivers[1], secondKey) {
		t.Fatal("second route signed to the wrong receiver")
	}
}

func secondReceiverAccountKey(t *testing.T) string {
	t.Helper()
	buf := make([]byte, profile.AccountKeyLen)
	for i := range buf {
		buf[i] = byte(0x40 | (i % 32))
	}
	return profile.EncodeAccount(buf)
}

func decodeAccountKey(t *testing.T, s string) []byte {
	t.Helper()
	b, err := profile.DecodeKey(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
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

func TestAccountTransferRequiresDriver(t *testing.T) {
	entry := NewBookEntry(mustProfile(t), mockTreasury{}, nil)
	tr := NewTransfer(entry).Amount(big.NewInt(1)).Chain(1)
	routed, err := tr.To(receiverAccountKey(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = routed.Account.Build()
	if err == nil {
		t.Fatal("expected error with nil driver")
	}
	if !IsDriverRequired(err) {
		t.Errorf("expected driver-required error, got: %v", err)
	}
}

func TestTransferToRejectsInvalid(t *testing.T) {
	entry := NewBookEntry(mustProfile(t), mockTreasury{}, nil)
	tr := NewTransfer(entry).Amount(big.NewInt(1))
	if _, err := tr.To("not-a-key"); err == nil {
		t.Fatal("expected error for invalid identifier")
	}
}

func mustProfile(t *testing.T) *profile.Profile {
	t.Helper()
	md := &mockDriver{}
	seed := make([]byte, 64)
	for i := range seed {
		seed[i] = byte(i*3 + 1)
	}
	gen := profile.NewGenerator(func() ([]byte, error) { return seed, nil }, md)
	p, err := gen.Default()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestAccountTransferRequiresSeed verifies that a profile parsed from public
// keys alone (no seed) cannot silently become a signer using its own public
// account key as seed material.
func TestAccountTransferRequiresSeed(t *testing.T) {
	md := &mockDriver{}
	entry := NewBookEntry(seedlessProfile(t), mockTreasury{}, md)
	tr := NewTransfer(entry).Amount(big.NewInt(1)).Chain(1)
	routed, err := tr.To(receiverAccountKey(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = routed.Account.Build()
	if err == nil {
		t.Fatal("expected error signing with a seedless profile")
	}
	if md.called {
		t.Fatal("driver.Moonlight was called for a seedless profile")
	}
}

// seedlessProfile builds a profile parsed purely from public bytes.
func seedlessProfile(t *testing.T) *profile.Profile {
	t.Helper()
	buf := make([]byte, profile.ProfileLen)
	for i := range buf {
		buf[i] = byte(i)
	}
	p, err := profile.Parse(buf)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func receiverAccountKey(t *testing.T) string {
	t.Helper()
	buf := make([]byte, profile.AccountKeyLen)
	for i := range buf {
		buf[i] = byte(0xff - i)
	}
	return profile.EncodeAccount(buf)
}
