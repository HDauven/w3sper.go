package network

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

type NodeInfo struct {
	ChainID int                        `json:"chainId"`
	Chain   string                     `json:"chain"`
	Extra   map[string]json.RawMessage `json:"extra"`
}
type Node struct{ client *Client }

func NewNode(c *Client) *Node { return &Node{client: c} }

// Info uses the current /on/node/info RPC, not a nonexistent GraphQL chain().
func (n *Node) Info(ctx context.Context) (*NodeInfo, error) {
	b, e := n.client.Scope("node").Call(ctx, "info", nil)
	if e != nil {
		return nil, e
	}
	var data map[string]json.RawMessage
	if e = json.Unmarshal(b, &data); e != nil {
		return nil, e
	}
	raw, ok := data["chain_id"]
	if !ok {
		return nil, fmt.Errorf("network: node info missing chain_id")
	}
	v, e := decodeBigInt(raw)
	if e != nil || !v.IsUint64() || v.Uint64() > 255 {
		return nil, fmt.Errorf("network: invalid chain_id %s", raw)
	}
	info := &NodeInfo{ChainID: int(v.Int64()), Chain: "unknown", Extra: make(map[string]json.RawMessage)}
	chains := []string{"localnet", "mainnet", "testnet", "devnet"}
	if info.ChainID < len(chains) {
		info.Chain = chains[info.ChainID]
	}
	for k, v := range data {
		parts := strings.Split(k, "_")
		for i := 1; i < len(parts); i++ {
			if len(parts[i]) > 0 {
				parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
			}
		}
		info.Extra[strings.Join(parts, "")] = v
	}
	return info, nil
}
func (n *Node) CRS(ctx context.Context) ([]byte, error) {
	return n.client.Scope("node").Call(ctx, "crs", nil)
}
func (n *Node) Provisioners(ctx context.Context) (json.RawMessage, error) {
	b, e := n.client.Scope("node").Call(ctx, "provisioners", nil)
	if e == nil && !json.Valid(b) {
		e = fmt.Errorf("network: invalid provisioners JSON")
	}
	return b, e
}
func (n *Node) BlockHeight(ctx context.Context) (*big.Int, error) {
	data, e := n.client.Query(ctx, "block(height: -1) { header { height } }")
	if e != nil {
		return nil, e
	}
	var block struct {
		Header struct {
			Height json.RawMessage `json:"height"`
		} `json:"header"`
	}
	if e = json.Unmarshal(data["block"], &block); e != nil {
		return nil, e
	}
	v, e := decodeBigInt(block.Header.Height)
	if e == nil && !v.IsUint64() {
		return nil, fmt.Errorf("network: block height outside u64")
	}
	return v, e
}

// decodeBigInt never passes integers through floating point.
func decodeBigInt(raw json.RawMessage) (*big.Int, error) {
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, "\"") {
		if e := json.Unmarshal(raw, &s); e != nil {
			return nil, e
		}
	}
	if s == "" {
		return nil, fmt.Errorf("network: missing integer")
	}
	for i, c := range s {
		if c == '-' && i == 0 {
			continue
		}
		if c < '0' || c > '9' {
			return nil, fmt.Errorf("network: invalid integer %q", s)
		}
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("network: invalid integer %q", s)
	}
	return v, nil
}
func (c *Client) GasPrice(ctx context.Context) (map[string]*big.Int, error) {
	b, e := c.Blocks().Call(ctx, "gas-price", nil)
	if e != nil {
		return nil, e
	}
	var data map[string]json.RawMessage
	if e = json.Unmarshal(b, &data); e != nil {
		return nil, e
	}
	out := make(map[string]*big.Int)
	for k, b := range data {
		v, e := decodeBigInt(b)
		if e != nil || !v.IsUint64() {
			return nil, fmt.Errorf("network: invalid gas price %s", b)
		}
		out[k] = v
	}
	return out, nil
}
