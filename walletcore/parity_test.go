package walletcore

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"strconv"
	"testing"

	"github.com/HDauven/w3sper.go/datadriver"
	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/profile"
	"github.com/HDauven/w3sper.go/tx"
)

type oracle struct {
	Seed     string
	Profiles []struct {
		Index             int64
		Bytes, RawAccount string
	}
	Messages []struct {
		Index              int64
		Message, Signature string
	}
	Transactions []struct {
		Name, Bytes, Hash, ContractID string
		Input                         struct {
			Kind, Value, Memo, Deposit, ContractID, FnName, FnArgs, Bytecode, DeployNonce, InitArgs string
			ReceiverIndex, OwnerIndex                                                               int64
		}
	}
	DataDrivers map[string]struct {
		ID, Version, FnName, Encoded string
		Schema, Input, Decoded       json.RawMessage
	}
	Notes []struct {
		Index                 int64
		Entries, Picked       [][2]string
		Balance               struct{ Value, Spendable string }
		Bookmarks, PickAmount string
	}
}

func loadOracle(t *testing.T) oracle {
	t.Helper()
	b, e := os.ReadFile("../testdata/js-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var o oracle
	if e = json.Unmarshal(b, &o); e != nil {
		t.Fatal(e)
	}
	return o
}
func hexBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, e := hex.DecodeString(s)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func decimal(t *testing.T, s string) *big.Int {
	t.Helper()
	if s == "" {
		return big.NewInt(0)
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatal(s)
	}
	return v
}
func oracleSigners(t *testing.T, w *WalletCore, o oracle) (*profile.Generator, []*profile.Signer) {
	t.Helper()
	seed := hexBytes(t, o.Seed)
	g := profile.NewGenerator(func() ([]byte, error) { return seed, nil }, w)
	ss := make([]*profile.Signer, 3)
	for i := range ss {
		p, e := g.Derive(int64(i))
		if e != nil {
			t.Fatal(e)
		}
		ss[i], e = g.Signer(context.Background(), p)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(ss[i].Dispose)
	}
	return g, ss
}
func TestJSOracleProfilesAndBLS(t *testing.T) {
	w := testWallet(t)
	o := loadOracle(t)
	g, ss := oracleSigners(t, w, o)
	ctx := context.Background()
	for _, v := range o.Profiles {
		t.Run(fmt.Sprintf("profile-%d", v.Index), func(t *testing.T) {
			p, e := g.Derive(v.Index)
			if e != nil {
				t.Fatal(e)
			}
			if hex.EncodeToString(p.Bytes()) != v.Bytes {
				t.Fatal("profile differs from JS")
			}
			raw, e := w.AccountsIntoRaw(ctx, [][]byte{p.Account().Bytes()})
			if e != nil || hex.EncodeToString(raw[0]) != v.RawAccount {
				t.Fatalf("raw account differs: %v", e)
			}
			if p.Seed() != nil {
				t.Fatal("public identity retained seed")
			}
		})
	}
	for i, v := range o.Messages {
		t.Run(fmt.Sprintf("message-%d", i), func(t *testing.T) {
			sig, e := ss[v.Index].SignBLSMessage(ctx, hexBytes(t, v.Message))
			if e != nil || hex.EncodeToString(sig) != v.Signature {
				t.Fatalf("BLS vector mismatch: %v", e)
			}
		})
	}
}
func TestJSOracleTransactionsAndBuilders(t *testing.T) {
	w := testWallet(t)
	o := loadOracle(t)
	g, ss := oracleSigners(t, w, o)
	ctx := context.Background()
	treasury := &integrationTreasury{nonce: big.NewInt(5), value: big.NewInt(10000000000000000)}
	entry := tx.NewBookEntry(ss[0], treasury, w)
	for _, v := range o.Transactions {
		t.Run(v.Name, func(t *testing.T) {
			input := v.Input
			var payload *driver.Payload
			in := driver.TransactionInput{Signer: ss[0], GasLimit: 500000000, GasPrice: 3, Nonce: 6, ChainID: 42, Amount: decimal(t, input.Value).Uint64(), Deposit: decimal(t, input.Deposit).Uint64()}
			var builder *tx.Builder
			switch input.Kind {
			case "deploy":
				deploy := driver.Deployment{Bytecode: hexBytes(t, input.Bytecode), Owner: ss[0].Account().Bytes(), InitArgs: hexBytes(t, input.InitArgs), Nonce: decimal(t, input.DeployNonce).Uint64()}
				payload = &driver.Payload{Deploy: &deploy}
				builder = entry.Deploy(deploy.Bytecode).InitArgs(deploy.InitArgs).DeployNonce(deploy.Nonce)
			case "stake":
				in.Operation = driver.Stake
				in.Owner = ss[input.OwnerIndex]
				builder = entry.Stake(decimal(t, input.Value)).Owner(in.Owner)
			default:
				receiver, _ := g.Derive(input.ReceiverIndex)
				in.Receiver = receiver.Account().Bytes()
				builder = entry.Transfer(decimal(t, input.Value)).Deposit(decimal(t, input.Deposit))
				if input.Memo != "" {
					payload = &driver.Payload{Memo: []byte(input.Memo)}
				}
				if input.FnName != "" {
					payload = &driver.Payload{Call: &driver.Call{ContractID: hexBytes(t, input.ContractID), Function: input.FnName, Args: hexBytes(t, input.FnArgs), Committed: input.Kind == "committed"}}
				}
				if input.Kind == "committed" {
					in.Receiver = nil
					receiver = ss[0].Profile()
					builder.Committed()
				}
				if payload != nil {
					builder.Payload(*payload)
				}
				route, e := builder.To(receiver.Account().String())
				if e != nil {
					t.Fatal(e)
				}
				builder = route.Account.Builder
			}
			if payload != nil {
				var e error
				in.Data, e = w.CreateData(ctx, *payload)
				if e != nil {
					t.Fatal(e)
				}
			}
			raw, e := w.Build(ctx, in)
			if e != nil {
				t.Fatal(e)
			}
			if hex.EncodeToString(raw.Buffer) != v.Bytes || raw.Hash != v.Hash {
				t.Fatalf("low-level JS mismatch hash %s want %s", raw.Hash, v.Hash)
			}
			built, e := builder.Chain(42).Gas(tx.Gas(big.NewInt(500000000), big.NewInt(3))).BuildContext(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(built.Buffer, raw.Buffer) || built.Hash != v.Hash {
				t.Fatal("high-level JS mismatch")
			}
			if v.ContractID != "" && built.ContractID != v.ContractID {
				t.Fatal("contract ID mismatch")
			}
		})
	}
	if len(w.allocs) != 0 {
		t.Fatalf("leaked WASM inputs: %v", w.allocs)
	}
}
func noteEntries(t *testing.T, entries [][2]string) []driver.Note {
	out := make([]driver.Note, len(entries))
	for i, n := range entries {
		out[i] = driver.Note{Nullifier: hexBytes(t, n[0]), Value: hexBytes(t, n[1])}
	}
	return out
}
func TestJSOracleOwnedNotes(t *testing.T) {
	w := testWallet(t)
	o := loadOracle(t)
	_, ss := oracleSigners(t, w, o)
	ctx := context.Background()
	var leaves []byte
	for _, v := range o.Notes {
		notes := noteEntries(t, v.Entries)
		for _, n := range notes {
			leaves = append(leaves, n.Value...)
		}
		t.Run(fmt.Sprintf("notes-%d", v.Index), func(t *testing.T) {
			value, sp, e := w.BalanceFor(ctx, ss[v.Index], notes)
			if e != nil || strconv.FormatUint(value, 10) != v.Balance.Value || strconv.FormatUint(sp, 10) != v.Balance.Spendable {
				t.Fatalf("balance mismatch %d %d %v", value, sp, e)
			}
			picked, e := w.PickFor(ctx, ss[v.Index], notes, decimal(t, v.PickAmount).Uint64())
			if e != nil || !reflect.DeepEqual(picked, noteEntries(t, v.Picked)) {
				t.Fatalf("picking mismatch: %v", e)
			}
			marks, e := w.Bookmarks(ctx, notes)
			if e != nil {
				t.Fatal(e)
			}
			b := make([]byte, len(marks)*8)
			for i, m := range marks {
				binary.LittleEndian.PutUint64(b[i*8:], m)
			}
			if hex.EncodeToString(b) != v.Bookmarks {
				t.Fatal("bookmark mismatch")
			}
		})
	}
	mapped, info, e := w.MapOwned(ctx, ss, leaves)
	if e != nil {
		t.Fatal(e)
	}
	if info.Bookmark != 23 || info.BlockHeight != 0 {
		t.Fatalf("bad sync info %+v", info)
	}
	for i, v := range o.Notes {
		if !reflect.DeepEqual(mapped[i], noteEntries(t, v.Entries)) {
			t.Fatal("ownership mapping mismatch")
		}
	}
	if _, _, e = w.MapOwned(ctx, ss, leaves[:len(leaves)-1]); e == nil {
		t.Fatal("truncated leaf accepted")
	}
	if len(w.allocs) != 0 {
		t.Fatal("leaked WASM inputs")
	}
}
func TestJSOracleDataDrivers(t *testing.T) {
	o := loadOracle(t)
	ctx := context.Background()
	for name, v := range o.DataDrivers {
		t.Run(name, func(t *testing.T) {
			b, e := os.ReadFile("../testdata/" + name + "-driver.wasm")
			if e != nil {
				t.Fatal(e)
			}
			d, e := datadriver.Load(ctx, b)
			if e != nil {
				t.Fatal(e)
			}
			defer d.Close(ctx)
			version, e := d.Version(ctx)
			if e != nil || version != v.Version {
				t.Fatalf("version: %s %v", version, e)
			}
			schema, e := d.Schema(ctx)
			if e != nil {
				t.Fatal(e)
			}
			var want, got any
			json.Unmarshal(v.Schema, &want)
			json.Unmarshal(schema, &got)
			if !reflect.DeepEqual(want, got) {
				t.Fatal("schema differs from JS")
			}
			encoded, e := d.EncodeJSON(ctx, v.FnName, v.Input)
			if e != nil || hex.EncodeToString(encoded) != v.Encoded {
				t.Fatalf("encoding mismatch %v", e)
			}
			decoded, e := d.DecodeInput(ctx, v.FnName, encoded)
			if e != nil {
				t.Fatal(e)
			}
			json.Unmarshal(v.Decoded, &want)
			json.Unmarshal(decoded, &got)
			if !reflect.DeepEqual(want, got) {
				t.Fatal("decoded differs")
			}
			if _, e = d.Encode(ctx, "not_a_function", nil); e == nil {
				t.Fatal("missing function accepted")
			}
			if _, e = d.DecodeInput(ctx, v.FnName, []byte{1}); e == nil {
				t.Fatal("truncated archive accepted")
			}
		})
	}
}
