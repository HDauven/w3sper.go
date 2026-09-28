package walletcore

import (
	"context"
	"math/big"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/profile"
)

// GenerateProfile derives public address/account keys. It retains no seed.
// Use GenerateProfileContext for cancelable admission to the serialized runtime.
func (w *WalletCore) GenerateProfile(seed []byte, index profile.ProfileIndex) ([]byte, error) {
	return w.GenerateProfileContext(opsCtx(), seed, index)
}
func (w *WalletCore) GenerateProfileContext(ctx context.Context, seed []byte, index profile.ProfileIndex) (out []byte, err error) {
	seed = cloneBytes(seed)
	defer clear(seed)
	if err = validateSeed(seed); err != nil {
		return
	}
	err = w.operation(ctx, func(a *arena) error {
		input, output := a.box(seed), a.space(profile.ProfileLen)
		if e := a.call("generate_profile", input, uint64(index), output); e != nil {
			return e
		}
		out, err = a.read(output, profile.ProfileLen)
		return err
	})
	return
}
func (w *WalletCore) MinimumStake() (*big.Int, error) { return w.MinimumStakeContext(opsCtx()) }
func (w *WalletCore) MinimumStakeContext(ctx context.Context) (out *big.Int, err error) {
	err = w.runLocked(ctx, func(w *WalletCore) error { out = new(big.Int).SetUint64(w.glob.MinStake); return nil })
	return
}

// Balance adapts the legacy seed/index interface. Prefer BalanceFor with a
// separately owned signer and context.
func (w *WalletCore) Balance(seed []byte, index profile.ProfileIndex, notes []driver.Note) (*big.Int, *big.Int, error) {
	seed = cloneBytes(seed)
	defer clear(seed)
	notes = driver.CloneNotes(notes)
	s, e := w.legacySigner(opsCtx(), seed, index)
	if e != nil {
		return nil, nil, e
	}
	defer s.Dispose()
	value, sp, e := w.BalanceFor(opsCtx(), s, notes)
	if e != nil {
		return nil, nil, e
	}
	return new(big.Int).SetUint64(value), new(big.Int).SetUint64(sp), nil
}
func (w *WalletCore) PickNotes(seed []byte, index profile.ProfileIndex, notes []driver.Note, value *big.Int) ([]driver.Note, error) {
	seed = cloneBytes(seed)
	defer clear(seed)
	notes = driver.CloneNotes(notes)
	v, e := u64OrErr("pick value", copyBig(value))
	if e != nil {
		return nil, e
	}
	s, e := w.legacySigner(opsCtx(), seed, index)
	if e != nil {
		return nil, e
	}
	defer s.Dispose()
	return w.PickFor(opsCtx(), s, notes, v)
}
