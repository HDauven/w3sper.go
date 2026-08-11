package walletcore

import (
	"context"
	"math/big"
	"testing"
)

func TestLoadGlobals(t *testing.T) {
	ctx := context.Background()
	w, err := Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(ctx)

	g := w.Globals()
	if g.KeySize != 32 {
		t.Errorf("KeySize = %d, want 32", g.KeySize)
	}
	if g.ItemSize == 0 {
		t.Errorf("ItemSize = 0, want non-zero (%d)", g.ItemSize)
	}
	if g.MinStake == 0 {
		t.Errorf("MinStake = 0, want non-zero")
	}
	t.Logf("KeySize=%d ItemSize=%d MinStake=%d HeapBase=%d",
		g.KeySize, g.ItemSize, g.MinStake, g.HeapBase)
}

func TestMinimumStake(t *testing.T) {
	ctx := context.Background()
	w, err := Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(ctx)

	v, err := w.MinimumStake()
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).SetUint64(w.glob.MinStake)
	if v.Cmp(want) != 0 {
		t.Errorf("MinimumStake = %s, want %s", v, want)
	}
}

func TestGenerateProfile(t *testing.T) {
	ctx := context.Background()
	w, err := Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(ctx)

	seed := make([]byte, 64)
	// deterministic, non-trivial seed
	for i := range seed {
		seed[i] = byte(i * 7)
	}

	out, err := w.GenerateProfile(seed, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 160 {
		t.Fatalf("profile length = %d, want 160", len(out))
	}

	// The address (first 64) and account (last 96) must be non-trivial.
	if allZero(out[:64]) {
		t.Fatal("address key is all zeros")
	}
	if allZero(out[64:]) {
		t.Fatal("account key is all zeros")
	}

	// Same seed+index must be deterministic.
	out2, err := w.GenerateProfile(seed, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := range out {
		if out[i] != out2[i] {
			t.Fatal("profile derivation is not deterministic")
		}
	}

	t.Logf("profile addr=%x... acct=%x...", out[:8], out[64:72])
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
