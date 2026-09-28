package network_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/HDauven/w3sper.go/network"
	"github.com/HDauven/w3sper.go/profile"
	"github.com/HDauven/w3sper.go/walletcore"
)

func noteFixture(t *testing.T) ([]byte, []*profile.Signer, *walletcore.WalletCore) {
	t.Helper()
	ctx := context.Background()
	w, e := walletcore.Load(ctx)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = w.Close(ctx) })
	var fixture struct {
		Seed  string
		Notes []struct {
			Index   int64
			Entries [][2]string
		}
	}
	b, e := os.ReadFile("../testdata/js-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &fixture); e != nil {
		t.Fatal(e)
	}
	seed, _ := hex.DecodeString(fixture.Seed)
	g := profile.NewGenerator(func() ([]byte, error) { return seed, nil }, w)
	var items []byte
	var signers []*profile.Signer
	for _, owner := range fixture.Notes {
		p, e := g.Derive(owner.Index)
		if e != nil {
			t.Fatal(e)
		}
		s, e := g.Signer(ctx, p)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(s.Dispose)
		signers = append(signers, s)
		for _, pair := range owner.Entries {
			item, e := hex.DecodeString(pair[1])
			if e != nil || len(item) != int(w.NoteItemSize()) {
				t.Fatal("note fixture", e)
			}
			items = append(items, item...)
		}
	}
	return items, signers, w
}
func feedServer(t *testing.T, items []byte, count int, hold bool) (*network.Client, <-chan struct{}) {
	t.Helper()
	closed := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/graphql":
			_, _ = w.Write([]byte(`{"data":{"block":{"header":{"height":100}}}}`))
		case "/on/contracts:" + network.TransferContract + "/num_notes":
			var b [8]byte
			binary.LittleEndian.PutUint64(b[:], uint64(count))
			_, _ = w.Write(b[:])
		case "/on/contracts:" + network.TransferContract + "/leaves_from_height":
			if r.Header.Get("Rusk-feeder") != "true" {
				t.Error("missing feeder header")
			}
			_, _ = w.Write(items)
			w.(http.Flusher).Flush()
			if hold {
				<-r.Context().Done()
			}
			close(closed)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(s.Close)
	n, e := network.New(s.URL, network.WithTimeout(2*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	return n, closed
}
func TestRealParallelOwnershipStreamAndEOF(t *testing.T) {
	items, owners, w := noteFixture(t)
	worker, e := walletcore.Load(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer worker.Close(context.Background())
	size := int(w.NoteItemSize())
	n, _ := feedServer(t, items, len(items)/size, false)
	progress := 0
	stream, e := network.NewAddressSyncer(n, w, worker).Notes(context.Background(), owners, network.SyncOptions{ChunkSize: 3, Progress: func(p network.SyncProgress) {
		progress++
		if p.Progress < 0 || p.Progress > 1 {
			t.Error("invalid progress")
		}
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer stream.Close()
	counts := make([]int, len(owners))
	for {
		batch, e := stream.Next(context.Background())
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		for i, notes := range batch.Owned {
			counts[i] += len(notes)
		}
	}
	for _, count := range counts {
		if count != 8 {
			t.Fatalf("lost/duplicated mapped notes: %v", counts)
		}
	}
	if progress == 0 {
		t.Fatal("no progress")
	}
	if _, e = stream.Next(context.Background()); e != io.EOF {
		t.Fatal("unstable EOF", e)
	}
}
func TestNoteStreamCancellationDisposalAndTruncation(t *testing.T) {
	for _, mode := range []string{"truncated", "close", "dispose", "next-cancel"} {
		t.Run(mode, func(t *testing.T) {
			items, owners, w := noteFixture(t)
			size := int(w.NoteItemSize())
			payload := items[:size]
			hold := mode != "truncated"
			if !hold {
				payload = payload[:size-1]
			}
			n, closed := feedServer(t, payload, 1, hold)
			s, e := network.NewAddressSyncer(n, w).Notes(context.Background(), owners, network.SyncOptions{ChunkSize: 1})
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			if mode == "truncated" {
				if _, e = s.Next(context.Background()); e == nil {
					t.Fatal("truncated feeder accepted")
				}
				return
			}
			if _, e = s.Next(context.Background()); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "close":
				_ = s.Close()
			case "dispose":
				owners[0].Dispose()
			case "next-cancel":
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				defer cancel()
				if _, e = s.Next(ctx); e == nil {
					t.Fatal("canceled read succeeded")
				}
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("stream did not close transport")
			}
			if _, e = s.Next(context.Background()); e == nil {
				t.Fatal("closed stream read succeeded")
			}
			sig, e := owners[1].SignBLSMessage(context.Background(), []byte{0, 255})
			if e != nil || bytes.Equal(sig, make([]byte, 48)) {
				t.Fatal("stream cancellation poisoned sibling signer", e)
			}
		})
	}
}
