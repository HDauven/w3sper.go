// Command example demonstrates the w3sper Go SDK.
package main

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/dusk-network/w3sper-go"
	"github.com/dusk-network/w3sper-go/driver"
	"github.com/dusk-network/w3sper-go/profile"
	"github.com/dusk-network/w3sper-go/tx"
	"github.com/dusk-network/w3sper-go/walletcore"
)

// memTreasury is a trivial in-memory treasury returning a fixed balance.
type memTreasury struct{}

func (memTreasury) AccountBalance(account []byte) (tx.AccountBalance, error) {
	return tx.AccountBalance{Nonce: big.NewInt(7), Value: big.NewInt(10_000_000_000_000)}, nil
}
func (memTreasury) AddressNotes(address []byte) ([]driver.Note, error) { return nil, nil }
func (memTreasury) StakeInfo(account []byte) (tx.StakeInfo, error)     { return tx.StakeInfo{}, nil }

func main() {
	ctx := context.Background()

	// 1. Unit conversion.
	dusk, err := w3sper.FormatToDusk(big.NewInt(11000000001))
	if err != nil {
		panic(err)
	}
	fmt.Println("11000000001 lux =", dusk, "dusk")

	// 2. Gas.
	g := w3sper.NewGas(big.NewInt(3_000_000), big.NewInt(2))
	fmt.Printf("gas total = %s\n", g.Total)

	// 3. Load the wallet-core WASM driver (real Dusk cryptography).
	wc, err := walletcore.Load(ctx)
	if err != nil {
		panic(err)
	}
	defer wc.Close(ctx)

	// Derive a deterministic profile.
	seed := make([]byte, 64)
	for i := range seed {
		seed[i] = byte(i*7 + 1)
	}
	gen := profile.NewGenerator(func() ([]byte, error) { return seed, nil }, wc)
	sender, err := gen.Default()
	if err != nil {
		panic(err)
	}
	receiverProf, err := gen.Next()
	if err != nil {
		panic(err)
	}

	fmt.Println("sender   address:", sender.Address())
	fmt.Println("sender   account:", sender.Account())
	fmt.Println("receiver account:", receiverProf.Account())

	// 4. Build and sign an account transfer end-to-end.
	entry := tx.NewBookEntry(sender, memTreasury{}, wc)
	lux1Dusk, _ := w3sper.ParseFromDusk("1") // 1 Dusk in lux
	tr := tx.NewTransfer(entry).
		Amount(lux1Dusk).
		Chain(0x2a)

	routed, err := tr.To(receiverProf.Account().String())
	if err != nil {
		panic(err)
	}
	if routed.Account == nil {
		panic("expected account transfer")
	}

	res, err := routed.Account.Build()
	if err != nil {
		var dreq *driver.DriverRequiredError
		if errors.As(err, &dreq) {
			fmt.Println("driver missing:", err)
			return
		}
		panic(err)
	}
	fmt.Printf("signed account tx: %d bytes, nonce=%s hash=%s\n",
		len(res.Buffer), res.Nonce, res.Hash)

	// Gas helpers.
	_ = g
}
