package network

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

func TestQueryWrapsSelectionSet(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(req.Query, "query { ") {
			t.Fatalf("query not wrapped: %q", req.Query)
		}
		if r.Header.Get("rusk-version") != DefaultVersion {
			t.Fatalf("rusk-version header missing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"block":{"height":42}}}`))
	})
	defer srv.Close()

	c, err := New(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.Query(context.Background(), "block(height: -1){header{height}}")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := data["block"]; !ok {
		t.Fatalf("expected block key, got %v", data)
	}
}

func TestQueryGraphQLError(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"errors":[{"message":"boom"}]}`))
	})
	defer srv.Close()

	c, _ := New(srv.URL)
	_, err := c.Query(context.Background(), "query{foo}")
	if err == nil {
		t.Fatal("expected error")
	}
	var gqlErr *GraphQLError
	if !errorsAs(err, &gqlErr) {
		t.Fatalf("expected GraphQLError, got %T: %v", err, err)
	}
	if gqlErr.Message != "" && gqlErr.Errors[0].Message != "boom" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNodeBlockHeight(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"block":{"header":{"height":"9007199254740993"}}}}`))
	})
	defer srv.Close()

	c, _ := New(srv.URL)
	height, err := NewNode(c).BlockHeight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := new(big.Int).SetString("9007199254740993", 10)
	if height.Cmp(want) != 0 {
		t.Fatalf("BlockHeight = %s, want %s", height, want)
	}
}

func TestNewRejectsNonHTTPURL(t *testing.T) {
	if _, err := New("ftp://example.com"); err == nil {
		t.Fatal("expected error for non-http URL")
	}
}

func errorsAs(err error, target interface{}) bool {
	switch t := target.(type) {
	case **GraphQLError:
		if g, ok := err.(*GraphQLError); ok {
			*t = g
			return true
		}
	}
	return false
}
