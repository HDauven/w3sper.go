package walletcore

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/profile"
)

func randRead(b []byte) (int, error) { return io.ReadFull(crand.Reader, b) }
func leU64Bytes(v uint64) []byte     { var b [8]byte; binary.LittleEndian.PutUint64(b[:], v); return b[:] }
func copyBig(v *big.Int) *big.Int {
	if v == nil {
		return nil
	}
	return new(big.Int).Set(v)
}

// legacySigner adapts deprecated seed/index operations to selected child keys.
// The temporary signer is always disposed by its caller; profiles retain no seed.
func (w *WalletCore) legacySigner(ctx context.Context, seed []byte, index profile.ProfileIndex) (*profile.Signer, error) {
	if err := validateSeed(seed); err != nil {
		return nil, err
	}
	seed = cloneBytes(seed)
	defer clear(seed)
	g := profile.NewGenerator(func() ([]byte, error) { return seed, nil }, w)
	p, e := g.DeriveContext(ctx, int64(index))
	if e != nil {
		return nil, e
	}
	return g.Signer(ctx, p)
}

// Moonlight is the legacy seed/index API. New code should use Build with an
// explicitly owned Signer and a caller context.
func (w *WalletCore) Moonlight(in driver.MoonlightInput) (tx []byte, hash string, err error) {
	in.Seed = cloneBytes(in.Seed)
	defer clear(in.Seed)
	in.Receiver = cloneNullable(in.Receiver)
	in.Data = cloneNullable(in.Data)
	in.TransferValue = copyBig(in.TransferValue)
	in.Deposit = copyBig(in.Deposit)
	in.GasLimit = copyBig(in.GasLimit)
	in.GasPrice = copyBig(in.GasPrice)
	in.Nonce = copyBig(in.Nonce)
	if err = w.validateMoonlight(in, false); err != nil {
		return
	}
	s, e := w.legacySigner(opsCtx(), in.Seed, in.SenderIndex)
	if e != nil {
		return nil, "", e
	}
	defer s.Dispose()
	raw, e := w.Build(opsCtx(), driver.TransactionInput{Signer: s, Receiver: in.Receiver, Amount: in.TransferValue.Uint64(), Deposit: in.Deposit.Uint64(), GasLimit: in.GasLimit.Uint64(), GasPrice: in.GasPrice.Uint64(), Nonce: in.Nonce.Uint64(), ChainID: in.ChainID, Data: in.Data})
	return raw.Buffer, raw.Hash, e
}
func cloneNullable(b []byte) []byte {
	if b == nil {
		return nil
	}
	return cloneBytes(b)
}

// Phoenix is the legacy seed/index API; Build additionally supports deposits,
// conversions and shielded staking through the current selected-key ABI.
func (w *WalletCore) Phoenix(in driver.PhoenixInput) (tx, circuits []byte, err error) {
	in.Seed = cloneBytes(in.Seed)
	defer clear(in.Seed)
	in.Receiver = cloneBytes(in.Receiver)
	in.Inputs = driver.CloneNotes(in.Inputs)
	in.Root = cloneBytes(in.Root)
	in.Data = cloneNullable(in.Data)
	openings := make([][]byte, len(in.Openings))
	for i, b := range in.Openings {
		openings[i] = cloneBytes(b)
	}
	in.Openings = openings
	in.Value = copyBig(in.Value)
	in.GasLimit = copyBig(in.GasLimit)
	in.GasPrice = copyBig(in.GasPrice)
	for name, v := range map[string]*big.Int{"value": in.Value, "gas limit": in.GasLimit, "gas price": in.GasPrice} {
		if _, e := u64OrErr(name, v); e != nil {
			return nil, nil, e
		}
	}
	s, e := w.legacySigner(opsCtx(), in.Seed, in.SenderIndex)
	if e != nil {
		return nil, nil, e
	}
	defer s.Dispose()
	raw, e := w.Build(opsCtx(), driver.TransactionInput{Signer: s, Shielded: true, Receiver: in.Receiver, Amount: in.Value.Uint64(), GasLimit: in.GasLimit.Uint64(), GasPrice: in.GasPrice.Uint64(), ChainID: in.ChainID, Data: in.Data, Notes: in.Inputs, Openings: in.Openings, Root: in.Root})
	return raw.Buffer, raw.Circuits, e
}

// IntoProven is the context-free compatibility form of ProveTransaction.
func (w *WalletCore) IntoProven(tx, proof []byte) ([]byte, string, error) {
	if len(tx) == 0 {
		return nil, "", fmt.Errorf("walletcore: transaction is empty")
	}
	raw, e := w.ProveTransaction(opsCtx(), tx, proof)
	return raw.Buffer, raw.Hash, e
}
