package network

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type ruesTestServer struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	write    sync.Mutex
	conn     *websocket.Conn
	counts   map[string]int
	dropPing atomic.Bool
	get      func(http.ResponseWriter, *http.Request)
}

func newRuesTestServer(t *testing.T, get func(http.ResponseWriter, *http.Request)) *ruesTestServer {
	h := &ruesTestServer{t: t, counts: make(map[string]int), get: get}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/on" {
			ws, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if e != nil {
				return
			}
			h.mu.Lock()
			h.conn = ws
			h.mu.Unlock()
			h.write.Lock()
			e = ws.WriteMessage(websocket.TextMessage, []byte("test-session"))
			h.write.Unlock()
			if e != nil {
				return
			}
			defer ws.Close()
			for {
				kind, b, e := ws.ReadMessage()
				if e != nil {
					return
				}
				if kind == websocket.TextMessage && strings.HasPrefix(string(b), "rues:ping:") && !h.dropPing.Load() {
					h.write.Lock()
					_ = ws.WriteMessage(websocket.TextMessage, []byte(strings.Replace(string(b), "rues:ping:", "rues:pong:", 1)))
					h.write.Unlock()
				}
			}
		}
		if r.Header.Get("rusk-session-id") != "test-session" {
			t.Error("missing session header")
		}
		h.mu.Lock()
		h.counts[r.Method+" "+r.URL.Path]++
		h.mu.Unlock()
		if r.Method == http.MethodGet && h.get != nil {
			h.get(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(func() {
		h.mu.Lock()
		ws := h.conn
		h.mu.Unlock()
		if ws != nil {
			_ = ws.Close()
		}
		h.server.Close()
	})
	return h
}
func (h *ruesTestServer) count(method, path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.counts[method+" "+path]
}
func eventFrame(path string, payload []byte) []byte {
	b, _ := json.Marshal(map[string]string{"content-location": path})
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, uint32(len(b)))
	out = append(out, b...)
	return append(out, payload...)
}
func (h *ruesTestServer) emit(path string, payload []byte) {
	h.t.Helper()
	h.mu.Lock()
	ws := h.conn
	h.mu.Unlock()
	h.write.Lock()
	e := ws.WriteMessage(websocket.BinaryMessage, eventFrame(path, payload))
	h.write.Unlock()
	if e != nil {
		h.t.Errorf("emit: %v", e)
	}
}
func connected(t *testing.T, h *ruesTestServer) *Client {
	t.Helper()
	c, e := New(h.server.URL, WithTimeout(time.Second))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Disconnect(context.Background()); _ = c.DataDrivers.Close(context.Background()) })
	if e = c.Connect(context.Background()); e != nil {
		t.Fatal(e)
	}
	return c
}
func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}
func nextEvent(t *testing.T, s *Subscription) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	e, err := s.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestRuesEarlyDeliveryAndSharedRegistration(t *testing.T) {
	const path = "/on/transactions:abc/executed"
	var h *ruesTestServer
	h = newRuesTestServer(t, func(w http.ResponseWriter, r *http.Request) { h.emit(path, []byte("early")); w.WriteHeader(204) })
	c := connected(t, h)
	a, e := c.Transactions().WithID("abc").Subscribe(context.Background(), "executed")
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if got := string(nextEvent(t, a).Payload); got != "early" {
		t.Fatal(got)
	}
	b, e := c.Transactions().WithID("abc").Subscribe(context.Background(), "executed")
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	if h.count("GET", path) != 1 {
		t.Fatal("duplicate remote registration")
	}
	h.emit(path, []byte("shared"))
	one, two := nextEvent(t, a), nextEvent(t, b)
	one.Payload[0] = 'X'
	one.Headers.Set("test", "mutated")
	if string(two.Payload) != "shared" || two.Headers.Get("test") != "" {
		t.Fatal("shared mutable event")
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	if h.count("DELETE", path) != 0 {
		t.Fatal("sibling registration removed")
	}
	if e = b.Close(); e != nil {
		t.Fatal(e)
	}
	if h.count("DELETE", path) != 1 {
		t.Fatal("last registration leaked")
	}
}
func TestRuesOverflowReleasesRemoteWithSibling(t *testing.T) {
	const path = "/on/blocks/statechange"
	h := newRuesTestServer(t, nil)
	c := connected(t, h)
	slow, e := c.Blocks().Subscribe(context.Background(), "statechange", 1)
	if e != nil {
		t.Fatal(e)
	}
	fast, e := c.Blocks().Subscribe(context.Background(), "statechange", 4)
	if e != nil {
		t.Fatal(e)
	}
	h.emit(path, []byte("1"))
	nextEvent(t, fast)
	h.emit(path, []byte("2"))
	nextEvent(t, fast)
	eventually(t, func() bool {
		select {
		case <-slow.Done():
			return true
		default:
			return false
		}
	})
	if h.count("DELETE", path) != 0 {
		t.Fatal("overflow removed active sibling")
	}
	nextEvent(t, slow)
	if _, e = slow.Next(context.Background()); !errors.Is(e, ErrSlowConsumer) {
		t.Fatal(e)
	}
	for i := 0; i < 5; i++ {
		h.emit(path, []byte("x"))
	}
	eventually(t, func() bool { return h.count("DELETE", path) == 1 })
	session, _ := c.currentSession()
	eventually(t, func() bool { session.mu.Lock(); defer session.mu.Unlock(); return len(session.paths) == 0 })
	// No explicit Close was required on either overflowing subscriber.
}
func TestRuesOverflowDuringRegistrationCleanup(t *testing.T) {
	const path = "/on/blocks/accepted"
	var h *ruesTestServer
	h = newRuesTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		h.emit(path, []byte("1"))
		h.emit(path, []byte("2"))
		<-r.Context().Done()
	})
	c := connected(t, h)
	start := time.Now()
	if _, e := c.Blocks().Subscribe(context.Background(), "accepted", 1); e == nil {
		t.Fatal("overflow during GET accepted")
	}
	if time.Since(start) > time.Second {
		t.Fatal("registration cancellation stalled")
	}
	eventually(t, func() bool { return h.count("DELETE", path) == 1 })
}
func TestRuesCanceledRegistrationWaiterAndProbeIsolation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	h := newRuesTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
			w.WriteHeader(204)
		case <-r.Context().Done():
		}
	})
	c := connected(t, h)
	first := make(chan *Subscription, 1)
	go func() {
		s, e := c.Blocks().Subscribe(context.Background(), "statechange")
		if e != nil {
			t.Error(e)
		}
		first <- s
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, e := c.Blocks().Subscribe(ctx, "statechange"); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("canceled waiter blocked by sibling")
	}
	close(release)
	s := <-first
	defer s.Close()
	h.dropPing.Store(true)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if e := c.PingWebSocket(ctx2); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	h.dropPing.Store(false)
	if e := c.PingWebSocket(context.Background()); e != nil {
		t.Fatal("timed-out probe poisoned session", e)
	}
	h.emit("/on/blocks/statechange", []byte("still live"))
	nextEvent(t, s)
}
func TestRuesDisconnectDuringHelloAndReconnect(t *testing.T) {
	start := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer ws.Close()
		close(start)
		_, _, _ = ws.ReadMessage()
	}))
	defer server.Close()
	c, _ := New(server.URL, WithTimeout(time.Second))
	result := make(chan error, 1)
	go func() { result <- c.Connect(context.Background()) }()
	<-start
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := c.Disconnect(ctx); e != nil {
		t.Fatal(e)
	}
	if e := <-result; e == nil {
		t.Fatal("canceled hello accepted")
	}
	if c.Connected() {
		t.Fatal("late session installed")
	}
	h := newRuesTestServer(t, nil)
	c2 := connected(t, h)
	_ = c2.Disconnect(ctx)
	if e := c2.Connect(ctx); e != nil {
		t.Fatal("reconnect", e)
	}
	if e := c2.PingWebSocket(ctx); e != nil {
		t.Fatal(e)
	}
}
func TestHTTPConsumptionBudgetsAndFeederFallback(t *testing.T) {
	var feederCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/on/test/stall":
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/on/test/feed":
			feederCalls.Add(1)
			if r.Header.Get("Rusk-feeder") == "" {
				http.Error(w, "missing feeder", 400)
				return
			}
			_, _ = w.Write([]byte("feed"))
		default:
			http.Error(w, "rejection details", http.StatusUnprocessableEntity)
		}
	}))
	defer server.Close()
	c, _ := New(server.URL, WithTimeout(30*time.Millisecond))
	response, e := c.Scope("test").Stream(context.Background(), "stall", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	time.Sleep(50 * time.Millisecond)
	if _, e = io.ReadAll(response.Body); e == nil {
		t.Fatal("paused stream outlived budget")
	}
	b, e := c.Scope("test").Call(context.Background(), "feed", nil)
	if e != nil || string(b) != "feed" || feederCalls.Load() != 2 {
		t.Fatalf("fallback %q %v %d", b, e, feederCalls.Load())
	}
	_, e = c.Scope("test").Call(context.Background(), "reject", nil)
	var rejected *HTTPError
	if !errors.As(e, &rejected) || rejected.Status != 422 || !strings.Contains(rejected.Body, "details") {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = c.Scope("test").Call(ctx, "feed", nil); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if feederCalls.Load() != 2 {
		t.Fatal("pre-abort reached server")
	}
}
func TestEventAndArchiveValidation(t *testing.T) {
	for _, b := range [][]byte{nil, {1, 2, 3}, {255, 255, 255, 255}, eventFrame("garbage", nil)} {
		if _, e := parseEvent(b); e == nil {
			t.Fatal("malformed frame accepted")
		}
	}
	e, err := parseEvent(eventFrame("/on/transactions:id/executed", []byte(`{"gas_spent":18446744073709551615,"inner":{"fee":{"gas_price":1},"memo":"e29883"}}`)))
	if err != nil {
		t.Fatal(err)
	}
	fee, err := e.GasPaid()
	if err != nil || fee.String() != "18446744073709551615" {
		t.Fatal(fee, err)
	}
	memo, err := e.Memo()
	if err != nil || string(memo) != "☃" {
		t.Fatal(memo, err)
	}
	for _, b := range [][]byte{nil, {2}, {1}, {1, 0, 0, 0, 0, 0, 0, 0, 2}} {
		if _, e := ParseStake(b); e == nil {
			t.Fatal("malformed stake accepted")
		}
	}
}
