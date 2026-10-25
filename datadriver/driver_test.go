package datadriver

import (
	"context"
	"errors"
	"math/big"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, e := os.ReadFile("../testdata/transfer-driver.wasm")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestRealDriverLifecycleAndInputOwnership(t *testing.T) {
	ctx := context.Background()
	b := fixture(t)
	d, e := Load(ctx, b)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close(ctx)
	clear(b)
	expected := []byte{255, 255, 255, 255, 255, 255, 255, 255}
	out, e := d.Encode(ctx, "deposit", new(big.Int).SetUint64(^uint64(0)))
	if e != nil || string(out) != string(expected) {
		t.Fatalf("max u64 %x %v", out, e)
	}
	out[0] = 0
	out, e = d.Encode(ctx, "deposit", big.NewInt(7))
	if e != nil || len(out) != 8 || out[0] != 7 {
		t.Fatalf("owned output: %x %v", out, e)
	}
	if _, e = d.Encode(ctx, "deposit", "not an integer"); e == nil {
		t.Fatal("invalid amount accepted")
	} else {
		var typed *Error
		if !errors.As(e, &typed) {
			t.Fatalf("untyped ABI status: %T %v", e, e)
		}
	}
	if _, e = d.Encode(ctx, "no such method", nil); e == nil {
		t.Fatal("unknown function accepted")
	}
	if _, e = d.Schema(ctx); e != nil {
		t.Fatal("failed encode poisoned instance", e)
	}
	if e = d.Close(ctx); e != nil {
		t.Fatal(e)
	}
	if e = d.Close(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = d.Version(ctx); !errors.Is(e, ErrClosed) {
		t.Fatalf("closed: %v", e)
	}
}
func TestJSONBoundary(t *testing.T) {
	x := struct {
		Amount *big.Int `json:"amount"`
		Bytes  []byte   `json:"bytes"`
	}{new(big.Int).Lsh(big.NewInt(1), 100), []byte{0, 255}}
	b, e := MarshalValue(x)
	if e != nil || string(b) != `{"amount":"1267650600228229401496703205376","bytes":[0,255]}` {
		t.Fatalf("%s %v", b, e)
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	if _, e = MarshalValue(cycle); e == nil {
		t.Fatal("cycle accepted")
	}
}
func TestRegistryConcurrentLoadReplaceDelete(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry()
	defer r.Close(ctx)
	code := fixture(t)
	var calls atomic.Int32
	if e := r.Register("x", Source(func(ctx context.Context) ([]byte, error) { calls.Add(1); return code, nil })); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	drivers := make(chan *Driver, 12)
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			d, e := r.Get(ctx, "x")
			if e != nil {
				t.Error(e)
				return
			}
			drivers <- d
		})
	}
	wg.Wait()
	close(drivers)
	var first *Driver
	for d := range drivers {
		if first != nil && first != d {
			t.Fatal("load not deduplicated")
		}
		first = d
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	if e := r.Register("x", Bytes(code)); e != nil {
		t.Fatal(e)
	}
	second, e := r.Get(ctx, "x")
	if e != nil || second == first {
		t.Fatal("replacement", e)
	}
	if _, e = first.Version(ctx); e != nil {
		t.Fatal("in-flight reference retired early", e)
	}
	r.Delete("x")
	if r.Has("x") {
		t.Fatal("delete")
	}
	if _, e = r.Get(ctx, "x"); e == nil {
		t.Fatal("missing source")
	}
	if e = r.Close(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = second.Version(ctx); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	if e = r.Register("x", Bytes(code)); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
}
func TestRegistryWaitingCancellation(t *testing.T) {
	r := NewRegistry()
	defer r.Close(context.Background())
	start, release := make(chan struct{}), make(chan struct{})
	code := fixture(t)
	_ = r.Register("x", Source(func(ctx context.Context) ([]byte, error) {
		close(start)
		select {
		case <-release:
			return code, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}))
	done := make(chan error, 1)
	go func() { _, e := r.Get(context.Background(), "x"); done <- e }()
	<-start
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e := r.Get(ctx, "x"); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}

// Tiny hand-encoded malicious ABI modules are only negative boundary fixtures;
// cryptographic and successful data-driver tests always use the real artifacts.
func uleb(v int) []byte {
	var b []byte
	for {
		a := byte(v & 127)
		v >>= 7
		if v > 0 {
			a |= 128
		}
		b = append(b, a)
		if v == 0 {
			return b
		}
	}
}
func section(dst []byte, id byte, body []byte) []byte {
	dst = append(dst, id)
	dst = append(dst, uleb(len(body))...)
	return append(dst, body...)
}
func maliciousABI(variant string) []byte {
	names := []string{"alloc", "dealloc", "encode_input_fn", "decode_output_fn", "decode_event", "get_schema", "get_version", "get_last_error", "init", "decode_input_fn"}
	params := []int{1, 2, 6, 6, 6, 2, 2, 2, 0, 6}
	results := []int{1, 0, 1, 1, 1, 1, 1, 1, 0, 1}
	if variant == "alloc-arity" {
		results[0] = 0
	}
	if variant == "error-arity" {
		results[7] = 0
	}
	wasm := []byte{0, 97, 115, 109, 1, 0, 0, 0}
	types := uleb(len(names))
	funcs := uleb(len(names))
	exports := uleb(len(names) + 1)
	codes := uleb(len(names))
	for i, name := range names {
		types = append(types, 0x60, byte(params[i]))
		for j := 0; j < params[i]; j++ {
			types = append(types, 0x7f)
		}
		types = append(types, byte(results[i]))
		if results[i] > 0 {
			types = append(types, 0x7f)
		}
		funcs = append(funcs, byte(i))
		exports = append(exports, byte(len(name)))
		exports = append(exports, name...)
		exports = append(exports, 0, byte(i))
		body := []byte{0}
		if variant == "version-trap" && name == "get_version" {
			body = append(body, 0x00)
		}
		if variant == "oversized" && name == "get_version" {
			body = append(body, 0x41, 32, 0x41, 0x7f, 0x36, 2, 0)
		}
		if name == "get_schema" && (variant == "schema-json" || variant == "schema-utf8") {
			length := byte(1)
			if variant == "schema-utf8" {
				length = 3
			}
			body = append(body, 0x41, 32, 0x41, length, 0x36, 2, 0)
			payload := []byte{33}
			if variant == "schema-utf8" {
				payload = []byte{34, 0x7f, 34}
			} // i32.const -1 -> store8 0xff
			for j, value := range payload {
				body = append(body, 0x41, byte(36+j), 0x41, value, 0x3a, 0, 0)
			}
		}
		if (variant == "dealloc-loop" && name == "dealloc") || (variant == "init-loop" && name == "init") || (variant == "version-loop" && name == "get_version") {
			body = append(body, 0x03, 0x40, 0x0c, 0, 0x0b)
		}
		if results[i] > 0 {
			body = append(body, 0x41, 0)
			if name == "alloc" {
				body[len(body)-1] = 32
			}
		}
		body = append(body, 0x0b)
		codes = append(codes, uleb(len(body))...)
		codes = append(codes, body...)
	}
	exports = append(exports, 6)
	exports = append(exports, "memory"...)
	exports = append(exports, 2, 0)
	wasm = section(wasm, 1, types)
	wasm = section(wasm, 3, funcs)
	wasm = section(wasm, 5, []byte{1, 0, 2})
	wasm = section(wasm, 7, exports)
	return section(wasm, 10, codes)
}
func TestMalformedABIAndBoundedCleanup(t *testing.T) {
	for _, variant := range []string{"alloc-arity", "error-arity"} {
		t.Run(variant, func(t *testing.T) {
			_, e := Load(context.Background(), maliciousABI(variant))
			var abi *ABIError
			if !errors.As(e, &abi) {
				t.Fatalf("expected ABIError: %T %v", e, e)
			}
		})
	}
	for _, variant := range []string{"dealloc-loop", "init-loop", "version-loop"} {
		t.Run(variant, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			start := time.Now()
			loadCtx := context.Background()
			if variant == "init-loop" {
				loadCtx = ctx
			}
			d, e := Load(loadCtx, maliciousABI(variant))
			if variant != "init-loop" {
				if e != nil {
					t.Fatal(e)
				}
				_, e = d.Version(ctx)
				defer d.Close(context.Background())
				if _, again := d.Version(context.Background()); !errors.Is(again, ErrClosed) {
					t.Fatalf("didn't retire failed instance: %v", again)
				}
			}
			if e == nil {
				t.Fatal("malicious module succeeded")
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("cleanup exceeded budget")
			}
		})
	}
}
func TestCanceledAdmissionDoesNotPoisonDriver(t *testing.T) {
	ctx := context.Background()
	d, e := Load(ctx, fixture(t))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close(ctx)
	<-d.gate
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, e = d.Version(cancelCtx)
	d.gate <- struct{}{}
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if v, e := d.Version(ctx); e != nil || strings.Trim(v, "\"") == "" {
		t.Fatal(v, e)
	}
}
