// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
// Package contract combines data-driver encoding with RUES calls and builders.
package contract

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HDauven/w3sper.go/datadriver"
	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/network"
	"github.com/HDauven/w3sper.go/tx"
)

type Contract struct {
	id      string
	idBytes []byte
	network *network.Client
	entry   *tx.BookEntry
	driver  *datadriver.Driver
}

// New accepts an optional loaded driver; otherwise the network registry resolves
// it lazily. Contract does not own the supplied driver or network.
func New(id string, n *network.Client, entry *tx.BookEntry, drivers ...*datadriver.Driver) (*Contract, error) {
	b, e := hex.DecodeString(id)
	if e != nil || len(b) != 32 {
		return nil, fmt.Errorf("contract: ID must be 32-byte hex")
	}
	c := &Contract{id: strings.ToLower(id), idBytes: b, network: n, entry: entry}
	if len(drivers) > 0 {
		c.driver = drivers[0]
	}
	if c.driver == nil && n == nil {
		return nil, fmt.Errorf("contract: driver or registry required")
	}
	return c, nil
}
func (c *Contract) ID() string { return c.id }
func (c *Contract) budget(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.network != nil {
		return c.network.ReadContext(ctx)
	}
	return context.WithTimeout(ctx, 120*time.Second)
}
func (c *Contract) resolve(ctx context.Context) (*datadriver.Driver, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if c.driver != nil {
		return c.driver, nil
	}
	return c.network.DataDrivers.Get(ctx, c.id)
}
func (c *Contract) Schema(ctx context.Context) (json.RawMessage, error) {
	ctx, cancel := c.budget(ctx)
	defer cancel()
	d, e := c.resolve(ctx)
	if e != nil {
		return nil, e
	}
	return d.Schema(ctx)
}
func (c *Contract) Version(ctx context.Context) (string, error) {
	ctx, cancel := c.budget(ctx)
	defer cancel()
	d, e := c.resolve(ctx)
	if e != nil {
		return "", e
	}
	return d.Version(ctx)
}
func (c *Contract) Encode(ctx context.Context, function string, value any) ([]byte, error) {
	input, err := datadriver.MarshalValue(value)
	if err != nil {
		return nil, err
	}
	ctx, cancel := c.budget(ctx)
	defer cancel()
	d, e := c.resolve(ctx)
	if e != nil {
		return nil, e
	}
	return d.EncodeJSON(ctx, function, input)
}
func (c *Contract) Call(ctx context.Context, function string, value any, opts ...network.CallOptions) (json.RawMessage, error) {
	if c.network == nil {
		return nil, fmt.Errorf("contract: network required")
	}
	input, err := datadriver.MarshalValue(value)
	if err != nil {
		return nil, err
	}
	options := network.CallOptions{}
	if len(opts) > 0 {
		options = opts[0]
		options.Headers = options.Headers.Clone()
	}
	ctx, cancel := c.budget(ctx)
	defer cancel()
	d, e := c.resolve(ctx)
	if e != nil {
		return nil, e
	}
	args, e := d.EncodeJSON(ctx, function, input)
	if e != nil {
		return nil, e
	}
	out, e := c.network.Contracts().WithID(c.id).Call(ctx, function, args, options)
	if e != nil {
		return nil, e
	}
	return d.DecodeOutput(ctx, function, out)
}
func (c *Contract) Transaction(ctx context.Context, function string, value any) (*tx.Transfer, error) {
	if c.entry == nil {
		return nil, fmt.Errorf("contract: book entry required")
	}
	if e := c.entry.Signer().Check(); e != nil {
		return nil, e
	}
	input, err := datadriver.MarshalValue(value)
	if err != nil {
		return nil, err
	}
	ctx, cancel := c.budget(ctx)
	defer cancel()
	ctx, stop, e := c.entry.Signer().OperationContext(ctx)
	if e != nil {
		return nil, e
	}
	defer stop()
	d, e := c.resolve(ctx)
	if e != nil {
		return nil, e
	}
	args, e := d.EncodeJSON(ctx, function, input)
	if e != nil {
		return nil, e
	}
	if e = c.entry.Signer().Check(); e != nil {
		return nil, e
	}
	return c.entry.Transfer(big.NewInt(0)).ContractCall(driver.Call{ContractID: c.idBytes, Function: function, Args: args}), nil
}
func (c *Contract) decode(ctx context.Context, name string, event network.Event) (json.RawMessage, error) {
	payload := event.Payload
	if strings.Contains(event.Headers.Get("Content-Type"), "json") || json.Valid(payload) {
		if !utf8.Valid(payload) {
			return nil, fmt.Errorf("contract: invalid UTF-8 event JSON")
		}
		var str string
		if json.Unmarshal(payload, &str) != nil {
			if !json.Valid(payload) {
				return nil, fmt.Errorf("contract: malformed event JSON")
			}
			return append(json.RawMessage(nil), payload...), nil
		}
		bytes, e := hex.DecodeString(str)
		if e != nil {
			bytes, e = base64.StdEncoding.DecodeString(str)
		}
		if e != nil {
			return append(json.RawMessage(nil), payload...), nil
		}
		payload = bytes
	}
	d, e := c.resolve(ctx)
	if e != nil {
		return nil, e
	}
	return d.DecodeEvent(ctx, name, payload)
}
func (c *Contract) Once(ctx context.Context, name string) (json.RawMessage, error) {
	if c.network == nil {
		return nil, fmt.Errorf("contract: network required")
	}
	ctx, cancel := c.budget(ctx)
	defer cancel()
	event, e := c.network.Contracts().WithID(c.id).Once(ctx, name)
	if e != nil {
		return nil, e
	}
	return c.decode(ctx, name, event)
}

// Events is a pull-based decoded subscription, avoiding callback goroutine leaks.
type Events struct {
	contract     *Contract
	name         string
	subscription *network.Subscription
	ctx          context.Context
	cancel       context.CancelFunc
}

func (c *Contract) Subscribe(ctx context.Context, name string) (*Events, error) {
	if c.network == nil {
		return nil, fmt.Errorf("contract: network required")
	}
	ctx, cancel := context.WithCancel(ctx)
	sub, e := c.network.Contracts().WithID(c.id).Subscribe(ctx, name)
	if e != nil {
		cancel()
		return nil, e
	}
	return &Events{contract: c, name: name, subscription: sub, ctx: ctx, cancel: cancel}, nil
}
func (e *Events) Close() error { e.cancel(); return e.subscription.Close() }
func (e *Events) Next(ctx context.Context) (json.RawMessage, error) {
	op, cancel := e.subscription.OperationContext(e.ctx)
	stop := context.AfterFunc(ctx, cancel)
	defer func() { stop(); cancel() }()
	event, err := e.subscription.Next(op)
	if err != nil {
		return nil, err
	}
	op, budget := e.contract.budget(op)
	defer budget()
	out, err := e.contract.decode(op, e.name, event)
	if err != nil {
		e.cancel()
		e.subscription.Cancel()
	}
	return out, err
}
