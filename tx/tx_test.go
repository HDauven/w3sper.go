package tx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HDauven/w3sper.go/network"
)

func TestNetworkChainSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"data":{"chain":{"header":{"chainId":42}}}}`)
	}))
	defer server.Close()

	client, err := network.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := NetworkChainSource(client).ChainID()
	if err != nil || got != 42 {
		t.Fatalf("ChainID() = %d, %v; want 42, nil", got, err)
	}
}
