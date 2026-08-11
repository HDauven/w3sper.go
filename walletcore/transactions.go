package walletcore

import (
	crand "crypto/rand"
	"fmt"
	"io"

	"github.com/dusk-network/w3sper-go/driver"
)

// randRead fills b with cryptographically secure random bytes.
func randRead(b []byte) (int, error) {
	return io.ReadFull(crand.Reader, b)
}

// Moonlight builds and signs an account (moonlight) transaction. It mirrors the
// JS `moonlight` wrapper in protocol-driver/mod.js.
//
// The returned transaction bytes and hash are Go-owned copies; the WASM input
// and output allocations are released before returning.
func (w *WalletCore) Moonlight(in driver.MoonlightInput) (tx []byte, hash string, err error) {
	err = w.runLocked(opsCtx(), func(w *WalletCore) error {
		if err := w.validateMoonlight(in, false); err != nil {
			return err
		}

		seedPtr, err := w.box(opsCtx(), in.Seed)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), seedPtr)

		var receiverPtr uint32
		if in.Receiver != nil {
			receiverPtr, err = w.box(opsCtx(), in.Receiver)
			if err != nil {
				return err
			}
			defer w.free(opsCtx(), receiverPtr)
		}

		uv, err := w.box(opsCtx(), leU64Bytes(in.TransferValue.Uint64()))
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), uv)

		dep, err := w.box(opsCtx(), leU64Bytes(in.Deposit.Uint64()))
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), dep)

		gl, err := w.box(opsCtx(), leU64Bytes(in.GasLimit.Uint64()))
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), gl)

		gp, err := w.box(opsCtx(), leU64Bytes(in.GasPrice.Uint64()))
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), gp)

		nonce, err := w.box(opsCtx(), leU64Bytes(in.Nonce.Uint64()))
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), nonce)

		var dataPtr uint32
		if in.Data != nil {
			dataPtr, err = w.box(opsCtx(), in.Data)
			if err != nil {
				return err
			}
			defer w.free(opsCtx(), dataPtr)
		}

		txSlot, err := w.alloc(opsCtx(), 4)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), txSlot)

		hashOut, err := w.alloc(opsCtx(), 64)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), hashOut)

		fn := w.mod.ExportedFunction("moonlight")
		if fn == nil {
			return fmt.Errorf("walletcore: moonlight not exported")
		}

		if err := w.invoke(opsCtx(), "moonlight", fn,
			uint64(seedPtr),
			in.SenderIndex,
			uint64(receiverPtr),
			uint64(uv),
			uint64(dep),
			uint64(gl),
			uint64(gp),
			uint64(nonce),
			uint64(in.ChainID),
			uint64(dataPtr),
			uint64(txSlot),
			uint64(hashOut),
		); err != nil {
			return err
		}

		buf, err := w.takeLenPrefixedOutput(opsCtx(), txSlot, false)
		if err != nil {
			return err
		}

		hashBytes, err := w.readBytes(opsCtx(), hashOut, 64)
		if err != nil {
			return err
		}

		tx = buf
		hash = trimNull(hashBytes)
		return nil
	})

	return
}

// IntoProven attaches a ZK proof to an unproven transaction, returning the
// proven bytes and its hash.
func (w *WalletCore) IntoProven(txIn, proof []byte) (buf []byte, hash string, err error) {
	err = w.runLocked(opsCtx(), func(w *WalletCore) error {
		if len(txIn) == 0 {
			return fmt.Errorf("walletcore: transaction is empty")
		}

		txPtr, err := w.box(opsCtx(), txIn)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), txPtr)

		// proof is prefixed with its 4-byte length.
		proofBuf := make([]byte, 4+len(proof))
		putLeUint32(proofBuf, uint32(len(proof)))
		copy(proofBuf[4:], proof)
		proofPtr, err := w.box(opsCtx(), proofBuf)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), proofPtr)

		provedSlot, err := w.alloc(opsCtx(), 4)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), provedSlot)

		hashOut, err := w.alloc(opsCtx(), 64)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), hashOut)

		fn := w.mod.ExportedFunction("into_proven")
		if fn == nil {
			return fmt.Errorf("walletcore: into_proven not exported")
		}

		if err := w.invoke(opsCtx(), "into_proven", fn,
			uint64(txPtr), uint64(proofPtr), uint64(provedSlot), uint64(hashOut),
		); err != nil {
			return err
		}

		out, err := w.takeLenPrefixedOutput(opsCtx(), provedSlot, false)
		if err != nil {
			return err
		}

		hashBytes, err := w.readBytes(opsCtx(), hashOut, 64)
		if err != nil {
			return err
		}

		buf = out
		hash = trimNull(hashBytes)
		return nil
	})

	return
}

// Phoenix builds an unproven phoenix (shielded) transaction and its circuits.
// The returned transaction and circuit bytes are Go-owned copies.
func (w *WalletCore) Phoenix(in driver.PhoenixInput) (txOut []byte, proofOut []byte, err error) {
	err = w.runLocked(opsCtx(), func(w *WalletCore) error {
		if err := validateSeed(in.Seed); err != nil {
			return err
		}
		if err := validateAddressKey(in.Receiver); err != nil {
			return err
		}
		if in.Value == nil || in.GasLimit == nil || in.GasPrice == nil {
			return fmt.Errorf("walletcore: value, gas limit and gas price are required")
		}
		if _, err := u64OrErr("value", in.Value); err != nil {
			return err
		}
		if _, err := u64OrErr("gas limit", in.GasLimit); err != nil {
			return err
		}
		if _, err := u64OrErr("gas price", in.GasPrice); err != nil {
			return err
		}
		if _, err := validateChainID(in.ChainID); err != nil {
			return err
		}

		rng := make([]byte, 32)
		if _, err := randRead(rng); err != nil {
			return err
		}
		rngPtr, err := w.box(opsCtx(), rng)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), rngPtr)

		seedPtr, err := w.box(opsCtx(), in.Seed)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), seedPtr)

		receiverPtr, err := w.box(opsCtx(), in.Receiver)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), receiverPtr)

		inputsPtr, err := w.boxNotesValues(opsCtx(), in.Inputs)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), inputsPtr)

		openingsPtr, err := w.boxOpenings(opsCtx(), in.Openings)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), openingsPtr)

		rootPtr, err := w.box(opsCtx(), in.Root)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), rootPtr)

		valPtr, err := w.box(opsCtx(), leU64Bytes(in.Value.Uint64()))
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), valPtr)

		// deposit is 0 for phoenix value transfers in this driver.
		depPtr, err := w.box(opsCtx(), leU64Bytes(0))
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), depPtr)

		glPtr, err := w.box(opsCtx(), leU64Bytes(in.GasLimit.Uint64()))
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), glPtr)

		gpPtr, err := w.box(opsCtx(), leU64Bytes(in.GasPrice.Uint64()))
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), gpPtr)

		var dataPtr uint32
		if in.Data != nil {
			dataPtr, err = w.box(opsCtx(), in.Data)
			if err != nil {
				return err
			}
			defer w.free(opsCtx(), dataPtr)
		}

		txSlot, err := w.alloc(opsCtx(), 4)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), txSlot)

		proofSlot, err := w.alloc(opsCtx(), 4)
		if err != nil {
			return err
		}
		defer w.free(opsCtx(), proofSlot)

		fn := w.mod.ExportedFunction("phoenix")
		if fn == nil {
			return fmt.Errorf("walletcore: phoenix not exported")
		}

		if err := w.invoke(opsCtx(), "phoenix", fn,
			uint64(rngPtr),
			uint64(seedPtr),
			in.SenderIndex,
			uint64(receiverPtr),
			uint64(inputsPtr),
			uint64(openingsPtr),
			uint64(rootPtr),
			uint64(valPtr),
			uint64(depPtr),
			uint64(glPtr),
			uint64(gpPtr),
			uint64(in.ChainID),
			uint64(dataPtr),
			uint64(txSlot),
			uint64(proofSlot),
		); err != nil {
			return err
		}

		txRes, err := w.takeLenPrefixedOutput(opsCtx(), txSlot, true)
		if err != nil {
			return err
		}
		proofRes, err := w.takeLenPrefixedOutput(opsCtx(), proofSlot, false)
		if err != nil {
			return err
		}

		txOut = txRes
		proofOut = proofRes
		return nil
	})

	return
}

func leU64Bytes(v uint64) []byte {
	var b [8]byte
	putLeUint64(b[:], v)
	return b[:]
}

func trimNull(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
