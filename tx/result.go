package tx

import "math/big"

// NonceResult is the result of an account-based (moonlight) transaction.
type NonceResult struct {
	Buffer  []byte
	ChainID int
	Hash    string
	Nonce   *big.Int
}

// NullifierResult is the result of a nullifier-based (phoenix) transaction.
type NullifierResult struct {
	Buffer     []byte
	ChainID    int
	Hash       string
	Nullifiers [][]byte
}

// DeploymentResult is the result of a contract deployment transaction.
type DeploymentResult struct {
	NonceResult
	ContractID string
}
