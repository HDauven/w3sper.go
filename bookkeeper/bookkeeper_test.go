package bookkeeper

import (
	"errors"
	"math/big"
	"testing"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/profile"
	"github.com/HDauven/w3sper.go/tx"
)

type fakeTreasury struct{}

func (fakeTreasury) AccountBalance(account []byte) (tx.AccountBalance, error) {
	return tx.AccountBalance{Nonce: big.NewInt(3), Value: big.NewInt(50)}, nil
}

func (fakeTreasury) AddressNotes(address []byte) ([]driver.Note, error) {
	return []driver.Note{{Nullifier: []byte("n1"), Value: []byte("v1")}}, nil
}

func (fakeTreasury) StakeInfo(account []byte) (tx.StakeInfo, error) {
	return tx.StakeInfo{}, nil
}

type fakeDriver struct{}

func (fakeDriver) GenerateProfile(seed []byte, index profile.ProfileIndex) ([]byte, error) {
	return make([]byte, profile.ProfileLen), nil
}

func (fakeDriver) Balance(seed []byte, index profile.ProfileIndex, notes []driver.Note) (*big.Int, *big.Int, error) {
	return big.NewInt(10), big.NewInt(4), nil
}

func (fakeDriver) PickNotes(seed []byte, index profile.ProfileIndex, notes []driver.Note, value *big.Int) ([]driver.Note, error) {
	return nil, nil
}

func (fakeDriver) Moonlight(in driver.MoonlightInput) ([]byte, string, error) {
	return nil, "", nil
}

func (fakeDriver) Phoenix(in driver.PhoenixInput) ([]byte, []byte, error) {
	return nil, nil, nil
}

func (fakeDriver) IntoProven(tx, proof []byte) ([]byte, string, error) {
	return nil, "", nil
}

func (fakeDriver) MinimumStake() (*big.Int, error) {
	return big.NewInt(1000000), nil
}

func TestBookkeeperAccountBalance(t *testing.T) {
	b := New(fakeTreasury{}, nil)
	bal, err := b.Balance(testAccountKey(t))
	if err != nil {
		t.Fatal(err)
	}
	if bal.AccountBalance() == nil {
		t.Fatal("expected account balance")
	}
	if bal.AccountBalance().Nonce.Cmp(big.NewInt(3)) != 0 {
		t.Errorf("nonce = %s, want 3", bal.AccountBalance().Nonce)
	}
}

func TestBookkeeperAddressBalanceUnavailable(t *testing.T) {
	b := New(fakeTreasury{}, &fakeDriver{})
	_, err := b.Balance(addressKey(t))
	if err == nil {
		t.Fatal("expected error for Phoenix address balance")
	}
	if !errors.Is(err, ErrPhoenixUnavailable) {
		t.Fatalf("expected ErrPhoenixUnavailable, got %v", err)
	}
}

func TestBookkeeperMinimumStake(t *testing.T) {
	b := New(fakeTreasury{}, fakeDriver{})
	v, err := b.MinimumStake()
	if err != nil {
		t.Fatal(err)
	}
	if v.Cmp(big.NewInt(1000000)) != 0 {
		t.Errorf("min stake = %s, want 1000000", v)
	}
}

func testAccountKey(t *testing.T) string {
	t.Helper()
	return profile.EncodeAccount(make([]byte, profile.AccountKeyLen))
}

func addressKey(t *testing.T) string {
	t.Helper()
	return profile.EncodeAccount(make([]byte, profile.AddressKeyLen))
}
