package datadriver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNativeCorruptionRetiresInstance(t *testing.T) {
	// encoding/json deliberately permits invalid UTF-8 in a quoted string, so
	// json.Valid alone is not a valid native UTF-8 trust-boundary check.
	if !json.Valid([]byte{'"', 255, '"'}) {
		t.Fatal("JSON parser behavior changed")
	}
	for _, variant := range []string{"version-trap", "oversized", "schema-json", "schema-utf8"} {
		t.Run(variant, func(t *testing.T) {
			ctx := context.Background()
			d, e := Load(ctx, maliciousABI(variant))
			if e != nil {
				t.Fatal(e)
			}
			defer d.Close(ctx)
			if strings.HasPrefix(variant, "schema-") {
				_, e = d.Schema(ctx)
			} else {
				_, e = d.Version(ctx)
			}
			if e == nil {
				t.Fatal("corrupt native output accepted")
			}
			if variant == "schema-utf8" && !strings.Contains(e.Error(), "UTF-8") {
				t.Fatalf("not a UTF-8 rejection: %v", e)
			}
			if _, e = d.Version(ctx); !errors.Is(e, ErrClosed) {
				t.Fatalf("corrupt instance reused: %v", e)
			}
		})
	}
}
func TestDriverTimeoutInheritance(t *testing.T) {
	ctx, cancel := readBudget(WithRequestTimeout(context.Background(), 0))
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("timeout zero restored a default deadline")
	}
	parent, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	ctx2, stop2 := readBudget(WithRequestTimeout(parent, 0))
	defer stop2()
	if _, ok := ctx2.Deadline(); !ok {
		t.Fatal("timeout zero disabled parent deadline")
	}
	bad, stop3 := readBudget(WithRequestTimeout(context.Background(), -1))
	defer stop3()
	if bad.Err() == nil {
		t.Fatal("negative timeout accepted")
	}
}
func TestInvalidInputDoesNotRetireNativeInstance(t *testing.T) {
	ctx := context.Background()
	d, e := Load(ctx, fixture(t))
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close(ctx)
	if _, e = d.EncodeJSON(ctx, "deposit", []byte{'"', 255, '"'}); e == nil {
		t.Fatal("invalid UTF-8 input accepted")
	}
	if _, e = d.Version(ctx); e != nil {
		t.Fatal("invalid caller input poisoned native instance", e)
	}
}
