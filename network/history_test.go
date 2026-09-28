package network

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HDauven/w3sper.go/profile"
)

func TestHistoryExactIntegersAndStableEOF(t *testing.T) {
	account := make([]byte, 96)
	key := profile.EncodeAccount(account)
	hash := strings.Repeat("ab", 32)
	rows, _ := json.Marshal(map[string]any{"data": map[string]any{"fullMoonlightHistory": map[string]any{"json": []any{map[string]any{"origin": hash, "block_height": uint64(math.MaxUint64), "events": []any{map[string]any{"target": TransferContract, "topic": "moonlight", "data": map[string]any{"sender": "other", "receiver": key, "value": "9007199254740993"}}}}}}}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q Request
		if e := json.NewDecoder(r.Body).Decode(&q); e != nil {
			t.Error(e)
			return
		}
		if strings.Contains(q.Query, "fullMoonlightHistory") {
			_, _ = w.Write(rows)
		} else {
			_, _ = w.Write([]byte(`{"data":{"tx0":{"blockHash":"block","blockTimestamp":1,"err":null,"gasSpent":9007199254740993,"tx":{"gasLimit":18446744073709551615,"gasPrice":1,"isDeploy":false,"memo":""}}}}`))
		}
	}))
	defer server.Close()
	n, _ := New(server.URL)
	to := uint64(math.MaxUint64)
	s, e := NewAccountSyncer(n, nil).History(context.Background(), account, HistoryOptions{To: &to})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	row, e := s.Next(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if row.BlockHeight.String() != "18446744073709551615" || row.Value.String() != "9007199254740993" || row.FeePaid.String() != "9007199254740993" || row.BlockTimestamp != 1000 {
		t.Fatalf("integer precision lost: %+v", row)
	}
	for i := 0; i < 2; i++ {
		if _, e = s.Next(context.Background()); e != io.EOF {
			t.Fatal("unstable EOF", e)
		}
	}
	if _, e = enrichHistory(key, historyRow{Origin: hash, Height: json.RawMessage(`1`)}, json.RawMessage(`null`)); e == nil {
		t.Fatal("missing transaction accepted")
	}
}
func TestHistoryCloseCancelsPendingEnrichment(t *testing.T) {
	entered, ended := make(chan struct{}), make(chan struct{})
	var enrichCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q Request
		_ = json.NewDecoder(r.Body).Decode(&q)
		if strings.Contains(q.Query, "fullMoonlightHistory") {
			_, _ = w.Write([]byte(`{"data":{"fullMoonlightHistory":{"json":[{"origin":"` + strings.Repeat("01", 32) + `","block_height":1,"events":[]}]}}}`))
			return
		}
		enrichCalls.Add(1)
		close(entered)
		<-r.Context().Done()
		close(ended)
	}))
	defer server.Close()
	n, _ := New(server.URL, WithTimeout(time.Second))
	to := uint64(100)
	s, e := NewAccountSyncer(n, nil).History(context.Background(), make([]byte, 96), HistoryOptions{To: &to})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	result := make(chan error, 1)
	go func() { _, e := s.Next(context.Background()); result <- e }()
	<-entered
	_ = s.Close()
	select {
	case e = <-result:
		if e == nil {
			t.Fatal("closed enrichment succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("enrichment did not abort")
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("enrichment socket leaked")
	}
	if enrichCalls.Load() != 1 {
		t.Fatal("unexpected enrichment calls")
	}
}
