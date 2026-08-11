package profile

import (
	"errors"
	"testing"
)

// fakeDriver derives a profile by filling the buffer with the index byte.
type fakeDriver struct{ count int }

func (f *fakeDriver) GenerateProfile(seed []byte, index uint64) ([]byte, error) {
	f.count++
	buf := make([]byte, ProfileLen)
	for i := range buf {
		buf[i] = byte(index)
	}
	return buf, nil
}

func TestGeneratorNextAndIndex(t *testing.T) {
	drv := &fakeDriver{}
	seed := []byte("seed-material-0000000000000000000000")
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
