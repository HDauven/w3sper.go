// Command live-parity exercises a funded synthetic wallet against a live Rusk
// node. It never creates genesis or changes node configuration. Submission is
// opt-in; use only disposable local-network funds.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"strconv"
	"time"

	"github.com/HDauven/w3sper.go/bookkeeper"
	"github.com/HDauven/w3sper.go/contract"
	"github.com/HDauven/w3sper.go/datadriver"
	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/network"
	"github.com/HDauven/w3sper.go/profile"
	"github.com/HDauven/w3sper.go/tx"
	"github.com/HDauven/w3sper.go/walletcore"
)

func env(k, fallback string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return fallback
}

type state struct {
	Accounts []network.AccountBalance    `json:"accounts"`
	Phoenix  []bookkeeper.AddressBalance `json:"phoenix"`
	Stakes   []network.StakeInfo         `json:"stakes"`
	Keys     []*network.StakeKeys        `json:"keys"`
}
type transaction struct {
	Name, Hash, ContractID string
	Bytes                  int
	Milliseconds           int64
	GasPaid                string
	Mined                  json.RawMessage
	Before, After          state
	Nullifiers             []string
	ExecutionError         string `json:"executionError,omitempty"`
}
type report struct {
	URL            string        `json:"url"`
	Index          int           `json:"index"`
	Status         string        `json:"status"`
	Error          string        `json:"error,omitempty"`
	Checks         []string      `json:"checks"`
	Failures       []string      `json:"failures,omitempty"`
	Transactions   []transaction `json:"transactions"`
	Initial, Final state
	History        []network.AccountHistoryEntry `json:"history"`
	BlockEvents    []network.Event               `json:"blockEvents"`
}

func main() {
	url := flag.String("url", env("W3SPER_NODE_URL", "http://127.0.0.1:18280"), "Rusk URL")
	seed := flag.String("seed", env("W3SPER_SEED", ""), "64-byte synthetic seed in hex")
	indexDefault, _ := strconv.Atoi(env("W3SPER_PROFILE_INDEX", "10"))
	index := flag.Int("index", indexDefault, "sender profile; receiver/owner/reward are +1/+2/+3")
	group := flag.String("group", "all", "read, transfers, contracts, staking or all")
	submit := flag.Bool("submit", false, "submit real transactions using synthetic funded profiles")
	stakeDriver := flag.String("stake-driver", env("W3SPER_STAKE_DRIVER", "testdata/stake-driver.wasm"), "stake data-driver WASM")
	transferDriver := flag.String("transfer-driver", env("W3SPER_TRANSFER_DRIVER", "testdata/transfer-driver.wasm"), "transfer data-driver WASM")
	bytecode := flag.String("contract", env("W3SPER_CONTRACT", "testdata/bob.wasm"), "Bob test contract with init/value/reset ABI")
	committedTarget := flag.String("committed-contract", env("W3SPER_COMMITTED_CONTRACT", "testdata/committed-target.wasm"), "host-backed B ABI test target")
	flag.Parse()
	r := &report{URL: *url, Index: *index, Status: "failed"}
	err := run(r, *seed, *group, *submit, *stakeDriver, *transferDriver, *bytecode, *committedTarget)
	if err != nil {
		r.Error = err.Error()
	} else {
		r.Status = "passed"
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(r *report, seedHex, group string, submit bool, stakePath, transferPath, contractPath, committedPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	seed, e := hex.DecodeString(seedHex)
	if e != nil || len(seed) != 64 {
		return errors.New("provide a 64-byte synthetic W3SPER_SEED/-seed")
	}
	defer clear(seed)
	if r.Index < 0 || r.Index > 252 {
		return errors.New("index must be in 0..252")
	}
	if group != "all" && group != "read" && group != "transfers" && group != "contracts" && group != "staking" {
		return errors.New("invalid group")
	}
	w, e := walletcore.Load(ctx)
	if e != nil {
		return e
	}
	defer w.Close(context.Background())
	n, e := network.New(r.URL, network.WithTimeout(180*time.Second))
	if e != nil {
		return e
	}
	defer n.DataDrivers.Close(context.Background())
	if e = n.Connect(ctx); e != nil {
		return e
	}
	defer n.Disconnect(context.Background())
	if e = n.Ping(ctx); e != nil {
		return e
	}
	if e = n.PingWebSocket(ctx); e != nil {
		return e
	}
	info, e := network.NewNode(n).Info(ctx)
	if e != nil {
		return e
	}
	r.Checks = append(r.Checks, fmt.Sprintf("HTTP/RUES/node-info chain=%d", info.ChainID))
	for _, topic := range []string{"accepted", "statechange"} {
		wait, stop := context.WithTimeout(ctx, 15*time.Second)
		event, e := n.Blocks().Once(wait, topic)
		stop()
		if e != nil {
			return fmt.Errorf("block %s: %w", topic, e)
		}
		if event.Origin.Scope != "blocks" || event.Origin.Topic != topic || !json.Valid(event.Payload) {
			return errors.New("invalid block event")
		}
		r.BlockEvents = append(r.BlockEvents, event)
	}
	if _, e = n.GasPrice(ctx); e != nil {
		return e
	}
	if _, e = network.NewNode(n).Provisioners(ctx); e != nil {
		return e
	}
	crs, e := network.NewNode(n).CRS(ctx)
	if e != nil || len(crs) == 0 {
		return fmt.Errorf("CRS: %w", e)
	}
	r.Checks = append(r.Checks, fmt.Sprintf("block accepted/statechange events, gas price, provisioners, CRS (%d bytes)", len(crs)))
	if e = n.DataDrivers.Register(network.StakeContract, datadriver.File(stakePath)); e != nil {
		return e
	}
	if e = n.DataDrivers.Register(network.TransferContract, datadriver.File(transferPath)); e != nil {
		return e
	}
	generator := profile.NewGenerator(func() ([]byte, error) { return seed, nil }, w)
	signers := make([]*profile.Signer, 4)
	accounts := make([][]byte, 4)
	entries := make([]*tx.BookEntry, 4)
	treasury := bookkeeper.NewNetworkTreasury(n, w)
	keeper := bookkeeper.New(treasury, w, bookkeeper.WithNetwork(n))
	accountSync := network.NewAccountSyncer(n, w)
	addressSync := network.NewAddressSyncer(n, w)
	for i := range signers {
		p, e := generator.Derive(int64(r.Index + i))
		if e != nil {
			return e
		}
		signers[i], e = generator.Signer(ctx, p)
		if e != nil {
			return e
		}
		defer signers[i].Dispose()
		accounts[i] = p.Account().Bytes()
		entries[i], e = keeper.As(signers[i])
		if e != nil {
			return e
		}
	}
	refresh := func() (state, error) {
		var out state
		if _, e := treasury.Sync(ctx, signers...); e != nil {
			return out, e
		}
		var e error
		out.Accounts, e = accountSync.Balances(ctx, accounts)
		if e != nil {
			return out, e
		}
		out.Stakes, e = accountSync.Stakes(ctx, accounts)
		if e != nil {
			return out, e
		}
		out.Keys, e = accountSync.StakeKeys(ctx, accounts)
		if e != nil {
			return out, e
		}
		for _, s := range signers {
			b, e := keeper.BalanceContext(ctx, s)
			if e != nil {
				return out, e
			}
			out.Phoenix = append(out.Phoenix, *b.Address)
		}
		return out, nil
	}
	current, e := refresh()
	if e != nil {
		return e
	}
	r.Initial = current
	r.Final = current
	r.Checks = append(r.Checks, "account/stake/owner/note sync and balances")
	transfer, e := contract.New(network.TransferContract, n, entries[0])
	if e != nil {
		return e
	}
	chain, e := transfer.Call(ctx, "chain_id", nil)
	if e != nil || string(chain) != strconv.Itoa(info.ChainID) {
		return fmt.Errorf("contract chain_id mismatch %s: %v", chain, e)
	}
	if _, e = transfer.Schema(ctx); e != nil {
		return e
	}
	if _, e = transfer.Version(ctx); e != nil {
		return e
	}
	if _, e = transfer.Call(ctx, "sync_accounts", []string{"0", "5"}, network.CallOptions{Feeder: true}); e != nil {
		return e
	}
	r.Checks = append(r.Checks, "contract facade/schema/version/feeder")
	if !submit || group == "read" {
		r.Checks = append(r.Checks, "read-only run; no transactions submitted")
		return nil
	}
	gas := tx.Gas(big.NewInt(500000000), big.NewInt(1))
	deployGas := tx.Gas(big.NewInt(500000000), big.NewInt(2000))
	var lastHeight uint64
	var committedBuilder *tx.Builder
	var committedContract string
	perform := func(name string, b *tx.Builder, payer int, shielded bool, accountDelta, phoenixDelta [4]int64) (*tx.Result, error) {
		before := current
		start := time.Now()
		result, e := b.BuildContext(ctx, n)
		if e != nil {
			return nil, fmt.Errorf("%s build: %w", name, e)
		}
		wait, stop := context.WithTimeout(ctx, 180*time.Second)
		defer stop()
		event, e := result.ExecuteAndWait(wait, n)
		if e != nil {
			return nil, fmt.Errorf("%s execute: %w", name, e)
		}
		fee, e := event.GasPaid()
		if e != nil {
			return nil, e
		}
		data, e := n.Query(ctx, `query Check($hash: String!) { tx(hash: $hash) { id err gasSpent blockHeight tx { gasPrice gasLimit txType isDeploy contractId memo callData { contractId fnName committed } } } }`, network.WithVariables(map[string]interface{}{"hash": result.Hash}))
		if e != nil {
			return nil, e
		}
		var mined struct {
			ID          string
			Err         json.RawMessage
			GasSpent    json.Number
			BlockHeight uint64
			Tx          struct{ GasPrice json.Number }
		}
		if e = json.Unmarshal(data["tx"], &mined); e != nil {
			return nil, e
		}
		if mined.ID != result.Hash {
			return nil, fmt.Errorf("%s: mined transaction hash mismatch", name)
		}
		executionError := ""
		if string(mined.Err) != "null" {
			executionError = string(mined.Err)
		}
		lastHeight = mined.BlockHeight
		gasSpent, ok := new(big.Int).SetString(mined.GasSpent.String(), 10)
		if !ok {
			return nil, errors.New("invalid gasSpent")
		}
		price, ok := new(big.Int).SetString(mined.Tx.GasPrice.String(), 10)
		if !ok {
			return nil, errors.New("invalid gasPrice")
		}
		if fee.Cmp(new(big.Int).Mul(gasSpent, price)) != 0 {
			return nil, errors.New("event fee mismatch")
		}
		after, e := refresh()
		if e != nil {
			return nil, e
		}
		for i := 0; i < 4; i++ {
			expectedAccount := big.NewInt(accountDelta[i])
			expectedPhoenix := big.NewInt(phoenixDelta[i])
			nonce := int64(0)
			if i == payer {
				if shielded {
					expectedPhoenix.Sub(expectedPhoenix, fee)
				} else {
					expectedAccount.Sub(expectedAccount, fee)
					nonce = 1
				}
			}
			actualAccount := new(big.Int).Sub(after.Accounts[i].Value, before.Accounts[i].Value)
			actualPhoenix := new(big.Int).Sub(after.Phoenix[i].Value, before.Phoenix[i].Value)
			actualNonce := new(big.Int).Sub(after.Accounts[i].Nonce, before.Accounts[i].Nonce)
			if actualAccount.Cmp(expectedAccount) != 0 || actualPhoenix.Cmp(expectedPhoenix) != 0 || actualNonce.Cmp(big.NewInt(nonce)) != 0 {
				return nil, fmt.Errorf("%s state delta profile %d: account %s/%s Phoenix %s/%s nonce %s/%d", name, i, actualAccount, expectedAccount, actualPhoenix, expectedPhoenix, actualNonce, nonce)
			}
		}
		current = after
		r.Final = after
		record := transaction{Name: name, Hash: result.Hash, ContractID: result.ContractID, Bytes: len(result.Buffer), Milliseconds: time.Since(start).Milliseconds(), GasPaid: fee.String(), Mined: data["tx"], Before: before, After: after}
		if len(result.Nullifiers) > 0 {
			spent, e := addressSync.Spent(ctx, result.Nullifiers)
			if e != nil {
				return nil, e
			}
			if len(spent) != len(result.Nullifiers) {
				return nil, errors.New("spent nullifier check failed")
			}
			for _, x := range result.Nullifiers {
				record.Nullifiers = append(record.Nullifiers, hex.EncodeToString(x))
			}
		}
		record.ExecutionError = executionError
		r.Transactions = append(r.Transactions, record)
		if executionError != "" {
			return result, fmt.Errorf("%s: execution rejected %s (hash %s)", name, executionError, result.Hash)
		}
		fmt.Fprintln(os.Stderr, "passed", name, result.Hash)
		return result, nil
	}
	selected := func(name string) bool { return group == "all" || group == name }
	route := func(b *tx.Builder, target string) (*tx.Builder, error) {
		out, e := b.To(target)
		if e != nil {
			return nil, e
		}
		if out.Account != nil {
			return out.Account.Builder, nil
		}
		return out.Address.Builder, nil
	}
	if selected("transfers") {
		b, e := route(entries[0].Transfer(big.NewInt(123)).Memo([]byte("Go parity 🌒")).Gas(gas), signers[1].Account().String())
		if e != nil {
			return e
		}
		if _, e = perform("moonlight-memo", b, 0, false, [4]int64{-123, 123}, [4]int64{}); e != nil {
			return e
		}
		b, e = route(entries[0].Transfer(big.NewInt(456)).Memo([]byte("shielded Go parity")).Gas(gas), signers[1].Address().String())
		if e != nil {
			return e
		}
		shieldedTx, e := perform("phoenix-memo", b, 0, true, [4]int64{}, [4]int64{-456, 456})
		if e != nil {
			return e
		}
		corrupt := bytes.Clone(shieldedTx.Buffer)
		corrupt[len(corrupt)-1] ^= 1
		if e = n.Preverify(ctx, corrupt); e == nil {
			return errors.New("corrupt Phoenix transaction accepted")
		}
		r.Checks = append(r.Checks, "corrupt Phoenix transaction rejected by preverify")
		if _, e = perform("shield", entries[0].Shield(big.NewInt(1000000)).Gas(gas), 0, false, [4]int64{-1000000}, [4]int64{1000000}); e != nil {
			return e
		}
		if _, e = perform("unshield", entries[0].Unshield(big.NewInt(500000)).Gas(gas), 0, true, [4]int64{500000}, [4]int64{-500000}); e != nil {
			return e
		}
		b, e = route(entries[0].Transfer(big.NewInt(1)).Gas(gas), signers[1].Account().String())
		if e != nil {
			return e
		}
		signed, e := b.BuildContext(ctx, n)
		if e != nil {
			return e
		}
		corrupt = bytes.Clone(signed.Buffer)
		corrupt[len(corrupt)-1] ^= 1
		if e = n.Preverify(ctx, corrupt); e == nil {
			return errors.New("corrupt Moonlight signature accepted")
		}
		r.Checks = append(r.Checks, "corrupt Moonlight signature rejected by preverify")
	}
	if selected("contracts") {
		for _, shielded := range []bool{false, true} {
			payment := "account"
			target := signers[0].Account().String()
			var ad, pd [4]int64
			if shielded {
				payment = "address"
				target = signers[0].Address().String()
				pd[0] = -2
			} else {
				ad[0] = -2
			}
			events, e := transfer.Subscribe(ctx, "deposit")
			if e != nil {
				return e
			}
			b, e := transfer.Transaction(ctx, "deposit", "2")
			if e != nil {
				events.Close()
				return e
			}
			b, e = route(b.Deposit(big.NewInt(2)).Gas(gas), target)
			if e != nil {
				events.Close()
				return e
			}
			_, e = perform("contract-deposit-"+payment, b, 0, shielded, ad, pd)
			if e != nil {
				events.Close()
				return e
			}
			eventCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			payload, e := events.Next(eventCtx)
			cancel()
			events.Close()
			if e != nil {
				return e
			}
			var event struct{ Value string }
			if e = json.Unmarshal(payload, &event); e != nil || event.Value != "2" {
				return fmt.Errorf("deposit event %s: %v", payload, e)
			}
		}
		code, e := os.ReadFile(contractPath)
		if e != nil {
			return e
		}
		nonceBase := uint64(time.Now().UnixNano())
		for i, shielded := range []bool{false, true} {
			payment := "account"
			target := signers[0].Account().String()
			b := entries[0].Deploy(code).InitArgs([]byte{7}).DeployNonce(nonceBase + uint64(i)).Gas(deployGas)
			if shielded {
				payment = "address"
				target = signers[0].Address().String()
				b.Shielded()
			}
			deployed, e := perform("deploy-init-"+payment, b, 0, shielded, [4]int64{}, [4]int64{})
			if e != nil {
				return e
			}
			scope := n.Contracts().WithID(deployed.ContractID)
			value, e := scope.Call(ctx, "value", nil)
			if e != nil || !bytes.Equal(value, []byte{7}) {
				return fmt.Errorf("deployment init failed: %x %v", value, e)
			}
			id, _ := hex.DecodeString(deployed.ContractID)
			b, e = route(entries[0].Transfer(big.NewInt(0)).ContractCall(driver.Call{ContractID: id, Function: "reset", Args: []byte{9}}).Gas(gas), target)
			if e != nil {
				return e
			}
			name := "call-reset-" + payment
			if _, e = perform(name, b, 0, shielded, [4]int64{}, [4]int64{}); e != nil {
				return e
			}
			value, e = scope.Call(ctx, "value", nil)
			if e != nil || !bytes.Equal(value, []byte{9}) {
				return fmt.Errorf("persisted contract state failed: %x %v", value, e)
			}
		}
		r.Checks = append(r.Checks, "public/shielded deployment initialization and persisted call state")
		host, e := os.ReadFile(committedPath)
		if e != nil {
			return e
		}
		deployed, e := perform("deploy-host-committed", entries[0].Deploy(host).InitArgs(bytes.Repeat([]byte{7}, 32)).DeployNonce(nonceBase+2).Gas(deployGas), 0, false, [4]int64{}, [4]int64{})
		if e != nil {
			return e
		}
		committedContract = deployed.ContractID
		value, e := n.Contracts().WithID(committedContract).Call(ctx, "value", nil)
		if e != nil || !bytes.Equal(value, bytes.Repeat([]byte{7}, 32)) {
			return fmt.Errorf("host initialization %x: %v", value, e)
		}
		id, _ := hex.DecodeString(committedContract)
		committedBuilder, e = route(entries[0].Transfer(big.NewInt(0)).ContractCall(driver.Call{ContractID: id, Function: "run", Args: bytes.Repeat([]byte{9}, 32)}).Committed().Gas(gas), signers[0].Account().String())
		if e != nil {
			return e
		}
	}
	if selected("staking") {
		const stakeValue = 1000000000789
		for payer, shielded := range []bool{false, true} {
			payment := "account"
			if shielded {
				payment = "address"
			}
			entry := entries[payer]
			decorate := func(b *tx.Builder) *tx.Builder {
				b.Owner(signers[2]).Gas(gas)
				if shielded {
					b.Shielded()
				}
				return b
			}
			delta := func(value int64) ([4]int64, [4]int64) {
				var a, p [4]int64
				if shielded {
					p[payer] = value
				} else {
					a[payer] = value
				}
				return a, p
			}
			if current.Stakes[payer].Amount != nil {
				return fmt.Errorf("profile %d already staked; refusing to alter unknown stake", r.Index+payer)
			}
			a, p := delta(-stakeValue)
			if _, e = perform("stake-"+payment, decorate(entry.Stake(big.NewInt(stakeValue))), payer, shielded, a, p); e != nil {
				return e
			}
			if current.Keys[payer] == nil || current.Keys[payer].Owner.Account != signers[2].Account().String() {
				return errors.New("distinct stake owner mismatch")
			}
			wrong := entry.Topup(big.NewInt(1)).Owner(signers[payer]).Gas(gas)
			if shielded {
				wrong.Shielded()
			}
			if _, e = wrong.BuildContext(ctx, n); e == nil {
				return errors.New("wrong owner accepted")
			}
			r.Checks = append(r.Checks, "wrong stake owner rejected: "+payment)
			a, p = delta(-123)
			if _, e = perform("topup-"+payment, decorate(entry.Topup(big.NewInt(123))), payer, shielded, a, p); e != nil {
				return e
			}
			a, p = delta(12)
			if _, e = perform("partial-unstake-"+payment, decorate(entry.Unstake(big.NewInt(12))), payer, shielded, a, p); e != nil {
				return e
			}
			if current.Stakes[payer].Amount.Total.Cmp(big.NewInt(1000000000900)) != 0 {
				return errors.New("remaining stake mismatch")
			}
			a, p = delta(1000000000900)
			if _, e = perform("full-unstake-"+payment, decorate(entry.Unstake(nil)), payer, shielded, a, p); e != nil {
				return e
			}
			if current.Stakes[payer].Amount != nil || current.Keys[payer] != nil {
				return errors.New("full unstake did not clear stake")
			}
		}
		for _, shielded := range []bool{false, true} {
			payment := "account"
			b := entries[3].Withdraw(big.NewInt(100)).Owner(signers[2]).Gas(gas)
			var a, p [4]int64
			if shielded {
				payment = "address"
				b.Shielded()
				p[3] = 100
			} else {
				a[3] = 100
			}
			reward := new(big.Int).Set(current.Stakes[3].Reward)
			if _, e = perform("reward-"+payment, b, 3, shielded, a, p); e != nil {
				return e
			}
			if new(big.Int).Sub(reward, current.Stakes[3].Reward).Cmp(big.NewInt(100)) != 0 {
				return errors.New("reward state mismatch")
			}
		}
	}
	if committedBuilder != nil {
		_, e = perform("committed-run", committedBuilder, 0, false, [4]int64{}, [4]int64{})
		want := byte(9)
		if e != nil {
			r.Failures = append(r.Failures, e.Error())
			want = 7
		}
		value, readErr := n.Contracts().WithID(committedContract).Call(ctx, "value", nil)
		if readErr != nil || !bytes.Equal(value, bytes.Repeat([]byte{want}, 32)) {
			return fmt.Errorf("committed state expected %d got %x: %v", want, value, readErr)
		}
	}
	if len(r.Transactions) > 0 {
		for {
			h, e := network.NewNode(n).BlockHeight(ctx)
			if e != nil {
				return e
			}
			if h.Uint64() >= lastHeight+3 {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
		history, e := accountSync.History(ctx, accounts[0], network.HistoryOptions{Limit: 1000})
		if e != nil {
			return e
		}
		defer history.Close()
		for {
			row, e := history.Next(ctx)
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			r.History = append(r.History, row)
		}
		for _, tr := range r.Transactions {
			if tr.ExecutionError != "" {
				continue
			}
			if tr.After.Accounts[0].Nonce.Cmp(tr.Before.Accounts[0].Nonce) == 0 {
				continue
			}
			found := false
			for _, row := range r.History {
				if row.Hash == tr.Hash && row.Success {
					fee, ok := new(big.Int).SetString(tr.GasPaid, 10)
					if !ok {
						return errors.New("invalid recorded fee")
					}
					expected := new(big.Int).Sub(tr.After.Accounts[0].Value, tr.Before.Accounts[0].Value)
					expected.Add(expected, fee)
					if row.Value.Cmp(expected) != 0 || row.FeePaid.Cmp(fee) != 0 {
						return fmt.Errorf("history value/fee mismatch for %s", tr.Name)
					}
					found = true
				}
			}
			if !found {
				return fmt.Errorf("history missing %s", tr.Name)
			}
		}
		r.Checks = append(r.Checks, "finalized account history contains all sender account transactions with exact values and fees")
	}
	aborted, cancelAbort := context.WithCancel(ctx)
	cancelAbort()
	if _, e = n.Query(aborted, "block(height:-1){header{height}}"); e == nil {
		return errors.New("pre-aborted query accepted")
	}
	if e = n.PingWebSocket(ctx); e != nil {
		return e
	}
	r.Checks = append(r.Checks, "pre-aborted query rejected and final WebSocket ping")
	if len(r.Failures) > 0 {
		return fmt.Errorf("live capability failures: %v", r.Failures)
	}
	return nil
}
