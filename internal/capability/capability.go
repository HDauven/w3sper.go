// Package capability seals SDK-internal access to signer key snapshots.
package capability

type Token struct{ granted bool }

func Grant() Token          { return Token{true} }
func (t Token) Valid() bool { return t.granted }

type Kind uint8

const (
	BLS Kind = iota
	Phoenix
	View
	Both
)

func (k Kind) Range() (int, int) {
	switch k {
	case BLS:
		return 0, 32
	case Phoenix:
		return 32, 96
	case View:
		return 96, 160
	case Both:
		return 0, 96
	}
	return 0, 0
}
