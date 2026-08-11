package bookmark

import (
	"math/big"
	"testing"
)

func TestNewRejectsWrongLength(t *testing.T) {
	if _, err := New([]byte{0x00}); err == nil {
		t.Fatal("expected error for wrong length")
	}
}

func TestFromBigInt(t *testing.T) {
	b, err := From(big.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	if b.AsUint().Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("AsUint = %s, want 1", b.AsUint())
	}
}

func TestIsNone(t *testing.T) {
	none, err := New([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	if err != nil {
		t.Fatal(err)
	}
	if !none.IsNone() {
		t.Fatal("expected none bookmark")
	}

	zero, _ := New([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	if zero.IsNone() {
		t.Fatal("expected zero bookmark to not be none")
	}
}

func TestString(t *testing.T) {
	b, _ := New([]byte{0xde, 0xad, 0xbe, 0xef, 0, 0, 0, 0})
	if got := b.String(); got != "deadbeef00000000" {
		t.Errorf("String = %q, want %q", got, "deadbeef00000000")
	}
}
