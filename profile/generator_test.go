package profile

import (
	"errors"
	"testing"
)

// fakeDriver derives a profile by filling the buffer with the index byte.
type fakeDriver struct{ count int }

func (f *fakeDriver) GenerateProfile(seed []byte, index ProfileIndex) ([]byte, error) {
	f.count++
	buf := make([]byte, ProfileLen)
	for i := range buf {
		buf[i] = byte(index)
	}
	return buf, nil
}

func TestGeneratorNextAndIndex(t *testing.T) {
	drv := &fakeDriver{}
	seed := make([]byte, 64)
	g := NewGenerator(func() ([]byte, error) { return seed, nil }, drv)

	defaultP, err := g.Default()
	if err != nil {
		t.Fatal(err)
	}
	if defaultP.Index() != 0 {
		t.Errorf("default index = %d, want 0", defaultP.Index())
	}

	next, err := g.Next()
	if err != nil {
		t.Fatal(err)
	}
	if next.Index() != 1 {
		t.Errorf("next index = %d, want 1", next.Index())
	}
	if g.Len() != 2 {
		t.Errorf("Len = %d, want 2", g.Len())
	}
	if g.At(0) != defaultP {
		t.Error("At(0) is not the default profile")
	}
	if g.At(5) != nil {
		t.Error("At(5) should be nil")
	}
}

func TestGeneratorSeederError(t *testing.T) {
	wantErr := errors.New("seed error")
	g := NewGenerator(func() ([]byte, error) { return nil, wantErr }, &fakeDriver{})
	if _, err := g.Default(); !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
}

// TestGeneratorIndexOf ensures IndexOf works for a generator's own profiles and
// returns -1 for an unknown source, without panicking (regression for comparing
// non-comparable function sources).
func TestGeneratorIndexOf(t *testing.T) {
	seed := make([]byte, 64)
	g := NewGenerator(func() ([]byte, error) { return seed, nil }, &fakeDriver{})

	p, err := g.Default()
	if err != nil {
		t.Fatal(err)
	}
	if got := g.IndexOf(p); got != 0 {
		t.Errorf("IndexOf(default) = %d, want 0", got)
	}

	next, err := g.Next()
	if err != nil {
		t.Fatal(err)
	}
	if got := g.IndexOf(next); got != 1 {
		t.Errorf("IndexOf(next) = %d, want 1", got)
	}

	// A profile derived by a different generator (different seed source) must
	// not be attributed to this generator.
	otherSeed := make([]byte, 64)
	otherSeed[0] = 1
	otherGen := NewGenerator(func() ([]byte, error) { return otherSeed, nil }, &fakeDriver{})
	foreign, err := otherGen.Default()
	if err != nil {
		t.Fatal(err)
	}
	if got := g.IndexOf(foreign); got != -1 {
		t.Errorf("IndexOf(foreign) = %d, want -1", got)
	}
	if got := otherGen.IndexOf(p); got != -1 {
		t.Errorf("foreign.IndexOf(our) = %d, want -1", got)
	}
}

// TestGeneratorIndexExhaustion verifies that the wallet-core u8 index space is
// honored: index 255 is the last valid, and index 256 is rejected.
func TestGeneratorIndexExhaustion(t *testing.T) {
	seed := make([]byte, 64)
	g := NewGenerator(func() ([]byte, error) { return seed, nil }, &fakeDriver{})

	var last *Profile
	var err error
	for i := int64(1); i <= MaxProfileIndex; i++ {
		last, err = g.Next()
		if err != nil {
			t.Fatalf("Next(%d): %v", i, err)
		}
	}
	if last.Index() != MaxProfileIndex {
		t.Fatalf("last index = %d, want %d", last.Index(), MaxProfileIndex)
	}

	if _, err := g.Next(); err == nil {
		t.Fatal("expected error deriving beyond MaxProfileIndex")
	}
	if _, err := g.nth(MaxProfileIndex + 1); err == nil {
		t.Fatal("expected error for nth() beyond MaxProfileIndex")
	}
}

func TestSenderIndex(t *testing.T) {
	// Index() returns -1 for profiles created via Parse.
	p, err := Parse(make([]byte, ProfileLen))
	if err != nil {
		t.Fatal(err)
	}
	if p.Index() != -1 {
		t.Errorf("Index = %d, want -1", p.Index())
	}
}
