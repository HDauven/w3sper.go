package walletcore

import (
	"fmt"
	"math/big"

	"github.com/HDauven/w3sper.go/driver"
	"github.com/HDauven/w3sper.go/gas"
)

// Boundary constants. Receiver/sender sizes come from wallet-core.
const (
	seedLen       = 64
	accountKeyLen = 96
	addressKeyLen = 64
	maxChainID    = 0xff
)

// validateSeed rejects seeds that are not exactly 64 bytes.
func validateSeed(seed []byte) error {
	if len(seed) != seedLen {
		return fmt.Errorf("walletcore: seed must be %d bytes, got %d", seedLen, len(seed))
	}
	return nil
}

// validateAccountKey rejects account keys that are not exactly 96 bytes.
func validateAccountKey(k []byte) error {
	if len(k) != accountKeyLen {
		return fmt.Errorf("walletcore: account key must be %d bytes, got %d", accountKeyLen, len(k))
	}
	return nil
}

// validateAddressKey rejects address keys that are not exactly 64 bytes.
func validateAddressKey(k []byte) error {
	if len(k) != addressKeyLen {
		return fmt.Errorf("walletcore: address key must be %d bytes, got %d", addressKeyLen, len(k))
	}
	return nil
}

// u64OrErr converts a non-negative big.Int that fits in a uint64 into the
// value, rejecting nil, negative and overflowing inputs.
func u64OrErr(name string, v *big.Int) (uint64, error) {
	if v == nil || v.Sign() < 0 {
		return 0, fmt.Errorf("walletcore: %s must be a non-negative integer", name)
	}
	if !v.IsUint64() {
		return 0, fmt.Errorf("walletcore: %s does not fit in uint64", name)
	}
	return v.Uint64(), nil
}

// validateChainID rejects chain ids outside the supported range.
func validateChainID(chain int) (byte, error) {
	if chain < 0 || chain > maxChainID {
		return 0, fmt.Errorf("walletcore: chain id must be between 0 and %d, got %d", maxChainID, chain)
	}
	return byte(chain), nil
}

// validateMoonlight validates the shared account-transaction inputs.
func (w *WalletCore) validateMoonlight(in driver.MoonlightInput, requireReceiver bool) error {
	if err := validateSeed(in.Seed); err != nil {
		return err
	}
	if requireReceiver {
		if in.Receiver == nil {
			return fmt.Errorf("walletcore: receiver is required")
		}
		if err := validateAccountKey(in.Receiver); err != nil {
			return err
		}
	}
	if in.Receiver != nil {
		if err := validateAccountKey(in.Receiver); err != nil {
			return err
		}
	}
	if _, err := u64OrErr("transfer value", in.TransferValue); err != nil {
		return err
	}
	if _, err := u64OrErr("deposit", in.Deposit); err != nil {
		return err
	}
	if _, err := u64OrErr("gas limit", in.GasLimit); err != nil {
		return err
	}
	if _, err := u64OrErr("gas price", in.GasPrice); err != nil {
		return err
	}
	if err := gas.New(in.GasLimit, in.GasPrice).Validate(); err != nil {
		return err
	}
	if _, err := u64OrErr("nonce", in.Nonce); err != nil {
		return err
	}
	if _, err := validateChainID(in.ChainID); err != nil {
		return err
	}
	return nil
}
