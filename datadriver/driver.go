// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
// Package datadriver loads contract JSON/RKYV conversion WASM modules.
package datadriver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Driver serializes calls and Close over one WASM memory. Init runs exactly once.
type Driver struct {
	gate   chan struct{}
	rt     wazero.Runtime
	mod    api.Module
	closed bool
}

var ErrClosed = errors.New("datadriver: closed")

var required = map[string][2]int{"alloc": {1, 1}, "dealloc": {2, 0}, "get_last_error": {2, 1}, "encode_input_fn": {6, 1}, "decode_input_fn": {6, 1}, "decode_output_fn": {6, 1}, "decode_event": {6, 1}, "get_schema": {2, 1}, "get_version": {2, 1}, "init": {0, 0}}

// ABIError identifies a malformed or unsupported data-driver module.
type ABIError struct{ Export, Detail string }

func (e *ABIError) Error() string {
	return fmt.Sprintf("datadriver: invalid ABI for %s: %s", e.Export, e.Detail)
}

type timeoutKey struct{}

// WithRequestTimeout overrides the standalone 120-second loader/call budget.
// Zero disables only the default deadline, never caller cancellation.
func WithRequestTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, timeoutKey{}, d)
}
func readBudget(ctx context.Context) (context.Context, context.CancelFunc) {
	if d, ok := ctx.Value(timeoutKey{}).(time.Duration); ok {
		if d < 0 {
			ctx, cancel := context.WithCancel(ctx)
			cancel()
			return ctx, cancel
		}
		if d == 0 {
			return context.WithCancel(ctx)
		}
		return context.WithTimeout(ctx, d)
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, 120*time.Second)
}
func (d *Driver) lock(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.gate:
		return nil
	}
}
func (d *Driver) unlock() { d.gate <- struct{}{} }

func Load(ctx context.Context, wasm []byte) (*Driver, error) {
	ctx, cancel := readBudget(ctx)
	defer cancel()
	wasm = append([]byte(nil), wasm...)
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true).WithMemoryLimitPages(4096))
	mod, e := rt.InstantiateWithConfig(ctx, wasm, wazero.NewModuleConfig())
	if e != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("datadriver: load: %w", e)
	}
	d := &Driver{rt: rt, mod: mod, gate: make(chan struct{}, 1)}
	d.gate <- struct{}{}
	fail := func(e error) (*Driver, error) { _ = rt.Close(ctx); return nil, e }
	if mod.ExportedMemory("memory") == nil {
		return fail(&ABIError{"memory", "missing exported memory"})
	}
	for name, shape := range required {
		fn := mod.ExportedFunction(name)
		if fn == nil {
			return fail(&ABIError{name, "missing export"})
		}
		p, r := fn.Definition().ParamTypes(), fn.Definition().ResultTypes()
		if len(p) != shape[0] || len(r) != shape[1] {
			return fail(&ABIError{name, "wrong parameter/result arity"})
		}
		for _, types := range [][]api.ValueType{p, r} {
			for _, kind := range types {
				if kind != api.ValueTypeI32 {
					return fail(&ABIError{name, "expected i32 ABI"})
				}
			}
		}
	}
	if _, e = mod.ExportedFunction("init").Call(ctx); e != nil {
		return fail(e)
	}
	return d, nil
}
func (d *Driver) Close(ctx context.Context) error {
	if e := d.lock(ctx); e != nil {
		return e
	}
	defer d.unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	return d.rt.Close(ctx)
}
func (d *Driver) run(ctx context.Context, op, name string, input []byte) (out []byte, err error) {
	ctx, cancel := readBudget(ctx)
	defer cancel()
	defer func() {
		if e := ctx.Err(); e != nil && err == nil {
			out = nil
			err = e
		}
	}()
	input = append([]byte(nil), input...)
	if !utf8.ValidString(name) {
		return nil, fmt.Errorf("datadriver: invalid function name")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if e := d.lock(ctx); e != nil {
		return nil, e
	}
	defer d.unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if d.closed || d.mod.IsClosed() {
		return nil, ErrClosed
	}
	type allocation struct{ p, n uint32 }
	var owned []allocation
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		// A trap, malformed output or allocator failure leaves native state
		// untrusted. Ordinary reported operation errors remain reusable.
		var status *Error
		if err != nil && !errors.As(err, &status) {
			d.closed = true
			_ = d.rt.Close(cleanup)
			return
		}
		for i := len(owned) - 1; i >= 0; i-- {
			if d.mod.IsClosed() {
				break
			}
			a := owned[i]
			if b, ok := d.mod.Memory().Read(a.p, a.n); ok {
				clear(b)
			}
			if _, e := d.mod.ExportedFunction("dealloc").Call(cleanup, uint64(a.p), uint64(a.n)); e != nil {
				d.closed = true
				_ = d.rt.Close(cleanup)
				if err == nil {
					out = nil
					err = fmt.Errorf("datadriver: cleanup failed: %w", e)
				}
				break
			}
		}
	}()
	alloc := func(b []byte) (uint64, error) {
		n := len(b)
		if n == 0 {
			n = 1
		}
		if n > 0x7fffffff {
			return 0, fmt.Errorf("datadriver: input too large")
		}
		res, e := d.mod.ExportedFunction("alloc").Call(ctx, uint64(n))
		if e != nil {
			return 0, e
		}
		if len(res) != 1 {
			return 0, &ABIError{"alloc", "missing result"}
		}
		p := uint32(res[0])
		if p == 0 {
			return 0, &ABIError{"alloc", "allocation failed"}
		}
		owned = append(owned, allocation{p, uint32(n)})
		if !d.mod.Memory().Write(p, b) {
			return 0, &ABIError{"alloc", "write outside memory"}
		}
		return uint64(p), nil
	}
	const capacity = 64 * 1024
	output, e := alloc(make([]byte, capacity))
	if e != nil {
		return nil, e
	}
	args := []uint64{output, capacity}
	if op != "get_schema" && op != "get_version" {
		np, e := alloc([]byte(name))
		if e != nil {
			return nil, e
		}
		ip, e := alloc(input)
		if e != nil {
			return nil, e
		}
		args = append([]uint64{np, uint64(len(name)), ip, uint64(len(input))}, args...)
	}
	res, e := d.mod.ExportedFunction(op).Call(ctx, args...)
	if e != nil {
		return nil, e
	}
	read := func() ([]byte, error) {
		b, ok := d.mod.Memory().Read(uint32(output), capacity)
		if !ok {
			return nil, &ABIError{op, "invalid output pointer"}
		}
		n := binary.LittleEndian.Uint32(b)
		if n > capacity-4 {
			return nil, &ABIError{op, fmt.Sprintf("oversized output %d", n)}
		}
		return append([]byte(nil), b[4:4+n]...), nil
	}
	if len(res) == 0 {
		return nil, &ABIError{op, "missing status"}
	}
	if res[0] != 0 {
		code := res[0]
		errorResult, e := d.mod.ExportedFunction("get_last_error").Call(ctx, output, capacity)
		if e != nil {
			return nil, e
		}
		if len(errorResult) != 1 || errorResult[0] != 0 {
			return nil, &ABIError{"get_last_error", "unable to retrieve error text"}
		}
		message, e := read()
		if e != nil {
			return nil, e
		}
		if !utf8.Valid(message) {
			return nil, &ABIError{"get_last_error", "invalid UTF-8"}
		}
		return nil, &Error{Operation: op, Code: code, Message: string(message)}
	}
	out, err = read()
	if err == nil && op != "encode_input_fn" {
		if !utf8.Valid(out) {
			return nil, &ABIError{op, "invalid UTF-8 output"}
		}
		if op != "get_version" && !json.Valid(out) {
			return nil, &ABIError{op, "invalid JSON output"}
		}
	}
	return out, err
}

type Error struct {
	Operation string
	Code      uint64
	Message   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("datadriver: %s (%d): %s", e.Operation, e.Code, e.Message)
}
func (d *Driver) Encode(ctx context.Context, name string, value any) ([]byte, error) {
	b, e := MarshalValue(value)
	if e != nil {
		return nil, e
	}
	return d.EncodeJSON(ctx, name, b)
}
func (d *Driver) EncodeJSON(ctx context.Context, name string, b json.RawMessage) ([]byte, error) {
	if !utf8.Valid(b) || !json.Valid(b) {
		return nil, fmt.Errorf("datadriver: invalid JSON")
	}
	return d.run(ctx, "encode_input_fn", name, b)
}
func (d *Driver) json(ctx context.Context, op, name string, b []byte) (json.RawMessage, error) {
	return d.run(ctx, op, name, b)
}
func (d *Driver) DecodeInput(ctx context.Context, name string, b []byte) (json.RawMessage, error) {
	return d.json(ctx, "decode_input_fn", name, b)
}
func (d *Driver) DecodeOutput(ctx context.Context, name string, b []byte) (json.RawMessage, error) {
	return d.json(ctx, "decode_output_fn", name, b)
}
func (d *Driver) DecodeEvent(ctx context.Context, name string, b []byte) (json.RawMessage, error) {
	return d.json(ctx, "decode_event", name, b)
}
func (d *Driver) Schema(ctx context.Context) (json.RawMessage, error) {
	return d.json(ctx, "get_schema", "", nil)
}
func (d *Driver) Version(ctx context.Context) (string, error) {
	b, e := d.run(ctx, "get_version", "", nil)
	return string(b), e
}
