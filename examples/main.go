// Command example builds a real signed transaction offline. It never submits.
package main

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/HDauven/w3sper.go/profile"
	"github.com/HDauven/w3sper.go/tx"
	"github.com/HDauven/w3sper.go/walletcore"
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}
func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	core, err := walletcore.Load(ctx)
	if err != nil {
		return err
	}
	defer core.Close(context.Background())
	// PUBLIC SYNTHETIC SEED: never use this example wallet for real funds.
	seed := make([]byte, 64)
	for i := range seed {
		seed[i] = byte(i)
	}
	defer clear(seed)
	generator := profile.NewGenerator(func() ([]byte, error) { return seed, nil }, core)
	sender, err := generator.DeriveContext(ctx, 0)
	if err != nil {
		return err
	}
	receiver, err := generator.DeriveContext(ctx, 1)
	if err != nil {
		return err
	}
	// Public profiles are not authority. Issue and dispose a separate signer.
	signer, err := generator.Signer(ctx, sender)
	if err != nil {
		return err
	}
	defer signer.Dispose()
	entry := tx.NewBookEntry(signer, nil, core)
	routed, err := entry.Transfer(big.NewInt(1000000000)).Memo([]byte("offline Go example")).Chain(42).Nonce(big.NewInt(7)).To(receiver.Account().String())
	if err != nil {
		return err
	}
	result, err := routed.Account.BuildContext(ctx, nil)
	if err != nil {
		return err
	}
	fmt.Println("sender:", sender.Account())
	fmt.Println("receiver:", receiver.Account())
	fmt.Printf("signed %d bytes; next nonce=%s; hash=%s (NOT submitted)\n", len(result.Buffer), result.Nonce, result.Hash)
	return nil
}
