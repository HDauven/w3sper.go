package walletcore

import (
	"context"
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
func (w *WalletCore) Moonlight(in driver.MoonlightInput) ([]byte, string, error) {
	ctx := context.Background()

	seedPtr, err := w.box(ctx, in.Seed)
	if err != nil {
		return nil, "", err
	}
	defer w.free(ctx, seedPtr)

	// receiver is nullable (nil for contract deployment).
	var receiverPtr uint32
	if in.Receiver != nil {
		receiverPtr, err = w.box(ctx, in.Receiver)
		if err != nil {
			return nil, "", err
		}
		defer w.free(ctx, receiverPtr)
	}

	uv, err := w.box(ctx, leU64Bytes(in.TransferValue.Uint64()))
	if err != nil {
		return nil, "", err
	}
	defer w.free(ctx, uv)

	dep, err := w.box(ctx, leU64Bytes(in.Deposit.Uint64()))
	if err != nil {
		return nil, "", err
	}
	defer w.free(ctx, dep)

	gl, err := w.box(ctx, leU64Bytes(in.GasLimit.Uint64()))
	if err != nil {
		return nil, "", err
	}
	defer w.free(ctx, gl)

	gp, err := w.box(ctx, leU64Bytes(in.GasPrice.Uint64()))
	if err != nil {
		return nil, "", err
	}
	defer w.free(ctx, gp)

	nonce, err := w.box(ctx, leU64Bytes(in.Nonce.Uint64()))
	if err != nil {
		return nil, "", err
	}
	defer w.free(ctx, nonce)

	var dataPtr uint32
	if in.Data != nil {
		dataPtr, err = w.box(ctx, in.Data)
		if err != nil {
			return nil, "", err
		}
		defer w.free(ctx, dataPtr)
	}

	txSlot, err := w.alloc(ctx, 4)
	if err != nil {
		return nil, "", err
	}
	defer w.free(ctx, txSlot)

	hashOut, err := w.alloc(ctx, 64)
	if err != nil {
		return nil, "", err
	}
	defer w.free(ctx, hashOut)

	fn := w.mod.ExportedFunction("moonlight")
	if fn == nil {
		return nil, "", fmt.Errorf("walletcore: moonlight not exported")
	}

	res, err := fn.Call(ctx,
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
	)
	if err != nil {
		return nil, "", fmt.Errorf("walletcore: moonlight: %w", err)
	}
	if len(res) > 0 && res[0] > 0 {
		return nil, "", fmt.Errorf("walletcore: moonlight returned error code %d", res[0])
	}

	txBuffer, err := w.readBoxedDatabuffer(ctx, txSlot, false)
	if err != nil {
		return nil, "", err
	}

	hashBytes, err := w.readBytes(ctx, hashOut, 64)
	if err != nil {
		return nil, "", err
	}
	hash := string(hashBytes)
	// hash is returned NUL-padded in the output buffer; trim.
	for i := range hash {
		if hash[i] == 0 {
			hash = hash[:i]
			break
		}
	}

	return txBuffer, hash, nil
}

// IntoProven attaches a ZK proof to an unproven transaction, returning the
// proven bytes and its hash.
func (w *WalletCore) IntoProven(tx, proof []byte) ([]byte, string, error) {
	ctx := context.Background()

	txPtr, err := w.box(ctx, tx)
	if err != nil {
		return nil, "", err
	}
	defer w.free(ctx, txPtr)

	// proof is prefixed with its 4-byte length.
	proofBuf := make([]byte, 4+len(proof))
	putLeUint32(proofBuf, uint32(len(proof)))
	copy(proofBuf[4:], proof)
	proofPtr, err := w.box(ctx, proofBuf)
	if err != nil {
		return nil, "", err
	}
	defer w.free(ctx, proofPtr)

	provedSlot, err := w.alloc(ctx, 4)
	if err != nil {
		return nil, "", err
	}
	defer w.free(ctx, provedSlot)

	hashOut, err := w.alloc(ctx, 64)
	if err != nil {
		return nil, "", err
	}
	defer w.free(ctx, hashOut)

	fn := w.mod.ExportedFunction("into_proven")
	if fn == nil {
		return nil, "", fmt.Errorf("walletcore: into_proven not exported")
	}
	if _, err := fn.Call(ctx, uint64(txPtr), uint64(proofPtr), uint64(provedSlot), uint64(hashOut)); err != nil {
		return nil, "", fmt.Errorf("walletcore: into_proven: %w", err)
	}

	buf, err := w.readBoxedDatabuffer(ctx, provedSlot, false)
	if err != nil {
		return nil, "", err
	}

	hashBytes, err := w.readBytes(ctx, hashOut, 64)
	if err != nil {
		return nil, "", err
	}
	hash := trimNull(hashBytes)

	return buf, hash, nil
}

func leU64Bytes(v uint64) []byte {
	var b [8]byte
	putLeUint64(b[:], v)
	return b[:]
}

// Phoenix builds an unproven phoenix (shielded) transaction and its circuits.
func (w *WalletCore) Phoenix(in driver.PhoenixInput) ([]byte, []byte, error) {
	ctx := context.Background()

	rng := make([]byte, 32)
	if _, err := randRead(rng); err != nil {
		return nil, nil, err
	}
	rngPtr, err := w.box(ctx, rng)
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, rngPtr)

	seedPtr, err := w.box(ctx, in.Seed)
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, seedPtr)

	receiverPtr, err := w.box(ctx, in.Receiver)
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, receiverPtr)

	inputsPtr, err := w.packNoteValuesDataBuffer(ctx, in.Inputs)
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, inputsPtr)

	openingsPtr, err := w.packOpeningsDataBuffer(ctx, in.Openings)
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, openingsPtr)

	rootPtr, err := w.box(ctx, in.Root)
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, rootPtr)

	valPtr, err := w.box(ctx, leU64Bytes(in.Value.Uint64()))
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, valPtr)

	// deposit is 0 for phoenix value transfers in this driver.
	depPtr, err := w.box(ctx, leU64Bytes(0))
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, depPtr)

	glPtr, err := w.box(ctx, leU64Bytes(in.GasLimit.Uint64()))
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, glPtr)

	gpPtr, err := w.box(ctx, leU64Bytes(in.GasPrice.Uint64()))
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, gpPtr)

	var dataPtr uint32
	if in.Data != nil {
		dataPtr, err = w.box(ctx, in.Data)
		if err != nil {
			return nil, nil, err
		}
		defer w.free(ctx, dataPtr)
	}

	txSlot, err := w.alloc(ctx, 4)
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, txSlot)

	proofSlot, err := w.alloc(ctx, 4)
	if err != nil {
		return nil, nil, err
	}
	defer w.free(ctx, proofSlot)

	fn := w.mod.ExportedFunction("phoenix")
	if fn == nil {
		return nil, nil, fmt.Errorf("walletcore: phoenix not exported")
	}

	_, err = fn.Call(ctx,
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
	)
	if err != nil {
		return nil, nil, fmt.Errorf("walletcore: phoenix: %w", err)
	}

	txBuffer, err := w.readBoxedDatabuffer(ctx, txSlot, true)
	if err != nil {
		return nil, nil, err
	}

	proofBuffer, err := w.readBoxedDatabuffer(ctx, proofSlot, false)
	if err != nil {
		return nil, nil, err
	}

	return txBuffer, proofBuffer, nil
}

func putLeUint32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func trimNull(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
