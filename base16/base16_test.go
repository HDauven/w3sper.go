package base16

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	vectors := [][]byte{
		{},
		{0x00, 0x01, 0xff},
		[]byte("abc123"),
		bytes.Repeat([]byte{0xAB}, 32),
	}
	for _, in := range vectors {
		enc := Encode(in)
		dec, err := Decode(enc)
		if err != nil {
			t.Fatalf("Decode(%q) error: %v", enc, err)
		}
		if !bytes.Equal(in, dec) {
			t.Fatalf("round trip mismatch: %x vs %x", in, dec)
		}
	}
}

func TestEncodeLowercase(t *testing.T) {
	if got := Encode([]byte{0xAB}); got != "ab" {
		t.Errorf("Encode = %q, want %q", got, "ab")
	}
}

func TestDecodeOddLength(t *testing.T) {
	if _, err := Decode("abc"); err == nil {
		t.Fatal("expected error for odd length")
	}
}
