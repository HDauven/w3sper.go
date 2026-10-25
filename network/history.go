// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package network

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/HDauven/w3sper.go/profile"
)

type HistoryOptions struct {
	From  uint64
	To    *uint64
	Limit int
	Order string
}
type AccountHistoryEntry struct {
	BlockHash                           string   `json:"blockHash"`
	BlockHeight                         *big.Int `json:"blockHeight"`
	BlockTimestamp                      int64    `json:"blockTimestamp"`
	FeePaid                             *big.Int `json:"feePaid"`
	From                                string   `json:"from"`
	GasLimit, GasPrice, GasSpent        *big.Int
	Hash, Memo, Method, Owner, To, Type string
	Success                             bool
	Value                               *big.Int
}
type historyRow struct {
	Origin string          `json:"origin"`
	Height json.RawMessage `json:"block_height"`
	Events []historyEvent  `json:"events"`
}
type historyEvent struct {
	Target, Topic string
	Data          map[string]json.RawMessage
}

// HistoryStream is pull-based: there is no goroutine waiting on a consumer.
// Close cancels outstanding enrichment. The original budget spans the stream.
type HistoryStream struct {
	gate    chan struct{}
	eof     bool
	ctx     context.Context
	cancel  context.CancelFunc
	client  *Client
	account string
	rows    []historyRow
	offset  int
	pending []AccountHistoryEntry
}

func (s *AccountSyncer) History(ctx context.Context, account []byte, o HistoryOptions) (*HistoryStream, error) {
	if len(account) != 96 {
		return nil, fmt.Errorf("network: history requires an account")
	}
	account = append([]byte(nil), account...)
	if o.To != nil {
		to := *o.To
		o.To = &to
	}
	ctx, cancel := s.network.ReadContext(ctx)
	fail := func(e error) (*HistoryStream, error) { cancel(); return nil, e }
	order := o.Order
	if order == "" {
		order = "asc"
	}
	if order != "asc" && order != "desc" {
		return fail(fmt.Errorf("network: history order must be asc or desc"))
	}
	if o.Limit < 0 {
		return fail(fmt.Errorf("network: negative history limit"))
	}
	var to uint64
	if o.To != nil {
		to = *o.To
	} else {
		h, e := NewNode(s.network).BlockHeight(ctx)
		if e != nil {
			return fail(e)
		}
		to = h.Uint64()
	}
	if o.From > to {
		return fail(fmt.Errorf("network: invalid history range"))
	}
	key := profile.EncodeAccount(account)
	q := fmt.Sprintf("fullMoonlightHistory(address: %s, fromBlock: %d, ord: %s, toBlock: %d) { json }", strconv.Quote(key), o.From, strconv.Quote(order), to)
	data, e := s.network.Query(ctx, q)
	if e != nil {
		return fail(e)
	}
	var result struct {
		Rows []historyRow `json:"json"`
	}
	if b := data["fullMoonlightHistory"]; len(b) > 0 && string(b) != "null" {
		if e = json.Unmarshal(b, &result); e != nil {
			return fail(e)
		}
	}
	if o.Limit > 0 && len(result.Rows) > o.Limit {
		result.Rows = result.Rows[:o.Limit]
	}
	out := &HistoryStream{gate: make(chan struct{}, 1), ctx: ctx, cancel: cancel, client: s.network, account: key, rows: result.Rows}
	out.gate <- struct{}{}
	return out, nil
}
func (s *HistoryStream) Close() error { s.cancel(); return nil }
func (s *HistoryStream) Next(ctx context.Context) (AccountHistoryEntry, error) {
	select {
	case <-ctx.Done():
		return AccountHistoryEntry{}, ctx.Err()
	case <-s.gate:
	}
	defer func() { s.gate <- struct{}{} }()
	if s.eof {
		return AccountHistoryEntry{}, io.EOF
	}
	if e := s.ctx.Err(); e != nil {
		return AccountHistoryEntry{}, e
	}
	if e := ctx.Err(); e != nil {
		return AccountHistoryEntry{}, e
	}
	if len(s.pending) == 0 && s.offset < len(s.rows) {
		op, cancel := context.WithCancel(s.ctx)
		stop := context.AfterFunc(ctx, cancel)
		e := s.fill(op)
		stop()
		cancel()
		if e != nil {
			s.cancel()
			return AccountHistoryEntry{}, e
		}
	}
	if e := ctx.Err(); e != nil {
		s.cancel()
		return AccountHistoryEntry{}, e
	}
	if e := s.ctx.Err(); e != nil {
		return AccountHistoryEntry{}, e
	}
	if len(s.pending) == 0 {
		s.eof = true
		s.cancel()
		return AccountHistoryEntry{}, io.EOF
	}
	out := s.pending[0]
	s.pending = s.pending[1:]
	return out, nil
}
func (s *HistoryStream) fill(ctx context.Context) error {
	end := min(s.offset+500, len(s.rows))
	queries := make([]string, 0, end-s.offset)
	for i := s.offset; i < end; i++ {
		hash := s.rows[i].Origin
		if b, e := hex.DecodeString(hash); e != nil || len(b) != 32 {
			return fmt.Errorf("network: invalid history transaction hash")
		}
		queries = append(queries, fmt.Sprintf("tx%d: tx(hash: %s) { blockHash blockTimestamp err gasSpent tx { callData { fnName } gasLimit gasPrice isDeploy memo } }", i, strconv.Quote(hash)))
	}
	data, e := s.client.Query(ctx, strings.Join(queries, "\n"))
	if e != nil {
		return e
	}
	for i := s.offset; i < end; i++ {
		entry, e := enrichHistory(s.account, s.rows[i], data[fmt.Sprintf("tx%d", i)])
		if e != nil {
			return e
		}
		s.pending = append(s.pending, entry)
	}
	s.offset = end
	return nil
}
func number(raw json.RawMessage) (*big.Int, error) {
	v, e := decodeBigInt(raw)
	if e != nil {
		return nil, e
	}
	if !v.IsUint64() {
		return nil, fmt.Errorf("network: integer outside u64")
	}
	return v, nil
}
func text(raw json.RawMessage) string { var s string; _ = json.Unmarshal(raw, &s); return s }
func enrichHistory(account string, row historyRow, raw json.RawMessage) (out AccountHistoryEntry, err error) {
	var tx struct {
		BlockHash      string
		BlockTimestamp json.RawMessage
		Err            json.RawMessage
		GasSpent       json.RawMessage
		Tx             struct {
			CallData           *struct{ FnName string }
			GasLimit, GasPrice json.RawMessage
			IsDeploy           bool
			Memo               string
		}
	}
	if string(raw) == "null" || len(raw) == 0 {
		return out, fmt.Errorf("network: missing history transaction")
	}
	if err = json.Unmarshal(raw, &tx); err != nil {
		return
	}
	out.BlockHash = tx.BlockHash
	out.Hash = row.Origin
	out.Owner = account
	out.Type = "public"
	out.Memo = tx.Tx.Memo
	out.Method = "transfer"
	out.Success = string(tx.Err) == "null"
	if tx.Tx.IsDeploy {
		out.Method = "deploy"
	} else if tx.Tx.CallData != nil {
		out.Method = tx.Tx.CallData.FnName
	}
	out.BlockHeight, err = number(row.Height)
	if err != nil {
		return
	}
	ts, e := number(tx.BlockTimestamp)
	if e != nil || !ts.IsInt64() || ts.Int64() > math.MaxInt64/1000 {
		return out, fmt.Errorf("network: invalid block timestamp")
	}
	out.BlockTimestamp = ts.Int64() * 1000
	out.GasSpent, err = number(tx.GasSpent)
	if err != nil {
		return
	}
	out.GasLimit, err = number(tx.Tx.GasLimit)
	if err != nil {
		return
	}
	out.GasPrice, err = number(tx.Tx.GasPrice)
	if err != nil {
		return
	}
	out.FeePaid = new(big.Int).Mul(out.GasSpent, out.GasPrice)
	out.From, out.To = "N/A", "N/A"
	out.Value = new(big.Int)
	for _, event := range row.Events {
		if event.Target != TransferContract {
			continue
		}
		d := event.Data
		var receiver struct{ Moonlight string }
		_ = json.Unmarshal(d["receiver"], &receiver)
		sender := text(d["sender"])
		v := new(big.Int)
		if b := d["value"]; len(b) > 0 {
			v, err = number(b)
			if err != nil {
				return
			}
		}
		switch event.Topic {
		case "convert":
			if receiver.Moonlight == account {
				out.Value.Add(out.Value, v)
				out.From, out.To = "Phoenix", account
			}
			if sender == account {
				out.Value.Sub(out.Value, v)
				out.From, out.To = account, "Phoenix"
			}
		case "moonlight":
			to := text(d["receiver"])
			if to != "" {
				out.To = to
				if to == account {
					out.Value.Add(out.Value, v)
				}
			}
			out.From = sender
			if sender == account {
				out.Value.Sub(out.Value, v)
			}
			var refund []json.RawMessage
			if json.Unmarshal(d["refund_info"], &refund) == nil && len(refund) == 2 && text(refund[0]) == account && out.To == account {
				r, e := number(refund[1])
				if e != nil {
					return out, e
				}
				out.Value.Add(out.Value, r)
			}
		case "mint", "withdraw":
			if receiver.Moonlight == account {
				out.Value.Add(out.Value, v)
			}
		case "deposit":
			if sender == account {
				out.Value.Sub(out.Value, v)
			}
		case "contract_to_account":
			if text(d["receiver"]) == account {
				out.Value.Add(out.Value, v)
			}
		}
	}
	return
}
