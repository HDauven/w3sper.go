// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package driver

import (
	"context"
	"github.com/HDauven/w3sper.go/profile"
	"math/big"
)

type Operation uint8

const (
	Transfer Operation = iota
	Shield
	Unshield
	Stake
	Unstake
	WithdrawReward
)

// TransactionInput is a validated current key-backed wallet-core request.
// Data, when present, is a length-prefixed TransactionData archive from CreateData.
// Openings are complete archived Option<NoteOpening> values from the node.
type TransactionInput struct {
	Signer, Owner                              *profile.Signer
	Operation                                  Operation
	Shielded                                   bool
	Receiver                                   []byte
	Amount, Deposit, GasLimit, GasPrice, Nonce uint64
	ChainID                                    int
	Notes                                      []Note
	Openings                                   [][]byte
	Root, Data                                 []byte
}

// RawTransaction contains either final network bytes/hash or an unproven
// length-prefixed Phoenix archive and the circuits to submit to a prover.
type RawTransaction struct {
	Buffer   []byte
	Hash     string
	Circuits []byte
}

type Call struct {
	ContractID []byte
	Function   string
	Args       []byte
	Committed  bool
}
type Deployment struct {
	Bytecode, Owner, InitArgs []byte
	Nonce                     uint64
}

// Payload has exactly one variant. A non-nil empty memo omits transaction data.
type Payload struct {
	Memo   []byte
	Call   *Call
	Deploy *Deployment
}

type SyncInfo struct{ BlockHeight, Bookmark uint64 }

// Protocol is the complete context-aware current wallet-core boundary.
type Protocol interface {
	profile.KeyDriver
	MinimumStakeContext(context.Context) (*big.Int, error)
	NoteItemSize() uint32
	Build(context.Context, TransactionInput) (RawTransaction, error)
	CreateData(context.Context, Payload) ([]byte, error)
	ContractID(context.Context, Deployment) ([]byte, error)
	ProveTransaction(context.Context, []byte, []byte) (RawTransaction, error)
	BalanceFor(context.Context, *profile.Signer, []Note) (uint64, uint64, error)
	PickFor(context.Context, *profile.Signer, []Note, uint64) ([]Note, error)
	MapOwned(context.Context, []*profile.Signer, []byte) ([][]Note, SyncInfo, error)
	Bookmarks(context.Context, []Note) ([]uint64, error)
	AccountsIntoRaw(context.Context, [][]byte) ([][]byte, error)
}

func CloneNotes(notes []Note) []Note {
	out := make([]Note, len(notes))
	for i, n := range notes {
		out[i] = Note{Nullifier: append([]byte(nil), n.Nullifier...), Value: append([]byte(nil), n.Value...)}
	}
	return out
}
