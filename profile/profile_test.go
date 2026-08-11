package profile

import "testing"

func TestNewProfileLayout(t *testing.T) {
	buf := make([]byte, ProfileLen)
	copy(buf[:AddressKeyLen], bytesOf(0xaa))
	copy(buf[AddressKeyLen:], bytesOf(0xbb))

	p, err := Parse(buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Address().Bytes()) != AddressKeyLen {
		t.Errorf("address len = %d", len(p.Address().Bytes()))
	}
	if len(p.Account().Bytes()) != AccountKeyLen {
		t.Errorf("account len = %d", len(p.Account().Bytes()))
	}
}

func TestParseRejectsWrongLength(t *testing.T) {
	if _, err := Parse(make([]byte, 4)); err == nil {
		t.Fatal("expected error for wrong length")
	}
}

func TestTypeOf(t *testing.T) {
	addr := EncodeAccount(make([]byte, AddressKeyLen))
	acct := EncodeAccount(make([]byte, AccountKeyLen))
	if got := TypeOf(addr); got != "address" {
		t.Errorf("TypeOf(addr) = %q, want address", got)
	}
	if got := TypeOf(acct); got != "account" {
		t.Errorf("TypeOf(acct) = %q, want account", got)
	}
	if got := TypeOf("garbage!"); got != "undefined" {
		t.Errorf("TypeOf(garbage) = %q, want undefined", got)
	}
}

func bytesOf(v byte) []byte {
	b := make([]byte, ProfileLen)
	for i := range b {
		b[i] = v
	}
	return b
}
