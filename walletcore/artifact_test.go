package walletcore

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
)

func TestPinnedArtifactDigests(t *testing.T) {
	artifacts := map[string]string{"../testdata/bob.wasm": "b9927fe73c106888910da15b878cf7888040f88c1325565137e34c886877c27d", "../testdata/committed-target.wasm": "81c11fbf30bb0a5eff376b11e1a754adb47f799afc4f98b38a1ffb1e2c4a291c", "../testdata/stake-driver.wasm": "489ee7354c89eca3cbc870e3d146aba209bb02691e4864ce93dd04759f2902e3", "../testdata/transfer-driver.wasm": "983a9794178a04165319c4d7be583f2530d1b14a0ca19d061c99f60c60264966", "embedded-walletcore": "f17a0b4a77044e4424a7a400d73d64d3366e41cd0e417d1cdd1d4b38b48185fc"}
	for path, want := range artifacts {
		t.Run(path, func(t *testing.T) {
			b := wasmBytes
			if path != "embedded-walletcore" {
				var e error
				b, e = os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != want {
				t.Fatalf("artifact changed: %s != %s", got, want)
			}
		})
	}
}
