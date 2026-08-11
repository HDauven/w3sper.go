package base58

import "testing"

func TestEncodeDecodeRoundTrip(t *testing.T) {
	vectors := [][]byte{
		{},
		{0x00},
		{0x00, 0x00, 0x00},
		{0xff},
		[]byte("hello"),
		[]byte("The quick brown fox jumps over the lazy dog"),
		{0xAA, 0xBB, 0xCC, 0xDD, 0xEE},
	}

	for _, in := range vectors {
		enc := Encode(in)
		dec, err := Decode(enc)
		if err != nil {
			t.Fatalf("Decode(%q) error: %v", enc, err)
		}
		if len(in) != len(dec) {
			t.Fatalf("round trip length mismatch: %d vs %d", len(in), len(dec))
		}
		for i := range in {
			if in[i] != dec[i] {
				t.Fatalf("round trip byte mismatch at %d", i)
			}
		}
	}
}

func TestEncodeKnownVectors(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte("Hello World!"), "2NEpo7TZRRrLZSi2U"},
		{[]byte{0x00}, "1"},
		{[]byte{0x00, 0x00, 0x00, 0x00}, "1111"},
	}
	for _, tc := range cases {
		if got := Encode(tc.in); got != tc.want {
			t.Errorf("Encode(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDecodeInvalidChar(t *testing.T) {
	if _, err := Decode("0OOIl"); err == nil {
		t.Fatal("expected error for invalid base58 characters")
	}
}
