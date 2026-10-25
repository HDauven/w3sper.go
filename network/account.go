// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package network

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/HDauven/w3sper.go/profile"
)

type AccountBalance struct {
	Nonce *big.Int `json:"nonce"`
	Value *big.Int `json:"value"`
}
type StakeAmount struct{ Value, Locked, Eligibility, Total *big.Int }
type StakeInfo struct {
	Amount             *StakeAmount
	Reward             *big.Int
	Faults, HardFaults int
}
type StakeOwner struct {
	Account  string `json:"Account,omitempty"`
	Contract string `json:"Contract,omitempty"`
}
type StakeKeys struct {
	Account string     `json:"account"`
	Owner   StakeOwner `json:"owner"`
}
type AccountCodec interface {
	AccountsIntoRaw(context.Context, [][]byte) ([][]byte, error)
}
type AccountSyncer struct {
	network *Client
	codec   AccountCodec
}

func NewAccountSyncer(c *Client, d AccountCodec) *AccountSyncer {
	return &AccountSyncer{network: c, codec: d}
}
func (s *AccountSyncer) raw(ctx context.Context, accounts [][]byte) ([][]byte, error) {
	if s.codec == nil {
		return nil, fmt.Errorf("network: wallet-core account codec required")
	}
	return s.codec.AccountsIntoRaw(ctx, accounts)
}
func (s *AccountSyncer) Balances(ctx context.Context, accounts [][]byte) ([]AccountBalance, error) {
	ctx, cancel := s.network.ReadContext(ctx)
	defer cancel()
	raw, e := s.raw(ctx, accounts)
	if e != nil {
		return nil, e
	}
	out := make([]AccountBalance, len(raw))
	for i, key := range raw {
		b, e := s.network.Contracts().WithID(TransferContract).Call(ctx, "account", key)
		if e != nil {
			return nil, e
		}
		if len(b) != 16 {
			return nil, fmt.Errorf("network: account response must be 16 bytes, got %d", len(b))
		}
		out[i] = AccountBalance{Nonce: uintBig(b), Value: uintBig(b[8:])}
	}
	return out, nil
}
func (s *AccountSyncer) Balance(ctx context.Context, account []byte) (AccountBalance, error) {
	out, e := s.Balances(ctx, [][]byte{account})
	if e != nil {
		return AccountBalance{}, e
	}
	return out[0], nil
}
func uintBig(b []byte) *big.Int { return new(big.Int).SetUint64(binary.LittleEndian.Uint64(b)) }
func ParseStake(b []byte) (StakeInfo, error) {
	out := StakeInfo{Reward: new(big.Int)}
	if len(b) < 1 || b[0] > 1 {
		return out, fmt.Errorf("network: invalid stake option")
	}
	if b[0] == 0 {
		return out, nil
	}
	if len(b) < 50 || b[8] > 1 {
		return out, fmt.Errorf("network: truncated or invalid stake")
	}
	if b[8] == 1 {
		v, l := uintBig(b[16:]), uintBig(b[24:])
		out.Amount = &StakeAmount{Value: v, Locked: l, Eligibility: uintBig(b[32:]), Total: new(big.Int).Add(v, l)}
	}
	out.Reward = uintBig(b[40:])
	out.Faults = int(b[48])
	out.HardFaults = int(b[49])
	return out, nil
}
func (s *AccountSyncer) Stakes(ctx context.Context, accounts [][]byte) ([]StakeInfo, error) {
	ctx, cancel := s.network.ReadContext(ctx)
	defer cancel()
	raw, e := s.raw(ctx, accounts)
	if e != nil {
		return nil, e
	}
	out := make([]StakeInfo, len(raw))
	for i, key := range raw {
		b, e := s.network.Contracts().WithID(StakeContract).Call(ctx, "get_stake", key)
		if e != nil {
			return nil, e
		}
		out[i], e = ParseStake(b)
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (s *AccountSyncer) Stake(ctx context.Context, account []byte) (StakeInfo, error) {
	out, e := s.Stakes(ctx, [][]byte{account})
	if e != nil {
		return StakeInfo{}, e
	}
	return out[0], nil
}
func (s *AccountSyncer) StakeKeys(ctx context.Context, accounts [][]byte) ([]*StakeKeys, error) {
	ctx, cancel := s.network.ReadContext(ctx)
	defer cancel()
	d, e := s.network.DataDrivers.Get(ctx, StakeContract)
	if e != nil {
		return nil, e
	}
	raw, e := s.raw(ctx, accounts)
	if e != nil {
		return nil, e
	}
	out := make([]*StakeKeys, len(raw))
	for i, key := range raw {
		b, e := s.network.Contracts().WithID(StakeContract).Call(ctx, "get_stake_keys", key)
		if e != nil {
			return nil, e
		}
		j, e := d.DecodeOutput(ctx, "get_stake_keys", b)
		if e != nil {
			return nil, e
		}
		if e = json.Unmarshal(j, &out[i]); e != nil {
			return nil, e
		}
		if out[i] != nil {
			owner := out[i].Owner
			if (owner.Account == "") == (owner.Contract == "") {
				return nil, fmt.Errorf("network: invalid stake owner")
			}
			if owner.Account != "" && profile.TypeOf(owner.Account) != "account" {
				return nil, fmt.Errorf("network: invalid owner account")
			}
		}
	}
	return out, nil
}
