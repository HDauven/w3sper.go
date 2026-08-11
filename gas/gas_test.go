package gas

import (
	"math/big"
	"testing"
)

func TestDefaults(t *testing.T) {
	g := New(nil, nil)
	if g.Limit.Cmp(big.NewInt(DefaultLimit)) != 0 {
		t.Errorf("limit = %s, want %d", g.Limit, DefaultLimit)
	}
	if g.Price.Cmp(big.NewInt(DefaultPrice)) != 0 {
		t.Errorf("price = %s, want %d", g.Price, DefaultPrice)
	}
	if g.Total.Cmp(big.NewInt(DefaultLimit)) != 0 {
		t.Errorf("total = %s, want %d", g.Total, DefaultLimit)
	}
}

func TestExplicit(t *testing.T) {
	g := New(big.NewInt(3_000_000), big.NewInt(2))
	if g.Limit.Cmp(big.NewInt(3_000_000)) != 0 {
		t.Errorf("limit = %s", g.Limit)
	}
	if g.Price.Cmp(big.NewInt(2)) != 0 {
		t.Errorf("price = %s", g.Price)
	}
	if g.Total.Cmp(big.NewInt(6_000_000)) != 0 {
		t.Errorf("total = %s", g.Total)
	}
}

func TestNonPositiveFallsBackToDefault(t *testing.T) {
	g := New(big.NewInt(0), big.NewInt(-5))
	if g.Limit.Cmp(big.NewInt(DefaultLimit)) != 0 {
		t.Errorf("zero limit should default, got %s", g.Limit)
	}
	if g.Price.Cmp(big.NewInt(DefaultPrice)) != 0 {
		t.Errorf("negative price should default, got %s", g.Price)
	}
}
