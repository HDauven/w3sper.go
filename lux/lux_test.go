package lux

import (
	"math/big"
	"testing"
)

func TestFormatToDusk(t *testing.T) {
	cases := []struct {
		lux  string
		want string
	}{
		{"0", "0"},
		{"11000000000", "11"},
		{"11000000001", "11.000000001"},
		{"1000000000", "1"},
		{"1", "0.000000001"},
		{"18446744073709551615", "18446744073.709551615"},
	}

	for _, tc := range cases {
		in, _ := new(big.Int).SetString(tc.lux, 10)
		got, err := FormatToDusk(in)
		if err != nil {
			t.Fatalf("FormatToDusk(%s) unexpected error: %v", tc.lux, err)
		}
		if got != tc.want {
			t.Errorf("FormatToDusk(%s) = %q, want %q", tc.lux, got, tc.want)
		}
	}
}

func TestFormatToDuskRejectsNegative(t *testing.T) {
	_, err := FormatToDusk(big.NewInt(-1))
	if err == nil {
		t.Fatal("expected error for negative value")
	}
}

func TestParseFromDusk(t *testing.T) {
	cases := []struct {
		dusk string
		want string
	}{
		{"11", "11000000000"},
		{"11.000000001", "11000000001"},
		{"0", "0"},
		{"0.000000001", "1"},
		{"1", "1000000000"},
		{"1.1", "1100000000"},
		{"18446744073.709551615", "18446744073709551615"},
	}

	for _, tc := range cases {
		got, err := ParseFromDusk(tc.dusk)
		if err != nil {
			t.Fatalf("ParseFromDusk(%q) unexpected error: %v", tc.dusk, err)
		}
		if got.String() != tc.want {
			t.Errorf("ParseFromDusk(%q) = %s, want %s", tc.dusk, got, tc.want)
		}
	}
}

func TestParseFromDuskRejects(t *testing.T) {
	bad := []string{"", "abc", "1.0000000001", "-1", "1.2.3", "12a"}
	for _, s := range bad {
		if _, err := ParseFromDusk(s); err == nil {
			t.Errorf("ParseFromDusk(%q) expected error, got nil", s)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, s := range []string{"0", "1", "999", "11000000001", "18446744073709551615"} {
		lux, _ := new(big.Int).SetString(s, 10)
		dusk, err := FormatToDusk(lux)
		if err != nil {
			t.Fatal(err)
		}
		back, err := ParseFromDusk(dusk)
		if err != nil {
			t.Fatal(err)
		}
		if back.String() != s {
			t.Errorf("round trip %s -> %q -> %s", s, dusk, back)
		}
	}
}
