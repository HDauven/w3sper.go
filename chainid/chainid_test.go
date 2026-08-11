package chainid

import "testing"

func TestValidate(t *testing.T) {
	valid := []any{0, 1, 42, 255, int64(128), uint(7), uint64(255)}
	for _, v := range valid {
		got, err := Validate(v)
		if err != nil {
			t.Errorf("Validate(%v) unexpected error: %v", v, err)
		}
		if got > MaxChainID {
			t.Errorf("Validate(%v) = %d, out of range", v, got)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	bad := []any{-1, 256, int64(1000), uint64(300), "42", 1.5}
	for _, v := range bad {
		if _, err := Validate(v); err == nil {
			t.Errorf("Validate(%v) expected error", v)
		}
	}
}
