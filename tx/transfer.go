package tx

import (
	"fmt"
	"math/big"

	"github.com/dusk-network/w3sper-go/driver"
	"github.com/dusk-network/w3sper-go/gas"
	"github.com/dusk-network/w3sper-go/profile"
)

// Transfer is a fluent value-transfer builder. Use [Transfer.To] to select the
// destination; the returned builder routes to the account or address flow.
type Transfer struct {
	*Builder
}

// NewTransfer returns a transfer builder bound to a book entry.
func NewTransfer(e *BookEntry) *Transfer {
	return &Transfer{Builder: newBuilder(e)}
}

// Amount sets the transfer amount in Lux. The value is copied so later caller
// mutation cannot alter a pending transaction.
func (t *Transfer) Amount(v *big.Int) *Transfer {
	t.attrs.amount = new(big.Int).Set(v)
	return t
}

// Deposit sets an optional deposit amount in Lux. The value is copied.
func (t *Transfer) Deposit(v *big.Int) *Transfer {
	t.attrs.deposit = new(big.Int).Set(v)
	return t
}

// Gas sets the gas configuration.
func (t *Transfer) Gas(g *gasConfig) *Transfer {
	t.attrs.gas = g.config
	return t
}

// Chain pins the chain id explicitly.
func (t *Transfer) Chain(v int) *Transfer {
	t.attrs.chain = &v
	return t
}

// Nonce pins the account nonce explicitly. The value is copied.
func (t *Transfer) Nonce(v *big.Int) *Transfer {
	t.attrs.nonce = new(big.Int).Set(v)
	return t
}

// Routed is the result of [Transfer.To]. Exactly one field is non-nil.
type Routed struct {
	Account *AccountTransfer
	Address *AddressTransfer
}

// To selects the destination. A 96-byte account key yields an account transfer;
// a 64-byte address key yields an address transfer.
//
// Each routed builder gets an independent snapshot of the transfer attributes,
// so routing the same base transfer to two receivers cannot change the first
// route's recipient.
func (t *Transfer) To(identifier string) (*Routed, error) {
	bytes, err := t.decodeReceiver(identifier)
	if err != nil {
		return nil, err
	}
	switch len(bytes) {
	case profile.AccountKeyLen:
		b := newBuilderSnapshot(t.entry, t.chain, t.attrs.clone())
		b.attrs.receiver = bytes
		a := &AccountTransfer{Transfer: &Transfer{Builder: b}}
		return &Routed{Account: a}, nil
	case profile.AddressKeyLen:
		b := newBuilderSnapshot(t.entry, t.chain, t.attrs.clone())
		b.attrs.receiver = bytes
		a := &AddressTransfer{Transfer: &Transfer{Builder: b}}
		return &Routed{Address: a}, nil
	default:
		return nil, fmt.Errorf("tx: invalid identifier")
	}
}

// AccountTransfer builds an account (moonlight) value transfer.
type AccountTransfer struct {
	*Transfer
}

// Build resolves the nonce, signs the moonlight transaction, and returns the
// result.
func (a *AccountTransfer) Build() (*NonceResult, error) {
	d, err := a.requireDriver()
	if err != nil {
		return nil, err
	}

	amount := a.attrs.amount
	if amount == nil {
		amount = big.NewInt(0)
	}
	deposit := a.attrs.deposit
	if deposit == nil {
		deposit = big.NewInt(0)
	}
	gc := a.gas()

	chainID, err := a.requireChain()
	if err != nil {
		return nil, err
	}

	// Resolve nonce.
	var nonce *big.Int
	if a.attrs.nonce != nil {
		nonce = new(big.Int).Set(a.attrs.nonce)
	} else if a.entry.treasury != nil {
		bal, err := a.entry.treasury.AccountBalance(a.entry.profile.Account().Bytes())
		if err != nil {
			return nil, err
		}
		nonce = bal.Nonce
	} else {
		return nil, fmt.Errorf("tx: account nonce is required")
	}
	nonce = new(big.Int).Add(nonce, big.NewInt(1))

	seed, err := a.profileSeed()
	if err != nil {
		return nil, err
	}

	in := driver.MoonlightInput{
		Seed:          seed,
		SenderIndex:   a.profileIndex(),
		Receiver:      a.attrs.receiver,
		TransferValue: amount,
		Deposit:       deposit,
		GasLimit:      gc.Limit,
		GasPrice:      gc.Price,
		Nonce:         nonce,
		ChainID:       chainID,
		Data:          encodePayload(a.attrs.payload),
	}

	buf, hash, err := d.Moonlight(in)
	if err != nil {
		return nil, err
	}

	return &NonceResult{Buffer: buf, ChainID: chainID, Hash: hash, Nonce: nonce}, nil
}

// AddressTransfer builds a phoenix (shielded) transfer that must be proven.
// Full shielded support requires note synchronization, merkle openings and a
// prover endpoint; the driver is responsible for the cryptographic steps.
type AddressTransfer struct {
	*Transfer
}

// Build currently requires a fully wired phoenix driver. It returns
// [driver.ErrDriverRequired] when the driver does not implement the shielded
// flow.
func (a *AddressTransfer) Build() (*NullifierResult, error) {
	d, err := a.requireDriver()
	if err != nil {
		return nil, err
	}
	_ = d
	return nil, fmt.Errorf("tx: shielded (phoenix) transfers require the phoenix driver flow")
}

type gasConfig struct{ config *gas.Config }

// Gas returns a gas config to pass to [Transfer.Gas].
func Gas(limit, price *big.Int) *gasConfig {
	return &gasConfig{config: gas.New(limit, price)}
}

func encodePayload(p interface{}) []byte {
	// rkyv serialization of memo / contract-call / deployment payloads is
	// performed by the driver. Returns nil; payloads are passed through the
	// driver interface.
	return nil
}
