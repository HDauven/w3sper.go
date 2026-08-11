package network

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
)

// NodeInfo is node metadata returned by a Rusk node.
type NodeInfo struct {
	ChainID int             `json:"chainId"`
	Chain   json.RawMessage `json:"chain"`
	Extra   map[string]json.RawMessage
}

// Node wraps the node-related GraphQL queries.
type Node struct {
	client *Client
}

// NewNode returns a node component backed by the given client.
func NewNode(c *Client) *Node { return &Node{client: c} }

// Info fetches node metadata, including the network chain id.
func (n *Node) Info(ctx context.Context) (*NodeInfo, error) {
	data, err := n.client.Query(ctx, "chain() { header { chainId } }")
	if err != nil {
		return nil, err
	}
	info := &NodeInfo{Extra: map[string]json.RawMessage{}}
	for k, v := range data {
		info.Extra[k] = v
	}
	// Decode chainId from arbitrary nesting.
	if raw, ok := data["chain"]; ok {
		var chain struct {
			Header struct {
				ChainID int `json:"chainId"`
			} `json:"header"`
		}
		if err := json.Unmarshal(raw, &chain); err == nil {
			info.ChainID = chain.Header.ChainID
		}
	}
	return info, nil
}

// BlockHeight returns the current block height as a big.Int.
func (n *Node) BlockHeight(ctx context.Context) (*big.Int, error) {
	data, err := n.client.Query(ctx, "block(height: -1) { header { height } }")
	if err != nil {
		return nil, err
	}
	raw, ok := data["block"]
	if !ok {
		return big.NewInt(0), nil
	}
	var block struct {
		Header struct {
			Height json.RawMessage `json:"height"`
		} `json:"header"`
	}
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil, err
	}
	return decodeBigInt(block.Header.Height)
}

// decodeBigInt reads a JSON number that may exceed int64 precision.
func decodeBigInt(raw json.RawMessage) (*big.Int, error) {
	if len(raw) == 0 {
		return big.NewInt(0), nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, ok := new(big.Int).SetString(s, 10); ok {
			return v, nil
		}
		return nil, fmt.Errorf("network: invalid bigint %q", s)
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return big.NewInt(int64(f)), nil
	}
	return nil, fmt.Errorf("network: invalid numeric value %s", raw)
}
