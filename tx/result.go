package tx

import (
	"bytes"
	"context"
	"github.com/HDauven/w3sper.go/network"
	"math/big"
)

// Result holds owned network bytes. Nonce or Nullifiers identifies the payment
// replay token; ContractID is present for deployments.
type Result struct {
	Buffer     []byte   `json:"buffer"`
	ChainID    int      `json:"chainId"`
	Hash       string   `json:"hash"`
	Nonce      *big.Int `json:"nonce,omitempty"`
	Nullifiers [][]byte `json:"nullifiers,omitempty"`
	ContractID string   `json:"contractId,omitempty"`
}
type NonceResult = Result
type NullifierResult = Result
type DeploymentResult = Result

func (r *Result) Execute(ctx context.Context, n *network.Client) error {
	return n.Execute(ctx, r.Buffer)
}

// ExecuteAndWait registers before propagation to avoid losing a fast execution.
func (r *Result) ExecuteAndWait(ctx context.Context, n *network.Client) (network.Event, error) {
	buffer, hash := bytes.Clone(r.Buffer), r.Hash
	ctx, cancel := n.ReadContext(ctx)
	defer cancel()
	sub, e := n.Transactions().WithID(hash).Subscribe(ctx, "executed", 1)
	if e != nil {
		return network.Event{}, e
	}
	defer sub.Cancel()
	if e = n.Execute(ctx, buffer); e != nil {
		return network.Event{}, e
	}
	return sub.Next(ctx)
}
func (b *Builder) Execute(ctx context.Context, n *network.Client) (*Result, error) {
	r, e := b.BuildContext(ctx, n)
	if e != nil {
		return nil, e
	}
	return r, r.Execute(ctx, n)
}
