package walletcore

import (
	"fmt"
	"math/big"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/profile"
)

// GenerateProfile derives a 160-byte profile buffer (address || account) from
// a seed and index. It returns an owned copy. Only profile indexes within
// wallet-core's u8 index space are accepted.
func (w *WalletCore) GenerateProfile(seed []byte, index profile.ProfileIndex) ([]byte, error) {
	var buf []byte
	err := w.runLocked(opsCtx(), func(w *WalletCore) error {
		if err := validateSeed(seed); err != nil {
			return err
		}

		seedPtr, err := w.box(opsCtx(), seed)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), seedPtr)

		out, err := w.alloc(opsCtx(), profile.ProfileLen)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), out)

		fn := w.mod.ExportedFunction("generate_profile")
		if fn == nil {
			return fmt.Errorf("walletcore: generate_profile not exported")
		}

		if err := w.invoke(opsCtx(), "generate_profile", fn,
			uint64(seedPtr), uint64(index), uint64(out),
		); err != nil {
			return err
		}

		buf, err = w.readBytes(opsCtx(), out, profile.ProfileLen)
		if err != nil {
			return err
		}
		return nil
	})
	return buf, err
}

// MinimumStake returns the network minimum stake amount.
func (w *WalletCore) MinimumStake() (*big.Int, error) {
	return new(big.Int).SetUint64(w.glob.MinStake), nil
}

// Balance computes {value, spendable} for an owner from its owned notes.
func (w *WalletCore) Balance(seed []byte, index profile.ProfileIndex, notes []driver.Note) (*big.Int, *big.Int, error) {
	var value, spendable *big.Int
	err := w.runLocked(opsCtx(), func(w *WalletCore) error {
		if err := validateSeed(seed); err != nil {
			return err
		}

		seedPtr, err := w.box(opsCtx(), seed)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), seedPtr)

		notesPtr, err := w.boxNotesValues(opsCtx(), notes)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), notesPtr)

		infoPtr, err := w.alloc(opsCtx(), 16)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), infoPtr)

		fn := w.mod.ExportedFunction("balance")
		if fn == nil {
			return fmt.Errorf("walletcore: balance not exported")
		}

		if err := w.invoke(opsCtx(), "balance", fn,
			uint64(seedPtr), uint64(index), uint64(notesPtr), uint64(infoPtr),
		); err != nil {
			return err
		}

		info, err := w.readBytes(opsCtx(), infoPtr, 16)
		if err != nil {
			return err
		}
		value = new(big.Int).SetUint64(leUint64(info[0:8]))
		spendable = new(big.Int).SetUint64(leUint64(info[8:16]))
		return nil
	})

	return value, spendable, err
}

// PickNotes selects notes spending up to value for an owner. It returns the
// selected notes (nullifier:value entries).
func (w *WalletCore) PickNotes(seed []byte, ownerIndex profile.ProfileIndex, notes []driver.Note, value *big.Int) ([]driver.Note, error) {
	if len(notes) == 0 {
		return nil, nil
	}

	var picked []driver.Note
	err := w.runLocked(opsCtx(), func(w *WalletCore) error {
		if err := validateSeed(seed); err != nil {
			return err
		}
		value64, err := u64OrErr("pick value", value)
		if err != nil {
			return err
		}

		seedPtr, err := w.box(opsCtx(), seed)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), seedPtr)

		// The input notes (key||value entries) are written in place by
		// pick_notes, so this buffer must not be freed until the result is read.
		notesPtr, err := w.boxNoteEntries(opsCtx(), notes)
		if err != nil {
			return err
		}
		// do NOT defer free here; read result below within the locked region.

		valueBytes := make([]byte, 8)
		putLeUint64(valueBytes, value64)
		valuePtr, err := w.box(opsCtx(), valueBytes)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), valuePtr)

		fn := w.mod.ExportedFunction("pick_notes")
		if fn == nil {
			w.free(opsCtx(), notesPtr)
			return fmt.Errorf("walletcore: pick_notes not exported")
		}

		if err := w.invoke(opsCtx(), "pick_notes", fn,
			uint64(seedPtr), uint64(ownerIndex), uint64(valuePtr), uint64(notesPtr),
		); err != nil {
			w.free(opsCtx(), notesPtr)
			return err
		}

		result, err := w.readPickedNotes(opsCtx(), notesPtr)
		w.free(opsCtx(), notesPtr) // release the input/output buffer
		if err != nil {
			return err
		}

		picked = result
		return nil
	})

	return picked, err
}
