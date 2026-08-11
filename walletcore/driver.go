package walletcore

import (
	"context"
	"fmt"
	"math/big"

	"github.com/dusk-network/w3sper-go/driver"
	"github.com/dusk-network/w3sper-go/profile"
)

// GenerateProfile derives a 160-byte profile buffer (address || account) from
// a seed and index, mirroring the JS `generate_profile` wrapper.
func (w *WalletCore) GenerateProfile(seed []byte, index uint64) ([]byte, error) {
	ctx := context.Background()

	seedPtr, err := w.box(ctx, seed[:])
	if err != nil {
		return nil, err
	}
	defer w.free(ctx, seedPtr)

	out, err := w.alloc(ctx, profile.ProfileLen)
	if err != nil {
		return nil, err
	}
	defer w.free(ctx, out)

	fn := w.mod.ExportedFunction("generate_profile")
	if fn == nil {
		return nil, fmt.Errorf("walletcore: generate_profile not exported")
	}
	if _, err := fn.Call(ctx, uint64(seedPtr), index, uint64(out)); err != nil {
		return nil, fmt.Errorf("walletcore: generate_profile: %w", err)
	}

	return w.readBytes(ctx, out, profile.ProfileLen)
}

// MinimumStake returns the network minimum stake amount.
func (w *WalletCore) MinimumStake() (*big.Int, error) {
	return new(big.Int).SetUint64(w.glob.MinStake), nil
}

// Balance computes {value, spendable} for an address from its owned notes.
func (w *WalletCore) Balance(seed []byte, index uint64, notes []driver.Note) (*big.Int, *big.Int, error) {
	ctx := context.Background()

	seedPtr, err := w.box(ctx, seed[:])
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, seedPtr)

	notesBuf, notesPtr, err := w.packNotesValues(ctx, notes)
	if err != nil {
		return nil, nil, err
	}
	_ = notesBuf
	defer w.free(ctx, notesPtr)

	infoPtr, err := w.alloc(ctx, 16)
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, infoPtr)

	fn := w.mod.ExportedFunction("balance")
	if fn == nil {
		return nil, nil, fmt.Errorf("walletcore: balance not exported")
	}
	if _, err := fn.Call(ctx, uint64(seedPtr), index, uint64(notesPtr), uint64(infoPtr)); err != nil {
		return nil, nil, fmt.Errorf("walletcore: balance: %w", err)
	}

	info, err := w.readBytes(ctx, infoPtr, 16)
	if err != nil {
		return nil, nil, err
	}
	value := new(big.Int).SetUint64(leUint64(info[0:8]))
	spendable := new(big.Int).SetUint64(leUint64(info[8:16]))

	return value, spendable, nil
}

// PickNotes selects notes spending up to value for an owner.
func (w *WalletCore) PickNotes(seed []byte, ownerIndex uint64, notes []driver.Note, value *big.Int) ([]driver.Note, error) {
	ctx := context.Background()

	if len(notes) == 0 {
		return nil, nil
	}

	seedPtr, err := w.box(ctx, seed[:])
	if err != nil {
		return nil, err
	}
	defer w.free(ctx, seedPtr)

	// pack notes as entries (key || value) with DataBuffer layout
	notesPtr, err := w.packNoteEntries(ctx, notes, false)
	if err != nil {
		return nil, err
	}
	defer w.free(ctx, notesPtr)

	valueBytes := make([]byte, 8)
	putLeUint64(valueBytes, value.Uint64())
	valuePtr, err := w.box(ctx, valueBytes)
	if err != nil {
		return nil, err
	}
	defer w.free(ctx, valuePtr)

	fn := w.mod.ExportedFunction("pick_notes")
	if fn == nil {
		return nil, fmt.Errorf("walletcore: pick_notes not exported")
	}
	if _, err := fn.Call(ctx, uint64(seedPtr), ownerIndex, uint64(valuePtr), uint64(notesPtr)); err != nil {
		return nil, fmt.Errorf("walletcore: pick_notes: %w", err)
	}

	// read result: at notesPtr, a u32 length, followed by packed entries
	return w.readPickedNotes(ctx, notesPtr)
}
